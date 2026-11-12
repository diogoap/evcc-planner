package service

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/planner"
	"github.com/diogoap/evcc-planner/internal/routes"
	"github.com/diogoap/evcc-planner/internal/trips"
)

// PlanView is the seven-day plan shown by the overview and trips pages.
type PlanView struct {
	Now         time.Time        `json:"now"`
	Timezone    string           `json:"timezone"`
	From        time.Time        `json:"from"`
	To          time.Time        `json:"to"`
	Settings    model.Settings   `json:"settings"`
	HomeMissing bool             `json:"homeMissing"`
	SoC         SoCView          `json:"soc"`
	Trips       []TripView       `json:"trips"`
	Ignored     []IgnoredView    `json:"ignored"`
	Days        []DayView        `json:"days"`
	Blocks      []BlockView      `json:"blocks"`
	Next        *BlockView       `json:"next"`
	SendOptions []SendOptionView `json:"sendOptions"`
	Timeline    []planner.Point  `json:"timeline"`
	Warnings    []string         `json:"warnings"`
	Advice      *AdviceView      `json:"advice"`
	Control     ControlView      `json:"control"`
}

// SoCView describes the SoC used for planning, where it came from, and how
// fresh it is.
type SoCView struct {
	Percent    *float64   `json:"percent"`
	Known      bool       `json:"known"`
	Source     string     `json:"source"` // evcc or homeassistant; empty when unknown
	Live       bool       `json:"live"`   // read from EVCC just now (car plugged in)
	ObservedAt *time.Time `json:"observedAt"`
	Stale      bool       `json:"stale"`     // not live and older than socStaleAfter
	Estimated  bool       `json:"estimated"` // lowered by trips driven since the reading
	Projected  *float64   `json:"projected"` // after subtracting those trips
	RangeKm    *float64   `json:"rangeKm"`   // the source's range at the reading
	// Other holds the other sources' last readings, newest first.
	Other []SoCReadingView `json:"other"`
}

// SoCReadingView is one source's last reading.
type SoCReadingView struct {
	Source     string    `json:"source"`
	Percent    float64   `json:"percent"`
	ObservedAt time.Time `json:"observedAt"`
	RangeKm    *float64  `json:"rangeKm"`
}

// socStaleAfter is when a reading that is not live is shown as stale: Home
// Assistant's car integrations update at least hourly, so an older reading
// means updates stopped (or, from EVCC, that the car has been unplugged a
// while).
const socStaleAfter = 3 * time.Hour

// TripView is one trip with its route and energy.
type TripView struct {
	Key                string    `json:"key"`
	Source             string    `json:"source"`
	SourceLabel        string    `json:"sourceLabel"`
	SourceID           int64     `json:"sourceId,omitempty"`
	Title              string    `json:"title"`
	Destination        string    `json:"destination"`
	Address            string    `json:"address"`
	PlaceName          string    `json:"placeName,omitempty"`
	Date               string    `json:"date"`
	ArriveAt           time.Time `json:"arriveAt"`
	EndAt              time.Time `json:"endAt"`
	ArriveLocal        string    `json:"arriveLocal"`
	EndLocal           string    `json:"endLocal"`
	DepartAt           time.Time `json:"departAt"`
	ReturnAt           time.Time `json:"returnAt"`
	DepartLocal        string    `json:"departLocal"`
	Enabled            bool      `json:"enabled"`
	NeedsReview        bool      `json:"needsReview"`
	Overridden         []string  `json:"overridden"`
	DistanceKm         *float64  `json:"distanceKm"`
	DistanceOverrideKm *float64  `json:"distanceOverrideKm"`
	DistanceSource     string    `json:"distanceSource"` // route, manual, unknown
	RouteProvider      string    `json:"routeProvider,omitempty"`
	RouteStale         bool      `json:"routeStale,omitempty"`
	RouteError         string    `json:"routeError,omitempty"`
	DisabledReason     string    `json:"disabledReason,omitempty"` // set when the distance is unknown
	EnergyKWh          *float64  `json:"energyKWh"`
	State              string    `json:"state"` // upcoming, in_progress, done
	BlockIndex         *int      `json:"blockIndex,omitempty"`
	RequiredSoC        *float64  `json:"requiredSoc,omitempty"`
}

