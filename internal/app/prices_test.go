package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

// publishing is the demo EVCC on Monday evening with tonight's prices set,
// and Tuesday's, all at one price, published only when asked.
type publishing struct {
	*evcc.Stub
	tonight, tuesday float64
	published        bool
}

func (p *publishing) State(ctx context.Context) (*evcc.State, error) {
	st, err := p.Stub.State(ctx)
	if err != nil {
		return nil, err
	}
	var slots []evcc.Rate
	for _, r := range st.PriceSlots {
		switch day := localtime.Date(r.Start, ams); {
		case day == "2026-11-09":
			r.Value = p.tonight
		case !p.published:
			continue
		case day == "2026-11-10":
			r.Value = p.tuesday
		}
		slots = append(slots, r)
	}
	st.PriceSlots, st.Solar = slots, nil
	return st, nil
}

// publish makes Tuesday's prices appear, as EVCC does around 14:00.
func (ta *testApp) publish(p *publishing) {
	ta.t.Helper()
	p.published = true
	ta.poll()
	if err := ta.app.Service.ReviewNewPrices(context.Background()); err != nil {
		ta.t.Fatal(err)
	}
}

func newPublishing(ta *testApp, tonight, tuesday float64) *publishing {
	p := &publishing{Stub: ta.app.Stub, tonight: tonight, tuesday: tuesday}
	ta.app.Service.UseIntegrations(func(i *service.Integrations) { i.EVCC = p })
	ta.poll()
	// The first look only learns where the prices end.
	if err := ta.app.Service.ReviewNewPrices(context.Background()); err != nil {
		ta.t.Fatal(err)
	}
	return p
}

func TestNewPricesUpdateExtraPlansMadeAutomatically(t *testing.T) {
	for _, c := range []struct {
		name    string
		tuesday float64
		soc     float64 // the new extra plan; 0: none
		note    string
	}{
		{"Tuesday is cheaper still: ended", 0.05, 0, "New prices: the advice no longer charges extra. "},
		{"a deeper discount: replaced", 0.25, 66, "New prices: the advice now charges extra to 66% by Tue 00:00. "},
	} {
		t.Run(c.name, func(t *testing.T) {
			ta := newTestApp(t, "")
			ta.pastWeek(0.40)
			// Monday evening at €0.10, Tuesday not published yet: charge extra
			// to 60% against the outlook, created automatically.
			p := newPublishing(ta, 0.10, c.tuesday)
			var as map[string]any
			ta.json("GET", "/api/settings/advice", nil, http.StatusOK, &as)
			as["autoApply"], as["autoMinConfidence"] = true, 2
			ta.json("PUT", "/api/settings/advice", as, http.StatusOK, nil)
			if err := ta.app.Service.AutoApply(context.Background()); err != nil {
				t.Fatal(err)
			}
			if plan := ta.evccPlan(); plan == nil || plan.Soc != 60 {
				t.Fatalf("extra plan before the prices = %+v", plan)
			}
			log := &eventLog{ta: ta}
			log.fresh()

			ta.publish(p)
			var ended, active *adjustment
			for _, a := range ta.advice().Advice.Adjustments {
				switch a.Status {
				case model.AdjustCancelled:
					ended = &a
				case model.AdjustActive:
					active = &a
				}
			}
			if ended == nil || !strings.HasPrefix(ended.Note, c.note) {
				t.Fatalf("ended = %+v, want a note starting %q", ended, c.note)
			}
			switch plan := ta.evccPlan(); {
			case c.soc == 0 && (active != nil || plan != nil):
				t.Errorf("still planned: %+v, EVCC %+v", active, plan)
			case c.soc > 0 && (active == nil || active.Plan.SoC != c.soc || plan == nil || plan.Soc != c.soc):
				t.Errorf("replacement = %+v, EVCC %+v", active, plan)
			}
			for _, e := range log.fresh() {
				if e.Source != model.SourceAutomation {
					t.Errorf("event from %s: %+v", e.Source, e)
				}
			}
			// Once per publication.
			ta.poll()
			if err := ta.app.Service.ReviewNewPrices(context.Background()); err != nil {
				t.Fatal(err)
			}
			log.none("a second look at the same prices")
		})
	}
}

