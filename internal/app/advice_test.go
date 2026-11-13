package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

var ams, _ = time.LoadLocation("Europe/Amsterdam")

type adviceDay struct {
	Date           string `json:"date"`
	Recommendation string `json:"recommendation"`
	NoSuggestion   bool   `json:"noSuggestion"`
	Reason         string `json:"reason"`
	Confidence     int    `json:"confidence"`
	Indicator      string `json:"indicator"`
	Action         string `json:"action"`
	ActionNote     string `json:"actionNote"`
	Dismissed      bool   `json:"dismissed"`
	DeferUntil     string `json:"deferUntil"`
	Price          *struct {
		Value float64  `json:"value"`
		Basis string   `json:"basis"`
		Min   *float64 `json:"min"`
		MinAt string   `json:"minAt"`
	} `json:"price"`
	SolarKWh     *float64    `json:"solarKWh"`
	SolarHomeKWh *float64    `json:"solarHomeKWh"`
	SunSource    string      `json:"sunSource"`
	Weather      *struct{}   `json:"weather"`
	Adjustment   *adjustment `json:"adjustment"`
}

type adjustment struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	Status    string `json:"status"`
	Note      string `json:"note"`
	ExpiresAt string `json:"expiresAt"`
	Plan      *struct {
		SoC float64 `json:"soc"`
	} `json:"plan"`
}

type advicePlan struct {
	Advice struct {
		Enabled   bool        `json:"enabled"`
		AutoApply bool        `json:"autoApply"`
		Days      []adviceDay `json:"days"`
		Sources   []struct {
			Name, Status, Detail string
		} `json:"sources"`
		Adjustments []adjustment `json:"adjustments"`
	} `json:"advice"`
}

func (ta *testApp) advice() advicePlan {
	var p advicePlan
	ta.json("GET", "/api/plan", nil, http.StatusOK, &p)
	return p
}

func (p advicePlan) day(date string) adviceDay {
	for _, d := range p.Advice.Days {
		if d.Date == date {
			return d
		}
	}
	return adviceDay{}
}

func (ta *testApp) at(t time.Time) { ta.app.Service.Clock = clock.Fixed{T: t} }

func (ta *testApp) evccPlan() *evcc.SocPlan {
	st, _ := ta.app.Stub.State(context.Background())
	return st.Vehicles[evcc.StubVehicle].Plan
}

func (ta *testApp) smartCostLimit() *float64 {
	st, _ := ta.app.Stub.State(context.Background())
	lp, _ := st.Loadpoint(1)
	return lp.SmartCostLimit
}

// poll is what the EVCC job does: refresh, then settle adjustments.
func (ta *testApp) poll() {
	ta.t.Helper()
	ctx := context.Background()
	if err := ta.app.Service.PollEVCC(ctx); err != nil {
		ta.t.Fatal(err)
	}
	if err := ta.app.Service.CheckAdjustments(ctx); err != nil {
		ta.t.Fatal(err)
	}
}

