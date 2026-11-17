package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/forecast"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/service"
)

// sundayOnly is the demo EVCC with only today's prices published, all at
// one price.
type sundayOnly struct {
	*evcc.Stub
	price float64
}

func (s sundayOnly) State(ctx context.Context) (*evcc.State, error) {
	st, err := s.Stub.State(ctx)
	if err != nil {
		return nil, err
	}
	var slots []evcc.Rate
	for _, r := range st.PriceSlots {
		if localtime.Date(r.Start, ams) == "2026-11-15" {
			r.Value = s.price
			slots = append(slots, r)
		}
	}
	st.PriceSlots = slots
	return st, nil
}

// calm is a week without wind or sun.
type calm struct{}

func (calm) Name() string { return "stub" }
func (calm) Fetch(ctx context.Context) (*forecast.Weather, error) {
	w := &forecast.Weather{SchemaVersion: forecast.WeatherSchemaVersion, Source: "calm", Days: []forecast.WeatherDay{}}
	day := time.Date(2026, 11, 15, 0, 0, 0, 0, ams)
	for i := 0; i < 7; i++ {
		bft, sun := 3, 20
		w.Days = append(w.Days, forecast.WeatherDay{Date: day.AddDate(0, 0, i).Format("2006-01-02"), WindBft: &bft, SunshinePct: &sun})
	}
	return w, nil
}

