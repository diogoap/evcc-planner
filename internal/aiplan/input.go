// Package aiplan is AI mode's decision step (AUTOMATION.md): it fills the
// prompt's placeholders with the planner's data, defines the answer's JSON
// Schema, and checks the model's decision against the planner's rules. It
// is pure: no I/O and no clock access.
//
// The model chooses targets and times; it never changes the foundational
// rules. Validate keeps every target within the charge-ahead cap and
// 100%, ignores windows outside the published prices, and the planner
// still charges at least each departure's own target by itself.
package aiplan

import "time"

// Input is the data the prompt's placeholders show. Times are absolute;
// they are written in Location. Prices are EUR/kWh including VAT.
type Input struct {
	Now      time.Time
	Location *time.Location
	Vehicle  Vehicle
	State    State
	// Next is the next departure; nil without one.
	Next *Next
	// Trips are the away periods that haven't left yet, in order; overlapping
	// trips form one away period.
	Trips []Trip
	// Days are the seven days of the plan, today first.
	Days []Day
	// Prices are EVCC's published slots from now on.
	Prices []Slot
	// Solar is EVCC's local PV forecast, per hour, from now on.
	Solar   []SolarSlot
	Outlook *Outlook
	Weather []Weather
	// History is the price history of the past 30 days, oldest first.
	History []HistoryDay
	// Baseline is what the automatic rules decide.
	Baseline Baseline
}

// Vehicle is the car, the charger, and the planner's settings.
type Vehicle struct {
	CapacityKWh            float64
	ChargeKW               float64 // at the wall
	EfficiencyPct          float64
	ConsumptionKWhPer100Km float64
	ReservePct             float64 // arrival reserve
	ChargeBufferMin        int     // charging ends this long before a departure
	HouseholdKW            float64 // subtracted from the solar forecast
	SolarTaxSaving         float64 // EUR/kWh not paid on own solar
	MinSolarKW             float64 // the least surplus the charger can use
	MaxAheadSoC            int     // charge ahead never goes above it
	MinSaving              float64 // the automatic rules' charge-ahead margin
	LimitSoC               int     // EVCC's charge limit; 0 unknown
}

// State is the car and EVCC right now.
type State struct {
	SoC       *float64
	SoCSource string // EVCC or Home Assistant
	SoCAt     *time.Time
	Projected *float64 // lowered by trips driven since the reading
	PluggedIn bool
	Charging  bool
	// Location is the car location sensor's state (home, not_home, a
	// zone); empty without one. The prompt only says home or away.
	Location   string
	Mode       string // EVCC's loadpoint mode
	Plan       *Plan  // EVCC's one-off plan
	CheapLimit *float64
	ExtraPlan  *Plan // an active extra plan from advice
	// PostponedUntil is when cheap-grid charging resumes, if paused.
	PostponedUntil *time.Time
}

// Plan is an EVCC one-off plan.
type Plan struct {
	SoC  int
	Time time.Time
	By   string // who set it, e.g. "the planner" or "EVCC"
}

// Next is the next departure.
type Next struct {
	TripID       string
	DepartAt     time.Time
	MinTargetSoC int // its own target, which always applies
	// StartSoC is the projected SoC when charging for it can start; nil
	// when the SoC is unknown.
	StartSoC          *float64
	LatestChargeStart *time.Time
}

// Trip is an away period: one trip, or overlapping trips driven together.
type Trip struct {
	ID          string // T1, T2, …
	Count       int    // trips in it
	DepartAt    time.Time
	ArriveBy    time.Time // the first trip's arrive-by time
	ReturnAt    time.Time // back home
	DistanceKm  float64
	EnergyKWh   float64
	EnergyPct   float64 // SoC points it uses
	RequiredSoC float64 // the minimum SoC to leave with
	Status      string  // ready, charge, shortfall, exceeds, unknown
	Unconfirmed bool    // a calendar event that may not be a car trip
}

// Day is one day of the plan.
type Day struct {
	Date       string
	DistanceKm float64
	EnergyKWh  float64
	Trips      int
	ChargePct  float64 // SoC points its departures need added
	TargetSoC  *int    // its first departure's target
	HomeHours  float64 // time at home (from now, today)
	// Published prices (today: the remaining ones), when the day has them.
	Published bool
	PriceMin  *float64
	PriceAvg  *float64
	PriceMax  *float64
	// SolarKWh is EVCC's local PV forecast for the (remaining) day, and
	// SolarHomeKWh while the car is home; nil beyond the forecast.
	SolarKWh     *float64
	SolarHomeKWh *float64
}

// Slot is a published price slot.
type Slot struct {
	Start time.Time
	End   time.Time
	Price float64
	Away  bool // the car is away for part of it
}

// SolarSlot is the forecast PV power in an hour.
type SolarSlot struct {
	Start time.Time
	End   time.Time
	KW    float64
}

// Outlook is the weekly price outlook.
type Outlook struct {
	ValidFrom   string
	ValidTo     string
	FetchedAt   time.Time
	Stale       bool
	Summary     string
	Trend       string
	WeeklyPrice *float64
	Days        []OutlookDay
}

// OutlookDay is the outlook for one day.
type OutlookDay struct {
	Date  string
	Price *float64 // estimate, EUR/kWh incl. VAT
	Level string
	Wind  string
	Solar string
	Note  string
}

// Weather is the national weather outlook for one day.
type Weather struct {
	Date          string
	WindBft       *int
	WindDir       string
	SunshinePct   *int
	TempMinC      *float64
	TempMaxC      *float64
	RainChancePct *int
}

// HistoryDay is one past day's figures.
type HistoryDay struct {
	Date        string
	PriceMin    *float64
	PriceAvg    *float64
	PriceMax    *float64
	DistanceKm  *float64
	EnergyKWh   *float64
	WindBft     *int
	SunshinePct *int
}

// Baseline is what the automatic rules would do.
type Baseline struct {
	TargetSoC int // the next departure's target with charge ahead; 0 without one
	AheadIDs  []string
	Days      []DayResult
}