func TestDemoAdviceUsesPublishedPricesFirst(t *testing.T) {
	ta := newTestApp(t, "")
	a := ta.advice().Advice
	if !a.Enabled || a.AutoApply || len(a.Days) != 7 {
		t.Fatalf("advice = %+v", a)
	}
	mon, tue, wed, thu := a.Days[0], a.Days[1], a.Days[2], a.Days[3]
	// Monday evening's published prices are dearer than Tuesday's, and no
	// trip leaves before Tuesday: postpone optional charging (today only).
	if mon.Recommendation != "defer" || mon.Confidence != 3 || mon.DeferUntil != "2026-11-10" || mon.Action != "postpone" {
		t.Errorf("Monday = %+v", mon)
	}
	// Tuesday is published and the outlook's cheapest day.
	if tue.Recommendation != "charge_ahead" || tue.Price.Basis != "published" || tue.Confidence != 2 ||
		tue.Indicator == "" || tue.Action != "extra_plan" {
		t.Errorf("Tuesday = %+v", tue)
	}
	if !wed.NoSuggestion || wed.Action != "" || wed.Price.Basis != "weekly" {
		t.Errorf("Wednesday = %+v", wed)
	}
	if thu.Price == nil || thu.Price.Basis != "estimate" || thu.Price.Value < 0.29 || thu.Price.Value > 0.31 || thu.Price.Min != nil {
		t.Errorf("Thursday's converted estimate = %+v", thu.Price)
	}
	// Published days also give their lowest slot.
	if tue.Price.Min == nil || *tue.Price.Min >= tue.Price.Value || !strings.HasPrefix(tue.Price.MinAt, "2026-11-10T") {
		t.Errorf("Tuesday's lowest price = %v at %s (average %.3f)", tue.Price.Min, tue.Price.MinAt, tue.Price.Value)
	}
	statuses := map[string]string{}
	for _, s := range a.Sources {
		statuses[s.Name] = s.Status
	}
	if statuses["EVCC prices"] != "stub" || statuses["EVCC solar"] != "stub" || statuses["Tibber outlook"] != "stub" ||
		statuses["KNMI weather"] != "stub" {
		t.Errorf("sources = %+v", a.Sources)
	}
	// Local sun comes from EVCC's solar forecast (today and tomorrow); none
	// is left on Monday evening. Later days use KNMI's sunshine.
	if mon.SolarKWh == nil || *mon.SolarKWh != 0 || mon.SunSource != "evcc" {
		t.Errorf("Monday evening solar = %v (%s)", mon.SolarKWh, mon.SunSource)
	}
	if tue.SolarKWh == nil || *tue.SolarKWh < 5 || tue.SolarHomeKWh == nil || *tue.SolarHomeKWh >= *tue.SolarKWh {
		t.Errorf("Tuesday solar = %v, while home %v (the car is away part of the day)", tue.SolarKWh, tue.SolarHomeKWh)
	}
	if wed.SolarKWh != nil || wed.SunSource != "knmi" || thu.SunSource != "knmi" || thu.Weather == nil {
		t.Errorf("after tomorrow KNMI stands in: %v %s %s", wed.SolarKWh, wed.SunSource, thu.SunSource)
	}
}

func TestExtraPlanNeedsConfirmationAndRestores(t *testing.T) {
	ta := newTestApp(t, "")
	ta.tripPlans(model.TripPlansManual) // extra plans restore the earlier plan
	// A plan the extra plan will replace and later restore.
	ta.json("POST", "/api/evcc/plan", map[string]any{"soc": 50, "time": "2026-11-11T06:00:00Z", "confirm": true}, http.StatusOK, nil)

	var p struct {
		Available bool   `json:"available"`
		SoC       int    `json:"soc"`
		MinSoC    int    `json:"minSoc"`
		ReadyBy   string `json:"readyBy"`
		ExpiresAt string `json:"expiresAt"`
		Notes     []string
	}
	ta.json("GET", "/api/advice/2026-11-10/proposal?kind=extra_plan", nil, http.StatusOK, &p)
	if !p.Available || p.MinSoC != 42 || p.SoC != 80 || !strings.HasPrefix(p.ReadyBy, "2026-11-10T07:48:00") {
		t.Fatalf("proposal = %+v", p)
	}
	if !strings.Contains(strings.Join(p.Notes, " "), "Replaces the current EVCC plan (50%") {
		t.Errorf("notes = %v", p.Notes)
	}
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 50 {
		t.Fatalf("opening a proposal must not write to EVCC: %+v", plan)
	}

	body := map[string]any{"kind": "extra_plan", "date": "2026-11-10", "soc": 70, "readyBy": p.ReadyBy, "expiresAt": p.ExpiresAt}
	ta.json("POST", "/api/adjustments", body, http.StatusBadRequest, nil)
	body["confirm"] = true
	body["soc"] = 30
	ta.json("POST", "/api/adjustments", body, http.StatusBadRequest, nil) // below the trip target
	body["soc"] = 70
	body["readyBy"] = "2026-11-10T08:00:00+01:00"
	ta.json("POST", "/api/adjustments", body, http.StatusBadRequest, nil) // after the protected departure
	body["readyBy"] = p.ReadyBy
	var a adjustment
	ta.json("POST", "/api/adjustments", body, http.StatusCreated, &a)
	if a.Status != "active" || a.Source != "ui" || a.Plan.SoC != 70 {
		t.Fatalf("adjustment = %+v", a)
	}
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 70 {
		t.Fatalf("EVCC plan = %+v", plan)
	}
	var status struct {
		Plan *struct {
			SetByPlanner bool `json:"setByPlanner"`
		} `json:"plan"`
	}
	ta.json("GET", "/api/status", nil, http.StatusOK, &status)
	if status.Plan == nil || !status.Plan.SetByPlanner {
		t.Errorf("an extra plan is set by the planner: %+v", status.Plan)
	}
	tue := ta.advice().Advice.Days[1]
	if tue.Adjustment == nil || tue.Action != "" {
		t.Errorf("Tuesday should show the active extra plan instead of an action: %+v", tue)
	}
	ta.json("POST", "/api/adjustments", body, http.StatusBadRequest, nil) // only one at a time

	// Before expiry nothing changes; at expiry the trip plan comes back.
	ta.at(time.Date(2026, 11, 10, 7, 0, 0, 0, ams))
	ta.poll()
	if plan := ta.evccPlan(); plan.Soc != 70 {
		t.Fatalf("restored too early: %+v", plan)
	}
	ta.at(time.Date(2026, 11, 10, 7, 48, 0, 0, ams))
	ta.poll()
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 50 {
		t.Fatalf("previous plan not restored: %+v", plan)
	}
	got, _ := ta.app.Store.Adjustment(a.ID)
	if got.Status != "restored" || !strings.Contains(got.Note, "50%") {
		t.Errorf("after expiry = %+v", got)
	}
}

