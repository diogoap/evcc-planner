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
	Now         time.Time       `json:"now"`
	Timezone    string          `json:"timezone"`
	From        time.Time       `json:"from"`
	To          time.Time       `json:"to"`
	Settings    model.Settings  `json:"settings"`
	HomeMissing bool            `json:"homeMissing"`
	SoC         SoCView         `json:"soc"`
	Trips       []TripView      `json:"trips"`
	Ignored     []IgnoredView   `json:"ignored"`
	Days        []DayView       `json:"days"`
	Blocks      []BlockView     `json:"blocks"`
	Next        *BlockView      `json:"next"`
	Timeline    []planner.Point `json:"timeline"`
	Warnings    []string        `json:"warnings"`
	Advice      *AdviceView     `json:"advice"`
}

// SoCView describes the SoC used for planning and how fresh it is.
type SoCView struct {
	Percent    *float64   `json:"percent"`
	Known      bool       `json:"known"`
	Live       bool       `json:"live"` // read from EVCC just now (car plugged in)
	ObservedAt *time.Time `json:"observedAt"`
	Estimated  bool       `json:"estimated"` // lowered by trips driven since the reading
	Projected  *float64   `json:"projected"` // after subtracting those trips
	RangeKm    *float64   `json:"rangeKm"`   // EVCC's range at the reading
}

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

	resolved := s.resolveRoutes(ctx, list, settings)
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
func (s *Service) resolveRoutes(ctx context.Context, list []trips.Trip, settings model.Settings) []tripRoute {
	out := make([]tripRoute, len(list))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range list {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = s.resolveTrip(ctx, list[i], settings)
		}(i)
	}
	wg.Wait()
	return out
}

func (s *Service) resolveTrip(ctx context.Context, t trips.Trip, settings model.Settings) tripRoute {
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
	if d, ok := s.Routes.Typical(home, t.Address); ok {
		guess = d
	}
	out, err := s.Routes.Leg(ctx, home, t.Address, t.Start.Add(-guess-buffer))
	if err != nil {
		r.err = routeError(err)
		estimate()
		return r
	}
	back, err := s.Routes.Leg(ctx, t.Address, home, t.End)
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

// currentSoC prefers a live EVCC reading; otherwise the last stored one.
func (s *Service) currentSoC() (planner.SoC, SoCView) {
	snap := s.snapshot()
	if snap.state != nil && snap.err == nil {
		if lp, ok := snap.state.Loadpoint(s.Cfg.EVCC.Loadpoint); ok && lp.Connected && lp.VehicleSoc > 0 {
			at := snap.okAt
			return planner.SoC{Percent: lp.VehicleSoc, Known: true, ObservedAt: at},
				SoCView{Percent: ptr(lp.VehicleSoc), Known: true, Live: true, ObservedAt: &at, RangeKm: positive(lp.VehicleRange)}
		}
	}
	last, err := s.Store.LastSoC()
	if err != nil || last == nil {
		return planner.SoC{}, SoCView{}
	}
	at := last.ObservedAt
	return planner.SoC{Percent: last.Percent, Known: true, ObservedAt: at},
		SoCView{Percent: ptr(last.Percent), Known: true, ObservedAt: &at, RangeKm: last.RangeKm}
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
