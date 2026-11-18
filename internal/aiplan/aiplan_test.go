package aiplan

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var ams, _ = time.LoadLocation("Europe/Amsterdam")

func at(day, hour, min int) time.Time { return time.Date(2026, 11, day, hour, min, 0, 0, ams) }

func ptr[T any](v T) *T { return &v }

// sample is Wednesday 18 November 15:00: prices published until Friday
// 00:00, a school run Thursday morning and a longer trip Thursday evening.
func sample() Input {
	in := Input{
		Now: at(18, 15, 0), Location: ams,
		Vehicle: Vehicle{
			CapacityKWh: 59, ChargeKW: 3.68, EfficiencyPct: 90, ConsumptionKWhPer100Km: 18, ReservePct: 15,
			ChargeBufferMin: 30, HouseholdKW: 0.4, SolarTaxSaving: 0.11, MinSolarKW: 1.38, MaxAheadSoC: 90, MinSaving: 0.05,
			LimitSoC: 80,
		},
		State: State{SoC: ptr(30.0), SoCSource: "EVCC", SoCAt: ptr(at(18, 14, 58)), PluggedIn: true, Location: "Office",
			Mode: "smart", CheapLimit: ptr(0.15)},
		Next: &Next{TripID: "T1", DepartAt: at(19, 7, 30), MinTargetSoC: 27, StartSoC: ptr(30.0)},
		Trips: []Trip{
			{ID: "T1", Name: "School run", Count: 1, DepartAt: at(19, 7, 30), ArriveBy: at(19, 8, 15), ReturnAt: at(19, 8, 50), DistanceKm: 40,
				EnergyKWh: 7.2, EnergyPct: 12.2, RequiredSoC: 27.2, Status: "ready"},
			{ID: "T2", Name: "Visit + Dinner", Count: 2, DepartAt: at(19, 16, 0), ArriveBy: at(19, 17, 0), ReturnAt: at(19, 21, 0), DistanceKm: 120,
				EnergyKWh: 21.6, EnergyPct: 36.6, RequiredSoC: 51.6, Status: "charge", Unconfirmed: true},
		},
		Outlook: &Outlook{ValidFrom: "2026-11-16", ValidTo: "2026-11-22", FetchedAt: at(17, 18, 0), Summary: "Windy weekend.",
			Trend: "falling", WeeklyPrice: ptr(0.27), Days: []OutlookDay{{Date: "2026-11-21", Price: ptr(0.22), Level: "cheapest", Wind: "high"}}},
		Weather: []Weather{{Date: "2026-11-18", WindBft: ptr(4), WindDir: "SW", SunshinePct: ptr(30), TempMinC: ptr(3.0), TempMaxC: ptr(9.0)}},
		History: []HistoryDay{
			{Date: "2026-11-16", PriceMin: ptr(0.20), PriceAvg: ptr(0.26), PriceMax: ptr(0.35), DistanceKm: ptr(30.0), EnergyKWh: ptr(5.4)},
			{Date: "2026-11-17", PriceMin: ptr(0.22), PriceAvg: ptr(0.28), PriceMax: ptr(0.37), DistanceKm: ptr(50.0), EnergyKWh: ptr(9.0)},
		},
		Solar: []SolarSlot{{Start: at(19, 9, 0), End: at(19, 10, 0), KW: 0.9}, {Start: at(19, 11, 0), End: at(19, 12, 0), KW: 2.1}},
	}
	for i := 0; i < 7; i++ {
		d := Day{Date: at(18+i, 0, 0).Format("2006-01-02"), HomeHours: 24}
		if i < 2 {
			d.Published, d.PriceMin, d.PriceAvg, d.PriceMax = true, ptr(0.18), ptr(0.25), ptr(0.38)
			d.SolarKWh = ptr(4.0)
		}
		in.Days = append(in.Days, d)
	}
	for t := at(18, 15, 0); t.Before(at(20, 0, 0)); t = t.Add(15 * time.Minute) {
		in.Prices = append(in.Prices, Slot{Start: t, End: t.Add(15 * time.Minute), Price: 0.25, Away: t.Hour() == 17 && t.Day() == 19})
	}
	in.Baseline = Baseline{TargetSoC: 64, AheadIDs: []string{"T2"}, Days: []DayResult{
		{Date: "2026-11-18", Recommendation: ChargeExtra, TargetSoC: 70, From: ptr(at(18, 23, 0)), Until: ptr(at(19, 5, 0)),
			Confidence: 3, Summary: "Cheap night."},
		{Date: "2026-11-19", Recommendation: None, Confidence: 0, Summary: "Nothing."},
	}}
	return in
}

