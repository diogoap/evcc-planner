package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

type controlView struct {
	Mode       string `json:"mode"`
	Paused     bool   `json:"paused"`
	PausedPlan *struct {
		SoC float64 `json:"soc"`
	} `json:"pausedPlan"`
	Target *struct {
		SoC     int    `json:"soc"`
		MinSoC  int    `json:"minSoc"`
		ReadyBy string `json:"readyBy"`
		TripKey string `json:"tripKey"`
		Covered bool   `json:"covered"`
	} `json:"target"`
}

func (ta *testApp) control() controlView {
	ta.t.Helper()
	var p struct {
		Control controlView `json:"control"`
	}
	ta.json("GET", "/api/plan", nil, http.StatusOK, &p)
	return p.Control
}

// sync runs the automatic plan check on a fresh EVCC state.
func (ta *testApp) sync() {
	ta.t.Helper()
	ta.poll()
	if err := ta.app.Service.SyncPlan(context.Background()); err != nil {
		ta.t.Fatal(err)
	}
}

func (ta *testApp) plug(connected bool, soc float64) {
	ta.t.Helper()
	ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": connected, "soc": soc}, http.StatusOK, nil)
}

func (ta *testApp) setting(key string, v any) {
	ta.t.Helper()
	ta.json("PUT", "/api/settings", map[string]any{key: v}, http.StatusOK, nil)
}