func TestNewPricesRaiseAChosenPlan(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	night := 0.20
	ta.app.Service.UseIntegrations(func(i *service.Integrations) { i.EVCC = pricedEVCC{Stub: ta.app.Stub, night: &night} })
	ctx := context.Background()
	tue := time.Date(2026, 11, 10, 7, 48, 0, 0, ams)
	ta.plug(true, 30)
	// A plan set in EVCC with at least the school run's 42% pauses automatic
	// plans, although charging ahead aims for 46%.
	ta.app.Stub.SetSocPlan(ctx, evcc.StubVehicle, 44, tue)
	ta.sync()
	if c := ta.control(); !c.Paused || c.Target.SoC != 46 {
		t.Fatalf("control = %+v", c)
	}
	log := &eventLog{ta: ta}
	log.fresh()
	// Between publications nothing changes; new prices raise it to 46%.
	if err := ta.app.Service.ReviewNewPrices(ctx); err != nil {
		t.Fatal(err)
	}
	log.none("prices seen before")
	if err := ta.app.Store.SetMeta("prices.reviewed", "2026-11-09T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.Service.ReviewNewPrices(ctx); err != nil {
		t.Fatal(err)
	}
	if p := ta.evccPlan(); p == nil || p.Soc != 46 || !p.Time.Equal(tue) {
		t.Fatalf("plan = %+v, want 46%% by 07:48", p)
	}
	log.summaries("New prices: raised the chosen plan (44% by 2026-11-10 07:48): sent plan 46% by Tue 10 Nov, 07:48 to EVCC")
	// It is still the chosen plan: automatic plans stay paused.
	ta.sync()
	if c := ta.control(); !c.Paused || c.PausedPlan == nil || c.PausedPlan.SoC != 46 {
		t.Errorf("control after raising = %+v", c)
	}
	log.none("the next check")
}

func TestNewPricesNotifyWhileUnplugged(t *testing.T) {
	ta := newTestApp(t, "")
	ta.pastWeek(0.40)
	ta.notifications(func(s map[string]any) { s["questions"] = false })
	p := newPublishing(ta, 0.10, 0.05)
	// Monday evening: charge extra tonight against the outlook.
	ta.check()
	ta.expectSent("Plug in to charge cheaply", "Plug in for charge extra")
	ta.check()
	ta.expectSent()

	// Tuesday's prices: Monday now defers, and Tuesday's cheap night is the
	// charge extra to plug in for.
	p.published = true
	ta.check()
	ta.expectSent("-Plug in for charge extra", "New prices: plug in")
	n := ta.notification("prices:" + itoa(time.Date(2026, 11, 11, 0, 0, 0, 0, ams).Unix()))
	if n.Kind != model.NotifyPrices || !strings.Contains(n.Message, "Charge extra to") ||
		!strings.HasSuffix(n.Message, "Create the plan in the planner. Plug in so it can charge.") {
		t.Errorf("notification = %+v", n)
	}
	ta.check()
	ta.expectSent()
	// Plugging in clears it.
	ta.plug(true, 38)
	ta.expectSent("-Plug in to charge cheaply", "-New prices: plug in")

	// Off in Settings: nothing.
	ta2 := newTestApp(t, "")
	ta2.pastWeek(0.40)
	ta2.notifications(func(s map[string]any) {
		s["questions"], s["prices"], s["extra"], s["early"] = false, false, false, false
	})
	p2 := newPublishing(ta2, 0.10, 0.05)
	ta2.check()
	p2.published = true
	ta2.check()
	ta2.expectSent()
}

func TestNewPricesRaiseAnExtraPlanMadeByHand(t *testing.T) {
	ta := newTestApp(t, "")
	ta.pastWeek(0.40)
	p := newPublishing(ta, 0.10, 0.25)
	// An extra plan made by hand from Monday's advice, lower than it says.
	var prop service.ProposalView
	ta.json("GET", "/api/advice/2026-11-09/proposal?kind=extra_plan", nil, http.StatusOK, &prop)
	body := map[string]any{"kind": "extra_plan", "date": "2026-11-09", "soc": 50, "readyBy": prop.ReadyBy,
		"expiresAt": prop.ExpiresAt, "confirm": true}
	var a adjustment
	ta.json("POST", "/api/adjustments", body, http.StatusCreated, &a)
	log := &eventLog{ta: ta}
	log.fresh()

	// Tuesday's prices make tonight a deeper discount: charge extra to 66%.
	ta.publish(p)
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 66 || !plan.Time.Equal(*prop.ReadyBy) {
		t.Fatalf("plan = %+v, want 66%% by %s", plan, prop.ReadyBy)
	}
	e := log.one("adjustment.update", model.SourceAutomation)
	if !strings.HasPrefix(e.Summary, "New prices: raised the extra plan to 66% by Mon 9 Nov, ") {
		t.Errorf("event = %+v", e)
	}
	if adj := ta.advice().Advice.Adjustments[0]; adj.Status != model.AdjustActive || adj.Plan.SoC != 66 || adj.Source != "ui" {
		t.Errorf("adjustment = %+v", adj)
	}

	// In Manual mode a plan you made is left as it is.
	ta2 := newTestApp(t, "")
	ta2.pastWeek(0.40)
	ta2.tripPlans(model.TripPlansManual)
	p2 := newPublishing(ta2, 0.10, 0.25)
	ta2.json("GET", "/api/advice/2026-11-09/proposal?kind=extra_plan", nil, http.StatusOK, &prop)
	body["readyBy"], body["expiresAt"] = prop.ReadyBy, prop.ExpiresAt
	ta2.json("POST", "/api/adjustments", body, http.StatusCreated, nil)
	ta2.publish(p2)
	if plan := ta2.evccPlan(); plan == nil || plan.Soc != 50 {
		t.Errorf("manual mode plan = %+v", plan)
	}
}
