package app

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/model"
)

func (ta *testApp) phone() *homeassistant.NotifyStub {
	return ta.app.Service.Clients().Notify.(*homeassistant.NotifyStub)
}

// notifications turns notifications on for the demo phone, with changes.
func (ta *testApp) notifications(change func(map[string]any)) {
	ta.t.Helper()
	var s map[string]any
	ta.json("GET", "/api/settings/notifications", nil, http.StatusOK, &s)
	s["enabled"], s["phones"] = true, []string{homeassistant.StubPhone}
	if change != nil {
		change(s)
	}
	ta.json("PUT", "/api/settings/notifications", s, http.StatusOK, nil)
}

func TestNotificationSettingsAndTest(t *testing.T) {
	ta := newTestApp(t, "")
	log := &eventLog{ta: ta}

	var s model.NotificationSettings
	ta.json("GET", "/api/settings/notifications", nil, http.StatusOK, &s)
	if s.Enabled || len(s.Phones) != 0 || s.QuietFrom != "23:00" || s.QuietTo != "07:00" || s.SnoozeMin != 30 ||
		!s.Early || s.Ready || !s.UrgentInQuiet {
		t.Fatalf("defaults = %+v", s)
	}
	var phones []string
	ta.json("GET", "/api/notifications/phones", nil, http.StatusOK, &phones)
	if len(phones) != 1 || phones[0] != homeassistant.StubPhone {
		t.Fatalf("phones = %v", phones)
	}
	if c := connection(ta, "Phone notifications"); c.Status != "not_configured" || !strings.HasPrefix(c.Detail, "Off") {
		t.Errorf("connection while off = %+v", c)
	}

	for _, bad := range []map[string]any{
		{"enabled": true},
		{"phones": []string{"notify.phone"}},
		{"linkUrl": "planner.local"},
		{"quietFrom": "11pm"},
		{"snoozeMin": 1},
		{"locationEntity": "car location"},
	} {
		ta.json("PUT", "/api/settings/notifications", bad, http.StatusBadRequest, nil)
	}
	log.none("invalid settings")

	ta.notifications(func(s map[string]any) { s["linkUrl"] = "http://planner.test:7201/" })
	if e := log.one("settings.update", model.SourceUI); e.Summary != "Changed notification settings: notifications, link address, phones" {
		t.Errorf("event = %+v", e)
	}
	if c := connection(ta, "Phone notifications"); c.Status != "stub" {
		t.Errorf("connection while on = %+v", c)
	}

	var n model.Notification
	ta.json("POST", "/api/notifications/test", map[string]any{}, http.StatusOK, &n)
	sent := ta.phone().Take()
	if len(sent) != 1 || sent[0].Phone != homeassistant.StubPhone || sent[0].Message.Title != "EVCC Trip Planner" ||
		len(sent[0].Message.Actions) != 1 || sent[0].Message.Actions[0].URI != "http://planner.test:7201/" ||
		sent[0].Message.Link != "http://planner.test:7201/" {
		t.Fatalf("sent = %+v", sent)
	}
	if e := log.one("notification.send", model.SourceUI); e.Summary != `Sent "EVCC Trip Planner" to 1 phone` {
		t.Errorf("event = %+v", e)
	}
	var recent []model.Notification
	ta.json("GET", "/api/notifications", nil, http.StatusOK, &recent)
	if len(recent) != 1 || recent[0].Kind != model.NotifyTest || recent[0].State != model.NotifyDone {
		t.Errorf("recent = %+v", recent)
	}

	// A failed send is recorded.
	ta.phone().Fail = errors.New("phone unreachable")
	ta.json("POST", "/api/notifications/test", map[string]any{}, http.StatusBadGateway, nil)
	got := log.fresh()
	if len(got) != 1 || got[0].Outcome != model.OutcomeFailure || !strings.Contains(got[0].Error, "phone unreachable") {
		t.Errorf("failure = %+v", got)
	}
}

func TestNotificationButtons(t *testing.T) {
	ta := newTestApp(t, "")
	ta.notifications(nil)
	log := &eventLog{ta: ta}
	log.fresh()
	latest := time.Date(2026, 11, 9, 19, 20, 0, 0, ams)
	save := func(key, subject string, verbs ...string) model.Notification {
		t.Helper()
		n, err := ta.app.Store.SaveNotification(model.Notification{
			Key: key, Kind: model.NotifyUrgent, Subject: subject, Title: "Plug in", Verbs: verbs,
			State: model.NotifySent, CreatedAt: time.Now(), Latest: &latest,
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	press := func(action string, status int) {
		t.Helper()
		ta.json("POST", "/api/demo/notification-action", map[string]any{"action": action}, status, nil)
	}

	// Remind in 30 min snoozes, but never past the notification's latest time.
	n := save("urgent:a", "recurring:1:2026-11-10", model.VerbSnooze, model.VerbSkip)
	press("EVCCP_SNOOZE_"+itoa(n.ID), http.StatusNoContent)
	got, _ := ta.app.Store.Notification(n.ID)
	if got.State != model.NotifySnoozed || !got.SnoozedUntil.Equal(latest) {
		t.Fatalf("snoozed = %+v", got)
	}
	if e := log.one("notification.action", model.SourceNotification); e.Summary != `Remind in 30 min pressed: "Plug in" snoozed until 19:20` {
		t.Errorf("event = %+v", e)
	}

	// Skip trip turns the trip off and confirms the new target.
	ta.phone().Take()
	press("EVCCP_SKIP_"+itoa(n.ID), http.StatusNoContent)
	if i, _ := ta.plan().trip("recurring:1:2026-11-10"); ta.plan().Trips[i].Enabled {
		t.Error("the trip is still on")
	}
	events := log.fresh()
	if len(events) != 3 || events[0].Action != "trip.disable" || events[0].Source != model.SourceNotification ||
		events[1].Action != "notification.action" || events[2].Action != "notification.send" {
		t.Fatalf("events = %+v", events)
	}
	sent := ta.phone().Take()
	if len(sent) != 1 || sent[0].Message.Title != "Trip skipped" ||
		sent[0].Message.Text != "The next target is 39% by Tue 08:20 for Office visit." {
		t.Errorf("follow-up = %+v", sent)
	}
	// A button of a closed notification changes nothing.
	press("EVCCP_SNOOZE_"+itoa(n.ID), http.StatusNoContent)
	log.none("pressing a button again")

	// Questions about calendar events: Count it confirms the trip.
	q := save("question:a", "calendar:3d528cfa4dfb8d60", model.VerbCount, model.VerbNoTrip)
	press("EVCCP_COUNT_"+itoa(q.ID), http.StatusNoContent)
	if i, _ := ta.plan().trip("calendar:3d528cfa4dfb8d60"); ta.plan().Trips[i].NeedsReview {
		t.Error("the trip still needs review")
	}
	log.fresh()

	// Other apps' buttons are ignored; unknown notifications are refused.
	press("SOMETHING_ELSE", http.StatusNoContent)
	press("EVCCP_SNOOZE_999", http.StatusBadRequest)
	log.none("foreign and unknown buttons")
}

type connectionStatus struct{ Name, Status, Detail string }

func connection(ta *testApp, name string) connectionStatus {
	ta.t.Helper()
	var list []connectionStatus
	ta.json("GET", "/api/connections", nil, http.StatusOK, &list)
	for _, c := range list {
		if c.Name == name {
			return c
		}
	}
	ta.t.Fatalf("no connection %q in %+v", name, list)
	return connectionStatus{}
}
