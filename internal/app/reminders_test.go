package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
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

// appTag matches the app's tag and the kind's emoji that start every
// title on the phone ("[EVCC Planner] 🔌 "), which the tests leave out.
var appTag = regexp.MustCompile(`\[EVCC Planner\] \S+ `)

// sent describes the stub's calls since the last look: "title" for a
// message, "-title" for clearing the notification with that title, both
// without the app's tag and emoji.
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
			out = append(out, "-"+appTag.ReplaceAllString(titles[s.Message.Tag], ""))
		} else {
			out = append(out, appTag.ReplaceAllString(s.Message.Title, ""))
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

// With the evening reminder on, an extra reminder comes at its time when
// charging is planned for tonight. The early reminder still comes before
// quiet hours while the car is unplugged, and replaces it on the phone.
func TestEveningReminder(t *testing.T) {
	mon := func(h, m int) time.Time { return time.Date(2026, 11, 9, h, m, 0, 0, ams) }
	start := func(at string) *testApp {
		ta := newTestApp(t, "")
		ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
		ta.notifications(func(s map[string]any) { s["questions"], s["evening"], s["eveningAt"] = false, true, at })
		ta.phone().Take()
		return ta
	}
	// Monday 19:00, unplugged at 38%; Tuesday's school run charges at night.
	ta := start("19:30")
	ta.check()
	ta.expectSent()
	ta.at(mon(19, 29))
	ta.check()
	ta.expectSent()
	ta.at(mon(19, 30))
	ta.check()
	ta.expectSent("Plug in tonight")
	n := ta.notification("evening:2026-11-09")
	if n.Kind != model.NotifyEvening || !strings.HasPrefix(n.Message, "Charging is cheapest from Tue ") ||
		!strings.HasSuffix(n.Message, "School drop-off needs 42% by Tue 07:48.") || len(n.Verbs) != 2 {
		t.Errorf("evening reminder = %+v", n)
	}
	ta.check()
	ta.expectSent()
	ta.at(mon(22, 46))
	ta.check()
	ta.expectSent("-Plug in tonight", "Plug in to charge cheaply")

	// Plugged in after it: no more reminders.
	ta = start("19:30")
	ta.at(mon(19, 30))
	ta.check()
	ta.expectSent("Plug in tonight")
	ta.plug(true, 38)
	ta.expectSent("-Plug in tonight")
	ta.at(mon(22, 46))
	ta.check()
	ta.expectSent()

	// The usual reminder already came (just before quiet hours, at 22:45):
	// no evening reminder after it.
	ta = start("22:50")
	ta.at(mon(22, 45))
	ta.check()
	ta.expectSent("Plug in to charge cheaply")
	ta.at(mon(22, 50))
	ta.check()
	ta.expectSent()
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

// sunnyWednesday is a demo morning with strong sun (the stub's 2.6 kW peak).
const sunnyWednesday = "2026-11-11T10:15:00+01:00"

// A sunny Wednesday in the demo: today's solar surplus while the car is home
// (late morning to mid-afternoon) costs much less than the past week's
// lowest prices, while the grid in those hours doesn't: the advice is to
// charge extra from the sun, with no plan; an unplugged car at home gets a
// reminder to plug in, cleared once it is plugged in.
func TestPlugInForTheSun(t *testing.T) {
	ta := newTestAppAt(t, "", sunnyWednesday)
	ta.pastWeek(0.20)
	ta.notifications(nil)
	ta.check()
	ta.sent() // other reminders are not under test

	var p struct {
		Advice struct {
			Days []struct {
				Date           string     `json:"date"`
				Recommendation string     `json:"recommendation"`
				Solar          bool       `json:"solar"`
				SolarUntil     *time.Time `json:"solarUntil"`
				Summary        string     `json:"summary"`
				Action         string     `json:"action"`
			} `json:"days"`
		} `json:"advice"`
	}
	ta.json("GET", "/api/plan", nil, http.StatusOK, &p)
	wed := p.Advice.Days[0]
	if wed.Date != "2026-11-11" || wed.Recommendation != "charge_extra" || !wed.Solar || wed.Action != "" {
		t.Fatalf("Wednesday's advice = %+v", wed)
	}
	if !strings.HasPrefix(wed.Summary, "Today's sun charges at about €0.") || !strings.Contains(wed.Summary, "without the tax you don't pay on own solar") ||
		!strings.HasSuffix(wed.Summary, "Keep the car plugged in.") {
		t.Errorf("summary = %q", wed.Summary)
	}

	n := ta.notification("solar:2026-11-11")
	if n.Title != "[EVCC Planner] ☀️ Plug in for the sun" || !strings.Contains(n.Message, "Plug in to use it.") || !strings.Contains(n.Message, "without the tax on own solar") ||
		n.Latest == nil || !n.Latest.Equal(*wed.SolarUntil) {
		t.Fatalf("solar reminder = %+v", n)
	}
	// Once a day.
	ta.check()
	for _, title := range ta.sent() {
		if title == "Plug in for the sun" {
			t.Error("sent the solar reminder again")
		}
	}
	// Plugged in: EVCC charges from the sun, and the reminder is cleared.
	ta.plug(true, 40)
	ta.check()
	if n := ta.notification("solar:2026-11-11"); n.State != model.NotifyDone {
		t.Errorf("after plugging in the reminder is %s", n.State)
	}
}

// The solar reminder can be turned off.
func TestNoSunReminderWhenOff(t *testing.T) {
	ta := newTestAppAt(t, "", sunnyWednesday)
	ta.pastWeek(0.20)
	ta.notifications(func(s map[string]any) { s["solar"] = false })
	ta.check()
	if n, _ := ta.app.Store.NotificationByKey("solar:2026-11-11"); n != nil {
		t.Errorf("sent with solar reminders off: %+v", n)
	}
}

// Back home from the day's last trip (by the car location sensor) and
// unplugged while the car still needs charging: a reminder right away,
// once, also in quiet hours. Other plug-in reminders wait half an hour.
func TestBackHomeReminder(t *testing.T) {
	mon := func(h, m int) time.Time { return time.Date(2026, 11, 9, h, m, 0, 0, ams) }
	// Monday evening, unplugged at 38%: Tuesday's school run needs 42%.
	start := func(away time.Time) *testApp {
		ta := newTestApp(t, "")
		ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
		ta.notifications(nil)
		ta.phone().MoveCar("not_home", away)
		ta.at(away)
		ta.check()
		ta.phone().Take()
		return ta
	}
	arrive := func(ta *testApp, at time.Time) {
		ta.phone().MoveCar("home", at)
		ta.at(at.Add(time.Minute))
		ta.check()
	}

	ta := start(mon(18, 0))
	ta.pastWeek(0.40) // Tuesday's cheap night: charge extra
	arrive(ta, mon(19, 5))
	ta.expectSent("Back home: plug in")
	n := ta.notification(fmt.Sprintf("arrived:%d", mon(19, 5).Unix()))
	tue := ta.advice().day("2026-11-10")
	if want := "School drop-off needs 42% by Tue 07:48. " + tue.Summary + " Create the plan in the planner."; n.Message != want ||
		!n.Urgent || n.Kind != "arrived" || len(n.Verbs) != 2 {
		t.Fatalf("back home = %+v, want %q", n, want)
	}
	ta.check()
	ta.expectSent()
	// Plugging in clears it.
	ta.plug(true, 38)
	ta.expectSent("-Back home: plug in")

	// In quiet hours too; the early reminder (due 22:45) waits half an hour
	// after it, and the quiet hours then keep it back.
	ta = start(mon(21, 0))
	arrive(ta, mon(22, 40))
	ta.expectSent("Back home: plug in")
	ta.at(mon(22, 50))
	ta.check()
	ta.expectSent()
	ta = start(mon(23, 0))
	arrive(ta, mon(23, 30))
	ta.expectSent("Back home: plug in")
	if n := ta.notification(fmt.Sprintf("arrived:%d", mon(23, 30).Unix())); n.Message != "School drop-off needs 42% by Tue 07:48." {
		t.Errorf("back home in quiet hours = %q", n.Message)
	}

	// Another trip later today: not the last one yet (that trip's own
	// reminders still apply).
	ta = start(mon(18, 0))
	ta.json("POST", "/api/trips", map[string]any{"title": "Gym", "destination": "Office", "date": "2026-11-09", "start": "21:00", "end": "22:00"},
		http.StatusCreated, nil)
	arrive(ta, mon(19, 5))
	for _, title := range ta.sent() {
		if title == "Back home: plug in" {
			t.Error("back home before the day's last trip")
		}
	}

	// In quiet hours it follows its own setting, not the one for urgent
	// reminders; snoozed, it comes again in quiet hours too.
	ta = start(mon(23, 0))
	ta.notifications(func(s map[string]any) { s["urgentInQuiet"] = false })
	arrive(ta, mon(23, 30))
	ta.expectSent("Back home: plug in")
	n = ta.notification(fmt.Sprintf("arrived:%d", mon(23, 30).Unix()))
	if err := ta.app.Service.HandleAction(context.Background(), "EVCCP_SNOOZE_"+itoa(n.ID)); err != nil {
		t.Fatal(err)
	}
	ta.phone().Take()
	ta.at(mon(23, 31).Add(31 * time.Minute))
	ta.check()
	ta.expectSent("Back home: plug in")

	// Turned off, or kept out of quiet hours at night: nothing.
	ta = start(mon(18, 0))
	ta.notifications(func(s map[string]any) { s["arrival"] = false })
	arrive(ta, mon(19, 5))
	ta.expectSent()
	ta = start(mon(23, 0))
	ta.notifications(func(s map[string]any) { s["arrivalInQuiet"] = false })
	arrive(ta, mon(23, 30))
	ta.expectSent()
}

// downPhone turns notifications on (without questions) with a phone that
// can't be reached until restored. Its check reports the error of the sends
// it tried; sends records the activity log's notification entries since the
// last look, failures marked with "!".
type downPhone struct {
	ta  *testApp
	log *eventLog
}

const unreachable = "phone unreachable"

func newDownPhone(ta *testApp) *downPhone {
	ta.t.Helper()
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.notifications(func(s map[string]any) { s["questions"] = false })
	ta.phone().Take()
	ta.phone().Fail = errors.New(unreachable)
	p := &downPhone{ta: ta, log: &eventLog{ta: ta}}
	p.log.fresh()
	return p
}

func (p *downPhone) check(at time.Time) error {
	p.ta.t.Helper()
	p.ta.at(at)
	p.ta.poll()
	if err := p.ta.app.Service.SyncPlan(context.Background()); err != nil {
		p.ta.t.Fatal(err)
	}
	return p.ta.app.Service.CheckNotifications(context.Background())
}

// fails checks at a time when a send is tried and fails.
func (p *downPhone) fails(at time.Time) {
	p.ta.t.Helper()
	if err := p.check(at); err == nil || !strings.Contains(err.Error(), unreachable) {
		p.ta.t.Fatalf("check at %s = %v, want the phone's error", at.Format("15:04"), err)
	}
}

func (p *downPhone) quiet(at time.Time) {
	p.ta.t.Helper()
	if err := p.check(at); err != nil {
		p.ta.t.Fatalf("check at %s = %v", at.Format("15:04"), err)
	}
}

func (p *downPhone) sends(want ...string) {
	p.ta.t.Helper()
	var got []string
	for _, e := range p.log.fresh() {
		if e.Action == "notification.send" {
			mark := ""
			if e.Outcome == model.OutcomeFailure {
				mark = "!"
			}
			got = append(got, mark+appTag.ReplaceAllString(e.Summary, ""))
		}
	}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		p.ta.t.Fatalf("activity %q, want %q", got, want)
	}
}

func TestFailedNotificationsAreTriedAgain(t *testing.T) {
	ta := newTestApp(t, "")
	p := newDownPhone(ta)
	mon := func(h, m int) time.Time { return time.Date(2026, 11, 9, h, m, 0, 0, ams) }
	tue := func(h, m int) time.Time { return time.Date(2026, 11, 10, h, m, 0, 0, ams) }
	const early = "early:recurring:1:2026-11-10"

	// The early reminder can't be sent: it is tried again in 2 minutes, and
	// Settings → Connections says so.
	p.fails(mon(22, 46))
	ta.expectSent()
	n := ta.notification(early)
	if n.State != model.NotifyRetrying || n.Tries != 1 || !n.RetryAt.Equal(mon(22, 48)) ||
		n.Note != "Not sent (mobile_app_demo_phone: phone unreachable); trying again at 22:48" || !n.Open() {
		t.Fatalf("after the first failure = %+v", n)
	}
	p.sends(`!Sending "Plug in to charge cheaply" failed; trying again at 22:48`)
	if c := connection(ta, "Phone notifications"); c.Status != "error" ||
		c.Detail != "1 phone; the last message failed at 22:46: mobile_app_demo_phone: phone unreachable" {
		t.Errorf("connection = %+v", c)
	}

	// Not before its time; the next failure waits 5 minutes, unlogged.
	p.quiet(mon(22, 47))
	p.fails(mon(22, 48))
	if n := ta.notification(early); n.Tries != 2 || !n.RetryAt.Equal(mon(22, 53)) {
		t.Fatalf("after the second failure = %+v", n)
	}
	p.sends()

	// The phone is back: the third try reaches it.
	ta.phone().Fail = nil
	p.quiet(mon(22, 53))
	ta.expectSent("Plug in to charge cheaply")
	if n := ta.notification(early); n.State != model.NotifySent || n.Tries != 0 || n.RetryAt != nil || n.Note != "Sent on try 3" {
		t.Fatalf("after the third try = %+v", n)
	}
	p.sends(`Sent "Plug in to charge cheaply" to 1 phone on try 3`)
	if c := connection(ta, "Phone notifications"); c.Status == "error" {
		t.Errorf("connection after a send = %+v", c)
	}

	// The urgent reminder (it may come in quiet hours) fails four times:
	// given up after waits of 2, 5, and 15 minutes.
	ta.phone().Fail = errors.New(unreachable)
	p.fails(tue(6, 10))
	p.fails(tue(6, 12))
	p.fails(tue(6, 17))
	p.quiet(tue(6, 31))
	p.fails(tue(6, 32))
	n = ta.notification("urgent:recurring:1:2026-11-10")
	if n.State != model.NotifyFailed || n.Tries != 4 || n.RetryAt != nil || n.Note != "Not sent after 4 tries (mobile_app_demo_phone: phone unreachable)" || n.Open() {
		t.Fatalf("given up = %+v", n)
	}
	p.sends(`!Sending "Plug in by 06:36" failed; trying again at 06:12`, `!Sending "Plug in by 06:36" failed 4 times; given up`)
}

func TestRetriesEndWhenNoLongerNeeded(t *testing.T) {
	mon := func(h, m int) time.Time { return time.Date(2026, 11, 9, h, m, 0, 0, ams) }
	const early = "early:recurring:1:2026-11-10"
	stopped := func(ta *testApp, state, note string) {
		t.Helper()
		if n := ta.notification(early); n.State != state || n.Note != note || n.RetryAt != nil || n.Open() {
			t.Errorf("stopped = %+v", n)
		}
	}

	// Plugging in ends it, as it ends a reminder on the phone; there is
	// nothing to clear.
	ta := newTestApp(t, "")
	p := newDownPhone(ta)
	p.fails(mon(22, 46))
	ta.phone().Fail = nil
	ta.at(mon(22, 47))
	ta.plug(true, 38)
	ta.expectSent()
	stopped(ta, model.NotifyDone, "Not sent: no longer needed")
	p.sends(`!Sending "Plug in to charge cheaply" failed; trying again at 22:48`,
		`Stopped sending "Plug in to charge cheaply": no longer needed`)

	// A non-urgent reminder doesn't come in quiet hours (from 23:00).
	ta = newTestApp(t, "")
	p = newDownPhone(ta)
	p.fails(mon(22, 46))
	p.fails(mon(22, 48))
	p.fails(mon(22, 53))
	p.quiet(mon(23, 8))
	stopped(ta, model.NotifyFailed, "Not sent: quiet hours")
	p.sends(`!Sending "Plug in to charge cheaply" failed; trying again at 22:48`,
		`!Stopped sending "Plug in to charge cheaply": quiet hours`)

	// Nor more than 15 minutes after its time, e.g. after the checks paused.
	ta = newTestApp(t, "")
	p = newDownPhone(ta)
	p.fails(mon(22, 46))
	ta.phone().Fail = nil
	p.quiet(mon(22, 48).Add(16 * time.Minute))
	ta.expectSent()
	stopped(ta, model.NotifyFailed, "Not sent: too late to try again")
}

func TestInformationIsDoneOnceARetryArrives(t *testing.T) {
	ta := newTestApp(t, "")
	p := newDownPhone(ta)
	at := time.Date(2026, 11, 9, 20, 0, 0, 0, ams)
	ta.at(at)
	ta.plug(true, 38)
	ta.plug(false, 38) // Tuesday's school run needs 42%
	recent, _ := ta.app.Store.RecentNotifications(1)
	if len(recent) != 1 || recent[0].Kind != model.NotifyUnplugged || recent[0].State != model.NotifyRetrying {
		t.Fatalf("unplugged early = %+v", recent)
	}
	ta.phone().Fail = nil
	p.quiet(at.Add(2 * time.Minute))
	ta.expectSent("Unplugged early")
	if n, _ := ta.app.Store.Notification(recent[0].ID); n.State != model.NotifyDone || n.Note != "Sent on try 2" {
		t.Errorf("after the retry = %+v", n)
	}
}