// IgnoredView is a calendar event that is not a trip.
type IgnoredView struct {
	Key      string    `json:"key"`
	Calendar string    `json:"calendar"`
	Title    string    `json:"title"`
	Location string    `json:"location"`
	Reason   string    `json:"reason"`
	Start    time.Time `json:"start"`
}

// DayView summarizes one local day.
type DayView struct {
	Date          string     `json:"date"`
	DistanceKm    float64    `json:"distanceKm"`
	EnergyKWh     float64    `json:"energyKWh"`
	TripCount     int        `json:"tripCount"` // upcoming or in progress, enabled
	DisabledCount int        `json:"disabledCount"`
	DoneCount     int        `json:"doneCount"` // already back home; not shown
	ReviewCount   int        `json:"reviewCount"`
	Status        string     `json:"status"`                // none, completed, plus planner statuses
	ChargePct     float64    `json:"chargePct"`             // SoC points to add for the day's departures
	RequiredSoC   *float64   `json:"requiredSoc,omitempty"` // first upcoming departure's
	TargetSoC     *int       `json:"targetSoc,omitempty"`
	FirstDepartAt *time.Time `json:"firstDepartAt,omitempty"`
}

// BlockView is an away period with its target and projection.
type BlockView struct {
	Index             int        `json:"index"`
	TripKeys          []string   `json:"tripKeys"`
	Titles            []string   `json:"titles"`
	FirstTitle        string     `json:"firstTitle"`
	FirstDestination  string     `json:"firstDestination"`
	FirstArriveAt     time.Time  `json:"firstArriveAt"`
	DepartAt          time.Time  `json:"departAt"`
	ReturnAt          time.Time  `json:"returnAt"`
	DistanceKm        float64    `json:"distanceKm"`
	EnergyKWh         float64    `json:"energyKWh"`
	EnergyPct         float64    `json:"energyPct"`
	OwnRequiredSoC    float64    `json:"ownRequiredSoc"`
	RequiredSoC       float64    `json:"requiredSoc"`
	TargetSoC         int        `json:"targetSoc"` // whole percent to send to EVCC
	Status            string     `json:"status"`
	Overlap           bool       `json:"overlap"`
	StartSoC          *float64   `json:"startSoc"`
	DepartSoC         *float64   `json:"departSoc"`
	ArrivalSoC        *float64   `json:"arrivalSoc"`
	ChargePct         float64    `json:"chargePct"` // SoC points to add before departure
	ChargeKWh         float64    `json:"chargeKWh"`
	GridKWh           float64    `json:"gridKWh"`
	ChargeMinutes     int        `json:"chargeMinutes"`
	ChargeFrom        *time.Time `json:"chargeFrom"`
	LatestChargeStart *time.Time `json:"latestChargeStart"`
	Shortfall         float64    `json:"shortfall"`
}

// SendOptionView is a plan for the next departure that also covers every
// trip until the end of a later day, driven without charging in between.
type SendOptionView struct {
	Days        int       `json:"days"`    // 1 = the day of the next departure
	From        string    `json:"from"`    // local date of the next departure
	Through     string    `json:"through"` // last local date covered
	ReadyBy     time.Time `json:"readyBy"` // the next departure
	RequiredSoC float64   `json:"requiredSoc"`
	TargetSoC   int       `json:"targetSoc"` // whole percent to send to EVCC
	TripKeys    []string  `json:"tripKeys"`
	Titles      []string  `json:"titles"`
	DistanceKm  float64   `json:"distanceKm"`
	EnergyKWh   float64   `json:"energyKWh"`
	Exceeds     bool      `json:"exceeds"` // needs more than a full battery
	// Covered: the projected SoC at the next departure already reaches the
	// target without charging (unknown SoC: false).
	Covered bool `json:"covered"`
}

// sendOptionDays is how many days ahead plans are offered.
const sendOptionDays = 3