func TestExtraPlanWithoutPreviousPlanIsRemoved(t *testing.T) {
	ta := newTestApp(t, "")
	ta.tripPlans(model.TripPlansManual) // extra plans restore the earlier plan
	ta.json("POST", "/api/adjustments", map[string]any{
		"kind": "extra_plan", "date": "2026-11-10", "soc": 60, "confirm": true,
		"readyBy": "2026-11-10T07:00:00+01:00", "expiresAt": "2026-11-10T07:30:00+01:00",
	}, http.StatusCreated, nil)
	ta.at(time.Date(2026, 11, 10, 7, 30, 0, 0, ams))
	ta.poll()
	if plan := ta.evccPlan(); plan != nil {
		t.Errorf("the extra plan should be removed at expiry: %+v", plan)
	}
	if st := ta.app.Service.Status(); st.Plan != nil {
		t.Errorf("the status should show the removal right away: %+v", st.Plan)
	}
	// A trip plan sent from the planner replaces an active extra plan.
	ta.at(time.Date(2026, 11, 9, 19, 0, 0, 0, ams))
	var a adjustment
	ta.json("POST", "/api/adjustments", map[string]any{
		"kind": "extra_plan", "date": "2026-11-10", "soc": 60, "confirm": true,
		"readyBy": "2026-11-10T07:00:00+01:00", "expiresAt": "2026-11-10T07:30:00+01:00",
	}, http.StatusCreated, &a)
	ta.json("POST", "/api/evcc/plan", map[string]any{"soc": 42, "time": "2026-11-10T06:48:00Z", "confirm": true}, http.StatusOK, nil)
	if got, _ := ta.app.Store.Adjustment(a.ID); got.Status != "kept" || !strings.Contains(got.Note, "trip plan") {
		t.Errorf("after a trip plan = %+v", got)
	}
}