func TestSchemaIsStrict(t *testing.T) {
	var walk func(path string, v any)
	walk = func(path string, v any) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		if props, ok := m["properties"].(map[string]any); ok {
			if m["additionalProperties"] != false {
				t.Errorf("%s allows additional properties", path)
			}
			req, _ := m["required"].([]string)
			if len(req) != len(props) {
				t.Errorf("%s requires %v of %d properties", path, req, len(props))
			}
			for k, p := range props {
				walk(path+"."+k, p)
			}
		}
		if items, ok := m["items"]; ok {
			walk(path+"[]", items)
		}
	}
	walk("$", Schema())
	if _, err := json.Marshal(Schema()); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPromptFillsEveryPlaceholder(t *testing.T) {
	for _, m := range Macros {
		if !strings.Contains(DefaultPrompt, "{{"+m.Name+"}}") && m.Name != "automatic_plan" {
			t.Errorf("the default prompt does not use {{%s}}", m.Name)
		}
	}
	out, err := Render(DefaultPrompt, sample())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "{{") {
		t.Errorf("placeholders left: %s", out)
	}
	for _, want := range []string{
		"Wednesday 2026-11-18 15:00",
		"T2 \"Visit + Dinner\": leaves 2026-11-19 16:00, arrives by 17:00, back home 2026-11-19 21:00; 120 km, 21.6 kWh, 36.6% of the battery; needs 52% at departure (2 trips together; calendar event that may not be a car trip)",
		"stays at or below 90%", "EVCC's charge limit is 80%",
		"published until 2026-11-20 00:00",
		"Thu 2026-11-19:\n  00:00  0.250 0.250 0.250 0.250\n",
		"  17:00  0.250 0.250 0.250 0.250  away\n",
		"A solar kWh costs the hour's price minus €0.11 (tax not paid on own solar).",
		"09:00 0.9 kW (too little to charge) · 11:00 2.1 kW (1.7 kW usable at 0.140)",
		"As a rough guide: about 20% cheaper → around 50%, 30% → around 80%, 50% or more (or negative prices) → up to 100%.",
		"Sat 2026-11-21: about 0.220 average level cheapest wind high",
		"Wed 2026-11-18: wind 4 Bft SW, sunshine 30% of possible, 3 to 9 °C",
		"Averages over 2 days: lowest 0.210, average 0.270, highest 0.360 EUR/kWh; 40 km and 7.2 kWh driving a day",
		"Next trip: T1 \"School run\" leaves 2026-11-19 07:30 and needs at least 27%",
		"- The car is away\n",
		"EVCC charges from the grid by itself at or below €0.150/kWh",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Trips carry their names but no places; the car's zone is only "away".
	if strings.Contains(out, "Office") {
		t.Error("places must not be sent")
	}
	base, err := Render("{{automatic_plan}}", sample())
	if err != nil || !strings.Contains(base, "Charge ahead: 64% by 2026-11-19 07:30, also for T2") ||
		!strings.Contains(base, "Wed 2026-11-18: charge extra to 70% 2026-11-18 23:00 to 2026-11-19 05:00. Cheap night.") {
		t.Errorf("baseline = %q, %v", base, err)
	}
}

