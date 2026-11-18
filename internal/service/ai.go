package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/advice"
	"github.com/diogoap/evcc-planner/internal/aiplan"
	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/openrouter"
	"github.com/diogoap/evcc-planner/internal/planner"
	"github.com/diogoap/evcc-planner/internal/store"
)

// AI mode (AUTOMATION.md): like automatic mode, but an AI model decides
// charge ahead, charge extra, solar, and deferring. The planner keeps the
// foundational rules: every departure's own target, plans on plug-in,
// pauses for chosen plans, and the charge-ahead cap.

// Timings of AI mode.
var (
	// aiMaxAge is how long an AI plan applies: a day's prices come once a
	// day, with some margin.
	aiMaxAge = 30 * time.Hour
	// aiRetryAfter is how long a failed automatic run waits before the
	// next try.
	aiRetryAfter = time.Hour
)

// historyDays is how many past days the AI plan sees.
const historyDays = 30

// maxResponseChars bounds the model's raw answer kept with a run.
const maxResponseChars = 64000

// ---- settings ----

// AISettings returns the AI mode settings.
func (s *Service) AISettings() (model.AISettings, error) {
	v, err := s.Store.AISettings(model.DefaultAISettings())
	if err != nil || v.AutoSet {
		return v, err
	}
	// Acting on the AI's advice follows the automatic rules' choice until
	// it is changed here.
	adv, err := s.AdviceSettings()
	v.AutoApply, v.AutoMinConfidence, v.AutoPostpone = adv.AutoApply, adv.AutoMinConfidence, adv.AutoPostpone
	return v, err
}

// AISettingsView is what Settings → Advice → AI mode edits: the settings with the prompt
// and goals in effect, their defaults, the placeholders, and the model in
// effect.
type AISettingsView struct {
	Settings      model.AISettings `json:"settings"`
	DefaultPrompt string           `json:"defaultPrompt"`
	DefaultGoals  string           `json:"defaultGoals"`
	Macros        []aiplan.Macro   `json:"macros"`
	DefaultModel  string           `json:"defaultModel"` // model.DefaultAIModel
	Decider       string           `json:"decider"`      // how decisions are made
	Note          string           `json:"note,omitempty"`
}

// AISettingsView returns the settings for editing; an empty prompt or goals
// show the default ones.
func (s *Service) AISettingsView() (AISettingsView, error) {
	v, err := s.AISettings()
	if err != nil {
		return AISettingsView{}, err
	}
	goals, err := s.defaultGoals()
	if v.Prompt == "" {
		v.Prompt = aiplan.DefaultPrompt
	}
	if v.Goals == "" {
		v.Goals = goals
	}
	if v.Model == "" {
		v.Model = model.DefaultAIModel
	}
	decider, note := s.aiDecider(v)
	return AISettingsView{
		Settings: v, DefaultPrompt: aiplan.DefaultPrompt, DefaultGoals: goals, Macros: aiplan.Macros,
		DefaultModel: model.DefaultAIModel, Decider: decider, Note: note,
	}, err
}

// SaveAISettings validates and stores the AI mode settings. A model, prompt,
// or goals equal to the default are stored empty, so they follow later
// default (and, for the goals, settings) changes.
func (s *Service) SaveAISettings(ctx context.Context, v model.AISettings) (AISettingsView, error) {
	if v.Model = strings.TrimSpace(v.Model); v.Model == model.DefaultAIModel {
		v.Model = ""
	}
	v.Prompt = strings.TrimSpace(strings.ReplaceAll(v.Prompt, "\r\n", "\n"))
	if v.Prompt == strings.TrimSpace(aiplan.DefaultPrompt) {
		v.Prompt = ""
	}
	goals, err := s.defaultGoals()
	if err != nil {
		return AISettingsView{}, err
	}
	// Acting on the AI's advice stops following the advice settings once it
	// differs from them.
	adv, err := s.AdviceSettings()
	if err != nil {
		return AISettingsView{}, err
	}
	v.AutoSet = v.AutoSet || v.AutoApply != adv.AutoApply || v.AutoMinConfidence != adv.AutoMinConfidence ||
		v.AutoPostpone != adv.AutoPostpone
	if v.Goals = strings.TrimSpace(strings.ReplaceAll(v.Goals, "\r\n", "\n")); v.Goals == strings.TrimSpace(goals) {
		v.Goals = ""
	}
	if err := v.Validate(); err != nil {
		return AISettingsView{}, ValidationError{Msg: err.Error()}
	}
	if u := aiplan.Unknown(v.Prompt); len(u) > 0 {
		return AISettingsView{}, invalid("unknown placeholders in the prompt: {{%s}}", strings.Join(u, "}}, {{"))
	}
	ev := model.AuditEvent{Action: "settings.update", SubjectType: "settings", SubjectID: "ai"}
	before, err := s.AISettings()
	if err != nil {
		return AISettingsView{}, err
	}
	err = s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		if err := tx.SaveAISettings(v); err != nil {
			return err
		}
		e.Before, e.After = changes(aiRecord(before), aiRecord(v))
		e.Summary = settingsSummary("AI settings", e.After)
		return nil
	})
	if err != nil {
		return AISettingsView{}, err
	}
	return s.AISettingsView()
}

// AIPreview is a prompt filled with today's data, as it would be sent.
type AIPreview struct {
	System string `json:"system"`
	Text   string `json:"text"`
	Chars  int    `json:"chars"`
	// Tokens is a rough estimate (four characters a token).
	Tokens int `json:"tokens"`
}