func TestPlanFinishedByEVCCStillRestores(t *testing.T) {
	ta := newTestApp(t, "")
	ta.tripPlans(model.TripPlansManual) // extra plans restore the earlier plan
	ctx := context.Background()
	ta.json("POST", "/api/evcc/plan", map[string]any{"soc": 50, "time": "2026-11-11T06:00:00Z", "confirm": true}, http.StatusOK, nil)
	extra := map[string]any{
		"kind": "extra_plan", "date": "2026-11-10", "soc": 60, "confirm": true,
		"readyBy": "2026-11-10T07:00:00+01:00", "expiresAt": "2026-11-10T07:00:00+01:00",
	}
	var a adjustment
	ta.json("POST", "/api/adjustments", extra, http.StatusCreated, &a)
	// The car reaches 60% at night and EVCC deletes the plan by itself.
	ta.app.Stub.SetVehicle(true, 61)
	ta.app.Stub.DeleteSocPlan(ctx, evcc.StubVehicle)
	ta.at(time.Date(2026, 11, 10, 3, 0, 0, 0, ams))
	ta.poll()
	if got, _ := ta.app.Store.Adjustment(a.ID); got.Status != "active" {
		t.Fatalf("a plan EVCC finished is not a manual change: %+v", got)
	}
	ta.at(time.Date(2026, 11, 10, 7, 0, 0, 0, ams))
	ta.poll()
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 50 {
		t.Fatalf("previous plan not restored after EVCC finished the extra plan: %+v", plan)
	}

	// Removed in EVCC before reaching the target: the removal is kept.
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.at(time.Date(2026, 11, 9, 19, 0, 0, 0, ams))
	ta.json("POST", "/api/adjustments", extra, http.StatusCreated, &a)
	ta.app.Stub.DeleteSocPlan(ctx, evcc.StubVehicle)
	ta.poll()
	if got, _ := ta.app.Store.Adjustment(a.ID); got.Status != "kept" {
		t.Errorf("manual removal = %+v", got)
	}
}

func TestManualEVCCChangesAreKept(t *testing.T) {
	ta := newTestApp(t, "")
	ctx := context.Background()
	var a adjustment
	ta.json("POST", "/api/adjustments", map[string]any{
		"kind": "extra_plan", "date": "2026-11-10", "soc": 60, "confirm": true,
		"readyBy": "2026-11-10T07:48:00+01:00", "expiresAt": "2026-11-10T07:48:00+01:00",
	}, http.StatusCreated, &a)
	// The user sets another plan in EVCC: the planner keeps it.
	ta.app.Stub.SetSocPlan(ctx, evcc.StubVehicle, 90, time.Date(2026, 11, 10, 9, 0, 0, 0, ams))
	ta.poll()
	if got, _ := ta.app.Store.Adjustment(a.ID); got.Status != "kept" {
		t.Fatalf("manual change = %+v", got)
	}
	ta.at(time.Date(2026, 11, 10, 8, 0, 0, 0, ams))
	ta.poll()
	if plan := ta.evccPlan(); plan.Soc != 90 {
		t.Errorf("manual plan was overwritten: %+v", plan)
	}

	// Postponement: the limit changed in EVCC is kept too.
	ta.at(time.Date(2026, 11, 9, 19, 0, 0, 0, ams))
	ta.poll()
	ta.json("POST", "/api/adjustments", map[string]any{
		"kind": "postpone", "date": "2026-11-09", "confirm": true, "expiresAt": "2026-11-10T00:00:00+01:00",
	}, http.StatusCreated, &a)
	if ta.smartCostLimit() != nil {
		t.Fatalf("cheap charging should be paused: %v", *ta.smartCostLimit())
	}
	limit := 0.15
	ta.app.Stub.SetSmartCostLimit(ctx, 1, &limit)
	ta.at(time.Date(2026, 11, 10, 0, 5, 0, 0, ams))
	ta.poll()
	if got, _ := ta.app.Store.Adjustment(a.ID); got.Status != "kept" || *ta.smartCostLimit() != 0.15 {
		t.Errorf("manual limit = %+v, %v", got, *ta.smartCostLimit())
	}
}

