package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/evcc"
)

// replay serves a captured EVCC state and refuses every write.
type replay struct{ st *evcc.State }

func (r replay) State(context.Context) (*evcc.State, error) { c := *r.st; return &c, nil }
func (replay) PreviewSocPlan(context.Context, int, int, time.Time) (*evcc.Preview, error) {
	return nil, errors.New("replay is read-only")
}
func (replay) SetSocPlan(context.Context, string, int, time.Time) (*evcc.SocPlan, error) {
	return nil, errors.New("replay is read-only")
}
func (replay) DeleteSocPlan(context.Context, string) error { return errors.New("replay is read-only") }
func (replay) SetSmartCostLimit(context.Context, int, *float64) error {
	return errors.New("replay is read-only")
}

// TestAdviceWithRealEVCCState runs the advice on a real EVCC payload kept out
// of Git (testdata/private); it skips when the file is absent.
func TestAdviceWithRealEVCCState(t *testing.T) {
	body, err := os.ReadFile("../../testdata/private/evcc-state.json")
	if err != nil {
		t.Skip("no private EVCC state in testdata/private (kept out of Git)")
	}
	st, err := evcc.ParseState(body)
	if err != nil {
		t.Fatal(err)
	}
	ta := newTestApp(t, "")
	ta.app.Service.EVCC = replay{st}
	ta.at(time.Date(2026, 11, 10, 15, 10, 14, 0, ams)) // when the payload was captured
	ctx := context.Background()
	if err := ta.app.Service.PollEVCC(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.Service.RefreshForecasts(ctx, true); err != nil {
		t.Fatal(err)
	}
	p := ta.advice()
	for _, d := range p.Advice.Days {
		price := "-"
		if d.Price != nil {
			price = d.Price.Basis
		}
		t.Logf("%s %-12s conf=%d action=%-10s price=%s sun=%v/%v (%s)", d.Date, d.Recommendation, d.Confidence, d.Action,
			price, val(d.SolarKWh), val(d.SolarHomeKWh), d.SunSource)
	}
	tue, wed, thu := p.day("2026-11-10"), p.day("2026-11-11"), p.day("2026-11-12")
	// Published prices: today's remaining slots, tomorrow's full day.
	if tue.Price == nil || tue.Price.Basis != "published" || tue.Price.Value < 0.55 || tue.Price.Value > 0.58 {
		t.Errorf("today = %+v", tue.Price)
	}
	if wed.Price == nil || wed.Price.Basis != "published" || wed.Price.Value < 0.505 || wed.Price.Value > 0.515 {
		t.Errorf("tomorrow = %+v", wed.Price)
	}
	// Lowest slots: today's remaining one (€0.3869 at 23:45) and tomorrow's
	// (€0.3696 at 03:45).
	if tue.Price.Min == nil || *tue.Price.Min != 0.3869 || !strings.HasPrefix(tue.Price.MinAt, "2026-11-10T23:45") ||
		wed.Price.Min == nil || *wed.Price.Min != 0.3696 || !strings.HasPrefix(wed.Price.MinAt, "2026-11-11T03:45") {
		t.Errorf("lowest prices: today %v at %s, tomorrow %v at %s", val(tue.Price.Min), tue.Price.MinAt,
			val(wed.Price.Min), wed.Price.MinAt)
	}
	var soc struct {
		SoC struct {
			Live    bool     `json:"live"`
			RangeKm *float64 `json:"rangeKm"`
		} `json:"soc"`
	}
	ta.json("GET", "/api/plan", nil, http.StatusOK, &soc)
	if !soc.SoC.Live || soc.SoC.RangeKm == nil || *soc.SoC.RangeKm != 107 {
		t.Errorf("EVCC's range = %+v", soc.SoC)
	}
	// EVCC's solar for today (remaining) and tomorrow; KNMI after that.
	if tue.SunSource != "evcc" || tue.SolarKWh == nil || *tue.SolarKWh < 0.7 || *tue.SolarKWh > 0.85 {
		t.Errorf("today's solar = %v (%s)", val(tue.SolarKWh), tue.SunSource)
	}
	if wed.SunSource != "evcc" || wed.SolarKWh == nil || *wed.SolarKWh < 3.7 || *wed.SolarKWh > 3.9 {
		t.Errorf("tomorrow's solar = %v (%s)", val(wed.SolarKWh), wed.SunSource)
	}
	if thu.SunSource != "knmi" || thu.SolarKWh != nil {
		t.Errorf("after tomorrow = %v (%s)", val(thu.SolarKWh), thu.SunSource)
	}
	var prop struct {
		Available      bool     `json:"available"`
		SmartCostLimit *float64 `json:"smartCostLimit"`
		Notes          []string `json:"notes"`
	}
	ta.json("GET", "/api/advice/2026-11-10/proposal?kind=postpone", nil, http.StatusOK, &prop)
	if !prop.Available || prop.SmartCostLimit == nil || *prop.SmartCostLimit != 0.2 {
		t.Errorf("postpone proposal should see the real cheap-charging limit: %+v", prop)
	}
	// Every price before tomorrow is far above the €0.20 limit.
	if !strings.Contains(strings.Join(prop.Notes, " "), "postponing changes nothing in practice") {
		t.Errorf("notes = %v", prop.Notes)
	}
}

func val(p *float64) any {
	if p == nil {
		return "-"
	}
	return *p
}