// PreviewAIPrompt fills a prompt and goals (empty: the defaults) with the
// current data without sending it.
func (s *Service) PreviewAIPrompt(ctx context.Context, prompt, goals string) (*AIPreview, error) {
	if strings.TrimSpace(prompt) == "" {
		prompt = aiplan.DefaultPrompt
	}
	if u := aiplan.Unknown(prompt); len(u) > 0 {
		return nil, invalid("unknown placeholders in the prompt: {{%s}}", strings.Join(u, "}}, {{"))
	}
	view, err := s.Plan(ctx)
	if err != nil {
		return nil, err
	}
	in, _ := s.aiInput(view)
	in.Goals = goals
	if ai, err := s.AISettings(); err == nil {
		in.State.AutoExtra = ai.AutoApply
	}
	text, err := aiplan.Render(prompt, in)
	if err != nil {
		return nil, invalid("%s", err.Error())
	}
	n := len([]rune(aiplan.SystemPrompt)) + len([]rune(text))
	return &AIPreview{System: aiplan.SystemPrompt, Text: text, Chars: n, Tokens: n / 4}, nil
}

// aiRecord is the settings as the activity log shows them: a changed
// prompt by its length, not its text.
func aiRecord(v model.AISettings) map[string]any {
	prompt, goals := "default", "default"
	if v.Prompt != "" {
		prompt = fmt.Sprintf("custom (%d characters)", len([]rune(v.Prompt)))
	}
	if v.Goals != "" {
		goals = fmt.Sprintf("custom (%d characters)", len([]rune(v.Goals)))
	}
	m := v.Model
	if m == "" {
		m = "default (" + model.DefaultAIModel + ")"
	}
	return map[string]any{
		"maxRunsPerDay": v.MaxRunsPerDay, "onPrices": v.OnPrices, "onPlugIn": v.OnPlugIn, "model": m, "prompt": prompt, "goals": goals,
		"autoApply": v.AutoApply, "autoMinConfidence": v.AutoMinConfidence, "autoPostpone": v.AutoPostpone,
	}
}

// aiModel is the model a run uses.
func (s *Service) aiModel(v model.AISettings) string {
	if v.Model != "" {
		return v.Model
	}
	return model.DefaultAIModel
}

// aiDecider describes how decisions are made, or why they can't be.
func (s *Service) aiDecider(v model.AISettings) (string, string) {
	cs := s.state()
	switch {
	case cs.AI == nil:
		return "", cs.AINote
	case cs.AI.Name() == "stub":
		return "Stand-in (no AI model): repeats the automatic rules", ""
	}
	return "OpenRouter " + s.aiModel(v), ""
}

// ---- status ----

// AIView is AI mode's status: whether it is on, the latest run, and the
// run whose decisions apply.
type AIView struct {
	Active    bool   `json:"active"` // trip plans are in AI mode
	Running   bool   `json:"running"`
	Decider   string `json:"decider"`
	Note      string `json:"note,omitempty"`
	RunsToday int    `json:"runsToday"`
	MaxRuns   int    `json:"maxRuns"`
	// InUse: Current applies to the plan now.
	InUse   bool       `json:"inUse"`
	Status  string     `json:"status"`
	Latest  *AIRunView `json:"latest"`
	Current *AIRunView `json:"current"`
}

// AIRunView is a run without the prompt and raw answer.
type AIRunView struct {
	ID               int64          `json:"id"`
	StartedAt        time.Time      `json:"startedAt"`
	FinishedAt       time.Time      `json:"finishedAt"`
	Trigger          string         `json:"trigger"`
	Status           string         `json:"status"`
	Error            string         `json:"error,omitempty"`
	Decider          string         `json:"decider"`
	Model            string         `json:"model"`
	PromptTokens     int            `json:"promptTokens"`
	CompletionTokens int            `json:"completionTokens"`
	Result           *aiplan.Result `json:"result"`
}

func aiRunView(r *model.AIRun) *AIRunView {
	if r == nil {
		return nil
	}
	v := &AIRunView{
		ID: r.ID, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Trigger: r.Trigger, Status: r.Status, Error: r.Error,
		Decider: r.Decider, Model: r.Model, PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens,
	}
	if len(r.Result) > 0 {
		var res aiplan.Result
		if json.Unmarshal(r.Result, &res) == nil {
			v.Result = &res
		}
	}
	return v
}

// AIStatus returns AI mode's status.
func (s *Service) AIStatus() (*AIView, error) {
	settings, err := s.Settings()
	if err != nil {
		return nil, err
	}
	ai, err := s.AISettings()
	if err != nil {
		return nil, err
	}
	v := &AIView{Active: settings.TripPlans == model.TripPlansAI, Running: s.aiRunning.Load(), MaxRuns: ai.MaxRunsPerDay}
	v.Decider, v.Note = s.aiDecider(ai)
	if v.RunsToday, err = s.Store.AIRunsSince(localtime.StartOfDay(s.now(), s.loc())); err != nil {
		return nil, err
	}
	latest, err := s.Store.LatestAIRun("")
	if err != nil {
		return nil, err
	}
	cur, why, err := s.currentAIRun()
	if err != nil {
		return nil, err
	}
	v.Latest, v.Current = aiRunView(latest), aiRunView(cur)
	v.InUse = v.Active && cur != nil
	switch {
	case !v.Active:
		v.Status = "Off: choose AI mode under Settings → Planning."
	case cur != nil:
		v.Status = "In use: the AI plan from " + dateTimeText(cur.FinishedAt, s.loc()) + "."
	default:
		v.Status = why
	}
	return v, nil
}