func TestYourOwnGoals(t *testing.T) {
	in := sample()
	if out, _ := Render("{{goals}}", in); out != strings.TrimRight(DefaultGoals(in), "\n") {
		t.Errorf("default goals = %q", out)
	}
	in.Goals = "  - Prefer 100% when prices are negative.\n"
	if out, _ := Render("Goals:\n{{goals}}", in); out != "Goals:\n- Prefer 100% when prices are negative." {
		t.Errorf("own goals = %q", out)
	}
}

func TestUnknownPlaceholders(t *testing.T) {
	p := "Prices {{energy_prices}} and {{ Trips }}, {{wether}} {{wether}} {{foo_bar}}"
	if got := Unknown(p); strings.Join(got, ",") != "wether,foo_bar" {
		t.Errorf("unknown = %v", got)
	}
	if got := Used(p); strings.Join(got, ",") != "energy_prices,trips" {
		t.Errorf("used = %v", got)
	}
	if _, err := Render(p, sample()); err == nil || !strings.Contains(err.Error(), "{{wether}}, {{foo_bar}}") {
		t.Errorf("render error = %v", err)
	}
	if out, err := Render("Hi {{ now }}.", sample()); err != nil || out != "Hi Wednesday 2026-11-18 15:00 (Europe/Amsterdam)." {
		t.Errorf("render = %q, %v", out, err)
	}
}

