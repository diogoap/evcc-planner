package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/advice"
	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/forecast"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/planner"
	"github.com/diogoap/evcc-planner/internal/store"
)

// Freshness limits for forecast sources.
const (
	outlookMaxAge = 8 * 24 * time.Hour // the article is weekly
	weatherMaxAge = 36 * time.Hour     // KNMI updates several times a day
)

// AdviceView is the daily advice for the plan window.
type AdviceView struct {
	Enabled     bool               `json:"enabled"`
	AutoApply   bool               `json:"autoApply"`
	Days        []DayAdviceView    `json:"days"`
	Sources     []SourceView       `json:"sources"`
	Adjustments []model.Adjustment `json:"adjustments"`
}

// DayAdviceView is one day's advice with its inputs.
type DayAdviceView struct {
	advice.Advice
	Price        *advice.DayPrice     `json:"price"`
	Weather      *forecast.WeatherDay `json:"weather"`
	SolarKWh     *float64             `json:"solarKWh"`     // EVCC forecast, the (remaining) day
	SolarHomeKWh *float64             `json:"solarHomeKWh"` // ... while the car is home
	SunSource    string               `json:"sunSource"`    // evcc (local), knmi (national), or empty
	MarketWind   string               `json:"marketWind,omitempty"`
	MarketWindGW *float64             `json:"marketWindGW,omitempty"`
	OutlookNote  string               `json:"outlookNote,omitempty"`
	Dismissed    bool                 `json:"dismissed"`
	// Action is the plan action offered (extra_plan or postpone); empty for
	// weak, no-data, dismissed, or "none" advice.
	Action string `json:"action,omitempty"`
	// ActionNote says why advice that would offer an action offers none
	// because it would change nothing in EVCC.
	ActionNote string            `json:"actionNote,omitempty"`
	Adjustment *model.Adjustment `json:"adjustment,omitempty"` // active for this day
}

// SourceView is one advice input with its freshness.
type SourceView struct {
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`   // published, estimate, weather
	Status    string     `json:"status"` // ok, stale, missing, error, stub
	Detail    string     `json:"detail"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

// AdviceSettings returns the effective advice settings.
func (s *Service) AdviceSettings() (model.AdviceSettings, error) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.advice == nil {
		v, err := s.Store.AdviceSettings(model.DefaultAdviceSettings())
		if err != nil {
			return v, err
		}
		s.advice = &v
	}
	return *s.advice, nil
}

// SaveAdviceSettings validates and stores advice settings.
func (s *Service) SaveAdviceSettings(ctx context.Context, v model.AdviceSettings) (model.AdviceSettings, error) {
	if err := v.Validate(); err != nil {
		return v, ValidationError{Msg: err.Error()}
	}
	ev := model.AuditEvent{Action: "settings.update", SubjectType: "settings", SubjectID: "advice"}
	err := s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		before, err := tx.AdviceSettings(model.DefaultAdviceSettings())
		if err != nil {
			return err
		}
		if err := tx.SaveAdviceSettings(v); err != nil {
			return err
		}
		e.Before, e.After = changes(before, v)
		e.Summary = settingsSummary("advice settings", e.After)
		return nil
	})
	if err == nil {
		s.cacheMu.Lock()
		s.advice = &v
		s.cacheMu.Unlock()
	}
	return v, err
}

// Dismiss hides a day's advice actions without changing anything in EVCC;
// undo brings them back.
func (s *Service) Dismiss(ctx context.Context, date string, undo bool) error {
	if _, err := time.Parse(localtime.DateLayout, date); err != nil {
		return invalid("invalid date")
	}
	ev := model.AuditEvent{SubjectType: "advice", SubjectID: date}
	if undo {
		ev.Action, ev.Summary = "advice.undismiss", "Restored the advice for "+s.dateText(date)
		return s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
			e.Before, e.After = audit.Marshal(map[string]bool{"dismissed": true}), audit.Marshal(map[string]bool{"dismissed": false})
			return tx.Undismiss(date)
		})
	}
	day, _, err := s.adviceFor(ctx, date)
	if err != nil {
		return err
	}
	ev.Action = "advice.dismiss"
	ev.Summary = fmt.Sprintf("Dismissed the advice for %s (%s); nothing changed in EVCC", s.dateText(date),
		strings.ReplaceAll(day.Recommendation, "_", " "))
	ev.Before, ev.After = audit.Marshal(map[string]bool{"dismissed": false}), audit.Marshal(map[string]bool{"dismissed": true})
	ev.Detail = adviceDetail(day)
	return s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		return tx.Dismiss(date, day.Recommendation, s.now())
	})
}