// aiConnection is AI mode's row under Settings → Connections (AI mode only).
func (s *Service) aiConnection() ConnectionView {
	c := ConnectionView{Name: "AI mode", Mode: s.Cfg.OpenRouter.Mode}
	ai, err := s.AISettings()
	if err != nil {
		c.Status, c.Detail = "error", err.Error()
		return c
	}
	decider, note := s.aiDecider(ai)
	latest, err := s.Store.LatestAIRun("")
	cur, _, cerr := s.currentAIRun()
	switch {
	case err != nil || cerr != nil:
		c.Status, c.Detail = "error", errors.Join(err, cerr).Error()
	case decider == "":
		c.Status, c.Detail = "not_configured", note
	case latest != nil && latest.Status == model.AIRunFailed:
		c.Status, c.Detail = "error", decider+"; the last run failed: "+latest.Error
	case s.state().AI.Name() == "stub":
		c.Status, c.Detail = "stub", decider
	case latest == nil:
		c.Status, c.Detail = "ok", decider+"; no plan made yet"
	default:
		c.Status, c.Detail = "ok", decider
	}
	if cur != nil {
		c.LastSuccessAt = &cur.FinishedAt
	}
	return c
}

// AIRun returns one run with what was sent and what came back.
func (s *Service) AIRun(id int64) (model.AIRun, error) { return s.Store.AIRun(id) }

// currentAIRun returns the newest successful run that still applies, or
// why there is none (the automatic rules apply then).
func (s *Service) currentAIRun() (*model.AIRun, string, error) {
	run, err := s.Store.LatestAIRun(model.AIRunOK)
	switch {
	case err != nil:
		return nil, "", err
	case run == nil:
		return nil, "No AI plan yet: the automatic rules apply until the first run.", nil
	case s.now().Sub(run.FinishedAt) > aiMaxAge:
		return nil, "The last AI plan is more than a day old: the automatic rules apply until the next run.", nil
	}
	return run, "", nil
}

// ---- runs ----

// RunAI makes an AI plan now: it fills the prompt with the planner's data,
// asks the model, checks the answer, and stores the run with an activity
// entry. A failed run is stored too; the automatic rules apply until a run
// succeeds. Only one run happens at a time.
func (s *Service) RunAI(ctx context.Context, trigger string) (*model.AIRun, error) {
	if !s.aiMu.TryLock() {
		return nil, invalid("an AI plan is already being made")
	}
	defer s.aiMu.Unlock()
	cs := s.state()
	ai, err := s.AISettings()
	if err != nil {
		return nil, err
	}
	if cs.AI == nil {
		return nil, invalid("%s", cs.AINote)
	}
	s.aiRunning.Store(true)
	defer s.aiRunning.Store(false)

	view, err := s.Plan(ctx)
	if err != nil {
		return nil, err
	}
	in, trips := s.aiInput(view)
	in.Goals, in.State.AutoExtra = ai.Goals, ai.AutoApply
	decider, _ := s.aiDecider(ai)
	run := model.AIRun{StartedAt: s.now(), Trigger: trigger, Decider: decider, Model: s.aiModel(ai), Trips: trips}
	if cs.AI.Name() == "stub" {
		run.Model = "stub"
	}
	if n := len(in.Prices); n > 0 {
		run.PricesUntil = &in.Prices[n-1].End
	}
	if view.Next != nil {
		run.NextTripKey = view.Next.TripKeys[0]
	}
	prompt := ai.Prompt
	if prompt == "" {
		prompt = aiplan.DefaultPrompt
	}
	var content string
	run.Prompt, err = aiplan.Render(prompt, in)
	if err == nil {
		var usage openrouter.Usage
		content, usage, err = cs.AI.Decide(audit.WithCall(ctx, audit.Call{
			Summary: "AI plan with " + run.Model,
			Detail:  map[string]any{"model": run.Model, "trigger": trigger, "promptChars": len(run.Prompt)},
		}), aiplan.Request{Model: run.Model, System: aiplan.SystemPrompt, User: run.Prompt, Input: in})
		run.PromptTokens, run.CompletionTokens = usage.PromptTokens, usage.CompletionTokens
		if usage.Model != "" && cs.AI.Name() != "stub" {
			run.Model = usage.Model
		}
	}
	run.Response = openrouter.Clip(content, maxResponseChars)
	var res aiplan.Result
	if err == nil {
		var d aiplan.Decision
		if d, err = aiplan.Parse(content); err == nil {
			res = aiplan.Validate(d, in)
			run.Result = audit.Marshal(res)
		}
	}
	run.FinishedAt, run.Status = s.now(), model.AIRunOK
	if err != nil {
		run.Status, run.Error = model.AIRunFailed, err.Error()
	}
	ev := model.AuditEvent{
		Action: "ai.run", SubjectType: "ai",
		Detail: audit.Marshal(map[string]any{
			"trigger": trigger, "model": run.Model, "promptTokens": run.PromptTokens, "completionTokens": run.CompletionTokens,
			"warnings": len(res.Warnings),
		}),
	}
	if run.Status == model.AIRunOK {
		ev.Summary = fmt.Sprintf("AI plan (%s): %s", triggerText(trigger), s.aiSummary(res, in))
		ev.After = audit.Marshal(map[string]any{"summary": res.Summary, "warnings": res.Warnings})
	} else {
		ev.Summary = fmt.Sprintf("AI plan (%s) failed; the automatic rules apply", triggerText(trigger))
	}
	if err := s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		saved, err := tx.AddAIRun(run)
		run = saved
		e.SubjectID = strconv.FormatInt(saved.ID, 10)
		if run.Status == model.AIRunFailed {
			e.Outcome, e.Error = model.OutcomeFailure, run.Error
		}
		return err
	}); err != nil {
		return nil, err
	}
	s.Log.Info("AI plan", "trigger", trigger, "status", run.Status, "model", run.Model, "error", run.Error)
	return &run, nil
}