// cheapSunday is the case from 2026-11-15: Sunday 16:41, the car away on a
// visit until about 18:25, only Sunday published with every remaining hour
// 30% below the past week's average lowest price, and (with calm weather)
// no sign of cheaper days ahead. refresh polls EVCC and the forecasts.
func cheapSunday(t *testing.T) (ta *testApp, refresh func()) {
	t.Helper()
	ta = newTestAppAt(t, "", "2026-11-15T16:41:00+01:00")
	ctx := context.Background()
	ta.json("POST", "/api/trips", map[string]any{"title": "Visit", "destination": "Office", "date": "2026-11-15", "start": "16:30", "end": "17:30"},
		http.StatusCreated, nil)
	ta.app.Service.UseIntegrations(func(i *service.Integrations) {
		i.EVCC = sundayOnly{Stub: ta.app.Stub, price: 0.28}
		i.Forecasts.Weather = calm{}
	})
	refresh = func() {
		t.Helper()
		if err := ta.app.Service.PollEVCC(ctx); err != nil {
			t.Fatal(err)
		}
		if err := ta.app.Service.RefreshForecasts(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	ta.pastWeek(0.40)
	return ta, refresh
}

// On the cheap Sunday charge extra plans step 2 of the little level (30%
// cheaper → 80%), as far as the hours at home before midnight go.
func TestChargeExtraOnACheapSundayWhileAway(t *testing.T) {
	ta, refresh := cheapSunday(t)
	stubKNMI := forecast.StubKNMI{Clock: ta.app.Service.Clock, Location: ams}

	sun := ta.advice().day("2026-11-15")
	// The car is back at about 15% after the visit; 18:25 to midnight adds
	// about 31%.
	if sun.Recommendation != "charge_extra" || sun.Action != "extra_plan" || sun.Confidence != 2 || sun.Indication != "little" ||
		sun.TargetSoC != 46 {
		t.Fatalf("Sunday = %+v", sun)
	}
	if sun.Summary != "Charge extra to 46%: 18:25–00:00 averages €0.28, 30% or more below this week's lowest prices (€0.40)." {
		t.Errorf("summary = %q", sun.Summary)
	}
	if mon := ta.advice().day("2026-11-16"); mon.Recommendation != "none" || !mon.NoSuggestion {
		t.Errorf("Monday isn't published yet: %+v", mon)
	}
	var p struct {
		Available bool     `json:"available"`
		SoC       int      `json:"soc"`
		ReadyBy   string   `json:"readyBy"`
		Notes     []string `json:"notes"`
	}
	ta.json("GET", "/api/advice/2026-11-15/proposal?kind=extra_plan", nil, http.StatusOK, &p)
	notes := strings.Join(p.Notes, " ")
	if !p.Available || p.SoC != 46 || !strings.HasPrefix(p.ReadyBy, "2026-11-16T00:00:00") ||
		!strings.Contains(notes, "Charge extra's target: up to 46%") || strings.Contains(notes, "Covering every later trip") {
		t.Fatalf("proposal = %+v", p)
	}

	// A windy Tuesday ahead (the demo's KNMI outlook): the medium steps,
	// so 30% cheaper charges only to 40%.
	ta.app.Service.UseIntegrations(func(i *service.Integrations) { i.Forecasts.Weather = stubKNMI })
	refresh()
	if sun := ta.advice().day("2026-11-15"); sun.Indication != "medium" || sun.TargetSoC != 40 {
		t.Fatalf("Sunday with a windy Tuesday ahead = %+v", sun)
	}
}

// The evening of the cheap Sunday: back home and unplugged before the cheap
// hours, the car gets a reminder to plug in (and to create the plan, as
// there is none yet), once; an extra plan created automatically is
// announced; plugging in clears the reminder.
func TestPlugInForChargeExtraAndAutomaticPlans(t *testing.T) {
	ta, _ := cheapSunday(t)
	ta.notifications(nil)
	ta.at(time.Date(2026, 11, 15, 18, 30, 0, 0, ams))
	ta.check()
	if titles := strings.Join(ta.sent(), ","); !strings.Contains(titles, "Plug in for charge extra") {
		t.Fatalf("sent %s", titles)
	}
	sun := ta.advice().day("2026-11-15")
	n := ta.notification("extra:2026-11-15")
	if n.Kind != "extra" || n.Message != sun.Summary+" Create the plan in the planner and plug in." ||
		n.Latest == nil || !n.Latest.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, ams)) {
		t.Fatalf("reminder = %+v (advice %q)", n, sun.Summary)
	}
	ta.check()
	for _, title := range ta.sent() {
		if title == "Plug in for charge extra" {
			t.Error("sent the charge extra reminder again")
		}
	}

	// Automatic plans: the extra plan is created, ready at midnight, and
	// announced.
	var s map[string]any
	ta.json("GET", "/api/settings/advice", nil, http.StatusOK, &s)
	s["autoApply"], s["autoMinConfidence"] = true, 2
	ta.json("PUT", "/api/settings/advice", s, http.StatusOK, nil)
	if err := ta.app.Service.AutoApply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if plan := ta.evccPlan(); plan == nil || plan.Soc != float64(sun.TargetSoC) {
		t.Fatalf("EVCC plan = %+v, want %d%%", plan, sun.TargetSoC)
	}
	if titles := ta.sent(); len(titles) != 1 || titles[0] != "Extra plan created" {
		t.Fatalf("sent %v", titles)
	}
	recent, _ := ta.app.Store.RecentNotifications(1)
	if want := fmt.Sprintf("The plan asks EVCC for %d%% by Mon 00:00 and ends then.", sun.TargetSoC); len(recent) != 1 ||
		recent[0].Kind != "extra_plan" || !strings.HasSuffix(recent[0].Message, want) || recent[0].State != "done" {
		t.Fatalf("announcement = %+v, want …%q", recent, want)
	}

	// Plugged in: the reminder is cleared.
	ta.plug(true, 40)
	ta.check()
	if n := ta.notification("extra:2026-11-15"); n.State != "done" {
		t.Errorf("after plugging in the reminder is %s", n.State)
	}
}

// The reminder can be turned off; so can the announcement.
func TestNoChargeExtraNotificationsWhenOff(t *testing.T) {
	ta, _ := cheapSunday(t)
	ta.notifications(func(s map[string]any) { s["extra"] = false })
	ta.at(time.Date(2026, 11, 15, 18, 30, 0, 0, ams))
	ta.check()
	var s map[string]any
	ta.json("GET", "/api/settings/advice", nil, http.StatusOK, &s)
	s["autoApply"], s["autoMinConfidence"] = true, 2
	ta.json("PUT", "/api/settings/advice", s, http.StatusOK, nil)
	if err := ta.app.Service.AutoApply(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"extra:2026-11-15"} {
		if n, _ := ta.app.Store.NotificationByKey(key); n != nil {
			t.Errorf("sent with charge extra reminders off: %+v", n)
		}
	}
	for _, title := range ta.sent() {
		if strings.Contains(title, "charge extra") || title == "Extra plan created" {
			t.Errorf("sent %q with charge extra reminders off", title)
		}
	}
}
