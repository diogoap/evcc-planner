package aiplan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Recommendations for a day.
const (
	ChargeExtra = "charge_extra" // charge above the trips' needs in a cheap window
	Solar       = "solar"        // keep the car plugged in while the sun charges it
	Defer       = "defer"        // wait for a cheaper day
	None        = "none"
)

// Decision is the model's answer, as the schema asks.
type Decision struct {
	Summary     string        `json:"summary"`
	Important   []string      `json:"important"`
	ChargeAhead DecisionAhead `json:"charge_ahead"`
	Days        []DecisionDay `json:"days"`
}

// DecisionAhead is the target for the next departure.
type DecisionAhead struct {
	TargetSoC *int     `json:"target_soc"`
	TripIDs   []string `json:"trip_ids"`
	Reason    string   `json:"reason"`
}

// DecisionDay is one day's recommendation.
type DecisionDay struct {
	Date           string   `json:"date"`
	Recommendation string   `json:"recommendation"`
	TargetSoC      *int     `json:"target_soc"`
	From           *string  `json:"from"`
	Until          *string  `json:"until"`
	DeferUntil     *string  `json:"defer_until"`
	Postpone       bool     `json:"postpone"`
	Confidence     int      `json:"confidence"`
	Summary        string   `json:"summary"`
	Rationale      []string `json:"rationale"`
}

func object(props map[string]any) map[string]any {
	required := make([]string, 0, len(props))
	for k := range props {
		required = append(required, k)
	}
	sort.Strings(required)
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
}

func text(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func nullable(kind, desc string) map[string]any {
	return map[string]any{"type": []string{kind, "null"}, "description": desc}
}

func list(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// Schema is the strict JSON Schema the answer must follow.
func Schema() map[string]any {
	when := "Local time YYYY-MM-DD HH:MM, or null."
	return object(map[string]any{
		"summary":   text("Two or three sentences: what the plan does and why."),
		"important": list("The inputs that mattered most, each in a few words with its value."),
		"charge_ahead": object(map[string]any{
			"target_soc": nullable("integer", "SoC (%) to have when the next trip leaves, with later trips and any reserve; null without a next trip."),
			"trip_ids":   list("IDs of the later trips this target also charges for, e.g. T3."),
			"reason":     text("Why this target, in one or two sentences."),
		}),
		"days": map[string]any{"type": "array", "description": "One entry per day, from today.", "items": object(map[string]any{
			"date": text("YYYY-MM-DD."),
			"recommendation": map[string]any{"type": "string", "enum": []string{ChargeExtra, Solar, Defer, None},
				"description": "charge_extra, solar, defer, or none."},
			"target_soc":  nullable("integer", "charge_extra: the SoC (%) to charge to; else null."),
			"from":        nullable("string", "charge_extra and solar: when the window starts. "+when),
			"until":       nullable("string", "charge_extra and solar: when it ends (charge_extra: the plan is ready by the next departure after the window starts, so EVCC may also use cheaper slots before that departure). "+when),
			"defer_until": nullable("string", "defer: YYYY-MM-DD of the cheaper day; else null."),
			"postpone":    map[string]any{"type": "boolean", "description": "defer, today only: pause EVCC's cheap-grid charging until defer_until."},
			"confidence":  map[string]any{"type": "integer", "enum": []int{1, 2, 3}, "description": "1 low, 2 medium, 3 high."},
			"summary":     text("One sentence for the day."),
			"rationale":   list("Short reasons, most important first."),
		})},
	})
}

// Parse decodes the model's answer; unknown or missing fields are an error.
func Parse(content string) (Decision, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &top); err != nil {
		return Decision{}, fmt.Errorf("the answer does not match the schema: %w", err)
	}
	var missing []string
	for _, k := range []string{"summary", "important", "charge_ahead", "days"} {
		if _, ok := top[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return Decision{}, fmt.Errorf("the answer does not match the schema: %s missing", strings.Join(missing, ", "))
	}
	var d Decision
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return Decision{}, fmt.Errorf("the answer does not match the schema: %w", err)
	}
	return d, nil
}

// Result is a checked decision, as stored with the run.
type Result struct {
	Summary   string      `json:"summary"`
	Important []string    `json:"important"`
	Ahead     *Ahead      `json:"ahead,omitempty"`
	Days      []DayResult `json:"days"`
	// Warnings say what the check corrected or left out.
	Warnings []string `json:"warnings"`
}

// Ahead is the target for the next departure, within its minimum and the
// charge-ahead cap.
type Ahead struct {
	TargetSoC int      `json:"targetSoc"`
	TripIDs   []string `json:"tripIds"`
	Reason    string   `json:"reason"`
}

// DayResult is a day's checked recommendation. Charge extra has a target and
// a window within the published prices; solar a window on a day with a
// local solar forecast; defer the cheaper day (and, today, whether to
// postpone cheap-grid charging).
type DayResult struct {
	Date           string     `json:"date"`
	Recommendation string     `json:"recommendation"`
	TargetSoC      int        `json:"targetSoc,omitempty"`
	From           *time.Time `json:"from,omitempty"`
	Until          *time.Time `json:"until,omitempty"`
	DeferUntil     string     `json:"deferUntil,omitempty"`
	Postpone       bool       `json:"postpone,omitempty"`
	Confidence     int        `json:"confidence"`
	Summary        string     `json:"summary"`
	Rationale      []string   `json:"rationale"`
}

// Text limits keep what the UI shows readable.
const (
	maxSummary   = 500
	maxLine      = 300
	maxLines     = 8
	minExtraSoC  = 10
	maxTargetSoC = 100
)

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n-1])) + "…"
}