func TestPostponeRestoresCheapCharging(t *testing.T) {
	ta := newTestApp(t, "")
	var p struct {
		Available      bool     `json:"available"`
		SmartCostLimit *float64 `json:"smartCostLimit"`
		ExpiresAt      string   `json:"expiresAt"`
	}
	ta.json("GET", "/api/advice/2026-11-09/proposal?kind=postpone", nil, http.StatusOK, &p)
	if !p.Available || p.SmartCostLimit == nil || *p.SmartCostLimit != 0.25 || !strings.HasPrefix(p.ExpiresAt, "2026-11-10T00:00:00") {
		t.Fatalf("proposal = %+v", p)
	}
	// Future days' deferrals are not offered (a pause starts right away).
	var later struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	ta.json("GET", "/api/advice/2026-11-10/proposal?kind=postpone", nil, http.StatusOK, &later)
	if later.Available || later.Reason == "" {
		t.Errorf("Tuesday postponement = %+v", later)
	}
	var a adjustment
	ta.json("POST", "/api/adjustments", map[string]any{"kind": "postpone", "date": "2026-11-09", "confirm": true, "expiresAt": p.ExpiresAt},
		http.StatusCreated, &a)
	if ta.smartCostLimit() != nil {
		t.Fatal("cheap charging should be paused")
	}
	var cancelled adjustment
	ta.json("POST", "/api/adjustments/"+itoa(a.ID)+"/cancel", map[string]any{}, http.StatusOK, &cancelled)
	if cancelled.Status != "cancelled" || ta.smartCostLimit() == nil || *ta.smartCostLimit() != 0.25 {
		t.Fatalf("cancel = %+v, limit %v", cancelled, ta.smartCostLimit())
	}
	ta.json("POST", "/api/adjustments/"+itoa(a.ID)+"/cancel", map[string]any{}, http.StatusBadRequest, nil)
}

// A postponement that would change nothing is not offered: EVCC has no
// cheap-grid limit, or every price until the cheaper day is above it.
func TestNeedlessPostponementIsNotOffered(t *testing.T) {
	ta := newTestApp(t, "")
	ctx := context.Background()
	// Monday's remaining prices are €0.24 and up.
	low := 0.2
	if err := ta.app.Stub.SetSmartCostLimit(ctx, 1, &low); err != nil {
		t.Fatal(err)
	}
	ta.poll()
	mon := ta.advice().Advice.Days[0]
	if mon.Recommendation != "defer" || mon.Action != "" ||
		!strings.Contains(mon.ActionNote, "every price until then is above EVCC's cheap-grid limit (€0.20/kWh; cheapest €0.24)") {
		t.Fatalf("Monday = %+v", mon)
	}
	var p struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	ta.json("GET", "/api/advice/2026-11-09/proposal?kind=postpone", nil, http.StatusOK, &p)
	if p.Available || p.Reason != mon.ActionNote {
		t.Errorf("proposal = %+v", p)
	}
	ta.json("POST", "/api/adjustments", map[string]any{"kind": "postpone", "date": "2026-11-09", "confirm": true,
		"expiresAt": "2026-11-10T00:00:00+01:00"}, http.StatusBadRequest, nil)

	// Automatic postponement does nothing either.
	var s map[string]any
	ta.json("GET", "/api/settings/advice", nil, http.StatusOK, &s)
	s["autoApply"], s["autoPostpone"], s["autoMinConfidence"] = true, true, 2
	s["targetLittle"], s["targetMedium"], s["targetHigh"] = 60, 70, 80
	s["readyBy"], s["expiryHours"] = "06:00", 1
	ta.json("PUT", "/api/settings/advice", s, http.StatusOK, nil)
	if err := ta.app.Service.AutoApply(ctx); err != nil {
		t.Fatal(err)
	}
	if adj := ta.advice().Advice.Adjustments; len(adj) != 0 || *ta.smartCostLimit() != 0.2 {
		t.Fatalf("auto postpone = %+v", adj)
	}

	if err := ta.app.Stub.SetSmartCostLimit(ctx, 1, nil); err != nil {
		t.Fatal(err)
	}
	ta.poll()
	if mon := ta.advice().Advice.Days[0]; mon.Action != "" || !strings.Contains(mon.ActionNote, "no cheap-grid limit") {
		t.Errorf("without a limit = %+v", mon)
	}
}