// sendOptions lists plans covering one to three days of trips, from the day
// of the next departure on.
func (s *Service) sendOptions(res planner.Result, p planner.Params, blocks []BlockView) []SendOptionView {
	out := []SendOptionView{}
	if res.Next < 0 {
		return out
	}
	next := blocks[res.Next]
	start := res.Blocks[res.Next].StartSoC
	first := localtime.StartOfDay(next.DepartAt, s.loc())
	for d := 1; d <= sendOptionDays; d++ {
		c, ok := planner.CoverUntil(res, p, localtime.AddDays(first, d))
		if !ok {
			break
		}
		o := SendOptionView{
			Days: d, From: localtime.Date(first, s.loc()), Through: localtime.Date(localtime.AddDays(first, d-1), s.loc()),
			ReadyBy: next.DepartAt, RequiredSoC: round(c.RequiredSoC, 1),
			TargetSoC: int(math.Min(100, math.Ceil(c.RequiredSoC-1e-6))), Exceeds: c.Exceeds,
			Covered:  start != nil && !c.Exceeds && *start >= c.RequiredSoC-1e-6,
			TripKeys: []string{}, Titles: []string{},
		}
		for _, i := range c.Blocks {
			b := blocks[i]
			o.TripKeys = append(o.TripKeys, b.TripKeys...)
			o.Titles = append(o.Titles, b.Titles...)
			o.DistanceKm += b.DistanceKm
			o.EnergyKWh += b.EnergyKWh
		}
		o.DistanceKm, o.EnergyKWh = round(o.DistanceKm, 1), round(o.EnergyKWh, 2)
		out = append(out, o)
	}
	return out
}

// tripRoute is a trip's resolved route and timing.
type tripRoute struct {
	departAt   time.Time
	returnAt   time.Time
	distanceKm *float64
	source     string
	provider   string
	stale      bool
	err        string
}

// fallbackSpeedKmh estimates travel time when only a manual distance is known.
const fallbackSpeedKmh = 50

func (s *Service) loadTrips(from, to time.Time, settings model.Settings, skipOverride string) ([]trips.Trip, []trips.Ignored, error) {
	places, err := s.Store.Places()
	if err != nil {
		return nil, nil, err
	}
	manual, err := s.Store.ManualTrips(from, to)
	if err != nil {
		return nil, nil, err
	}
	templates, err := s.Store.Templates()
	if err != nil {
		return nil, nil, err
	}
	events, err := s.Store.CalendarEvents(from, to)
	if err != nil {
		return nil, nil, err
	}
	overrides, err := s.Store.Overrides()
	if err != nil {
		return nil, nil, err
	}
	if skipOverride != "" {
		delete(overrides, skipOverride)
	}
	list, ignored := trips.Build(trips.Input{
		Location: s.loc(), From: from, To: to, Home: settings.HomeAddress, Places: places,
		Manual: manual, Templates: templates, Events: events, Overrides: overrides,
	})
	return list, ignored, nil
}