// summaries asserts the actions and summaries recorded since the last check.
func (l *eventLog) summaries(want ...string) {
	l.ta.t.Helper()
	got := l.fresh()
	if len(got) != len(want) {
		l.ta.t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i, e := range got {
		if e.Summary != want[i] || e.Outcome != model.OutcomeSuccess {
			l.ta.t.Errorf("event %d = %q (%s), want %q", i, e.Summary, e.Outcome, want[i])
		}
	}
}

func TestAutomaticPlansFollowTheTrips(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.setting("chargeAhead", false) // minimum targets only here
	log := &eventLog{ta: ta}
	log.fresh()
	ctx := context.Background()
	evccPlan := func(soc float64, at time.Time) {
		t.Helper()
		p := ta.evccPlan()
		if p == nil || p.Soc != soc || !p.Time.Equal(at) {
			t.Fatalf("EVCC plan = %+v, want %.0f%% by %s", p, soc, at)
		}
	}
	tuesday := time.Date(2026, 11, 10, 7, 48, 0, 0, ams)
	wednesday := time.Date(2026, 11, 11, 7, 0, 0, 0, ams)

	if c := ta.control(); c.Mode != model.TripPlansAutomatic || c.Paused || c.Target == nil ||
		c.Target.SoC != 42 || c.Target.MinSoC != 42 || c.Target.Covered || c.Target.TripKey != "recurring:1:2026-11-10" {
		t.Fatalf("control = %+v", c)
	}

	// Unplugged: nothing is written.
	ta.sync()
	if p := ta.evccPlan(); p != nil {
		t.Fatalf("written while unplugged: %+v", p)
	}
	log.none("checking while unplugged")

	// Plugging in writes the target; further checks change nothing.
	ta.plug(true, 30)
	evccPlan(42, tuesday)
	log.summaries("Car plugged in: sent plan 42% by Tue 10 Nov, 07:48 to EVCC")
	ta.sync()
	ta.sync()
	log.none("checking again")

	// A setting that changes the target updates the plan.
	ta.setting("arrivalReservePct", 20)
	ta.sync()
	evccPlan(47, tuesday)
	log.summaries("Changed settings: arrival reserve", "Automatic plan: sent plan 47% by Tue 10 Nov, 07:48 to EVCC")
	ta.setting("arrivalReservePct", 15)
	ta.sync()
	evccPlan(42, tuesday)
	log.fresh()

	// Turning Tuesday morning's trips off: 30% covers the pickup, so the
	// planner's plan is removed; turning them on again writes it again.
	for _, key := range []string{"recurring:1:2026-11-10", "calendar:2ac7eab85bc0ae48"} {
		ta.json("PATCH", "/api/trips/"+key, map[string]any{"enabled": false}, http.StatusOK, nil)
	}
	log.fresh()
	ta.sync()
	if p := ta.evccPlan(); p != nil {
		t.Fatalf("plan kept although the charge covers the next trip: %+v", p)
	}
	log.summaries("Automatic plan: removed the planner's plan (42% by 2026-11-10 07:48); the charge already covers School pickup (19%)")
	for _, key := range []string{"recurring:1:2026-11-10", "calendar:2ac7eab85bc0ae48"} {
		ta.json("PATCH", "/api/trips/"+key, map[string]any{"enabled": true}, http.StatusOK, nil)
	}
	log.fresh()
	ta.sync()
	evccPlan(42, tuesday)
	log.summaries("Automatic plan: sent plan 42% by Tue 10 Nov, 07:48 to EVCC")

	// A plan chosen in EVCC that covers the departure pauses automatic
	// changes, even when it is ready only after the departure.
	ta.app.Stub.SetSocPlan(ctx, evcc.StubVehicle, 80, wednesday)
	ta.sync()
	log.summaries("Automatic plans paused: plan chosen in EVCC (80% by 2026-11-11 07:00)")
	if c := ta.control(); !c.Paused || c.PausedPlan == nil || c.PausedPlan.SoC != 80 {
		t.Fatalf("control = %+v", c)
	}
	ta.setting("arrivalReservePct", 20)
	ta.sync()
	evccPlan(80, wednesday)
	ta.setting("arrivalReservePct", 15)
	log.fresh()

	// When the trips need more than the chosen plan, it is replaced.
	ta.app.Stub.SetSocPlan(ctx, evcc.StubVehicle, 40, wednesday)
	ta.sync()
	evccPlan(42, tuesday)
	log.summaries(
		"Automatic plans resumed: School drop-off now needs 42%, more than the chosen plan (40% by 2026-11-11 07:00)",
		"Plan in EVCC (40% by 2026-11-11 07:00) is below the 42% School drop-off needs: sent plan 42% by Tue 10 Nov, 07:48 to EVCC")

	// A chosen plan that finishes (EVCC removes it) resumes automatic plans.
	ta.app.Stub.SetSocPlan(ctx, evcc.StubVehicle, 80, wednesday)
	ta.sync()
	ta.app.Stub.DeleteSocPlan(ctx, evcc.StubVehicle)
	ta.sync()
	evccPlan(42, tuesday)
	log.summaries("Automatic plans paused: plan chosen in EVCC (80% by 2026-11-11 07:00)",
		"Automatic plans resumed: the chosen plan has finished",
		"Automatic plan: sent plan 42% by Tue 10 Nov, 07:48 to EVCC")

	// A lower chosen plan is replaced right away.
	ta.app.Stub.SetSocPlan(ctx, evcc.StubVehicle, 20, tuesday)
	ta.sync()
	evccPlan(42, tuesday)
	log.summaries("Plan in EVCC (20% by 2026-11-10 07:48) is below the 42% School drop-off needs: sent plan 42% by Tue 10 Nov, 07:48 to EVCC")

	// Resume hands the chosen plan to the planner.
	ta.app.Stub.SetSocPlan(ctx, evcc.StubVehicle, 80, wednesday)
	ta.sync()
	log.fresh()
	ta.json("POST", "/api/control/resume", map[string]any{}, http.StatusOK, nil)
	evccPlan(42, tuesday)
	got := log.fresh()
	if len(got) != 2 || got[0].Action != "control.resume" || got[0].Source != model.SourceUI ||
		got[1].Summary != "Automatic plan: sent plan 42% by Tue 10 Nov, 07:48 to EVCC" || got[1].Source != model.SourceAutomation {
		t.Fatalf("resume = %+v", got)
	}
	ta.json("POST", "/api/control/resume", map[string]any{}, http.StatusBadRequest, nil)

	// A plan sent by hand is a chosen plan too.
	ta.json("POST", "/api/evcc/plan", map[string]any{"soc": 60, "time": tuesday, "confirm": true}, http.StatusOK, nil)
	log.summaries("Sent plan 60% by Tue 10 Nov, 07:48 to EVCC",
		"Automatic plans paused: plan sent from the planner (60% by 2026-11-10 07:48)")

	// Leaving automatic mode ends the pause.
	ta.setting("tripPlans", model.TripPlansManual)
	if c := ta.control(); c.Paused || c.Mode != model.TripPlansManual {
		t.Fatalf("control in manual mode = %+v", c)
	}
	log.one("settings.update", model.SourceUI)
	ta.json("POST", "/api/control/resume", map[string]any{}, http.StatusBadRequest, nil)

	// A pause also ends while the car is unplugged, once the chosen plan's
	// time has passed; nothing is written until it is plugged in again.
	ta.setting("tripPlans", model.TripPlansAutomatic)
	ta.app.Stub.SetSocPlan(ctx, evcc.StubVehicle, 80, wednesday)
	ta.sync()
	ta.plug(false, 0)
	log.fresh()
	ta.at(wednesday.Add(30 * time.Minute))
	ta.sync()
	log.summaries("Automatic plans resumed: the chosen plan has finished")
	evccPlan(80, wednesday)
}

func TestExtraPlansShareTheAutomaticPlan(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.setting("chargeAhead", false)
	ta.plug(true, 30)
	log := &eventLog{ta: ta}
	log.fresh()

	// An extra plan with more charge by an earlier time covers the trip:
	// the automatic check leaves it alone.
	var a adjustment
	ta.json("POST", "/api/adjustments", map[string]any{
		"kind": "extra_plan", "date": "2026-11-10", "soc": 60, "confirm": true,
		"readyBy": "2026-11-10T03:00:00+01:00", "expiresAt": "2026-11-10T04:00:00+01:00",
	}, http.StatusCreated, &a)
	ta.sync()
	if p := ta.evccPlan(); p.Soc != 60 {
		t.Fatalf("extra plan replaced: %+v", p)
	}
	log.one("adjustment.create", model.SourceUI)

	// When trips need more, the merged plan has the higher SoC by the
	// earlier time.
	ta.setting("arrivalReservePct", 40)
	ta.sync()
	if p := ta.evccPlan(); p.Soc != 67 || !p.Time.Equal(time.Date(2026, 11, 10, 3, 0, 0, 0, ams)) {
		t.Fatalf("merged plan = %+v", p)
	}
	// A lower trip need leaves the extra plan's own target.
	ta.setting("arrivalReservePct", 20)
	ta.sync()
	if p := ta.evccPlan(); p.Soc != 60 {
		t.Fatalf("plan with the extra plan's target = %+v", p)
	}
	log.fresh()

	// At expiry the previous plan comes back, as in V2, and the automatic
	// check takes it over: it now updates it to the trip's 47%.
	ta.at(time.Date(2026, 11, 10, 4, 0, 0, 0, ams))
	ta.sync()
	if got, _ := ta.app.Store.Adjustment(a.ID); got.Status != model.AdjustRestored {
		t.Errorf("after expiry = %+v", got)
	}
	if p := ta.evccPlan(); p.Soc != 47 {
		t.Fatalf("plan after the extra plan = %+v, want the trip's 47%%", p)
	}
	log.summaries("Extra plan ended: Restored the previous plan: 42% by Tue 10 Nov, 07:48.",
		"Automatic plan: sent plan 47% by Tue 10 Nov, 07:48 to EVCC")
}

// pricedEVCC is the demo EVCC with other prices (night until Tuesday
// 07:00, then €0.32) and no solar forecast.
type pricedEVCC struct {
	*evcc.Stub
	night *float64
}

func (p pricedEVCC) State(ctx context.Context) (*evcc.State, error) {
	st, err := p.Stub.State(ctx)
	if err != nil {
		return nil, err
	}
	for i := range st.PriceSlots {
		st.PriceSlots[i].Value = 0.32
		if st.PriceSlots[i].Start.Before(time.Date(2026, 11, 10, 7, 0, 0, 0, ams)) {
			st.PriceSlots[i].Value = *p.night
		}
	}
	st.Solar = nil
	return st, nil
}

func TestChargeAheadInTheAutomaticPlan(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	night := 0.20
	ta.app.Service.UseIntegrations(func(i *service.Integrations) { i.EVCC = pricedEVCC{Stub: ta.app.Stub, night: &night} })
	log := &eventLog{ta: ta}
	log.fresh()

	// Tuesday's pickup (3.7% of the battery) costs €0.32/kWh at home before
	// it, €0.20 before the morning trips: it is charged ahead.
	ta.plug(true, 30)
	c := ta.control()
	if c.Target == nil || c.Target.MinSoC != 42 || c.Target.SoC != 46 {
		t.Fatalf("target = %+v", c.Target)
	}
	var p struct {
		Control struct {
			Target struct {
				Ahead []struct {
					TripKey    string   `json:"tripKey"`
					Title      string   `json:"title"`
					PriceAhead float64  `json:"priceAhead"`
					PriceLater *float64 `json:"priceLater"`
					SavingEUR  float64  `json:"savingEur"`
				} `json:"ahead"`
				Charging []struct{ Start, End string } `json:"charging"`
			} `json:"target"`
		} `json:"control"`
	}
	ta.json("GET", "/api/plan", nil, http.StatusOK, &p)
	ahead := p.Control.Target.Ahead
	if len(ahead) != 1 || ahead[0].TripKey != "recurring:2:2026-11-10" || ahead[0].PriceAhead != 0.2 ||
		ahead[0].PriceLater == nil || *ahead[0].PriceLater != 0.32 || ahead[0].SavingEUR <= 0 {
		t.Fatalf("ahead = %+v", ahead)
	}
	if len(p.Control.Target.Charging) == 0 {
		t.Error("no charging estimate")
	}
	log.summaries("Car plugged in: sent plan 46% by Tue 10 Nov, 07:48 to EVCC")

	// Once charged ahead, the pickup stays included while the night is no
	// dearer, even below the minimum saving...
	night = 0.29
	ta.sync()
	if p := ta.evccPlan(); p.Soc != 46 {
		t.Fatalf("plan with a small gap = %+v", p)
	}
	log.none("a smaller gap")
	// ...and drops out when the night becomes dearer.
	night = 0.33
	ta.sync()
	if p := ta.evccPlan(); p.Soc != 42 {
		t.Fatalf("plan with a dearer night = %+v", p)
	}
	log.summaries("Automatic plan: sent plan 42% by Tue 10 Nov, 07:48 to EVCC")
	// A small gap does not bring it back.
	night = 0.29
	ta.sync()
	log.none("a small gap without charging ahead before")

	// Off in Settings: the minimum target.
	night = 0.20
	ta.setting("chargeAhead", false)
	ta.sync()
	if c := ta.control(); c.Target.SoC != 42 {
		t.Errorf("target with charging ahead off = %+v", c.Target)
	}
}