func TestWeakAdviceHasNoPlanAction(t *testing.T) {
	ta := newTestApp(t, "")
	var p struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	ta.json("GET", "/api/advice/2026-11-11/proposal?kind=extra_plan", nil, http.StatusOK, &p)
	if p.Available || !strings.Contains(p.Reason, "not enough data") {
		t.Errorf("no-data day = %+v", p)
	}
	ta.json("POST", "/api/adjustments", map[string]any{"kind": "extra_plan", "date": "2026-11-11", "soc": 80, "confirm": true,
		"readyBy": "2026-11-11T07:00:00+01:00", "expiresAt": "2026-11-11T07:00:00+01:00"}, http.StatusBadRequest, nil)
	ta.json("GET", "/api/advice/2026-11-20/proposal?kind=extra_plan", nil, http.StatusBadRequest, nil)
}

func TestDismissAndUndo(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/advice/2026-11-10/dismiss", map[string]any{}, http.StatusNoContent, nil)
	tue := ta.advice().Advice.Days[1]
	if !tue.Dismissed || tue.Action != "" || tue.Recommendation != "charge_ahead" {
		t.Fatalf("dismissed = %+v", tue)
	}
	if ta.evccPlan() != nil {
		t.Error("dismiss must not change EVCC")
	}
	ta.json("POST", "/api/advice/2026-11-10/dismiss", map[string]any{"undo": true}, http.StatusNoContent, nil)
	if tue := ta.advice().Advice.Days[1]; tue.Dismissed || tue.Action != "extra_plan" {
		t.Fatalf("undone = %+v", tue)
	}
}

func TestAdviceSettingsAndAutoApply(t *testing.T) {
	ta := newTestApp(t, "")
	var s map[string]any
	ta.json("GET", "/api/settings/advice", nil, http.StatusOK, &s)
	if s["enabled"] != true || s["autoApply"] != false || s["autoMinConfidence"] != 3.0 {
		t.Fatalf("defaults = %v", s)
	}
	// Auto-apply needs a concrete policy.
	s["autoApply"] = true
	ta.json("PUT", "/api/settings/advice", s, http.StatusBadRequest, nil)

	// Off by default: nothing happens.
	ctx := context.Background()
	if err := ta.app.Service.AutoApply(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ta.advice().Advice.Adjustments) != 0 {
		t.Fatal("auto-apply is off by default")
	}

	s["targetLittle"], s["targetMedium"], s["targetHigh"] = 60, 70, 80
	s["readyBy"], s["expiryHours"], s["autoMinConfidence"], s["autoPostpone"] = "06:00", 1, 2, true
	ta.json("PUT", "/api/settings/advice", s, http.StatusOK, nil)

	// Monday: today's advice is to defer, and postponing is allowed.
	if err := ta.app.Service.AutoApply(ctx); err != nil {
		t.Fatal(err)
	}
	adj := ta.advice().Advice.Adjustments
	if len(adj) != 1 || adj[0].Kind != "postpone" || adj[0].Source != "auto" || ta.smartCostLimit() != nil {
		t.Fatalf("auto postpone = %+v", adj)
	}
	ta.app.Service.AutoApply(ctx)
	if n := len(ta.advice().Advice.Adjustments); n != 1 {
		t.Fatalf("auto-apply acts once per day, got %d adjustments", n)
	}

	// Tuesday just after midnight: charge ahead with the configured target,
	// ready by 06:00 (before the 07:48 departure), restored an hour later.
	ta.at(time.Date(2026, 11, 10, 0, 30, 0, 0, ams))
	ta.poll()
	if err := ta.app.Service.AutoApply(ctx); err != nil {
		t.Fatal(err)
	}
	var extra *adjustment
	for _, a := range ta.advice().Advice.Adjustments {
		if a.Kind == "extra_plan" {
			extra = &a
		}
	}
	// The target set for Tuesday's charge indicator.
	want := map[string]int{"little": 60, "medium": 70, "high": 80}[ta.advice().day("2026-11-10").Indicator]
	if want == 0 || extra == nil || extra.Source != "auto" || extra.Plan.SoC != float64(want) ||
		!strings.HasPrefix(extra.ExpiresAt, "2026-11-10T07:00:00") {
		t.Fatalf("auto extra plan = %+v, want %d%%", extra, want)
	}
	if plan := ta.evccPlan(); plan == nil || plan.Soc != float64(want) {
		t.Errorf("EVCC plan = %+v", plan)
	}
}

