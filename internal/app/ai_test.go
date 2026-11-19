package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/aiplan"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/openrouter"
	"github.com/diogoap/evcc-planner/internal/service"
)

// fakeAI answers every AI plan with the same decision (or error) and keeps
// the last request.
type fakeAI struct {
	content string
	err     error
	calls   int
	last    aiplan.Request
}

func (f *fakeAI) Name() string { return "openrouter" }

func (f *fakeAI) Decide(ctx context.Context, r aiplan.Request) (string, openrouter.Usage, error) {
	f.calls++
	f.last = r
	return f.content, openrouter.Usage{Model: "test/model", PromptTokens: 2300, CompletionTokens: 600}, f.err
}

// useAI replaces the stand-in with a fake model.
func (ta *testApp) useAI(f *fakeAI) {
	ta.app.Service.UseIntegrations(func(i *service.Integrations) { i.AI = f })
}

func (ta *testApp) checkAI() {
	ta.t.Helper()
	if err := ta.app.Service.CheckAI(context.Background()); err != nil {
		ta.t.Fatal(err)
	}
}

func (ta *testApp) aiStatus() service.AIView {
	ta.t.Helper()
	var v service.AIView
	ta.json("GET", "/api/ai", nil, http.StatusOK, &v)
	return v
}

// The decision used below: 95% for the school run (the cap is 90%), also
// for the next two trips, and charge extra on Tuesday night.
const aiDecision = `{
	"summary": "Charge for Tuesday's long morning and the afternoon trips tonight.",
	"important": ["Tuesday night averages €0.17", "88 km on Tuesday morning"],
	"charge_ahead": {"target_soc": 95, "trip_ids": ["T2", "T3", "T42"], "reason": "Tonight is cheaper than Tuesday afternoon."},
	"days": [
		{"date": "2026-11-09", "recommendation": "none", "target_soc": null, "from": null, "until": null, "defer_until": null,
			"postpone": false, "confidence": 3, "summary": "Nothing to do tonight before midnight.", "rationale": []},
		{"date": "2026-11-10", "recommendation": "charge_extra", "target_soc": 75, "from": "2026-11-10 01:00",
			"until": "2026-11-10 05:00", "defer_until": null, "postpone": false, "confidence": 3,
			"summary": "Charge extra to 75% between 01:00 and 05:00.", "rationale": ["The cheapest night of the week."]}
	]
}`