// Plan builds the seven-day plan.
func (s *Service) Plan(ctx context.Context) (*PlanView, error) {
	settings, err := s.Settings()
	if err != nil {
		return nil, err
	}
	from, to := s.window()
	now := s.now()
	list, ignored, err := s.loadTrips(from, to, settings, "")
	if err != nil {
		return nil, err
	}

	resolved := s.resolveRoutes(ctx, s.Clients().Routes, list, settings)
	var input []planner.Trip
	for i, t := range list {
		// Trips without a known distance are shown disabled and not planned.
		if !t.Enabled || resolved[i].distanceKm == nil {
			continue
		}
		input = append(input, planner.Trip{
			Key: t.Key, DepartAt: resolved[i].departAt, ReturnAt: resolved[i].returnAt, DistanceKm: *resolved[i].distanceKm,
		})
	}

	socIn, socView := s.currentSoC()
	params := planner.Params{
		CapacityKWh: settings.UsableCapacityKWh, ConsumptionKWhPer100Km: settings.ConsumptionKWhPer100Km,
		ReservePct: settings.ArrivalReservePct, EfficiencyPct: settings.ChargingEfficiencyPct,
		ChargePowerKW: settings.ChargePowerKW, ChargeBuffer: time.Duration(settings.ChargeBufferMin) * time.Minute,
	}
	result := planner.Plan(now, params, socIn, input)
	if socIn.Known {
		socView.Estimated = result.SoCEstimated
		socView.Projected = ptr(round(result.StartSoC, 1))
	}

	view := &PlanView{
		Now: now, Timezone: s.Cfg.Timezone, From: from, To: to, Settings: settings,
		HomeMissing: settings.HomeAddress == "", SoC: socView, Timeline: result.Timeline,
		Trips: []TripView{}, Ignored: []IgnoredView{}, Warnings: []string{},
	}
	if view.Timeline == nil {
		view.Timeline = []planner.Point{}
	}

	blockOf := map[string]int{}
	byKey := map[string]trips.Trip{}
	for _, t := range list {
		byKey[t.Key] = t
	}
	for i, b := range result.Blocks {
		for _, k := range b.TripKeys {
			blockOf[k] = i
		}
		view.Blocks = append(view.Blocks, blockView(i, b, byKey, s))
	}
	if view.Blocks == nil {
		view.Blocks = []BlockView{}
	}
	if result.Next >= 0 {
		view.Next = &view.Blocks[result.Next]
	}
	view.SendOptions = s.sendOptions(result, params, view.Blocks)
	ctl, err := s.Store.PlanControl()
	if err != nil {
		return nil, err
	}
	view.Control = s.controlView(settings, ctl, result, params, view.Blocks)

	for i, t := range list {
		r := resolved[i]
		blocked := r.distanceKm == nil
		tv := TripView{
			Key: t.Key, Source: string(t.Source), SourceLabel: t.SourceLabel, SourceID: t.SourceID,
			Title: t.Title, Destination: t.Destination, Address: t.Address, PlaceName: t.PlaceName,
			Date: localtime.Date(t.Start, s.loc()), ArriveAt: t.Start, EndAt: t.End,
			ArriveLocal: localtime.Clock(t.Start, s.loc()), EndLocal: localtime.Clock(t.End, s.loc()),
			DepartAt: r.departAt, ReturnAt: r.returnAt, DepartLocal: localtime.Clock(r.departAt, s.loc()),
			Enabled: t.Enabled && !blocked, NeedsReview: t.NeedsReview, Overridden: t.Overridden,
			DistanceOverrideKm: t.DistanceOverrideKm, DistanceSource: r.source, RouteProvider: r.provider,
			RouteStale: r.stale, RouteError: r.err, State: "upcoming",
		}
		if tv.Overridden == nil {
			tv.Overridden = []string{}
		}
		if blocked {
			tv.DisabledReason = r.err
		} else {
			tv.DistanceKm = ptr(round(*r.distanceKm, 2))
			tv.EnergyKWh = ptr(round(*r.distanceKm*settings.ConsumptionKWhPer100Km/100, 3))
		}
		switch {
		case !r.returnAt.After(now):
			tv.State = "done"
		case !r.departAt.After(now):
			tv.State = "in_progress"
		}
		if bi, ok := blockOf[t.Key]; ok {
			tv.BlockIndex = ptr(bi)
			if result.Blocks[bi].Status != planner.StatusDone && result.Blocks[bi].Status != planner.StatusInProgress {
				tv.RequiredSoC = ptr(view.Blocks[bi].RequiredSoC)
			}
		}
		view.Trips = append(view.Trips, tv)
	}
	for _, ig := range ignored {
		view.Ignored = append(view.Ignored, IgnoredView{
			Key: ig.Key, Calendar: ig.Calendar, Title: ig.Title, Location: ig.Location, Reason: ig.Reason, Start: ig.Start,
		})
	}

	view.Days = s.days(from, view.Trips, view.Blocks)
	// Past trips (back home already) are not shown anywhere.
	upcoming := view.Trips[:0]
	for _, t := range view.Trips {
		if t.State != "done" {
			upcoming = append(upcoming, t)
		}
	}
	view.Trips = upcoming
	if view.HomeMissing {
		view.Warnings = append(view.Warnings, "Set your home address in Settings to calculate routes.")
	}
	if snap := s.snapshot(); snap.state != nil && view.Next != nil && settings.TripPlans != model.TripPlansManual {
		if lp, ok := s.loadpoint(snap.state); ok && lp.Mode == "off" {
			view.Warnings = append(view.Warnings, "Charging is off in EVCC, so plans for your trips won't run.")
		}
	}
	if view.Advice, err = s.buildAdvice(view, result, socIn.Known); err != nil {
		return nil, err
	}
	return view, nil
}

