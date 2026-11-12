package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

// check is what the EVCC job does: refresh, plans, then notifications.
func (ta *testApp) check() {
	ta.t.Helper()
	ta.poll()
	ctx := context.Background()
	if err := ta.app.Service.SyncPlan(ctx); err != nil {
		ta.t.Fatal(err)
	}
	if err := ta.app.Service.CheckNotifications(ctx); err != nil {
		ta.t.Fatal(err)
	}
}

// sent describes the stub's calls since the last look: "title" for a
// message, "-title" for clearing the notification with that title.
func (ta *testApp) sent() []string {
	ta.t.Helper()
	titles := map[string]string{}
	recent, _ := ta.app.Store.RecentNotifications(100)
	for _, n := range recent {
		titles["evcc-planner-"+itoa(n.ID)] = n.Title
	}
	var out []string
	for _, s := range ta.phone().Take() {
		if s.Cleared {
			out = append(out, "-"+titles[s.Message.Tag])
		} else {
			out = append(out, s.Message.Title)
		}
	}
	return out
}

func (ta *testApp) expectSent(want ...string) []homeassistant.Sent {
	ta.t.Helper()
	got := ta.sent()
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		ta.t.Fatalf("sent %q, want %q", got, want)
	}
	return nil
}

func (ta *testApp) notification(key string) model.Notification {
	ta.t.Helper()
	n, err := ta.app.Store.NotificationByKey(key)
	if err != nil || n == nil {
		ta.t.Fatalf("notification %s: %v %v", key, n, err)
	}
	return *n
}

func TestPlugInReminders(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.notifications(nil)
	ta.phone().Take()
	tue := func(h, m int) time.Time { return time.Date(2026, 11, 10, h, m, 0, 0, ams) }

	// Monday 19:00, unplugged at 38%; Tuesday's school run needs 42%. The
	// unclear calendar event gets a question right away.
	ta.check()
	ta.expectSent("Is this a car trip?")
	if q := ta.notification("question:calendar:3d528cfa4dfb8d60"); q.Message != `"Birthday party" on Sat 14 Nov at 14:00, Grandma.` {
		t.Errorf("question = %q", q.Message)
	}
	ta.check()
	ta.expectSent()

	// The cheapest charging is at night, in quiet hours: the early reminder
	// comes 15 minutes before they start.
	ta.at(time.Date(2026, 11, 9, 22, 40, 0, 0, ams))
	ta.check()
	ta.expectSent()
	ta.at(time.Date(2026, 11, 9, 22, 46, 0, 0, ams))
	ta.check()
	ta.expectSent("Plug in to charge cheaply")
	early := ta.notification("early:recurring:1:2026-11-10")
	if !strings.HasPrefix(early.Message, "Charging is cheapest from Tue ") ||
		!strings.HasSuffix(early.Message, "School drop-off needs 42% by Tue 07:48.") || len(early.Verbs) != 3 {
		t.Errorf("early = %+v", early)
	}
	ta.at(tue(1, 0))
	ta.check()
	ta.expectSent()

	// Urgent: 30 minutes before the latest plug-in time (sent in quiet
	// hours by default); it replaces the early reminder.
	ta.at(tue(6, 10))
	ta.check()
	ta.expectSent("-Plug in to charge cheaply", "Plug in by 06:36")
	urgent := ta.notification("urgent:recurring:1:2026-11-10")
	if !urgent.Urgent || urgent.Message != "School drop-off needs 42% by 07:48." || !urgent.Latest.Equal(tue(6, 36)) {
		t.Errorf("urgent = %+v", urgent)
	}

	// Remind in 30 min: no later than the latest plug-in time.
	ta.json("POST", "/api/demo/notification-action", map[string]any{"action": "EVCCP_SNOOZE_" + itoa(urgent.ID)}, http.StatusNoContent, nil)
	if n := ta.notification(urgent.Key); n.State != model.NotifySnoozed || !n.SnoozedUntil.Equal(tue(6, 36)) {
		t.Fatalf("snoozed = %+v", n)
	}
	ta.at(tue(6, 30))
	ta.check()
	ta.expectSent()

	// After the latest plug-in time: too late for the full target.
	ta.at(tue(6, 38))
	ta.check()
	ta.expectSent("Plug in now")
	late := ta.notification("late:recurring:1:2026-11-10")
	if late.Message != "Plugging in now gives about 41% by 07:48 (School drop-off needs 42%)." {
		t.Errorf("too late = %q", late.Message)
	}
	if n := ta.notification(urgent.Key); n.State != model.NotifyDone {
		t.Errorf("urgent after too late = %+v", n)
	}

	// Plugging in ends the reminder and clears it from the phone.
	ta.plug(true, 38)
	ta.expectSent("-Plug in now")
	if n := ta.notification(late.Key); n.State != model.NotifyDone || n.Note != "No longer needed" {
		t.Errorf("after plugging in = %+v", n)
	}

	// The activity log has every message.
	var events int
	for _, e := range ta.activity("?type=notification").Events {
		if e.Action == "notification.send" && e.Source != model.SourceAutomation {
			t.Errorf("send from %s: %+v", e.Source, e)
		}
		events++
	}
	if events != 5 { // question, early, urgent, too late, and the snooze
		t.Errorf("%d notification events", events)
	}
}