func TestAISettingsAndPreview(t *testing.T) {
	ta := newTestApp(t, "")
	log := &eventLog{ta: ta}
	log.fresh()

	var v service.AISettingsView
	ta.json("GET", "/api/settings/ai", nil, http.StatusOK, &v)
	if v.Settings.MaxRunsPerDay != 3 || !v.Settings.OnPrices || !v.Settings.OnPlugIn || v.Settings.Model != model.DefaultAIModel ||
		v.Settings.Prompt != aiplan.DefaultPrompt || v.DefaultPrompt != aiplan.DefaultPrompt || len(v.Macros) != len(aiplan.Macros) ||
		v.DefaultModel != "~anthropic/claude-haiku-latest" || !strings.HasPrefix(v.Decider, "Stand-in") {
		t.Fatalf("defaults = %+v", v)
	}
	// The models offered, sorted by name ignoring the "~", with the default
	// and each one's price and cost per typical run (7,000 tokens in, 4,500 out).
	ids := make([]string, len(v.Models))
	for i, m := range v.Models {
		ids[i] = m.ID
		if m.InputPerM <= 0 || m.OutputPerM <= 0 || m.RunUSD <= 0 {
			t.Errorf("%s has no price: %+v", m.ID, m)
		}
		if m.ID == model.DefaultAIModel && math.Abs(m.RunUSD-0.00295) > 1e-9 {
			t.Errorf("default model run = %v", m.RunUSD)
		}
	}
	if len(ids) != 19 || ids[0] != "~anthropic/claude-fable-latest" || !slices.Contains(ids, model.DefaultAIModel) ||
		v.PricesDate != "2026-11-18" || !slices.IsSortedFunc(ids, func(a, b string) int {
		return strings.Compare(strings.TrimPrefix(a, "~"), strings.TrimPrefix(b, "~"))
	}) {
		t.Errorf("models = %v (prices %s)", ids, v.PricesDate)
	}
	if v.Macros[0].Name != "now" || v.Macros[0].Description == "" {
		t.Errorf("macros = %+v", v.Macros)
	}
	// The goals shown are the planner's rules with this install's values.
	if !strings.Contains(v.DefaultGoals, "EVCC's charge limit is 80%") || !strings.Contains(v.DefaultGoals, "at or below 90%") ||
		v.Settings.Goals != v.DefaultGoals {
		t.Errorf("goals = %q, default %q", v.Settings.Goals, v.DefaultGoals)
	}

	for _, bad := range []map[string]any{
		{"prompt": "Plan with {{prices}} and {{trips}}"},
		{"maxRunsPerDay": 11},
		{"model": "not a model"},
		{"prompt": strings.Repeat("x", model.MaxPromptChars+1)},
	} {
		code, body := ta.do("PUT", "/api/settings/ai", bad)
		if code != http.StatusBadRequest {
			t.Errorf("%v = %d %s", bad, code, body)
		}
	}
	code, body := ta.do("PUT", "/api/settings/ai", map[string]any{"prompt": "Plan with {{prices}}"})
	if code != http.StatusBadRequest || !strings.Contains(string(body), "unknown placeholders in the prompt: {{prices}}") {
		t.Errorf("unknown placeholder = %d %s", code, body)
	}
	log.none("invalid settings")

	custom := "Plan the week.\n{{trips}}\n{{energy_prices}}"
	ta.json("PUT", "/api/settings/ai", map[string]any{"prompt": custom, "maxRunsPerDay": 2, "model": "anthropic/claude-sonnet-5.5"},
		http.StatusOK, &v)
	if v.Settings.Prompt != custom || v.Settings.MaxRunsPerDay != 2 || v.Decider != "Stand-in (no AI model): repeats the automatic rules" {
		t.Errorf("saved = %+v", v.Settings)
	}
	e := log.one("settings.update", model.SourceUI)
	if e.Summary != "Changed AI settings: runs per day, AI model, prompt" {
		t.Errorf("event = %+v", e)
	}
	// The activity log shows the prompt's length, not its text.
	changed(t, e, "prompt", "default", "custom (42 characters)")

	// The default text is stored as the default, so it follows later changes.
	ta.json("PUT", "/api/settings/ai", map[string]any{"prompt": aiplan.DefaultPrompt + "\n"}, http.StatusOK, &v)
	if saved, _ := ta.app.Store.AISettings(model.DefaultAISettings()); saved.Prompt != "" || v.Settings.Prompt != aiplan.DefaultPrompt {
		t.Errorf("default prompt stored as %q", saved.Prompt)
	}
	changed(t, log.one("settings.update", model.SourceUI), "prompt", "custom (42 characters)", "default")

	// OpenRouter's aliases for a family's latest model start with "~".
	ta.json("PUT", "/api/settings/ai", map[string]any{"model": "~openai/gpt-luna-latest"}, http.StatusOK, &v)
	if v.Settings.Model != "~openai/gpt-luna-latest" || v.Decider != "Stand-in (no AI model): repeats the automatic rules" {
		t.Errorf("alias model = %+v", v.Settings)
	}
	ta.json("PUT", "/api/settings/ai", map[string]any{"model": "openai/~gpt"}, http.StatusBadRequest, nil)
	log.fresh()

	// The default model is stored as the default, so it follows later changes.
	ta.json("PUT", "/api/settings/ai", map[string]any{"model": " ~anthropic/claude-haiku-latest "}, http.StatusOK, &v)
	if saved, _ := ta.app.Store.AISettings(model.DefaultAISettings()); saved.Model != "" || v.Settings.Model != model.DefaultAIModel {
		t.Errorf("default model stored as %q", saved.Model)
	}
	changed(t, log.one("settings.update", model.SourceUI), "model", "~openai/gpt-luna-latest", "default (~anthropic/claude-haiku-latest)")

	// Own goals replace {{goals}}; the default text is stored as the default.
	goals := "- Charge extra up to 100% when prices are negative.\n- Keep the battery above 30%."
	ta.json("PUT", "/api/settings/ai", map[string]any{"goals": goals}, http.StatusOK, &v)
	changed(t, log.one("settings.update", model.SourceUI), "goals", "default", fmt.Sprintf("custom (%d characters)", len([]rune(goals))))
	if v.Settings.Goals != goals {
		t.Errorf("saved goals = %q", v.Settings.Goals)
	}
	var gp service.AIPreview
	ta.json("POST", "/api/ai/preview", map[string]any{"prompt": "{{goals}}", "goals": goals}, http.StatusOK, &gp)
	if gp.Text != goals {
		t.Errorf("goals preview = %q", gp.Text)
	}
	ta.json("PUT", "/api/settings/ai", map[string]any{"goals": v.DefaultGoals + "\n"}, http.StatusOK, &v)
	if saved, _ := ta.app.Store.AISettings(model.DefaultAISettings()); saved.Goals != "" || v.Settings.Goals != v.DefaultGoals {
		t.Errorf("default goals stored as %q", saved.Goals)
	}
	log.one("settings.update", model.SourceUI)
	ta.json("PUT", "/api/settings/ai", map[string]any{"goals": strings.Repeat("x", model.MaxGoalsChars+1)}, http.StatusBadRequest, nil)

	// The preview fills the placeholders with today's data; trips have no titles.
	var p service.AIPreview
	ta.json("POST", "/api/ai/preview", map[string]any{"prompt": "{{now}}\n{{trips}}"}, http.StatusOK, &p)
	if !strings.HasPrefix(p.Text, "Monday 2026-11-09 19:00") ||
		!strings.Contains(p.Text, `T1 "School drop-off + Office visit": leaves 2026-11-10 07:48`) ||
		strings.Contains(p.Text, "Schoolstraat") || strings.Contains(p.Text, "Office Park") || p.System != aiplan.SystemPrompt || p.Tokens == 0 {
		t.Errorf("preview = %+v", p)
	}
	ta.json("POST", "/api/ai/preview", map[string]any{"prompt": ""}, http.StatusOK, &p)
	if !strings.Contains(p.Text, "## Published prices") || strings.Contains(p.Text, "{{") ||

		!strings.Contains(p.Text, "12:00 1.5 kW (too little to charge)") {
		t.Errorf("default preview = %s", p.Text)
	}
	ta.json("POST", "/api/ai/preview", map[string]any{"prompt": "{{wether}}"}, http.StatusBadRequest, nil)
	log.none("previews")
}

