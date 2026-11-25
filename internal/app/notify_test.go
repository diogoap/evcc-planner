package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

func (ta *testApp) phone() *homeassistant.NotifyStub {
	return ta.app.Service.Clients().Notify.(*homeassistant.NotifyStub)
}

// notifications turns notifications on for the demo phone, with changes.
// Plans sent are off unless a change turns them on: each automatic plan
// would bring one.
func (ta *testApp) notifications(change func(map[string]any)) {
	ta.t.Helper()
	var s map[string]any
	ta.json("GET", "/api/settings/notifications", nil, http.StatusOK, &s)
	s["enabled"], s["phones"], s["planSent"] = true, []string{homeassistant.StubPhone}, false
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
		!s.Early || s.Ready || !s.UrgentInQuiet || s.Evening || s.EveningAt != "19:30" || !s.PlanSent {
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
		{"eveningAt": "7pm"},
		{"evening": true, "eveningAt": "23:30"}, // in quiet hours
		{"snoozeMin": 1},
		{"locationEntity": "car location"},
	} {
		ta.json("PUT", "/api/settings/notifications", bad, http.StatusBadRequest, nil)
	}
	log.none("invalid settings")

	ta.notifications(func(s map[string]any) { s["linkUrl"], s["planSent"] = "http://planner.test:7201/", true })
	if e := log.one("settings.update", model.SourceUI); e.Summary != "Changed notification settings: notifications, link address, phones" {
		t.Errorf("event = %+v", e)
	}
	if c := connection(ta, "Phone notifications"); c.Status != "stub" {
		t.Errorf("connection while on = %+v", c)
	}

	var n model.Notification
	ta.json("POST", "/api/notifications/test", map[string]any{}, http.StatusOK, &n)
	sent := ta.phone().Take()
	if len(sent) != 1 || sent[0].Phone != homeassistant.StubPhone || sent[0].Message.Title != "[EVCC Planner] 🔔 Test notification" ||
		len(sent[0].Message.Actions) != 1 || sent[0].Message.Actions[0].URI != "http://planner.test:7201/" ||
		sent[0].Message.Link != "http://planner.test:7201/" {
		t.Fatalf("sent = %+v", sent)
	}
	if e := log.one("notification.send", model.SourceUI); e.Summary != `Sent "[EVCC Planner] 🔔 Test notification" to 1 phone` {
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
	// A test is not tried again: you see the error right away.
	if recent, _ := ta.app.Store.RecentNotifications(1); len(recent) != 1 || recent[0].State != model.NotifyFailed {
		t.Errorf("failed test = %+v", recent)
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
			Key: key, Kind: model.NotifyUrgent, Subject: subject, Title: "Plug in by 19:20", Message: "School drop-off needs 42% by Tue 07:48.", Verbs: verbs,
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
	if e := log.one("notification.action", model.SourceNotification); e.Summary != `Remind in 30 min pressed: "Plug in by 19:20" snoozed until 19:20` {
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
	if len(sent) != 1 || sent[0].Message.Title != "[EVCC Planner] ⏭️ Trip skipped" ||
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

type connectionStatus struct{ Name, Status, Detail, Fix string }

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

// refusingListener answers like Home Assistant for a user who is not an
// administrator.
type refusingListener struct{}

func (refusingListener) ListenActions(context.Context, func(), func(string)) error {
	return homeassistant.ErrNotAdmin
}

func TestButtonsNeedAnAdminToken(t *testing.T) {
	ta := newTestApp(t, "")
	ta.notifications(nil)
	ta.app.Service.UseIntegrations(func(i *service.Integrations) { i.Actions = refusingListener{} })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ta.app.Service.ListenNotificationActions(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c := connection(ta, "Phone notifications")
		if c.Status == "error" {
			if !strings.Contains(c.Detail, "1 phone; sending works, but button presses need a token of a Home Assistant administrator") ||
				!strings.HasSuffix(c.Detail, "Make the token's user an administrator in Home Assistant.") {
				t.Errorf("detail = %q", c.Detail)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("connection = %+v", c)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPlanSentOncePerDeparture(t *testing.T) {
	ta := newTestApp(t, "") // Monday 19:00, unplugged; Automatic mode
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.notifications(func(s map[string]any) {
		s["planSent"], s["questions"], s["quietFrom"], s["quietTo"] = true, false, "18:00", "20:00"
	})
	ta.check()
	ta.phone().Take()
	consumption := func(kwh float64) {
		t.Helper()
		var s map[string]any
		ta.json("GET", "/api/settings", nil, http.StatusOK, &s)
		s["consumptionKWhPer100Km"] = kwh
		ta.json("PUT", "/api/settings", s, http.StatusOK, nil)
	}
	lastSoC := func() int {
		t.Helper()
		w, err := ta.app.Store.LastPlanWrite()
		if err != nil || w == nil {
			t.Fatalf("no plan written: %v", err)
		}
		return w.SoC
	}
	key := "plan_sent:recurring:1:2026-11-10"

	// Plugged in during quiet hours: the plan for Tuesday's school run goes
	// to EVCC without a message.
	ta.app.Stub.SetVehicle(true, 30)
	ta.check()
	first := lastSoC()
	ta.expectSent()
	if n, _ := ta.app.Store.NotificationByKey(key); n != nil {
		t.Fatalf("told in quiet hours: %+v", n)
	}

	// After quiet hours, the departure's plan changes: one message, then
	// no more for that departure.
	ta.at(time.Date(2026, 11, 9, 20, 5, 0, 0, ams))
	consumption(22)
	ta.check()
	second := lastSoC()
	if second <= first {
		t.Fatalf("plan not raised: %d%% then %d%%", first, second)
	}
	ta.expectSent("Plan sent")
	n := ta.notification(key)
	if n.Message != "EVCC charges to "+itoa(int64(second))+"% by Tue 07:48 for School drop-off." ||
		n.State != model.NotifyDone || len(n.Verbs) != 1 || n.Verbs[0] != model.VerbView {
		t.Errorf("plan sent = %+v", n)
	}
	consumption(26)
	ta.check()
	if lastSoC() <= second {
		t.Fatalf("plan not raised again: %d%%", lastSoC())
	}
	ta.expectSent()
}

func TestPlanMessagesAtTheSameTimeSendTheHighest(t *testing.T) {
	ta := newTestApp(t, "") // Monday 19:00
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.notifications(func(s map[string]any) { s["questions"] = false })
	ta.check()
	ta.phone().Take()
	now := ta.app.Service.Clock.Now()
	// As queued when the plans are made (the copy check reads them).
	queue := func(key, kind, _ string, soc int, at time.Time) {
		t.Helper()
		n := model.Notification{
			Key: key, Kind: kind, Title: "[EVCC Planner] 📤 Plan sent", PlanSoC: soc,
			Message: "EVCC charges to " + itoa(int64(soc)) + "% by Tue 15:00 for School pickup.",
			Verbs:   []string{model.VerbView}, State: model.NotifyPending, CreatedAt: at,
		}
		if kind == model.NotifyExtraPlan {
			n.Title = "[EVCC Planner] 🔌 Extra plan created"
			n.Message = "Charge extra to " + itoa(int64(soc)) + "%: 02:00–05:45. The plan asks EVCC for " +
				itoa(int64(soc)) + "% by Tue 05:45 and ends then."
		}
		if _, err := ta.app.Store.SaveNotification(n); err != nil {
			t.Fatal(err)
		}
	}

	// A trip's plan and an extra plan in the same check: only the higher
	// target is sent; the other counts as told.
	queue("plan_sent:recurring:2:2026-11-10", model.NotifyPlanSent, "Plan sent", 23, now)
	queue("extra_plan:7", model.NotifyExtraPlan, "Extra plan created", 52, now)
	ta.check()
	ta.expectSent("Extra plan created")
	if n := ta.notification("extra_plan:7"); n.State != model.NotifyDone || n.Note != "Information" {
		t.Errorf("sent = %+v", n)
	}
	if n := ta.notification("plan_sent:recurring:2:2026-11-10"); n.State != model.NotifyDone ||
		n.Note != "Not sent: the message about a 52% plan came at the same time" || n.SentAt != nil {
		t.Errorf("left out = %+v", n)
	}

	// Alone, a message is sent; one waiting too long (EVCC unreachable) is not.
	queue("plan_sent:recurring:1:2026-11-11", model.NotifyPlanSent, "Plan sent", 40, now.Add(-20*time.Minute))
	ta.check()
	ta.expectSent()
	if n := ta.notification("plan_sent:recurring:1:2026-11-11"); n.State != model.NotifyDone || n.Note != "Not sent: too old" {
		t.Errorf("too old = %+v", n)
	}
	queue("plan_sent:recurring:1:2026-11-12", model.NotifyPlanSent, "Plan sent", 19, now)
	ta.check()
	ta.expectSent("Plan sent")
}