func lines(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s = clip(s, maxLine); s != "" && len(out) < maxLines {
			out = append(out, s)
		}
	}
	return out
}

// Validate checks a decision against the input and the planner's rules,
// correcting what it can (each correction is a warning) and leaving out
// what it cannot use. The next departure's target stays between its own
// minimum and the charge-ahead cap; charge extra needs a target and a
// window with published prices; solar a window on a day with a local solar
// forecast; postponing applies to today only. Days the model left out get
// no result.
func Validate(d Decision, in Input) Result {
	r := Result{Summary: clip(d.Summary, maxSummary), Important: lines(d.Important), Days: []DayResult{}, Warnings: []string{}}
	warn := func(format string, args ...any) { r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...)) }
	r.Ahead = validateAhead(d.ChargeAhead, in, warn)

	days := map[string]int{}
	for i, day := range in.Days {
		days[day.Date] = i
	}
	seen := map[string]bool{}
	for _, dd := range d.Days {
		i, ok := days[dd.Date]
		switch {
		case !ok:
			warn("Left out advice for %s: not one of the plan's seven days.", dd.Date)
			continue
		case seen[dd.Date]:
			warn("Left out a second entry for %s.", dd.Date)
			continue
		}
		seen[dd.Date] = true
		r.Days = append(r.Days, validateDay(dd, in, in.Days[i], warn))
	}
	sort.Slice(r.Days, func(i, j int) bool { return r.Days[i].Date < r.Days[j].Date })
	if r.Summary == "" {
		r.Summary = "The AI gave no summary."
	}
	return r
}

func validateAhead(a DecisionAhead, in Input, warn func(string, ...any)) *Ahead {
	if in.Next == nil {
		if a.TargetSoC != nil {
			warn("Left out the charge-ahead target: there is no upcoming trip.")
		}
		return nil
	}
	n := in.Next
	out := &Ahead{TargetSoC: n.MinTargetSoC, TripIDs: []string{}, Reason: clip(a.Reason, maxLine)}
	if a.TargetSoC == nil {
		warn("No charge-ahead target given: %s keeps its own %d%%.", n.TripID, n.MinTargetSoC)
		return out
	}
	limit := max(n.MinTargetSoC, min(in.Vehicle.MaxAheadSoC, maxTargetSoC))
	switch t := *a.TargetSoC; {
	case t < n.MinTargetSoC:
		warn("Raised the charge-ahead target from %d%% to %d%%, the minimum %s needs.", t, n.MinTargetSoC, n.TripID)
	case t > limit:
		warn("Lowered the charge-ahead target from %d%% to the cap of %d%%.", t, limit)
		out.TargetSoC = limit
	default:
		out.TargetSoC = t
	}
	later := map[string]bool{}
	after := false
	for _, t := range in.Trips {
		if after {
			later[t.ID] = true
		}
		after = after || t.ID == n.TripID
	}
	for _, id := range a.TripIDs {
		if id = strings.TrimSpace(id); later[id] {
			out.TripIDs = append(out.TripIDs, id)
		} else if id != n.TripID {
			warn("Left out trip %s from charge ahead: not a later trip.", id)
		}
	}
	return out
}

