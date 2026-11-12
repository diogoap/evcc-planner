package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestNotifyCallsTheMobileAppService(t *testing.T) {
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/services":
			w.Write([]byte(`[{"domain":"light","services":{"turn_on":{}}},
				{"domain":"notify","services":{"mobile_app_b":{},"notify":{},"mobile_app_a":{}}}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/services/notify/mobile_app_a":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			calls = append(calls, body)
			w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &HTTPClient{BaseURL: srv.URL, Token: "token", HTTP: srv.Client()}
	ctx := context.Background()

	phones, err := c.Phones(ctx)
	if err != nil || strings.Join(phones, ",") != "mobile_app_a,mobile_app_b" {
		t.Fatalf("phones = %v, %v", phones, err)
	}
	err = c.Notify(ctx, "mobile_app_a", Message{
		Title: "Plug in by 10:20", Text: "Utrecht needs 52%", Tag: "evcc-planner-7", Link: "http://planner.test/",
		Urgent: true, Actions: []Action{{ID: "EVCCP_SNOOZE_7", Title: "Remind in 30 min"}, {Title: "View plan", URI: "http://planner.test/"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Clear(ctx, "mobile_app_a", "evcc-planner-7"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %v", calls)
	}
	got, _ := json.Marshal(calls[0])
	want := `{"data":{"actions":[{"action":"EVCCP_SNOOZE_7","title":"Remind in 30 min"},{"action":"URI","title":"View plan","uri":"http://planner.test/"}],` +
		`"clickAction":"http://planner.test/","group":"evcc-planner","priority":"high","push":{"interruption-level":"time-sensitive"},` +
		`"tag":"evcc-planner-7","ttl":0,"url":"http://planner.test/"},"message":"Utrecht needs 52%","title":"Plug in by 10:20"}`
	if string(got) != want {
		t.Errorf("notify body =\n%s\nwant\n%s", got, want)
	}
	if calls[1]["message"] != "clear_notification" || calls[1]["data"].(map[string]any)["tag"] != "evcc-planner-7" {
		t.Errorf("clear body = %v", calls[1])
	}
	if err := c.Notify(ctx, "mobile_app_b", Message{}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("unknown service: %v", err)
	}
}

// fakeHA speaks Home Assistant's websocket protocol: authentication, the
// subscription, then the given events. A nil events list refuses the
// subscription as Home Assistant does for users who are not admins.
func fakeHA(t *testing.T, token string, events []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/websocket" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()
		wsjson.Write(ctx, conn, map[string]string{"type": "auth_required"})
		var auth map[string]any
		if wsjson.Read(ctx, conn, &auth) != nil || auth["access_token"] != token {
			wsjson.Write(ctx, conn, map[string]string{"type": "auth_invalid"})
			return
		}
		wsjson.Write(ctx, conn, map[string]string{"type": "auth_ok"})
		var sub map[string]any
		if wsjson.Read(ctx, conn, &sub) != nil || sub["type"] != "subscribe_events" || sub["event_type"] != ActionEvent {
			return
		}
		if events == nil {
			wsjson.Write(ctx, conn, map[string]any{"id": sub["id"], "type": "result", "success": false,
				"error": map[string]string{"code": "unauthorized", "message": "Unauthorized"}})
			return
		}
		wsjson.Write(ctx, conn, map[string]any{"id": sub["id"], "type": "result", "success": true})
		for _, a := range events {
			wsjson.Write(ctx, conn, map[string]any{"id": sub["id"], "type": "event",
				"event": map[string]any{"event_type": ActionEvent, "data": map[string]any{"action": a}}})
		}
		// Answer pings until the client goes away.
		for {
			var m map[string]any
			if wsjson.Read(ctx, conn, &m) != nil {
				return
			}
			wsjson.Write(ctx, conn, map[string]any{"id": m["id"], "type": "pong"})
		}
	}))
}

func TestListenActionsOverTheWebsocket(t *testing.T) {
	srv := fakeHA(t, "token", []string{"EVCCP_SNOOZE_3", "EVCCP_SKIP_4"})
	defer srv.Close()
	c := &HTTPClient{BaseURL: srv.URL + "/", Token: "token", HTTP: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := false
	var got []string
	err := c.ListenActions(ctx, func() { ready = true }, func(id string) {
		got = append(got, id)
		if len(got) == 2 {
			cancel()
		}
	})
	if !ready || strings.Join(got, ",") != "EVCCP_SNOOZE_3,EVCCP_SKIP_4" || err != context.Canceled {
		t.Fatalf("ready %v, actions %v, err %v", ready, got, err)
	}

	bad := &HTTPClient{BaseURL: srv.URL, Token: "wrong", HTTP: srv.Client()}
	err = bad.ListenActions(context.Background(), func() { t.Error("ready with a wrong token") }, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "access token rejected") {
		t.Errorf("wrong token: %v", err)
	}

	// A user who is not an administrator may not subscribe.
	user := fakeHA(t, "token", nil)
	defer user.Close()
	c = &HTTPClient{BaseURL: user.URL, Token: "token", HTTP: user.Client()}
	err = c.ListenActions(context.Background(), func() { t.Error("ready without the subscription") }, func(string) {})
	if !errors.Is(err, ErrNotAdmin) {
		t.Errorf("not an admin: %v", err)
	}
}