func TestUrgentRemindersWaitForQuietHoursWhenAsked(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.notifications(func(s map[string]any) { s["urgentInQuiet"], s["questions"], s["early"] = false, false, false })
	ta.at(time.Date(2026, 11, 10, 6, 10, 0, 0, ams))
	ta.check()
	ta.expectSent()
	ta.at(time.Date(2026, 11, 10, 7, 0, 0, 0, ams))
	ta.check()
	ta.expectSent("Plug in now")
}

// modEVCC is the demo EVCC with a changed loadpoint.
type modEVCC struct {
	*evcc.Stub
	mod func(*evcc.Loadpoint)
}

func (m modEVCC) State(ctx context.Context) (*evcc.State, error) {
	st, err := m.Stub.State(ctx)
	if err == nil && m.mod != nil {
		m.mod(&st.Loadpoints[0])
	}
	return st, err
}

func TestChargingProblemsAndReadyMessages(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.notifications(func(s map[string]any) { s["questions"], s["early"], s["ready"] = false, false, true })
	ev := &modEVCC{Stub: ta.app.Stub}
	ta.app.Service.UseIntegrations(func(i *service.Integrations) { i.EVCC = ev })
	ta.setting("tripPlans", model.TripPlansManual) // no plans written here
	ta.check()
	ta.phone().Take()

	// Unplugged long before the departure while below its target.
	ta.plug(true, 30)
	ta.plug(false, 0)
	ta.expectSent("Unplugged early")
	if n := ta.notification("unplugged:recurring:1:2026-11-10:" + itoa(time.Date(2026, 11, 9, 19, 0, 0, 0, ams).Unix())); n.Message != "Unplugged at 30%; School drop-off needs 42% by Tue 07:48." || n.State != model.NotifyDone {
		t.Errorf("unplugged = %+v", n)
	}

	// EVCC charges by the plan, but the car does not take it for 10 minutes.
	ev.mod = func(lp *evcc.Loadpoint) { lp.PlanActive, lp.Charging = true, false }
	ta.plug(true, 30)
	ta.expectSent()
	ta.at(time.Date(2026, 11, 9, 19, 10, 0, 0, ams))
	ta.check()
	ta.expectSent("Charging stopped")
	ev.mod = func(lp *evcc.Loadpoint) { lp.PlanActive, lp.Charging = true, true }
	ta.check()
	ta.expectSent("-Charging stopped")

	// EVCC not in Smart mode while plugged in: a warning right away, a
	// notification after 10 minutes (another automation may switch it back
	// to Smart soon after the car arrives).
	warnings := func() []string {
		t.Helper()
		var p struct{ Warnings []string }
		ta.json("GET", "/api/plan", nil, http.StatusOK, &p)
		return p.Warnings
	}
	ev.mod = func(lp *evcc.Loadpoint) { lp.Mode = "off" }
	ta.check()
	ta.expectSent()
	if w := warnings(); len(w) != 1 || !strings.HasPrefix(w[0], "Charging is off in EVCC") {
		t.Errorf("warnings = %v", w)
	}
	ta.at(time.Date(2026, 11, 9, 19, 20, 0, 0, ams))
	ta.check()
	ta.expectSent("Charging is off in EVCC")
	if n := ta.notification("mode:off:recurring:1:2026-11-10"); !n.Urgent ||
		n.Message != "The plan won't run; School drop-off needs 42% by Tue 07:48. Set EVCC to Smart." {
		t.Errorf("off = %+v", n)
	}
	// Fast mode ignores the plan's cheapest slots.
	ev.mod = func(lp *evcc.Loadpoint) { lp.Mode = "now" }
	ta.check()
	ta.expectSent("-Charging is off in EVCC")
	if w := warnings(); len(w) != 1 || !strings.HasPrefix(w[0], "EVCC is in Fast mode") {
		t.Errorf("warnings = %v", w)
	}
	ta.at(time.Date(2026, 11, 9, 19, 30, 0, 0, ams))
	ta.check()
	ta.expectSent("EVCC is in Fast mode")
	ev.mod = nil
	ta.check()
	ta.expectSent("-EVCC is in Fast mode")

	// Off while the car is away is expected: no warning, no notification.
	ev.mod = func(lp *evcc.Loadpoint) { lp.Mode = "off" }
	ta.plug(false, 0)
	ta.phone().Take()
	ta.at(time.Date(2026, 11, 9, 20, 0, 0, 0, ams))
	ta.check()
	ta.expectSent()
	if w := warnings(); len(w) != 0 {
		t.Errorf("warnings while away = %v", w)
	}
	ev.mod = nil

	// Charged to the target while plugged in: ready (optional).
	ta.plug(true, 45)
	ta.expectSent("Ready for School drop-off")

	// Leaving with less than the target is a missed target, recorded even
	// with notifications off.
	ta.json("PUT", "/api/settings/notifications", map[string]any{"enabled": false}, http.StatusOK, nil)
	log := &eventLog{ta: ta}
	log.fresh()
	ta.at(time.Date(2026, 11, 10, 7, 30, 0, 0, ams))
	ta.plug(true, 35)
	ta.plug(false, 0)
	ta.expectSent()
	if e := log.one("plan.missed", model.SourceAutomation); e.Summary != "Left for School drop-off with 35% (target 42%)." {
		t.Errorf("missed = %+v", e)
	}
	ta.plug(true, 35)
	ta.plug(false, 0)
	log.none("leaving again")
}