func validateDay(dd DecisionDay, in Input, day Day, warn func(string, ...any)) DayResult {
	loc := in.loc()
	out := DayResult{
		Date: dd.Date, Recommendation: dd.Recommendation, Confidence: min(3, max(1, dd.Confidence)),
		Summary: clip(dd.Summary, maxSummary), Rationale: lines(dd.Rationale),
	}
	start, _ := time.ParseInLocation("2006-01-02", dd.Date, loc)
	end := start.AddDate(0, 0, 1)
	label := start.Format("Mon 2 Jan")
	downgrade := func(format string, args ...any) DayResult {
		warn(label+": "+format, args...)
		out.Recommendation, out.TargetSoC, out.From, out.Until, out.DeferUntil, out.Postpone = None, 0, nil, nil, "", false
		return out
	}
	switch dd.Recommendation {
	case ChargeExtra, Solar:
		from, until, err := window(dd.From, dd.Until, loc)
		if err != nil {
			return downgrade("left out %s: %v.", what(dd.Recommendation), err)
		}
		if from.Before(in.Now) {
			from = in.Now
		}
		if dd.Recommendation == ChargeExtra {
			published := pricesUntil(in.Prices)
			switch {
			case published.IsZero() || !from.Before(published):
				return downgrade("left out charge extra: its window has no published prices.")
			case until.After(published):
				warn("%s: ended charge extra at %s, the last published price.", label, published.In(loc).Format("Mon 15:04"))
				until = published
			}
		}
		switch {
		case !until.After(from):
			return downgrade("left out %s: its window has passed.", what(dd.Recommendation))
		case from.Before(start) || !from.Before(end):
			return downgrade("left out %s: its window doesn't start on that day.", what(dd.Recommendation))
		case until.Sub(from) > 24*time.Hour:
			return downgrade("left out %s: its window is longer than a day.", what(dd.Recommendation))
		}
		out.From, out.Until = &from, &until
		if dd.Recommendation == Solar {
			if day.SolarKWh == nil {
				return downgrade("left out charge extra from solar: no local solar forecast for that day.")
			}
			return out
		}
		if dd.TargetSoC == nil {
			return downgrade("left out charge extra: no target given.")
		}
		out.TargetSoC = min(maxTargetSoC, max(minExtraSoC, *dd.TargetSoC))
		if out.TargetSoC != *dd.TargetSoC {
			warn("%s: set charge extra's target to %d%% (it must be %d to %d%%).", label, out.TargetSoC, minExtraSoC, maxTargetSoC)
		}
	case Defer:
		if dd.DeferUntil == nil {
			warn("%s: no cheaper day given for defer.", label)
			return out
		}
		until, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(*dd.DeferUntil), loc)
		last := in.Days[len(in.Days)-1].Date
		if err != nil || !until.After(start) || until.Format("2006-01-02") > last {
			warn("%s: left out the cheaper day %q: it must be a later day of the plan.", label, *dd.DeferUntil)
			return out
		}
		out.DeferUntil = until.Format("2006-01-02")
		today := in.Now.In(loc).Format("2006-01-02")
		out.Postpone = dd.Postpone && dd.Date == today
	case None:
	default:
		return downgrade("unknown recommendation %q.", dd.Recommendation)
	}
	return out
}

func what(rec string) string {
	if rec == Solar {
		return "charge extra from solar"
	}
	return "charge extra"
}

// timeLayouts are the forms a local time may take in the answer.
var timeLayouts = []string{timeLayout, "2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04:05"}

func parseTime(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a time (YYYY-MM-DD HH:MM)", s)
}

func window(from, until *string, loc *time.Location) (time.Time, time.Time, error) {
	if from == nil || until == nil {
		return time.Time{}, time.Time{}, fmt.Errorf("no time window")
	}
	f, err := parseTime(*from, loc)
	if err != nil {
		return f, f, err
	}
	u, err := parseTime(*until, loc)
	return f, u, err
}

func pricesUntil(slots []Slot) time.Time {
	var last time.Time
	for _, s := range slots {
		if s.End.After(last) {
			last = s.End
		}
	}
	return last
}

// ---- stand-in ----

// StubDecision is the labelled stand-in without an OpenRouter key (demo
// mode and tests): it repeats what the automatic rules decide, so the rest
// of AI mode can be tried without a model.
func StubDecision(in Input) Decision {
	d := Decision{
		Summary:   "Stand-in decision (no AI model): the same as the automatic rules.",
		Important: []string{"The automatic rules' charge ahead and daily advice"},
		Days:      []DecisionDay{},
	}
	if in.Next != nil {
		t := max(in.Baseline.TargetSoC, in.Next.MinTargetSoC)
		d.ChargeAhead = DecisionAhead{TargetSoC: &t, TripIDs: append([]string{}, in.Baseline.AheadIDs...),
			Reason: "As the automatic rules: later trips only when charging for them now is cheaper."}
	} else {
		d.ChargeAhead.TripIDs = []string{}
	}
	at := func(t *time.Time) *string {
		if t == nil {
			return nil
		}
		s := in.at(*t)
		return &s
	}
	for _, b := range in.Baseline.Days {
		dd := DecisionDay{
			Date: b.Date, Recommendation: b.Recommendation, From: at(b.From), Until: at(b.Until), Postpone: b.Postpone,
			Confidence: max(1, b.Confidence), Summary: b.Summary, Rationale: append([]string{}, b.Rationale...),
		}
		if b.TargetSoC > 0 {
			dd.TargetSoC = &b.TargetSoC
		}
		if b.DeferUntil != "" {
			dd.DeferUntil = &b.DeferUntil
		}
		d.Days = append(d.Days, dd)
	}
	return d
}