func TestAIModeRunsAfterNewPricesAndPlugIn(t *testing.T) {
	ta := newTestApp(t, "")
	ta.pastWeek(0.40)
	ta.tripPlans(model.TripPlansAutomatic)
	log := &eventLog{ta: ta}
	plug := func(connected bool, soc float64) {
		t.Helper()
		ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": connected, "soc": soc}, http.StatusOK, nil)
	}
	plug(false, 38) // learn that the car is unplugged
	log.fresh()

	// Automatic mode never asks the AI.
	ta.checkAI()
	if s := ta.aiStatus(); s.Active || s.Latest != nil || s.Status != "Off: choose AI mode under Settings → Planning." {
		t.Fatalf("status in automatic mode = %+v", s)
	}

	ta.tripPlans(model.TripPlansAI)
	changed(t, log.one("settings.update", model.SourceUI), "tripPlans", model.TripPlansAutomatic, model.TripPlansAI)
	var p struct {
		Control struct {
			Mode   string `json:"mode"`
			Target struct {
				SoC    int    `json:"soc"`
				Source string `json:"source"`
			} `json:"target"`
		} `json:"control"`
		AI *service.AIPlanView `json:"ai"`
	}
	ta.json("GET", "/api/plan", nil, http.StatusOK, &p)
	if p.Control.Mode != model.TripPlansAI || p.AI == nil || p.AI.InUse || !strings.HasPrefix(p.AI.Note, "No AI plan yet") ||
		p.Control.Target.Source != "" {
		t.Fatalf("before the first run: %+v", p)
	}

	// The first check makes the day's plan; later checks wait for new prices.
	ta.checkAI()
	e := log.one("ai.run", model.SourceAutomation)
	if !strings.HasPrefix(e.Summary, "AI plan (new prices): target 42% by Tue 07:48; Mon defer; Tue charge extra to 60%") {
		t.Errorf("event = %+v", e)
	}
	s := ta.aiStatus()
	if !s.Active || !s.InUse || s.RunsToday != 1 || s.Latest == nil || s.Latest.Trigger != model.AITriggerPrices ||
		s.Latest.Status != model.AIRunOK || s.Current.ID != s.Latest.ID || s.Latest.Result == nil {
		t.Fatalf("status = %+v", s)
	}
	ta.checkAI()
	log.none("a check without new prices")
	if c := connection(ta, "AI mode"); c.Status != "stub" || !strings.HasPrefix(c.Detail, "Stand-in") {
		t.Errorf("connection = %+v", c)
	}
	ta.json("GET", "/api/plan", nil, http.StatusOK, &p)
	if !p.AI.InUse || p.AI.RunID != s.Latest.ID || p.Control.Target.Source != "ai" {
		t.Errorf("after the run: %+v", p)
	}

	// Plugging in sends the next departure's plan at once, then asks the AI.
	plug(true, 38)
	got := log.fresh()
	if len(got) != 2 || got[0].Action != "plan.send" || got[1].Action != "ai.run" ||
		got[0].Summary != "Car plugged in (AI mode): sent plan 42% by Tue 10 Nov, 07:48 to EVCC" ||
		!strings.HasPrefix(got[1].Summary, "AI plan (car plugged in)") {
		t.Fatalf("plug-in events = %+v", got)
	}

	// Up to the runs per day (3); a run by hand is always allowed.
	plug(false, 38)
	plug(true, 38)
	if s := ta.aiStatus(); s.RunsToday != 3 || s.Latest.Trigger != model.AITriggerPlugIn {
		t.Fatalf("third run: %+v", s)
	}
	log.fresh()
	plug(false, 38)
	plug(true, 38)
	if e := log.one("ai.skip", model.SourceAutomation); e.Summary !=
		"AI plan not made on plug-in: 3 runs today already (Settings → Advice → AI mode); the last plan applies" {
		t.Errorf("skip = %+v", e)
	}
	ta.json("POST", "/api/ai/run", map[string]any{}, http.StatusOK, &s)
	if s.RunsToday != 4 || s.Latest.Trigger != model.AITriggerManual {
		t.Errorf("run by hand = %+v", s)
	}
	if e := log.one("ai.run", model.SourceUI); !strings.HasPrefix(e.Summary, "AI plan (run by hand)") {
		t.Errorf("event = %+v", e)
	}

	// Runs on plug-in can be turned off.
	ta.json("PUT", "/api/settings/ai", map[string]any{"onPlugIn": false, "maxRunsPerDay": 10}, http.StatusOK, nil)
	log.fresh()
	plug(false, 38)
	plug(true, 38)
	log.none("plugging in with runs on plug-in off")
}