// triggerText says what started a run.
func triggerText(trigger string) string {
	switch trigger {
	case model.AITriggerPrices:
		return "new prices"
	case model.AITriggerPlugIn:
		return "car plugged in"
	case model.AITriggerManual:
		return "run by hand"
	}
	return trigger
}

// aiSummary is a run's decision in a few words for the activity log.
func (s *Service) aiSummary(r aiplan.Result, in aiplan.Input) string {
	var parts []string
	if r.Ahead != nil && in.Next != nil {
		parts = append(parts, fmt.Sprintf("target %d%% by %s", r.Ahead.TargetSoC, s.clockText(in.Next.DepartAt)))
	}
	for _, d := range r.Days {
		day := weekdayLabel(d.Date, s.loc())
		switch d.Recommendation {
		case aiplan.ChargeExtra:
			parts = append(parts, fmt.Sprintf("%s charge extra to %d%%", day, d.TargetSoC))
		case aiplan.Solar:
			parts = append(parts, day+" charge extra from solar")
		case aiplan.Defer:
			parts = append(parts, day+" defer")
		}
	}
	if len(parts) == 0 {
		return "nothing extra to charge"
	}
	return strings.Join(parts, "; ")
}

// RunAINow makes an AI plan from the UI and applies it at once.
func (s *Service) RunAINow(ctx context.Context) (*AIView, error) {
	settings, err := s.Settings()
	if err != nil {
		return nil, err
	}
	run, err := s.RunAI(ctx, model.AITriggerManual)
	if err != nil {
		return nil, err
	}
	if run.Status == model.AIRunOK && settings.TripPlans == model.TripPlansAI {
		s.applyNewAIPlan(ctx)
	}
	return s.AIStatus()
}

// applyNewAIPlan lets advice auto-apply and the automatic check act on a
// new AI plan right away, in that order: when the AI moves charge from
// charge ahead to charge extra, the extra plan is in EVCC before the check
// would remove the planner's plan, so EVCC keeps a plan throughout.
func (s *Service) applyNewAIPlan(ctx context.Context) {
	if err := s.AutoApply(audit.WithSource(ctx, model.SourceAutomation)); err != nil {
		s.Log.Warn("advice auto-apply after the AI plan failed", "err", err)
	}
	if err := s.SyncPlan(ctx); err != nil {
		s.Log.Warn("automatic plan check after the AI plan failed", "err", err)
	}
}

// CheckAI makes an AI plan when one is due in AI mode: after the car is
// plugged in, and when new prices are published (or no AI plan applies),
// up to the runs per day; a failed run is retried after an hour. It runs
// after each EVCC check and only acts on a fresh EVCC state. A plug-in
// while the day's runs are used up is noted in the activity log.
func (s *Service) CheckAI(ctx context.Context) error {
	ctx = audit.WithSource(ctx, model.SourceAutomation)
	plugIn := s.aiPlugIn.Swap(false)
	settings, err := s.Settings()
	if err != nil || settings.TripPlans != model.TripPlansAI || s.state().AI == nil {
		return err
	}
	snap := s.snapshot()
	if snap.state == nil || snap.err != nil || s.now().Sub(snap.okAt) > 3*s.Conn().EVCCPollInterval() {
		return nil
	}
	ai, err := s.AISettings()
	if err != nil {
		return err
	}
	trigger := ""
	switch {
	case plugIn && ai.OnPlugIn:
		trigger = model.AITriggerPlugIn
	case ai.OnPrices:
		due, err := s.aiPricesDue(snap.state)
		if err != nil || !due {
			return err
		}
		trigger = model.AITriggerPrices
	default:
		return nil
	}
	now := s.now()
	runs, err := s.Store.AIRunsSince(localtime.StartOfDay(now, s.loc()))
	if err != nil {
		return err
	}
	// The last run of the day waits for tomorrow's prices until a plan has
	// seen them, so plug-ins can't use it up.
	reserved := trigger == model.AITriggerPlugIn && runs+1 >= ai.MaxRunsPerDay && runs < ai.MaxRunsPerDay
	if reserved {
		seen, err := s.aiSawTomorrow(now)
		if err != nil {
			return err
		}
		reserved = !seen
	}
	if runs >= ai.MaxRunsPerDay || reserved {
		if trigger == model.AITriggerPlugIn {
			summary := fmt.Sprintf("AI plan not made on plug-in: %s today already (Settings → Advice → AI mode); the last plan applies", plural(runs, "run"))
			if reserved {
				summary = fmt.Sprintf("AI plan not made on plug-in: the last of today's %d runs is kept for tomorrow's prices; the current plan applies", ai.MaxRunsPerDay)
			}
			s.record(ctx, model.AuditEvent{Action: "ai.skip", SubjectType: "ai", Summary: summary})
		}
		return nil
	}
	if trigger == model.AITriggerPrices {
		if last, err := s.Store.LatestAIRun(""); err != nil || (last != nil && last.Status == model.AIRunFailed &&
			now.Sub(last.StartedAt) < aiRetryAfter) {
			return err
		}
	}
	run, err := s.RunAI(ctx, trigger)
	if err != nil {
		var ve ValidationError
		if errors.As(err, &ve) {
			return nil // already running, or no model
		}
		return err
	}
	if run.Status == model.AIRunOK {
		s.applyNewAIPlan(ctx)
	}
	return nil
}

