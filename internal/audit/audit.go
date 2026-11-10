// Package audit supports the activity log: what started a piece of work
// (the UI, a scheduled job, the calendar sync, or automation), secret
// redaction, and an HTTP transport that records every call to an external
// service.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

type ctxKey int

const (
	sourceKey ctxKey = iota
	callKey
)

// WithSource records what started the work done with ctx.
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceKey, source)
}

// Source returns what started the work done with ctx: a scheduled job unless
// marked otherwise.
func Source(ctx context.Context) string {
	if v, ok := ctx.Value(sourceKey).(string); ok && v != "" {
		return v
	}
	return model.SourceJob
}

// Call describes the external request about to be made with ctx, for the
// activity log. The Transport adds the outcome, duration, and size.
type Call struct {
	Summary   string
	SubjectID string
	Detail    map[string]any
}

// WithCall describes the external requests made with ctx.
func WithCall(ctx context.Context, c Call) context.Context {
	return context.WithValue(ctx, callKey, c)
}

func callFrom(ctx context.Context) Call {
	c, _ := ctx.Value(callKey).(Call)
	return c
}

// ---- redaction ----

const redacted = "[redacted]"

var patterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// Google Calendar secret iCal addresses.
	{regexp.MustCompile(`/private-[^/\s"?#]+`), "/private-" + redacted},
	// Keys, tokens, and signatures in query strings or messages.
	{regexp.MustCompile(`(?i)\b(key|api_key|apikey|token|access_token|signature|sig|x-amz-signature|x-amz-credential|x-amz-security-token)=[^&\s"]+`), "$1=" + redacted},
	{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`), "Bearer " + redacted},
}

// Redactor removes configured secrets (API keys, secret calendar addresses)
// and recognisable credentials from text before it is stored.
type Redactor struct {
	secrets []string
}

// NewRedactor redacts the given values; empty and very short ones are
// ignored, as they would match ordinary text.
func NewRedactor(secrets ...string) *Redactor {
	r := &Redactor{}
	for _, s := range secrets {
		if s = strings.TrimSpace(s); len(s) >= 6 {
			r.secrets = append(r.secrets, s)
		}
	}
	return r
}

// String redacts a text.
func (r *Redactor) String(s string) string {
	if r != nil {
		for _, secret := range r.secrets {
			s = strings.ReplaceAll(s, secret, redacted)
		}
	}
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// JSON redacts every string in a JSON document.
func (r *Redactor) JSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return json.RawMessage(`"` + redacted + `"`)
	}
	out, err := marshal(r.walk(v))
	if err != nil {
		return json.RawMessage(`"` + redacted + `"`)
	}
	return out
}

func (r *Redactor) walk(v any) any {
	switch t := v.(type) {
	case string:
		return r.String(t)
	case []any:
		for i := range t {
			t[i] = r.walk(t[i])
		}
	case map[string]any:
		for k, x := range t {
			t[k] = r.walk(x)
		}
	}
	return v
}

// marshal encodes without HTML escaping, so stored text stays readable.
func marshal(v any) (json.RawMessage, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return json.RawMessage(strings.TrimSpace(b.String())), nil
}

// Marshal encodes a value for an event's Before, After, or Detail; nil
// stays empty.
func Marshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	out, err := marshal(v)
	if err != nil || string(out) == "null" {
		return nil
	}
	return out
}

// ---- recorder ----

// Sink stores events.
type Sink interface {
	AddAuditEvent(model.AuditEvent) (model.AuditEvent, error)
}

// Recorder completes events (time, source, redaction) and stores them.
type Recorder struct {
	Sink     Sink
	Now      func() time.Time
	Redactor *Redactor
	Log      *slog.Logger
}

// Prepare sets the event's time and source (from ctx) when missing and
// redacts it. Use it for events stored inside a transaction.
func (r *Recorder) Prepare(ctx context.Context, e model.AuditEvent) model.AuditEvent {
	if e.At.IsZero() {
		e.At = time.Now()
		if r.Now != nil {
			e.At = r.Now()
		}
	}
	if e.Source == "" {
		e.Source = Source(ctx)
	}
	e.Summary = r.Redactor.String(e.Summary)
	e.Error = r.Redactor.String(e.Error)
	e.Before, e.After, e.Detail = r.Redactor.JSON(e.Before), r.Redactor.JSON(e.After), r.Redactor.JSON(e.Detail)
	return e
}

// Record stores an event on its own. A failure is logged, not returned:
// the activity log never breaks the work it describes.
func (r *Recorder) Record(ctx context.Context, e model.AuditEvent) {
	if r == nil || r.Sink == nil {
		return
	}
	e = r.Prepare(ctx, e)
	if _, err := r.Sink.AddAuditEvent(e); err != nil && r.Log != nil {
		r.Log.Warn("recording activity failed", "action", e.Action, "err", err)
	}
}