func TestAIModeKeepsTheFoundationalRules(t *testing.T) {
	ta := newTestApp(t, "")
	ta.tripPlans(model.TripPlansAI)
	f := &fakeAI{content: aiDecision}
	ta.useAI(f)
	ta.checkAI()
	if f.calls != 1 || f.last.Model != "~anthropic/claude-haiku-latest" || f.last.System != aiplan.SystemPrompt ||
		!strings.Contains(f.last.User, `T3 "School pickup": leaves 2026-11-11 14:33`) {
		t.Fatalf("request = %+v", f.last)
	}
	s := ta.aiStatus()
	if s.Latest.Model != "test/model" || s.Latest.PromptTokens != 2300 || s.Latest.Decider != "OpenRouter ~anthropic/claude-haiku-latest" {
		t.Errorf("run = %+v", s.Latest)
	}
	res := s.Latest.Result
	if res.Ahead.TargetSoC != 90 || strings.Join(res.Ahead.TripIDs, ",") != "T2,T3" || len(res.Warnings) != 2 ||
		!strings.Contains(res.Warnings[0], "Lowered the charge-ahead target from 95% to the cap of 90%") {
		t.Fatalf("result = %+v", res)
	}

	// The AI's target, capped, with the trips it charges for.
	view, err := ta.app.Service.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tg := view.Control.Target
	if tg.SoC != 90 || tg.MinSoC != 42 || tg.Source != "ai" || len(tg.Ahead) != 2 || tg.Ahead[0].Title != "School pickup" ||
		tg.Reason != "Tonight is cheaper than Tuesday afternoon." || len(tg.Charging) == 0 {
		t.Fatalf("target = %+v", tg)
	}
	// Tuesday's advice is the AI's, with the planner's own price for its window.
	var tue *service.DayAdviceView
	for i := range view.Advice.Days {
		if view.Advice.Days[i].Date == "2026-11-10" {
			tue = &view.Advice.Days[i]
		}
	}
	if !tue.AI || tue.Recommendation != "charge_extra" || tue.TargetSoC != 75 || tue.Action != model.AdjustExtraPlan ||
		len(tue.Blocks) != 1 || tue.Blocks[0].Price < 0.15 || tue.Blocks[0].Price > 0.19 || tue.Confidence != 3 {
		t.Fatalf("Tuesday = %+v", tue)
	}
	var prop service.ProposalView
	ta.json("GET", "/api/advice/2026-11-10/proposal?kind=extra_plan", nil, http.StatusOK, &prop)
	if !prop.Available || prop.SoC != 75 || !prop.ReadyBy.Equal(time.Date(2026, 11, 10, 5, 0, 0, 0, ams)) ||
		!strings.Contains(strings.Join(prop.Notes, " "), "The AI's target: 75% (AI mode)") {
		t.Errorf("proposal = %+v", prop)
	}

	// Plugged in: EVCC gets the AI's target.
	ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": false, "soc": 38}, http.StatusOK, nil)
	ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": true, "soc": 38}, http.StatusOK, nil)
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 90 {
		t.Fatalf("EVCC plan = %+v, want the AI's 90%%", plan)
	}

	// A target below the next departure's own is raised to it.
	f.content = strings.Replace(aiDecision, `"target_soc": 95`, `"target_soc": 10`, 1)
	ta.json("POST", "/api/ai/run", map[string]any{}, http.StatusOK, &s)
	if s.Current.Result.Ahead.TargetSoC != 42 {
		t.Errorf("raised target = %+v", s.Current.Result.Ahead)
	}
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 42 {
		t.Errorf("EVCC plan after a run by hand = %+v, want 42%%", plan)
	}

	// A failed run keeps the last good plan, and is a failure in the log.
	log := &eventLog{ta: ta}
	log.fresh()
	f.err = errors.New("openrouter: No auth credentials found")
	ta.json("POST", "/api/ai/run", map[string]any{}, http.StatusOK, &s)
	if s.Latest.Status != model.AIRunFailed || s.Latest.Error != "openrouter: No auth credentials found" || !s.InUse ||
		s.Current.ID == s.Latest.ID {
		t.Errorf("after a failure = %+v", s)
	}
	got := log.fresh()
	if len(got) != 1 || got[0].Outcome != model.OutcomeFailure || got[0].Summary != "AI plan (run by hand) failed; the automatic rules apply" {
		t.Errorf("failure event = %+v", got)
	}
	var full model.AIRun
	ta.json("GET", "/api/ai/runs/"+itoa(s.Current.ID), nil, http.StatusOK, &full)
	if !strings.Contains(full.Prompt, "## Trips") || !strings.Contains(full.Response, `"target_soc": 10`) || full.Trips["T1"] == "" ||
		strings.Contains(full.Prompt, "Demo Town") {
		t.Errorf("run detail = %+v", full)
	}
	ta.json("GET", "/api/ai/runs/999", nil, http.StatusNotFound, nil)

	// A broken answer fails too; failed automatic runs retry after an hour.
	f.err, f.content = nil, `{"summary": "missing fields"}`
	ta.app.Store.AddAIRun(model.AIRun{StartedAt: time.Date(2026, 11, 8, 19, 0, 0, 0, ams), FinishedAt: time.Date(2026, 11, 8, 19, 0, 0, 0, ams), Status: model.AIRunOK})
	if _, err := ta.app.Service.RunAI(context.Background(), model.AITriggerManual); err != nil {
		t.Fatal(err)
	}
	if s := ta.aiStatus(); s.Latest.Status != model.AIRunFailed || !strings.Contains(s.Latest.Error, "does not match the schema") {
		t.Errorf("broken answer = %+v", s.Latest)
	}
}