func TestValidateKeepsThePlannersRules(t *testing.T) {
	in := sample()
	in.Days[2].SolarKWh = ptr(3.0)
	str := func(s string) *string { return &s }
	d := Decision{
		Summary: "  Charge tonight.  ", Important: []string{"Night at €0.18", ""},
		ChargeAhead: DecisionAhead{TargetSoC: ptr(95), TripIDs: []string{"T2", "T9", "T1"}, Reason: "Long trip Thursday."},
		Days: []DecisionDay{
			{Date: "2026-11-18", Recommendation: ChargeExtra, TargetSoC: ptr(120), From: str("2026-11-18 14:00"),
				Until: str("2026-11-19 05:00"), Confidence: 5, Summary: "Cheap night."},
			{Date: "2026-11-18", Recommendation: None},
			{Date: "2026-11-19", Recommendation: ChargeExtra, TargetSoC: ptr(80), From: str("2026-11-19 20:00"), Until: str("2026-11-20 02:00")},
			{Date: "2026-11-20", Recommendation: Solar, From: str("2026-11-20T11:00"), Until: str("2026-11-20 14:00"), Confidence: 2},
			{Date: "2026-11-21", Recommendation: Defer, DeferUntil: str("2026-11-22"), Postpone: true, Confidence: 1},
			{Date: "2026-11-22", Recommendation: Solar, From: str("2026-11-22 11:00"), Until: str("2026-11-22 13:00")},
			{Date: "2026-11-23", Recommendation: ChargeExtra, TargetSoC: ptr(80), From: str("2026-11-23 01:00"), Until: str("2026-11-23 05:00")},
			{Date: "2026-12-01", Recommendation: None},
		},
	}
	r := Validate(d, in)
	if r.Summary != "Charge tonight." || len(r.Important) != 1 {
		t.Errorf("text = %q %v", r.Summary, r.Important)
	}
	if r.Ahead == nil || r.Ahead.TargetSoC != 90 || strings.Join(r.Ahead.TripIDs, ",") != "T2" {
		t.Errorf("ahead = %+v", r.Ahead)
	}
	byDate := map[string]DayResult{}
	for _, day := range r.Days {
		byDate[day.Date] = day
	}
	if len(r.Days) != 6 {
		t.Fatalf("days = %+v", r.Days)
	}
	// Charge extra starts now at the earliest and ends with the published prices.
	if w := byDate["2026-11-18"]; w.Recommendation != ChargeExtra || w.TargetSoC != 100 || !w.From.Equal(in.Now) ||
		!w.Until.Equal(at(19, 5, 0)) || w.Confidence != 3 {
		t.Errorf("Wednesday = %+v", w)
	}
	if w := byDate["2026-11-19"]; w.Recommendation != ChargeExtra || w.TargetSoC != 80 || !w.Until.Equal(at(20, 0, 0)) {
		t.Errorf("Thursday = %+v", w)
	}
	if s := byDate["2026-11-20"]; s.Recommendation != Solar || !s.From.Equal(at(20, 11, 0)) || !s.Until.Equal(at(20, 14, 0)) {
		t.Errorf("Friday = %+v", s)
	}
	// No published prices Monday; no solar forecast Sunday.
	if m := byDate["2026-11-23"]; m.Recommendation != None || m.From != nil {
		t.Errorf("Monday = %+v", m)
	}
	if s := byDate["2026-11-22"]; s.Recommendation != None {
		t.Errorf("Sunday = %+v", s)
	}
	// Defer keeps its day, but postponing is for today only.
	if s := byDate["2026-11-21"]; s.Recommendation != Defer || s.DeferUntil != "2026-11-22" || s.Postpone {
		t.Errorf("Saturday = %+v", s)
	}
	joined := strings.Join(r.Warnings, "\n")
	for _, want := range []string{
		"Lowered the charge-ahead target from 95% to the cap of 90%.",
		"Left out trip T9 from charge ahead",
		"Left out a second entry for 2026-11-18.",
		"Thu 19 Nov: ended charge extra at Fri 00:00, the last published price",
		"set charge extra's target to 100%",
		"Mon 23 Nov: left out charge extra: its window has no published prices.",
		"Sun 22 Nov: left out charge extra from solar: no local solar forecast",
		"Left out advice for 2026-12-01",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "T1") {
		t.Errorf("the next trip itself is no warning: %s", joined)
	}

	// Below the minimum is raised; today's postponement is kept.
	d = Decision{ChargeAhead: DecisionAhead{TargetSoC: ptr(10)}, Days: []DecisionDay{
		{Date: "2026-11-18", Recommendation: Defer, DeferUntil: str("2026-11-19"), Postpone: true, Confidence: 3},
		{Date: "2026-11-19", Recommendation: ChargeExtra, From: str("2026-11-19 01:00"), Until: str("2026-11-19 05:00")},
	}}
	r = Validate(d, in)
	if r.Ahead.TargetSoC != 27 || !strings.Contains(r.Warnings[0], "Raised the charge-ahead target from 10% to 27%") {
		t.Errorf("ahead = %+v, warnings %v", r.Ahead, r.Warnings)
	}
	if r.Days[0].Recommendation != Defer || !r.Days[0].Postpone || r.Days[1].Recommendation != None {
		t.Errorf("days = %+v", r.Days)
	}
	if r.Summary == "" {
		t.Error("a missing summary gets a placeholder")
	}

	// Without a next departure, no target.
	in.Next = nil
	if r := Validate(Decision{ChargeAhead: DecisionAhead{TargetSoC: ptr(60)}}, in); r.Ahead != nil || len(r.Warnings) != 1 {
		t.Errorf("no next trip: %+v", r)
	}
}

func TestStubRepeatsTheAutomaticRules(t *testing.T) {
	in := sample()
	data, err := json.Marshal(StubDecision(in))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	r := Validate(d, in)
	if r.Ahead.TargetSoC != 64 || strings.Join(r.Ahead.TripIDs, ",") != "T2" || len(r.Warnings) != 0 {
		t.Errorf("stub = %+v", r)
	}
	if w := r.Days[0]; w.Recommendation != ChargeExtra || w.TargetSoC != 70 || !w.From.Equal(at(18, 23, 0)) || w.Confidence != 3 {
		t.Errorf("Wednesday = %+v", w)
	}
	if _, err := Parse(`{"summary":"x","important":[],"charge_ahead":{"target_soc":null,"trip_ids":[],"reason":""},"days":[],"extra":1}`); err == nil {
		t.Error("unknown fields must be rejected")
	}
}