// aiSawTomorrow reports whether the current AI plan saw tomorrow's prices.
func (s *Service) aiSawTomorrow(now time.Time) (bool, error) {
	cur, _, err := s.currentAIRun()
	if err != nil || cur == nil || cur.PricesUntil == nil {
		return false, err
	}
	return cur.PricesUntil.After(localtime.AddDays(localtime.StartOfDay(now, s.loc()), 1)), nil
}

// aiPricesDue reports whether EVCC publishes prices beyond the last AI
// plan's, or no AI plan applies.
func (s *Service) aiPricesDue(st *evcc.State) (bool, error) {
	cur, _, err := s.currentAIRun()
	if err != nil || cur == nil {
		return err == nil && len(st.PriceSlots) > 0, err
	}
	var last time.Time
	for _, r := range st.PriceSlots {
		if r.End.After(last) {
			last = r.End
		}
	}
	return cur.PricesUntil == nil || last.Sub(*cur.PricesUntil) >= time.Hour, nil
}

// ---- input ----

// aiInput is the data for the prompt's placeholders, and the trip IDs it
// uses with their trip keys. Trips carry their titles, never their places.
func (s *Service) aiInput(view *PlanView) (aiplan.Input, map[string]string) {
	loc, now := s.loc(), view.Now
	set := view.Settings
	in := aiplan.Input{Now: now, Location: loc, Vehicle: aiVehicle(set)}
	trips := map[string]string{}
	ids := map[string]string{} // first trip key → ID
	for _, b := range view.Blocks {
		if b.Status == string(planner.StatusDone) || b.Status == string(planner.StatusInProgress) {
			continue
		}
		id := fmt.Sprintf("T%d", len(in.Trips)+1)
		trips[id], ids[b.TripKeys[0]] = b.TripKeys[0], id
		t := aiplan.Trip{
			ID: id, Name: tripName(b.Titles), Count: len(b.TripKeys), DepartAt: b.DepartAt, ArriveBy: b.FirstArriveAt, ReturnAt: b.ReturnAt,
			DistanceKm: b.DistanceKm, EnergyKWh: b.EnergyKWh, EnergyPct: b.EnergyPct, RequiredSoC: b.RequiredSoC, Status: b.Status,
		}
		for _, tv := range view.Trips {
			for _, k := range b.TripKeys {
				t.Unconfirmed = t.Unconfirmed || (tv.Key == k && tv.NeedsReview)
			}
		}
		in.Trips = append(in.Trips, t)
	}
	if n := view.Next; n != nil {
		in.Next = &aiplan.Next{
			TripID: ids[n.TripKeys[0]], DepartAt: n.DepartAt, MinTargetSoC: n.TargetSoC, StartSoC: n.StartSoC,
			LatestChargeStart: n.LatestChargeStart,
		}
	}

	snap := s.snapshot()
	var slots []evcc.Rate
	var solar *evcc.SolarForecast
	st := aiplan.State{SoC: view.SoC.Percent, SoCAt: view.SoC.ObservedAt, Location: ""}
	if view.SoC.Estimated {
		st.Projected = view.SoC.Projected
	}
	switch view.SoC.Source {
	case model.SoCSourceEVCC:
		st.SoCSource = "EVCC"
	case model.SoCSourceHomeAssistant:
		st.SoCSource = "Home Assistant"
	}
	if snap.state != nil {
		slots, solar = snap.state.PriceSlots, snap.state.Solar
		if lp, ok := s.loadpoint(snap.state); ok {
			st.PluggedIn, st.Charging, st.Mode, st.CheapLimit = lp.Connected, lp.Charging, modeName(lp.Mode), lp.SmartCostLimit
		}
		if veh, ok := s.resolveVehicle(snap.state); ok {
			in.Vehicle.LimitSoC = veh.LimitSoc
			if veh.Plan != nil {
				by := "EVCC"
				if pv := s.planView(veh); pv != nil && pv.SetByPlanner {
					by = "the planner"
				}
				st.Plan = &aiplan.Plan{SoC: int(math.Round(veh.Plan.Soc)), Time: veh.Plan.Time, By: by}
			}
		}
	}
	s.mu.RLock()
	if s.carLoc != nil {
		st.Location = s.carLoc.State
	}
	s.mu.RUnlock()
	if view.Advice != nil {
		for _, a := range view.Advice.Adjustments {
			switch {
			case a.Status != model.AdjustActive:
			case a.Kind == model.AdjustExtraPlan && a.Plan != nil:
				st.ExtraPlan = &aiplan.Plan{SoC: int(math.Round(a.Plan.SoC)), Time: a.Plan.Time, By: "the planner"}
			case a.Kind == model.AdjustPostpone:
				st.PostponedUntil = ptr(a.ExpiresAt)
			}
		}
	}
	in.State = st

	away := make([][2]time.Time, 0, len(view.Blocks))
	for _, b := range view.Blocks {
		away = append(away, [2]time.Time{b.DepartAt, b.ReturnAt})
	}
	isAway := func(from, to time.Time) bool {
		for _, a := range away {
			if from.Before(a[1]) && to.After(a[0]) {
				return true
			}
		}
		return false
	}
	for _, r := range slots {
		if r.End.After(now) && r.End.After(r.Start) {
			in.Prices = append(in.Prices, aiplan.Slot{Start: r.Start, End: r.End, Price: r.Value, Away: isAway(r.Start, r.End)})
		}
	}
	if solar != nil && len(solar.Points) > 0 {
		for h := now.Truncate(time.Hour); solar.Covers(h.Add(time.Hour)); h = h.Add(time.Hour) {
			from := maxTime(h, now)
			if end := h.Add(time.Hour); end.After(from) {
				in.Solar = append(in.Solar, aiplan.SolarSlot{Start: h, End: end, KW: solar.EnergyKWh(from, end) / end.Sub(from).Hours()})
			}
		}
	}

	today := localtime.StartOfDay(now, loc)
	for _, d := range view.Days {
		start, _ := localtime.ParseDate(loc, d.Date)
		from, end := maxTime(start, now), localtime.AddDays(start, 1)
		day := aiplan.Day{
			Date: d.Date, DistanceKm: d.DistanceKm, EnergyKWh: d.EnergyKWh, Trips: d.TripCount, ChargePct: d.ChargePct,
			TargetSoC: d.TargetSoC, HomeHours: round(blockHomeHours(view.Blocks, from, end), 1),
		}
		if publishedPrice(slots, from, end, start.Equal(today)) != nil {
			day.Published = true
			day.PriceMin, day.PriceAvg, day.PriceMax, _ = dayPrices(slots, from, end)
		}
		// Local sun from EVCC's forecast, as in the daily advice.
		if solar.Covers(end) {
			total := solar.EnergyKWh(from, end)
			home := total
			for _, a := range away {
				if as, ae := maxTime(a[0], from), minTime(a[1], end); ae.After(as) {
					home -= solar.EnergyKWh(as, ae)
				}
			}
			day.SolarKWh, day.SolarHomeKWh = ptr(round(total, 1)), ptr(round(math.Max(0, home), 1))
		}
		in.Days = append(in.Days, day)
	}

	if o, _, err := s.outlook(); err == nil && o != nil {
		ov := &aiplan.Outlook{
			ValidFrom: o.ValidFrom, ValidTo: o.ValidTo, FetchedAt: o.FetchedAt, Summary: o.Weekly.Summary, Trend: o.Weekly.Trend,
			Stale: now.Sub(o.FetchedAt) > outlookMaxAge || o.ValidTo < localtime.Date(now, loc),
		}
		if o.Weekly.Price != nil {
			ov.WeeklyPrice = ptr(o.Weekly.Price.EURPerKWh)
		}
		for _, d := range o.Days {
			od := aiplan.OutlookDay{Date: d.Date, Level: d.Level, Wind: d.Wind, Solar: d.Solar, Note: d.Note}
			if d.Price != nil {
				od.Price = ptr(d.Price.EURPerKWh)
			}
			ov.Days = append(ov.Days, od)
		}
		in.Outlook = ov
	}
	if w, _, err := s.weather(); err == nil && w != nil {
		for _, d := range view.Days {
			if wd := w.Day(d.Date); wd != nil {
				in.Weather = append(in.Weather, aiplan.Weather{
					Date: wd.Date, WindBft: wd.WindBft, WindDir: wd.WindDir, SunshinePct: wd.SunshinePct,
					TempMinC: wd.TempMinLowC, TempMaxC: wd.TempMaxHighC, RainChancePct: wd.PrecipChancePct,
				})
			}
		}
	}
	past, err := s.Store.DayStats(localtime.AddDays(today, -historyDays).Format(localtime.DateLayout),
		localtime.AddDays(today, -1).Format(localtime.DateLayout))
	if err != nil {
		s.Log.Warn("price history for the AI plan", "err", err)
	}
	for _, d := range past {
		in.History = append(in.History, aiplan.HistoryDay{
			Date: d.Date, PriceMin: d.PriceMin, PriceAvg: d.PriceAvg, PriceMax: d.PriceMax, DistanceKm: d.DistanceKm,
			EnergyKWh: d.EnergyKWh, WindBft: d.WindBft, SunshinePct: d.SunshinePct,
		})
	}
	in.Baseline = s.aiBaseline(view, ids)
	return in, trips
}