// adviceDetail is the advice an action was based on.
func adviceDetail(day *DayAdviceView) json.RawMessage {
	d := map[string]any{"adviceDate": day.Date, "recommendation": day.Recommendation, "confidence": day.Confidence}
	if day.Indicator != "" {
		d["chargeIndicator"] = day.Indicator
	}
	return audit.Marshal(d)
}

// adviceFor returns the current advice for a date in the window.
func (s *Service) adviceFor(ctx context.Context, date string) (*DayAdviceView, *PlanView, error) {
	view, err := s.Plan(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !view.Advice.Enabled {
		return nil, view, invalid("daily advice is turned off in Settings")
	}
	for i := range view.Advice.Days {
		if view.Advice.Days[i].Date == date {
			return &view.Advice.Days[i], view, nil
		}
	}
	return nil, view, invalid("no advice for %s: pick a day in the next seven days", date)
}

// buildAdvice computes the advice view from the plan's blocks, EVCC's
// published prices, and the stored forecasts.
func (s *Service) buildAdvice(view *PlanView, result planner.Result, socKnown bool) (*AdviceView, error) {
	settings, err := s.AdviceSettings()
	if err != nil {
		return nil, err
	}
	out := &AdviceView{
		Enabled: settings.Enabled, AutoApply: settings.AutoApply,
		Days: []DayAdviceView{}, Sources: []SourceView{}, Adjustments: []model.Adjustment{},
	}
	now := view.Now
	today := localtime.Date(now, s.loc())
	adjustments, err := s.Store.Adjustments(today)
	if err != nil {
		return nil, err
	}
	out.Adjustments = adjustments
	if !settings.Enabled {
		return out, nil
	}
	outlook, orec, err := s.outlook()
	if err != nil {
		return nil, err
	}
	weather, wrec, err := s.weather()
	if err != nil {
		return nil, err
	}
	dismissed, err := s.Store.Dismissals()
	if err != nil {
		return nil, err
	}
	snap := s.snapshot()
	var slots []evcc.Rate
	var solar *evcc.SolarForecast
	if snap.state != nil {
		slots, solar = snap.state.PriceSlots, snap.state.Solar
	}
	outlookStale := outlook != nil && now.Sub(outlook.FetchedAt) > outlookMaxAge

	in := advice.Input{SoCKnown: socKnown, SolarMinKW: planner.MinChargeKW(planParams(view.Settings))}
	var away [][2]time.Time
	for _, b := range result.Blocks {
		away = append(away, [2]time.Time{b.DepartAt, b.ReturnAt})
	}
	for _, d := range view.Days {
		day := advice.Day{Date: d.Date, Label: weekdayLabel(d.Date, s.loc()), Name: weekdayName(d.Date, s.loc())}
		start, _ := localtime.ParseDate(s.loc(), d.Date)
		end := localtime.AddDays(start, 1)
		day.HomeHours = homeHours(result.Blocks, maxTime(start, now), end)
		day.Price = publishedPrice(slots, maxTime(start, now), end, d.Date == today)
		if outlook != nil && outlook.Covers(d.Date) {
			od := outlook.Day(d.Date)
			if day.Price == nil && od != nil && od.Price != nil {
				day.Price = &advice.DayPrice{Value: od.Price.EURPerKWh, Basis: advice.BasisEstimate,
					Detail: estimateDetail(od.Price), Stale: outlookStale}
			}
			if day.Price == nil && outlook.Weekly.Price != nil {
				day.Price = &advice.DayPrice{Value: outlook.Weekly.Price.EURPerKWh, Basis: advice.BasisWeekly,
					Detail: "weekly average from the Tibber outlook", Stale: outlookStale}
			}
			if od != nil {
				day.Level, day.MarketWind, day.MarketWindGW = od.Level, od.Wind, od.WindGW
			}
		}
		// Local sun comes from EVCC's solar forecast (today and tomorrow);
		// later days use KNMI's national sunshine.
		if weather != nil {
			if wd := weather.Day(d.Date); wd != nil {
				day.WindBft, day.SunshinePct = wd.WindBft, wd.SunshinePct
			}
		}
		if solar.Covers(end) {
			from := maxTime(start, now)
			total := solar.EnergyKWh(from, end)
			home := total
			for _, b := range result.Blocks {
				if bs, be := maxTime(b.DepartAt, from), minTime(b.ReturnAt, end); be.After(bs) {
					home -= solar.EnergyKWh(bs, be)
				}
			}
			day.SolarKWh, day.SolarHomeKWh = ptr(round(total, 1)), ptr(round(math.Max(0, home), 1))
			day.SunshinePct = nil
			// What the surplus can charge while home, valued as in charging ahead.
			sc := planner.Solar(solarSlots(solar, slots, from, end, day.Price), from, away, planParams(view.Settings),
				planner.AheadParams{HouseholdKW: view.Settings.HouseholdKW, SolarTaxSaving: view.Settings.SolarTaxSaving})
			if sc.Pct > 0 {
				day.Solar = &advice.Solar{Pct: sc.Pct, Price: sc.Price, GridPrice: sc.GridPrice, From: sc.From, Until: sc.Until}
			}
		}
		in.Days = append(in.Days, day)
	}
	for i, b := range result.Blocks {
		if b.Status == planner.StatusDone || b.Status == planner.StatusInProgress {
			continue
		}
		in.Departures = append(in.Departures, advice.Departure{
			Date: localtime.Date(b.DepartAt, s.loc()), DepartAt: b.DepartAt,
			ChargePct: view.Blocks[i].ChargePct, EnergyPct: b.EnergyPct,
		})
	}

	for i, a := range advice.Advise(in) {
		d := in.Days[i]
		dv := DayAdviceView{
			Advice: a, Price: d.Price, MarketWind: d.MarketWind, MarketWindGW: d.MarketWindGW,
			SolarKWh: d.SolarKWh, SolarHomeKWh: d.SolarHomeKWh,
		}
		switch {
		case d.SolarKWh != nil:
			dv.SunSource = "evcc"
		case d.SunshinePct != nil:
			dv.SunSource = "knmi"
		}
		if weather != nil {
			dv.Weather = weather.Day(a.Date)
		}
		if outlook != nil {
			if od := outlook.Day(a.Date); od != nil {
				dv.OutlookNote = od.Note
			}
		}
		dv.Dismissed = dismissed[a.Date] != "" && dismissed[a.Date] == a.Recommendation
		for j := range adjustments {
			if adjustments[j].AdviceDate == a.Date && adjustments[j].Status == model.AdjustActive {
				dv.Adjustment = &adjustments[j]
			}
		}
		if actionable(a) && !dv.Dismissed && dv.Adjustment == nil {
			switch {
			// Charging ahead from solar needs no plan: EVCC charges from the
			// sun by itself while the car is plugged in.
			case a.Recommendation == advice.ChargeAhead && !a.Solar:
				dv.Action = model.AdjustExtraPlan
			case a.Recommendation == advice.Defer && a.Date == today:
				if dv.ActionNote = s.postponeNeedless(snap, now, a.DeferUntil); dv.ActionNote == "" {
					dv.Action = model.AdjustPostpone
				}
			}
		}
		out.Days = append(out.Days, dv)
	}
	out.Sources = s.adviceSources(now, slots, solar, outlook, orec, weather, wrec, view.Days)
	return out, nil
}

// actionable reports whether advice may lead to a plan action: weak (low
// confidence) and no-data advice never do.
func actionable(a advice.Advice) bool {
	return !a.NoSuggestion && a.Confidence >= 2 && (a.Recommendation == advice.ChargeAhead || a.Recommendation == advice.Defer)
}

func estimateDetail(p *forecast.Price) string {
	if p.Provenance == "converted" {
		return "Tibber outlook estimate, converted to incl. VAT"
	}
	return "Tibber outlook estimate"
}

// publishedPrice averages EVCC's published slots in [from, to); today only
// counts the slots still ahead.
func publishedPrice(slots []evcc.Rate, from, to time.Time, today bool) *advice.DayPrice {
	var sum, hours float64
	var low *evcc.Rate
	for i, r := range slots {
		start, end := maxTime(r.Start, from), minTime(r.End, to)
		if !end.After(start) {
			continue
		}
		h := end.Sub(start).Hours()
		sum += r.Value * h
		hours += h
		if low == nil || r.Value < low.Value {
			low = &slots[i]
		}
	}
	if hours < 0.25 || (!today && hours < 20) {
		return nil
	}
	detail := "published day-ahead prices"
	if today {
		detail = fmt.Sprintf("published prices for the remaining %s", hoursText(hours))
	}
	minAt := maxTime(low.Start, from)
	return &advice.DayPrice{
		Value: math.Round(sum/hours*10000) / 10000, Basis: advice.BasisPublished, Detail: detail,
		Min: ptr(low.Value), MinAt: &minAt,
	}
}

// homeHours is the time in [from, to) outside away blocks.
func homeHours(blocks []planner.Block, from, to time.Time) float64 {
	if !to.After(from) {
		return 0
	}
	total := to.Sub(from)
	for _, b := range blocks {
		start, end := maxTime(b.DepartAt, from), minTime(b.ReturnAt, to)
		if end.After(start) {
			total -= end.Sub(start)
		}
	}
	return math.Max(0, total.Hours())
}

func (s *Service) adviceSources(now time.Time, slots []evcc.Rate, solar *evcc.SolarForecast, o *forecast.Outlook, orec *store.ForecastRecord,
	w *forecast.Weather, wrec *store.ForecastRecord, days []DayView) []SourceView {
	var out []SourceView
	ev := SourceView{Name: "EVCC prices", Kind: "published"}
	if len(slots) == 0 {
		ev.Status, ev.Detail = "missing", "No published prices from EVCC"
	} else {
		last := slots[len(slots)-1].End
		ev.Status, ev.Detail = "ok", "Published until "+dateTimeText(last, s.loc())
		if localtime.Clock(last, s.loc()) == "00:00" {
			ev.Detail = "Published through " + last.Add(-time.Minute).In(s.loc()).Format("Mon 2 Jan")
		}
		if s.Cfg.EVCC.Mode == config.ModeStub {
			ev.Status = "stub"
		}
		snap := s.snapshot()
		ev.UpdatedAt = &snap.okAt
	}
	out = append(out, ev)

	sol := SourceView{Name: "EVCC solar", Kind: "solar"}
	covered := 0
	for _, d := range days {
		start, _ := localtime.ParseDate(s.loc(), d.Date)
		if solar.Covers(localtime.AddDays(start, 1)) {
			covered++
		}
	}
	switch {
	case solar == nil:
		sol.Status, sol.Detail = "missing", "EVCC has no solar forecast (configure one in EVCC)"
	default:
		last := solar.Points[len(solar.Points)-1].Time
		sol.Status = "ok"
		sol.Detail = fmt.Sprintf("Local PV forecast until %s · %d of %d days; later days use KNMI sunshine",
			dateTimeText(last, s.loc()), covered, len(days))
		if s.Cfg.EVCC.Mode == config.ModeStub {
			sol.Status = "stub"
		}
		snap := s.snapshot()
		sol.UpdatedAt = &snap.okAt
	}
	out = append(out, sol)

	tib := SourceView{Name: "Tibber outlook", Kind: "estimate"}
	switch {
	case o == nil:
		tib.Status, tib.Detail = "missing", "No weekly outlook yet"
		if orec != nil && orec.LastError != "" {
			tib.Status, tib.Detail = "error", orec.LastError
		}
	default:
		tib.Status = "ok"
		tib.Detail = fmt.Sprintf("Report %s to %s · %s", o.ValidFrom, o.ValidTo, o.Extraction)
		tib.UpdatedAt = &o.FetchedAt
		today := localtime.Date(now, s.loc())
		switch {
		case orec != nil && orec.LastError != "":
			tib.Status, tib.Detail = "error", orec.LastError+" (showing the last valid outlook: "+o.ValidFrom+" to "+o.ValidTo+")"
		case now.Sub(o.FetchedAt) > outlookMaxAge || o.ValidTo < today:
			tib.Status = "stale"
		case s.Clients().Forecasts.Article.Name() == "stub" || s.Clients().Forecasts.Extractor.Name() == "pattern":
			tib.Status = "stub"
		}
	}
	out = append(out, tib)

	kn := SourceView{Name: "KNMI weather", Kind: "weather"}
	switch {
	case s.Clients().Forecasts.Weather == nil:
		kn.Status, kn.Detail = "missing", s.Clients().Forecasts.WeatherNote
	case w == nil:
		kn.Status, kn.Detail = "missing", "No weather outlook yet"
		if wrec != nil && wrec.LastError != "" {
			kn.Status, kn.Detail = "error", wrec.LastError
		}
	default:
		covered, missing := 0, ""
		for _, d := range days {
			if w.Day(d.Date) != nil {
				covered++
			} else {
				if missing != "" {
					missing += ", "
				}
				missing += weekdayLabel(d.Date, s.loc())
			}
		}
		kn.Status = "ok"
		kn.Detail = fmt.Sprintf("National outlook: wind, and sunshine after EVCC's forecast · %d of %d days", covered, len(days))
		if missing != "" {
			kn.Detail += " (no forecast for " + missing + ")"
		}
		kn.UpdatedAt = w.IssuedAt
		switch {
		case wrec != nil && wrec.LastError != "":
			kn.Status, kn.Detail = "error", wrec.LastError+" (showing the last valid outlook)"
		case w.IssuedAt != nil && now.Sub(*w.IssuedAt) > weatherMaxAge:
			kn.Status = "stale"
		case s.Clients().Forecasts.Weather.Name() == "stub":
			kn.Status = "stub"
		}
	}
	return append(out, kn)
}

func weekdayLabel(date string, loc *time.Location) string {
	t, err := localtime.ParseDate(loc, date)
	if err != nil {
		return date
	}
	return t.Weekday().String()[:3]
}

// weekdayName is the full name, e.g. "Sunday", for advice summaries.
func weekdayName(date string, loc *time.Location) string {
	t, err := localtime.ParseDate(loc, date)
	if err != nil {
		return date
	}
	return t.Weekday().String()
}

func hoursText(h float64) string {
	if h < 1 {
		return fmt.Sprintf("%d min", int(math.Round(h*60)))
	}
	return fmt.Sprintf("%.0f h", math.Round(h))
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// solarSlots are the quarter hours from `from` to `to` with forecast solar
// power, priced from EVCC's published slot or, without one, the day's price
// level.
func solarSlots(solar *evcc.SolarForecast, rates []evcc.Rate, from, to time.Time, day *advice.DayPrice) []planner.Slot {
	var out []planner.Slot
	for t := from.Truncate(15 * time.Minute); t.Before(to); t = t.Add(15 * time.Minute) {
		start, end := maxTime(t, from), minTime(t.Add(15*time.Minute), to)
		if !end.After(start) {
			continue
		}
		kw := solar.EnergyKWh(start, end) / end.Sub(start).Hours()
		if kw <= 0 {
			continue
		}
		price, ok := 0.0, false
		for _, r := range rates {
			if !start.Before(r.Start) && start.Before(r.End) {
				price, ok = r.Value, true
				break
			}
		}
		if !ok && day != nil {
			price, ok = day.Value, true
		}
		if ok {
			out = append(out, planner.Slot{Start: start, End: end, Price: price, SolarKW: kw})
		}
	}
	return out
}