func blockView(i int, b planner.Block, byKey map[string]trips.Trip, s *Service) BlockView {
	v := BlockView{
		Index: i, TripKeys: b.TripKeys, DepartAt: b.DepartAt, ReturnAt: b.ReturnAt,
		DistanceKm: round(b.DistanceKm, 2), EnergyKWh: round(b.EnergyKWh, 3), EnergyPct: round(b.EnergyPct, 2),
		OwnRequiredSoC: round(b.OwnRequiredSoC, 1), RequiredSoC: round(b.RequiredSoC, 1),
		TargetSoC: int(math.Min(100, math.Ceil(b.RequiredSoC-1e-6))), Status: string(b.Status), Overlap: b.Overlap,
		ChargePct: round(b.ChargePct, 1), ChargeKWh: round(b.ChargeKWh, 2), GridKWh: round(b.GridKWh, 2),
		ChargeMinutes: int(math.Ceil(b.ChargeDuration.Minutes())), ChargeFrom: b.ChargeFrom,
		LatestChargeStart: floorMinute(b.LatestChargeStart), Shortfall: round(b.Shortfall, 1),
		StartSoC: roundPtr(b.StartSoC), DepartSoC: roundPtr(b.DepartSoC), ArrivalSoC: roundPtr(b.ArrivalSoC),
	}
	for j, k := range b.TripKeys {
		t := byKey[k]
		v.Titles = append(v.Titles, t.Title)
		if j == 0 {
			v.FirstTitle, v.FirstArriveAt = t.Title, t.Start
			v.FirstDestination = t.Destination
			if t.PlaceName != "" {
				v.FirstDestination = t.PlaceName
			}
		}
	}
	return v
}

// days summarizes each local day from its upcoming trips; trips already
// back home are only counted as done. A day's charge is the SoC its
// departures need added (minimum plan); its target is the first upcoming
// departure's, which already covers later trips the car cannot recharge for.
func (s *Service) days(from time.Time, all []TripView, blocks []BlockView) []DayView {
	rank := map[string]int{
		"ready": 1, "in_progress": 2, "charge": 3, "unknown": 4, "shortfall": 5, "exceeds": 6,
	}
	var days []DayView
	for i := 0; i < WindowDays; i++ {
		date := localtime.AddDays(from, i).Format(localtime.DateLayout)
		d := DayView{Date: date, Status: "none"}
		inDay := map[int]bool{}
		for _, t := range all {
			switch {
			case t.Date != date:
				continue
			case t.State == "done":
				d.DoneCount++
				continue
			case !t.Enabled:
				d.DisabledCount++
				continue
			}
			d.TripCount++
			if t.NeedsReview {
				d.ReviewCount++
			}
			if t.DistanceKm != nil {
				d.DistanceKm += *t.DistanceKm
			}
			if t.EnergyKWh != nil {
				d.EnergyKWh += *t.EnergyKWh
			}
			if t.BlockIndex != nil {
				inDay[*t.BlockIndex] = true
			}
		}
		d.DistanceKm, d.EnergyKWh = round(d.DistanceKm, 1), round(d.EnergyKWh, 2)
		switch {
		case d.TripCount > 0:
			var first *BlockView
			for bi := range inDay {
				b := &blocks[bi]
				if b.Status == string(planner.StatusDone) {
					continue
				}
				if d.Status == "none" || rank[b.Status] > rank[d.Status] {
					d.Status = b.Status
				}
				if b.Status == string(planner.StatusInProgress) || localtime.Date(b.DepartAt, s.loc()) != date {
					continue
				}
				d.ChargePct += b.ChargePct
				if first == nil || b.DepartAt.Before(first.DepartAt) {
					first = b
				}
			}
			d.ChargePct = round(d.ChargePct, 1)
			if first != nil {
				d.RequiredSoC = ptr(first.RequiredSoC)
				d.TargetSoC = ptr(first.TargetSoC)
				d.FirstDepartAt = ptr(first.DepartAt)
			}
		case d.DoneCount > 0 && d.DisabledCount == 0:
			d.Status = "completed"
		}
		days = append(days, d)
	}
	return days
}

// resolveRoutes computes each trip's legs concurrently (cache hits are
// instant; misses call the routing provider).
func (s *Service) resolveRoutes(ctx context.Context, rr *routes.Resolver, list []trips.Trip, settings model.Settings) []tripRoute {
	out := make([]tripRoute, len(list))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range list {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = resolveTrip(ctx, rr, list[i], settings)
		}(i)
	}
	wg.Wait()
	return out
}