// aiVehicle is the car, the charger, and the planner's settings for the
// prompt; EVCC's charge limit is added from its state.
func aiVehicle(set model.Settings) aiplan.Vehicle {
	return aiplan.Vehicle{
		CapacityKWh: set.UsableCapacityKWh, ChargeKW: set.ChargePowerKW, EfficiencyPct: set.ChargingEfficiencyPct,
		ConsumptionKWhPer100Km: set.ConsumptionKWhPer100Km, ReservePct: set.ArrivalReservePct,
		ChargeBufferMin: set.ChargeBufferMin, HouseholdKW: set.HouseholdKW, SolarTaxSaving: set.SolarTaxSaving,
		MinSolarKW: planner.MinChargeKW(planParams(set)), MaxAheadSoC: set.ChargeAheadMaxSoC, MinSaving: set.ChargeAheadMinSaving,
	}
}

// defaultGoals are the goals until you write your own, with the current
// settings and EVCC's charge limit.
func (s *Service) defaultGoals() (string, error) {
	set, err := s.Settings()
	if err != nil {
		return "", err
	}
	in := aiplan.Input{Vehicle: aiVehicle(set)}
	if snap := s.snapshot(); snap.state != nil {
		if veh, ok := s.resolveVehicle(snap.state); ok {
			in.Vehicle.LimitSoC = veh.LimitSoc
		}
	}
	return aiplan.DefaultGoals(in), nil
}

// tripName is an away period's name for the prompt: its trips' titles,
// on one line and short. Places and addresses are never sent.
func tripName(titles []string) string {
	var parts []string
	for _, t := range titles {
		if t = strings.Join(strings.Fields(t), " "); t != "" {
			parts = append(parts, openrouter.Clip(t, 60))
		}
	}
	return strings.Join(parts, " + ")
}

