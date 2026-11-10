package audit

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

// Transport records each request to an external service in the activity
// log: what it was for, the HTTP status, duration, and response size. Query
// strings are never recorded, and with HideURL neither is the path.
type Transport struct {
	Service  string // google_routes, calendar, tibber, openrouter, knmi
	Base     http.RoundTripper
	Recorder *Recorder
	// Label summarizes a request when its context carries no Call.
	Label func(*http.Request) string
	// HideURL records only the host: the address itself is a secret, as
	// with secret iCal addresses.
	HideURL bool
}

// RoundTrip performs the request and records it once its response body has
// been read or closed.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	start := time.Now()
	resp, err := base.RoundTrip(req)
	if err != nil {
		t.record(req, start, 0, 0, err)
		return nil, err
	}
	resp.Body = &countingBody{ReadCloser: resp.Body, done: func(n int64, readErr error) {
		t.record(req, start, resp.StatusCode, n, readErr)
	}}
	return resp, nil
}

func (t *Transport) record(req *http.Request, start time.Time, status int, size int64, err error) {
	call := callFrom(req.Context())
	summary := call.Summary
	if summary == "" && t.Label != nil {
		summary = t.Label(req)
	}
	endpoint := req.URL.Host
	if !t.HideURL {
		endpoint += req.URL.EscapedPath()
	}
	if summary == "" {
		summary = req.Method + " " + endpoint
	}
	detail := map[string]any{"method": req.Method, "endpoint": endpoint}
	for k, v := range call.Detail {
		detail[k] = v
	}
	e := model.AuditEvent{
		Type: model.EventAPICall, Action: "api.call", SubjectType: "api", SubjectID: call.SubjectID,
		Summary: summary, Detail: Marshal(detail), Outcome: model.OutcomeSuccess, Service: t.Service,
		StatusCode: status, DurationMs: time.Since(start).Milliseconds(), Bytes: size,
	}
	switch {
	case err != nil:
		e.Outcome, e.Error = model.OutcomeFailure, callError(err)
	case status/100 != 2:
		e.Outcome, e.Error = model.OutcomeFailure, fmt.Sprintf("HTTP %d %s", status, http.StatusText(status))
	}
	t.Recorder.Record(req.Context(), e)
}

// callError describes a failed request without its URL, which may hold a
// secret.
func callError(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	return err.Error()
}

// countingBody counts the bytes read and reports once, at the end of the
// body or when it is closed.
type countingBody struct {
	io.ReadCloser
	n    int64
	once sync.Once
	done func(n int64, err error)
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n += int64(n)
	switch {
	case errors.Is(err, io.EOF):
		b.finish(nil)
	case err != nil:
		b.finish(err)
	}
	return n, err
}

func (b *countingBody) Close() error {
	err := b.ReadCloser.Close()
	b.finish(nil)
	return err
}

func (b *countingBody) finish(err error) {
	b.once.Do(func() { b.done(b.n, err) })
}