func TestForecastScheduleAndFailures(t *testing.T) {
	ta := newTestApp(t, "")
	svc := ta.app.Service
	ctx := context.Background()
	first, _ := ta.app.Store.Forecast(service.SourceTibber)

	// Not due again before the next daily slot (18:00).
	ta.at(time.Date(2026, 11, 10, 10, 0, 0, 0, ams))
	svc.RefreshForecasts(ctx, false)
	if rec, _ := ta.app.Store.Forecast(service.SourceTibber); !rec.LastAttemptAt.Equal(*first.LastAttemptAt) {
		t.Fatalf("fetched before the daily slot: %v", rec.LastAttemptAt)
	}
	// Due after 18:00; the unchanged article is not structured again.
	ta.at(time.Date(2026, 11, 10, 18, 30, 0, 0, ams))
	svc.RefreshForecasts(ctx, false)
	rec, _ := ta.app.Store.Forecast(service.SourceTibber)
	if !rec.LastSuccessAt.Equal(time.Date(2026, 11, 10, 18, 30, 0, 0, ams)) || rec.ContentHash != first.ContentHash ||
		string(rec.Payload) != string(first.Payload) {
		t.Fatalf("daily refresh = %+v", rec)
	}

	// A failing source keeps the last valid outlook and reports the error.
	svc.UseIntegrations(func(i *service.Integrations) { i.Forecasts.Article = failingArticle{} })
	if err := svc.RefreshForecasts(ctx, true); err == nil {
		t.Fatal("want an error")
	}
	var conns []struct{ Name, Status, Detail string }
	ta.json("GET", "/api/connections", nil, http.StatusOK, &conns)
	found := false
	for _, c := range conns {
		if c.Name == "Tibber outlook" {
			found = c.Status == "error" && strings.Contains(c.Detail, "keeping the last valid forecast")
		}
	}
	if !found {
		t.Errorf("connections = %+v", conns)
	}
	thu := ta.advice().day("2026-11-12")
	if thu.Price == nil || thu.Price.Basis != "estimate" {
		t.Errorf("the last valid outlook should still be used: %+v", thu)
	}
}

type failingArticle struct{}

func (failingArticle) Name() string { return "live" }
func (failingArticle) URL() string  { return "https://example.test" }
func (failingArticle) Fetch(context.Context) ([]byte, error) {
	return nil, errors.New("outlook page returned 503")
}

func TestForecastSecretsStayServerSide(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := "fixed_time: " + fixedNow + "\ndata_dir: " + filepath.Join(dir, "data") +
		"\nevcc:\n  mode: stub\ncalendar:\n  mode: stub\nroutes:\n  mode: stub\ntibber:\n  mode: stub\n"
	os.WriteFile(cfgPath, []byte(yaml), 0o600)
	secrets := map[string]string{"OPENROUTER_API_KEY": "sk-or-secret-123", "KNMI_API_KEY": "knmi-secret-456"}
	cfg, err := config.Load(config.Options{Path: cfgPath, Getenv: func(k string) string { return secrets[k] }})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ta := &testApp{t: t, app: a, dir: dir}
	for _, path := range []string{"/api/connections", "/api/plan", "/api/forecasts", "/api/settings/advice"} {
		_, body := ta.do("GET", path, nil)
		for _, secret := range secrets {
			if strings.Contains(string(body), secret) {
				t.Errorf("%s exposes a secret", path)
			}
		}
	}
	var conns []struct{ Name, Status, Detail string }
	ta.json("GET", "/api/connections", nil, http.StatusOK, &conns)
	for _, c := range conns {
		if c.Name == "OpenRouter" && (c.Status != "ok" || !strings.Contains(c.Detail, "claude-haiku")) {
			t.Errorf("OpenRouter = %+v", c)
		}
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