func TestAIModeWithoutAPlanUsesTheAutomaticRules(t *testing.T) {
	ta := newTestApp(t, "")
	ta.pastWeek(0.40)
	ta.tripPlans(model.TripPlansAI)
	ta.app.Service.UseIntegrations(func(i *service.Integrations) {
		i.AI, i.AINote = nil, "Set OPENROUTER_API_KEY to an OpenRouter API key to use AI mode; until then the automatic rules apply"
	})
	ta.checkAI()
	s := ta.aiStatus()
	if s.Latest != nil || s.InUse || !strings.HasPrefix(s.Note, "Set OPENROUTER_API_KEY") || s.Decider != "" {
		t.Fatalf("status without a key = %+v", s)
	}
	code, body := ta.do("POST", "/api/ai/run", map[string]any{})
	if code != http.StatusBadRequest || !strings.Contains(string(body), "OPENROUTER_API_KEY") {
		t.Errorf("run without a key = %d %s", code, body)
	}
	if c := connection(ta, "AI mode"); c.Status != "not_configured" || !strings.HasPrefix(c.Detail, "Set OPENROUTER_API_KEY") {
		t.Errorf("connection = %+v", c)
	}
	// The automatic rules' advice and target stay.
	view, err := ta.app.Service.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.AI == nil || view.AI.InUse || view.AI.CanRun || !strings.HasPrefix(view.AI.Note, "Set OPENROUTER_API_KEY") ||
		view.Control.Target.Source != "" {
		t.Errorf("plan = %+v", view.AI)
	}
	for _, d := range view.Advice.Days {
		if d.AI {
			t.Errorf("%s decided by the AI without a plan", d.Date)
		}
	}
}

