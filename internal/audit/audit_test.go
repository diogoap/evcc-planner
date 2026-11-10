package audit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

type memSink struct{ events []model.AuditEvent }

func (m *memSink) AddAuditEvent(e model.AuditEvent) (model.AuditEvent, error) {
	e.ID = int64(len(m.events) + 1)
	m.events = append(m.events, e)
	return e, nil
}

var fixed = time.Date(2026, 11, 9, 18, 0, 0, 0, time.UTC)

func recorder(secrets ...string) (*Recorder, *memSink) {
	sink := &memSink{}
	return &Recorder{Sink: sink, Now: func() time.Time { return fixed }, Redactor: NewRedactor(secrets...)}, sink
}

func TestSourceDefaultsToScheduledJob(t *testing.T) {
	ctx := context.Background()
	if got := Source(ctx); got != model.SourceJob {
		t.Errorf("default source = %q", got)
	}
	if got := Source(WithSource(ctx, model.SourceUI)); got != model.SourceUI {
		t.Errorf("source = %q", got)
	}
}

func TestTransportRecordsCallsWithoutQueryStrings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(strings.Repeat("x", 1234)))
	}))
	defer srv.Close()
	rec, sink := recorder()
	client := &http.Client{Transport: &Transport{Service: "knmi", Recorder: rec}}

	ctx := WithSource(context.Background(), model.SourceUI)
	ctx = WithCall(ctx, Call{Summary: "Download outlook", SubjectID: "s1", Detail: map[string]any{"slot": "tue-0730"}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/files/a.xml?X-Amz-Signature=SIG123", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	resp, err = client.Get(srv.URL + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if len(sink.events) != 2 {
		t.Fatalf("events = %d, want 2 (one per call, recorded once)", len(sink.events))
	}
	ok := sink.events[0]
	if ok.Type != model.EventAPICall || ok.Service != "knmi" || ok.Source != model.SourceUI || ok.Summary != "Download outlook" ||
		ok.SubjectID != "s1" || ok.StatusCode != 200 || ok.Bytes != 1234 || ok.Outcome != model.OutcomeSuccess || !ok.At.Equal(fixed) {
		t.Errorf("success event = %+v", ok)
	}
	var detail map[string]any
	json.Unmarshal(ok.Detail, &detail)
	if detail["slot"] != "tue-0730" || detail["method"] != "GET" || !strings.HasSuffix(detail["endpoint"].(string), "/files/a.xml") {
		t.Errorf("detail = %v", detail)
	}
	if strings.Contains(string(ok.Detail), "SIG123") {
		t.Errorf("query string recorded: %s", ok.Detail)
	}
	bad := sink.events[1]
	if bad.Outcome != model.OutcomeFailure || bad.StatusCode != 404 || bad.Error != "HTTP 404 Not Found" ||
		bad.Source != model.SourceJob || !strings.HasPrefix(bad.Summary, "GET 127.0.0.1:") {
		t.Errorf("failure event = %+v", bad)
	}
}

func TestTransportHidesSecretAddresses(t *testing.T) {
	rec, sink := recorder()
	failing := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") })
	client := &http.Client{Transport: &Transport{Service: "calendar", Recorder: rec, HideURL: true, Base: failing}}
	secret := "https://calendar.example.test/calendar/ical/me/private-0123456789abcdef/basic.ics"
	if _, err := client.Get(secret); err == nil {
		t.Fatal("expected an error")
	}
	e := sink.events[0]
	all := e.Summary + e.Error + string(e.Detail)
	if strings.Contains(all, "private-") || strings.Contains(all, "basic.ics") {
		t.Errorf("secret address recorded: %+v", e)
	}
	if e.Error != "connection refused" || e.Outcome != model.OutcomeFailure || e.Summary != "GET calendar.example.test" {
		t.Errorf("event = %+v", e)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRedactorRemovesSecretsEverywhere(t *testing.T) {
	r := NewRedactor("AIzaSECRETKEY", "", "abc")
	got := r.String("key AIzaSECRETKEY in https://x.test/a?key=other&b=1 with Bearer sk-or-v1-zzz and /ical/x/private-a1b2/basic.ics")
	for _, leak := range []string{"AIzaSECRETKEY", "key=other", "sk-or-v1-zzz", "private-a1b2"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaked in %q", leak, got)
		}
	}
	if !strings.Contains(got, "b=1") {
		t.Errorf("ordinary text removed: %q", got)
	}
	if r.String("abc") != "abc" {
		t.Error("short secrets must be ignored")
	}

	raw := json.RawMessage(`{"url":"https://h.test/p?token=T0KEN&x=<y>","list":["AIzaSECRETKEY",1],"n":2}`)
	out := string(r.JSON(raw))
	if strings.Contains(out, "T0KEN") || strings.Contains(out, "AIzaSECRETKEY") || !strings.Contains(out, "x=<y>") ||
		!strings.Contains(out, `"n":2`) {
		t.Errorf("redacted JSON = %s", out)
	}
}

func TestRecordPreparesEvents(t *testing.T) {
	rec, sink := recorder("TOPSECRET99")
	rec.Record(WithSource(context.Background(), model.SourceAutomation), model.AuditEvent{
		Action: "x", Summary: "used TOPSECRET99", Error: "TOPSECRET99 rejected", After: Marshal(map[string]string{"k": "TOPSECRET99"}),
	})
	e := sink.events[0]
	if e.Source != model.SourceAutomation || !e.At.Equal(fixed) {
		t.Errorf("event = %+v", e)
	}
	if strings.Contains(e.Summary+e.Error+string(e.After), "TOPSECRET99") {
		t.Errorf("secret stored: %+v", e)
	}
	var nilRec *Recorder
	nilRec.Record(context.Background(), model.AuditEvent{}) // must not panic
	if Marshal(nil) != nil {
		t.Error("Marshal(nil) must stay empty")
	}
}