func resolveTrip(ctx context.Context, rr *routes.Resolver, t trips.Trip, settings model.Settings) tripRoute {
	buffer := time.Duration(settings.DepartureBufferMin) * time.Minute
	r := tripRoute{departAt: t.Start.Add(-buffer), returnAt: t.End, source: "unknown"}
	manual := t.DistanceOverrideKm
	estimate := func() {
		if manual == nil {
			return
		}
		oneWay := time.Duration(*manual / 2 / fallbackSpeedKmh * float64(time.Hour))
		r.departAt = t.Start.Add(-oneWay - buffer)
		r.returnAt = t.End.Add(oneWay)
	}
	if manual != nil {
		r.distanceKm = ptr(*manual)
		r.source = "manual"
	}
	home := settings.HomeAddress
	switch {
	case home == "":
		r.err = "Home address not set"
		estimate()
		return r
	case t.Address == "":
		r.err = "No location"
		estimate()
		return r
	}

	guess := 20 * time.Minute
	if d, ok := rr.Typical(home, t.Address); ok {
		guess = d
	}
	out, err := rr.Leg(ctx, home, t.Address, t.Start.Add(-guess-buffer))
	if err != nil {
		r.err = routeError(err)
		estimate()
		return r
	}
	back, err := rr.Leg(ctx, t.Address, home, t.End)
	if err != nil {
		r.err = routeError(err)
		estimate()
		return r
	}
	r.departAt = t.Start.Add(-out.Duration - buffer)
	r.returnAt = t.End.Add(back.Duration)
	r.provider = out.Provider
	r.stale = out.Stale || back.Stale
	if manual == nil {
		r.distanceKm = ptr(out.DistanceKm + back.DistanceKm)
		r.source = "route"
	}
	return r
}

func routeError(err error) string {
	if err == routes.ErrNoRoute {
		return "No route found for this address"
	}
	return "Route lookup failed: " + err.Error()
}

// currentSoC picks the SoC to plan with: EVCC's live reading while the car
// is plugged in (what EVCC charges by), otherwise a stored reading chosen by
// chooseSoC.
func (s *Service) currentSoC() (planner.SoC, SoCView) {
	readings, err := s.Store.SoCReadings()
	if err != nil {
		s.Log.Warn("read soc", "err", err)
	}
	var cur *model.SoCReading
	live := false
	snap := s.snapshot()
	if snap.state != nil && snap.err == nil {
		if lp, ok := s.loadpoint(snap.state); ok && lp.Connected && lp.VehicleSoc > 0 {
			cur = &model.SoCReading{
				Percent: lp.VehicleSoc, RangeKm: positive(lp.VehicleRange), ObservedAt: snap.okAt, Source: model.SoCSourceEVCC,
			}
			live = true
		}
	}
	if cur == nil {
		cur = chooseSoC(readings)
	}
	view := SoCView{Other: []SoCReadingView{}}
	for _, r := range readings {
		if cur == nil || r.Source != cur.Source {
			view.Other = append(view.Other, SoCReadingView{Source: r.Source, Percent: r.Percent, ObservedAt: r.ObservedAt, RangeKm: r.RangeKm})
		}
	}
	if cur == nil {
		return planner.SoC{}, view
	}
	at := cur.ObservedAt
	view.Percent, view.Known, view.Source, view.Live, view.ObservedAt, view.RangeKm =
		ptr(cur.Percent), true, cur.Source, live, &at, cur.RangeKm
	view.Stale = !live && s.now().Sub(at) > socStaleAfter
	return planner.SoC{Percent: cur.Percent, Known: true, ObservedAt: at}, view
}

// chooseSoC picks the stored reading to plan with while EVCC has no live
// value. EVCC's last reading, from when the car was plugged in, stands until
// Home Assistant has news from after it: a value that changed since (e.g.
// after a drive), or the same value reported again. An older value that
// Home Assistant reports again, such as a cloud integration that lagged
// while the car charged, never replaces it.
func chooseSoC(readings []model.SoCReading) *model.SoCReading {
	var ev, ha *model.SoCReading
	for i := range readings {
		switch readings[i].Source {
		case model.SoCSourceEVCC:
			ev = &readings[i]
		case model.SoCSourceHomeAssistant:
			ha = &readings[i]
		}
	}
	switch {
	case ha == nil:
		return ev
	case ev == nil:
		return ha
	case ha.ObservedAt.After(ev.ObservedAt) &&
		(ha.Changed().After(ev.ObservedAt) || math.Abs(ha.Percent-ev.Percent) < 0.5):
		return ha
	}
	return ev
}

func ptr[T any](v T) *T { return &v }

// floorMinute rounds down so the latest start is never reported too late.
func floorMinute(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	return ptr(t.Truncate(time.Minute))
}

func roundPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	return ptr(round(*v, 1))
}