func TestAIModeNotifications(t *testing.T) {
	ta := newTestApp(t, "")
	ta.tripPlans(model.TripPlansAI)
	ta.notifications(func(s map[string]any) { s["snoozeMin"] = 5 })
	// Messages that aren't about charging don't mention the mode.
	ta.json("POST", "/api/notifications/test", map[string]any{}, http.StatusOK, nil)
	ta.expectSent("EVCC Trip Planner")
	ta.check()
	ta.expectSent("Is this a car trip?")
	// Charging messages say AI mode is on: here the urgent plug-in reminder.
	ta.at(time.Date(2026, 11, 10, 6, 10, 0, 0, ams))
	ta.check()
	ta.expectSent("AI mode: Plug in by 06:36")
	// Sent again after a snooze, it keeps one label.
	n := ta.notification("urgent:recurring:1:2026-11-10")
	if err := ta.app.Service.HandleAction(context.Background(), "EVCCP_SNOOZE_"+itoa(n.ID)); err != nil {
		t.Fatal(err)
	}
	ta.at(time.Date(2026, 11, 10, 6, 16, 0, 0, ams))
	ta.check()
	ta.expectSent("AI mode: Plug in by 06:36")
}

func TestAIModeCreatesPlansByItsOwnSwitch(t *testing.T) {
	ta := newTestApp(t, "")
	ta.tripPlans(model.TripPlansAI)
	// Monday's AI advice: charge extra to 75% tonight.
	f := &fakeAI{content: strings.Replace(strings.Replace(aiDecision,
		`"date": "2026-11-09", "recommendation": "none", "target_soc": null, "from": null, "until": null,`,
		`"date": "2026-11-09", "recommendation": "charge_extra", "target_soc": 75, "from": "2026-11-09 23:00", "until": "2026-11-10 05:00",`, 1),
		`"target_soc": 95`, `"target_soc": 42`, 1)}
	ta.useAI(f)
	ctx := context.Background()
	var v service.AISettingsView
	ta.json("GET", "/api/settings/ai", nil, http.StatusOK, &v)
	if v.Settings.AutoApply || v.Settings.AutoMinConfidence != 3 || v.Settings.AutoPostpone || v.Settings.AutoSet {
		t.Fatalf("AI auto defaults = %+v", v.Settings)
	}

	// Until set here, acting on the AI's advice follows the automatic rules'
	// choice; daily advice being off doesn't hide the AI's advice.
	var adv map[string]any
	ta.json("GET", "/api/settings/advice", nil, http.StatusOK, &adv)
	adv["enabled"], adv["autoApply"], adv["autoMinConfidence"] = false, true, 2
	ta.json("PUT", "/api/settings/advice", adv, http.StatusOK, nil)
	ta.json("GET", "/api/settings/ai", nil, http.StatusOK, &v)
	if !v.Settings.AutoApply || v.Settings.AutoMinConfidence != 2 || v.Settings.AutoSet {
		t.Fatalf("AI auto following the advice settings = %+v", v.Settings)
	}
	// Saving other AI settings keeps it following.
	ta.json("PUT", "/api/settings/ai", map[string]any{"maxRunsPerDay": 4}, http.StatusOK, &v)
	if v.Settings.AutoSet {
		t.Fatalf("saving the runs set the switch: %+v", v.Settings)
	}

	// Turned off here, it no longer follows them.
	ta.json("PUT", "/api/settings/ai", map[string]any{"autoApply": false}, http.StatusOK, &v)
	if v.Settings.AutoApply || !v.Settings.AutoSet {
		t.Fatalf("AI auto off = %+v", v.Settings)
	}
	ta.checkAI()
	mon := ta.advice().day("2026-11-09")
	if !ta.advice().Advice.Enabled || mon.Recommendation != "charge_extra" || mon.TargetSoC != 75 {
		t.Fatalf("Monday's advice in AI mode = %+v", mon)
	}
	if err := ta.app.Service.AutoApply(ctx); err != nil {
		t.Fatal(err)
	}
	if a := ta.advice().Advice; len(a.Adjustments) != 0 || a.AutoApply {
		t.Fatalf("acted with AI mode's switch off: %+v", a.Adjustments)
	}
	if !strings.Contains(f.last.User, "become plans only when the driver confirms them") {
		t.Error("the prompt doesn't say charge extra needs confirmation")
	}

	// On, it acts from the AI's target and window.
	ta.json("PUT", "/api/settings/ai", map[string]any{"autoApply": true, "autoMinConfidence": 3}, http.StatusOK, nil)
	ta.json("PUT", "/api/settings/ai", map[string]any{"autoMinConfidence": 1}, http.StatusBadRequest, nil)
	if err := ta.app.Service.AutoApply(ctx); err != nil {
		t.Fatal(err)
	}
	a := ta.advice().Advice
	if !a.AutoApply || len(a.Adjustments) != 1 || a.Adjustments[0].Kind != "extra_plan" || a.Adjustments[0].Source != "auto" ||
		a.Adjustments[0].Plan.SoC != 75 || !sameInstant(a.Adjustments[0].ExpiresAt, "2026-11-10T05:00:00+01:00") {
		t.Fatalf("AI auto extra plan = %+v", a.Adjustments)
	}
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 75 {
		t.Errorf("EVCC plan = %+v", plan)
	}
}