// aiBaseline is what the automatic rules decide, for the prompt and the
// stand-in decider.
func (s *Service) aiBaseline(view *PlanView, ids map[string]string) aiplan.Baseline {
	target, days := rulesOf(view)
	var b aiplan.Baseline
	if target != nil {
		b.TargetSoC = target.SoC
		for _, a := range target.Ahead {
			if id := ids[a.TripKey]; id != "" {
				b.AheadIDs = append(b.AheadIDs, id)
			}
		}
	}
	for _, d := range days {
		r := aiplan.DayResult{Date: d.Date, Recommendation: aiplan.None, Confidence: d.Confidence, Summary: d.Summary,
			Rationale: d.Rationale}
		switch {
		case d.NoSuggestion:
		case d.Recommendation == advice.ChargeExtra && d.Solar && d.SolarFrom != nil && d.SolarUntil != nil:
			r.Recommendation, r.From, r.Until = aiplan.Solar, d.SolarFrom, d.SolarUntil
		case d.Recommendation == advice.ChargeExtra && len(d.Blocks) > 0:
			r.Recommendation, r.TargetSoC = aiplan.ChargeExtra, d.TargetSoC
			r.From, r.Until = ptr(d.Blocks[0].From), ptr(d.Blocks[len(d.Blocks)-1].Until)
		case d.Recommendation == advice.Defer:
			r.Recommendation, r.DeferUntil, r.Postpone = aiplan.Defer, d.DeferUntil, d.Action == model.AdjustPostpone
		}
		b.Days = append(b.Days, r)
	}
	return b
}

// rulesOf is the automatic rules' target and advice, also when an AI plan
// replaced them in the view.
func rulesOf(view *PlanView) (*TargetView, []DayAdviceView) {
	if r := view.rules; r != nil {
		return r.target, r.days
	}
	var days []DayAdviceView
	if view.Advice != nil {
		days = view.Advice.Days
	}
	return view.Control.Target, days
}

// blockHomeHours is the time in [from, to) outside the away periods.
func blockHomeHours(blocks []BlockView, from, to time.Time) float64 {
	if !to.After(from) {
		return 0
	}
	total := to.Sub(from)
	for _, b := range blocks {
		if start, end := maxTime(b.DepartAt, from), minTime(b.ReturnAt, to); end.After(start) {
			total -= end.Sub(start)
		}
	}
	return math.Max(0, total.Hours())
}

// ---- applying ----

// rulesBaseline keeps the automatic rules' target and advice when an AI
// plan replaces them, for the next AI run.
type rulesBaseline struct {
	target *TargetView
	days   []DayAdviceView
}

// AIPlanView is AI mode on the Overview: whether an AI plan applies, and
// if not, why.
type AIPlanView struct {
	InUse   bool       `json:"inUse"`
	Running bool       `json:"running"`
	CanRun  bool       `json:"canRun"` // a model (or the stand-in) is set up
	Note    string     `json:"note,omitempty"`
	RunID   int64      `json:"runId,omitempty"`
	RunAt   *time.Time `json:"runAt,omitempty"`
	Summary string     `json:"summary,omitempty"`
}

// applyAI puts the current AI plan's decisions in place of the automatic
// rules' (AI mode): the next departure's target, when the plan was made for
// that departure, and each day's advice. Every departure's own target still
// applies. Without a current AI plan the rules' decisions stay.
func (s *Service) applyAI(view *PlanView, result planner.Result) error {
	cs := s.state()
	view.AI = &AIPlanView{Running: s.aiRunning.Load(), CanRun: cs.AI != nil}
	run, why, err := s.currentAIRun()
	if err != nil {
		return err
	}
	if cs.AI == nil && run == nil {
		why = cs.AINote
	}
	var res aiplan.Result
	if run != nil && json.Unmarshal(run.Result, &res) != nil {
		run, why = nil, "The AI plan could not be read: the automatic rules apply."
	}
	if run == nil {
		view.AI.Note = why
		return nil
	}
	view.AI.InUse, view.AI.RunID, view.AI.RunAt, view.AI.Summary = true, run.ID, &run.FinishedAt, res.Summary
	view.rules = &rulesBaseline{target: view.Control.Target}
	if view.Advice != nil {
		view.rules.days = append([]DayAdviceView(nil), view.Advice.Days...)
	}
	settings := view.Settings
	params := planParams(settings)
	ap := planner.AheadParams{HouseholdKW: settings.HouseholdKW, SolarTaxSaving: settings.SolarTaxSaving}

	if t := view.Control.Target; t != nil {
		switch {
		case res.Ahead == nil || run.NextTripKey != t.TripKey:
			view.AI.Note = "The AI plan was made before " + t.Title + " was the next trip: the automatic rules set its target until the next run."
		default:
			a := planner.ChargeFor(result, params, s.aheadSlots(), ap, float64(res.Ahead.TargetSoC))
			nt := *t
			nt.SoC = max(t.MinSoC, int(math.Min(100, math.Ceil(a.TargetSoC-1e-6))))
			nt.Ahead, nt.SavingEUR, nt.Charging, nt.HorizonEnd = []AheadView{}, 0, []evcc.Rate{}, nil
			nt.Source, nt.Reason = "ai", res.Ahead.Reason
			for _, id := range res.Ahead.TripIDs {
				for _, b := range view.Blocks {
					if b.TripKeys[0] == run.Trips[id] {
						nt.Ahead = append(nt.Ahead, AheadView{TripKey: b.TripKeys[0], Title: b.FirstTitle, DepartAt: b.DepartAt,
							EnergyPct: round(b.EnergyPct, 1)})
					}
				}
			}
			for _, c := range a.Charging {
				nt.Charging = append(nt.Charging, evcc.Rate{Start: c.Start, End: c.End, Value: c.Price})
			}
			if !a.HorizonEnd.IsZero() {
				nt.HorizonEnd = &a.HorizonEnd
			}
			next := view.Next
			nt.Covered = next != nil && next.StartSoC != nil && *next.StartSoC >= float64(nt.SoC)-1e-6
			view.Control.Target = &nt
		}
	}
	if view.Advice == nil || !view.Advice.Enabled {
		return nil
	}
	dismissed, err := s.Store.Dismissals()
	if err != nil {
		return err
	}
	days := map[string]aiplan.DayResult{}
	for _, d := range res.Days {
		days[d.Date] = d
	}
	snap := s.snapshot()
	for i := range view.Advice.Days {
		dv := &view.Advice.Days[i]
		d, ok := days[dv.Date]
		if !ok {
			d = aiplan.DayResult{Date: dv.Date, Recommendation: aiplan.None, Summary: "The AI gave no advice for this day."}
		}
		s.aiDay(dv, d, view, snap, params, ap)
		dv.Dismissed = dismissed[dv.Date] != "" && dismissed[dv.Date] == dv.Recommendation
		s.adviceAction(dv, snap, view.Now)
	}
	return nil
}