func TestANewAIPlanKeepsEVCCsPlanWhenChargeMovesToChargeExtra(t *testing.T) {
	ta := newTestApp(t, "")
	ta.tripPlans(model.TripPlansAI)
	ta.json("PUT", "/api/settings/ai", map[string]any{"autoApply": true, "autoMinConfidence": 3}, http.StatusOK, nil)
	// First 80% as charge ahead, written when the car is plugged in.
	f := &fakeAI{content: strings.Replace(aiDecision, `"target_soc": 95`, `"target_soc": 80`, 1)}
	ta.useAI(f)
	ta.checkAI()
	ta.plug(false, 38)
	ta.plug(true, 38)
	tue := time.Date(2026, 11, 10, 7, 48, 0, 0, ams)
	if p := ta.evccPlan(); p == nil || p.Soc != 80 || !p.Time.Equal(tue) {
		t.Fatalf("plan = %+v", p)
	}
	log := &eventLog{ta: ta}
	log.fresh()

	// Then the AI moves the 80% to tonight's charge extra, charge ahead back
	// to the trip's own 42%: the extra plan replaces the planner's plan
	// directly, without removing it first.
	f.content = strings.Replace(strings.Replace(aiDecision,
		`"date": "2026-11-09", "recommendation": "none", "target_soc": null, "from": null, "until": null,`,
		`"date": "2026-11-09", "recommendation": "charge_extra", "target_soc": 80, "from": "2026-11-09 23:00", "until": "2026-11-10 05:00",`, 1),
		`"target_soc": 95`, `"target_soc": 42`, 1)
	ta.json("POST", "/api/ai/run", map[string]any{}, http.StatusOK, nil)
	var actions []string
	for _, e := range log.fresh() {
		actions = append(actions, e.Action)
	}
	if strings.Join(actions, ",") != "ai.run,adjustment.create" {
		t.Errorf("events = %v, want the run and the extra plan only", actions)
	}
	if p := ta.evccPlan(); p == nil || p.Soc != 80 || !p.Time.Equal(time.Date(2026, 11, 10, 5, 0, 0, 0, ams)) {
		t.Errorf("plan = %+v, want 80%% by 05:00", p)
	}
}