// aiDay puts an AI recommendation in a day's advice, keeping the day's
// facts (price, weather, sun). Prices and solar figures are the planner's
// own, not the model's.
func (s *Service) aiDay(dv *DayAdviceView, d aiplan.DayResult, view *PlanView, snap evccSnapshot,
	params planner.Params, ap planner.AheadParams) {
	a := advice.Advice{
		Date: d.Date, Recommendation: advice.None, Confidence: d.Confidence, Summary: d.Summary,
		Rationale: append([]string{}, d.Rationale...), LaterChargePct: dv.LaterChargePct, ExportHint: dv.ExportHint,
	}
	if dv.Price == nil || dv.Price.Basis != advice.BasisPublished {
		a.Outlook = dv.Outlook
	}
	var slots []evcc.Rate
	var solar *evcc.SolarForecast
	if snap.state != nil {
		slots, solar = snap.state.PriceSlots, snap.state.Solar
	}
	switch d.Recommendation {
	case aiplan.ChargeExtra:
		if d.From == nil || d.Until == nil {
			break
		}
		from, until := d.From.In(s.loc()), d.Until.In(s.loc())
		_, avg, _, hours := dayPrices(slots, from, until)
		a.Recommendation, a.TargetSoC = advice.ChargeExtra, d.TargetSoC
		span := advice.Span{From: from, Until: until, Pct: round(hours*params.BatteryPowerKW()/params.CapacityKWh*100, 1)}
		if avg != nil {
			span.Price = *avg
			a.Rationale = append(a.Rationale, fmt.Sprintf("%s to %s averages €%.3f/kWh (published).",
				localtime.Clock(from, s.loc()), localtime.Clock(until, s.loc()), *avg))
		}
		a.Blocks = []advice.Span{span}
		if view.SoC.Known {
			if start := s.extraStart(view, from); start != nil {
				a.ExtraPct = round(math.Max(0, float64(d.TargetSoC)-*start), 1)
			}
		}
	case aiplan.Solar:
		if d.From == nil || d.Until == nil || !solar.Covers(*d.Until) {
			break
		}
		from, until := *d.From, *d.Until
		var away [][2]time.Time
		for _, b := range view.Blocks {
			away = append(away, [2]time.Time{b.DepartAt, b.ReturnAt})
		}
		sc := planner.Solar(solarSlots(solar, slots, from, until, dv.Price), from, away, params, ap)
		if sc.Pct < 1 {
			a.Rationale = append(a.Rationale, "The planner's forecast has too little solar surplus then to charge the car.")
			break
		}
		a.Recommendation, a.Solar, a.SolarPct = advice.ChargeExtra, true, round(sc.Pct, 1)
		a.SolarPrice, a.SolarGridPrice = ptr(round(sc.Price, 3)), ptr(round(sc.GridPrice, 3))
		a.SolarFrom, a.SolarUntil = ptr(sc.From.In(s.loc())), ptr(sc.Until.In(s.loc()))
	case aiplan.Defer:
		a.Recommendation, a.DeferUntil = advice.Defer, d.DeferUntil
	}
	if a.Summary == "" {
		a.Summary = "The AI gave no summary for this day."
	}
	dv.Advice, dv.AI, dv.AIPostpone = a, true, d.Postpone
}

// extraStart is the projected SoC when charge extra's window starts: after
// the last trip back home before it, else now.
func (s *Service) extraStart(view *PlanView, from time.Time) *float64 {
	at := view.SoC.Projected
	for _, b := range view.Blocks {
		if !b.ReturnAt.After(from) && b.ArrivalSoC != nil {
			at = b.ArrivalSoC
		}
	}
	return at
}

// adviceAction sets the plan action a day's advice offers: an extra plan
// for charge extra from the grid, a postponement for today's defer (AI
// mode: when the AI asks for it). Weak, dismissed, or handled advice offers
// none.
func (s *Service) adviceAction(dv *DayAdviceView, snap evccSnapshot, now time.Time) {
	dv.Action, dv.ActionNote = "", ""
	if !actionable(dv.Advice) || dv.Dismissed || dv.Adjustment != nil {
		return
	}
	switch {
	case dv.Recommendation == advice.ChargeExtra && !dv.Solar && len(dv.Blocks) > 0:
		dv.Action = model.AdjustExtraPlan
	case dv.Recommendation == advice.Defer && dv.Date == localtime.Date(now, s.loc()) && (!dv.AI || dv.AIPostpone):
		if dv.ActionNote = s.postponeNeedless(snap, now, dv.DeferUntil); dv.ActionNote == "" {
			dv.Action = model.AdjustPostpone
		}
	}
}