func TestAIModeKeepsARunForTomorrowsPrices(t *testing.T) {
	ta := newTestApp(t, "")
	ta.tripPlans(model.TripPlansAI)
	ta.json("PUT", "/api/settings/ai", map[string]any{"maxRunsPerDay": 2}, http.StatusOK, nil)
	p := newPublishing(ta, 0.30, 0.30)
	ta.plug(false, 38)
	log := &eventLog{ta: ta}
	// The first plan, before Tuesday's prices.
	ta.checkAI()
	log.fresh()
	// A plug-in may not take the day's last run while tomorrow's prices
	// haven't been seen...
	ta.plug(true, 38)
	if e := lastAction(log, "ai.skip"); e == nil || e.Summary !=
		"AI plan not made on plug-in: the last of today's 2 runs is kept for tomorrow's prices; the current plan applies" {
		t.Errorf("skip = %+v", e)
	}
	// ...which then get it.
	p.published = true
	ta.poll()
	ta.checkAI()
	if s := ta.aiStatus(); s.RunsToday != 2 || s.Latest.Trigger != model.AITriggerPrices {
		t.Fatalf("after the prices = %+v", s)
	}
	log.fresh()
	ta.plug(false, 38)
	ta.plug(true, 38)
	if e := lastAction(log, "ai.skip"); e == nil || !strings.Contains(e.Summary, "2 runs today already") {
		t.Errorf("skip at the limit = %+v", e)
	}
}

// lastAction is the newest event with the action since the last look.
func lastAction(l *eventLog, action string) *activityEvent {
	var found *activityEvent
	for _, e := range l.fresh() {
		if e.Action == action {
			found = &e
		}
	}
	return found
}

func TestAIPlanForADifferentTripLeavesTheTargetToTheRules(t *testing.T) {
	ta := newTestApp(t, "")
	ta.tripPlans(model.TripPlansAI)
	ta.checkAI()
	// A trip before Tuesday's school run, which the AI plan was made for.
	ta.json("POST", "/api/trips", map[string]any{
		"title": "Hardware store", "destination": "Bouwmarkt 5, Demo Town", "date": "2026-11-09", "start": "21:00",
	}, http.StatusCreated, nil)
	var p struct {
		Control struct {
			Target struct {
				Title  string `json:"title"`
				Source string `json:"source"`
			} `json:"target"`
		} `json:"control"`
		AI *service.AIPlanView `json:"ai"`
	}
	ta.json("GET", "/api/plan", nil, http.StatusOK, &p)
	want := "This AI plan was made for a different trip. Until the AI runs again, the automatic rules set the target for Hardware store."
	if !p.AI.InUse || p.AI.Note != want || p.Control.Target.Title != "Hardware store" || p.Control.Target.Source == "ai" {
		t.Errorf("plan = %+v, AI = %+v", p.Control, p.AI)
	}
}
