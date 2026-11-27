package demo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/diogoap/evcc-planner/internal/calendar"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/rejected"
	"github.com/diogoap/evcc-planner/internal/routes"
)

// The demo services that can be made to fail.
const (
	FaultEVCC          = "evcc"
	FaultHomeAssistant = "homeassistant"
	FaultCalendar      = "calendar"
	FaultRoutes        = "routes"
)

// How a service fails: unreachable, so slow that calls time out, or
// refusing its key (or token, or address); "" means it works.
const (
	FaultDown = "down"
	FaultSlow = "slow"
	FaultKey  = "key"
)

// SlowFor is how long a slow service keeps a call waiting before it times
// out (shorter in tests); a caller's earlier deadline ends it sooner.
var SlowFor = 2 * time.Second

// errSlow is how a call to a slow service ends: a timeout.
type errSlow struct{}

func (errSlow) Error() string { return "timed out (demo outage)" }
func (errSlow) Unwrap() error { return context.DeadlineExceeded }

// faultService is a service that can fail: its name in the UI, and the
// refusal it answers with when it rejects the key (none for EVCC, which
// has no key).
type faultService struct {
	key, name string
	refusal   *rejected.Error
}

// faultNames are the services, in order.
var faultNames = []faultService{
	{FaultEVCC, "EVCC", nil},
	{FaultHomeAssistant, "Home Assistant", &rejected.Error{What: "access token", Status: 401, Message: "refused by the demo"}},
	{FaultCalendar, "Google Calendar", &rejected.Error{What: "calendar address", Status: 404, Message: "refused by the demo"}},
	{FaultRoutes, "Google Routes", &rejected.Error{What: "API key", Status: 403, Message: "refused by the demo"}},
}

// errDown is what a service that is down answers.
var errDown = errors.New("connection refused (demo outage)")

// Fault is one demo service, how it fails ("" while it works), and the
// faults it can have.
type Fault struct {
	Service string   `json:"service"`
	Name    string   `json:"name"`
	Fault   string   `json:"fault"`
	Faults  []string `json:"faults"`
}

// Faults makes demo services fail on request, to see and test how the
// planner handles an outage (demo mode only). Home Assistant being down
// also fails the demo phone and the demo car's climate.
type Faults struct {
	Phone   *homeassistant.NotifyStub
	Climate *homeassistant.ClimateStub
	mu      sync.Mutex
	set     map[string]string
}

// Set makes a service fail, or work again with fault "".
func (f *Faults) Set(service, fault string) error {
	i := slices.IndexFunc(faultNames, func(s faultService) bool { return s.key == service })
	switch {
	case i < 0:
		return fmt.Errorf("unknown service %q", service)
	case fault != "" && fault != FaultDown && fault != FaultSlow && fault != FaultKey,
		fault == FaultKey && faultNames[i].refusal == nil:
		return fmt.Errorf("%s can't have fault %q", faultNames[i].name, fault)
	}
	f.mu.Lock()
	if f.set == nil {
		f.set = map[string]string{}
	}
	f.set[service] = fault
	f.mu.Unlock()
	if service == FaultHomeAssistant && f.Phone != nil {
		f.Phone.SetFail(f.faultErr(service))
	}
	if service == FaultHomeAssistant && f.Climate != nil {
		f.Climate.SetFail(f.faultErr(service))
	}
	return nil
}

// Clear makes every service work again.
func (f *Faults) Clear() {
	for _, s := range faultNames {
		_ = f.Set(s.key, "")
	}
}

// List is every service with its fault.
func (f *Faults) List() []Fault {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Fault, len(faultNames))
	for i, s := range faultNames {
		out[i] = Fault{Service: s.key, Name: s.name, Fault: f.set[s.key], Faults: []string{FaultDown, FaultSlow}}
		if s.refusal != nil {
			out[i].Faults = append(out[i].Faults, FaultKey)
		}
	}
	return out
}

// faultErr is what a call to the service fails with now (nil while it
// works), without waiting.
func (f *Faults) faultErr(service string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.set[service] {
	case FaultDown:
		return errDown
	case FaultSlow:
		return errSlow{}
	case FaultKey:
		return faultNames[slices.IndexFunc(faultNames, func(s faultService) bool { return s.key == service })].refusal
	}
	return nil
}

// fail is how a call to the service ends: nil while it works. A slow
// service first keeps the call waiting.
func (f *Faults) fail(ctx context.Context, service string) error {
	err := f.faultErr(service)
	if errors.Is(err, errSlow{}) {
		select {
		case <-ctx.Done():
		case <-time.After(SlowFor):
		}
	}
	return err
}

// EVCC wraps an EVCC client: every call fails while EVCC has a fault.
func (f *Faults) EVCC(c evcc.Client) evcc.Client { return faultyEVCC{c, f} }

// HomeAssistant wraps the SoC reader, and its sensor reads when it has
// them: they fail while Home Assistant has a fault.
func (f *Faults) HomeAssistant(r homeassistant.Reader) homeassistant.Reader {
	if l, ok := r.(homeassistant.Locator); ok {
		return faultyHASensors{faultyHA{r, f}, l}
	}
	return faultyHA{r, f}
}

// Calendar wraps the calendar source: feeds fail while it has a fault.
func (f *Faults) Calendar(s calendar.Source) calendar.Source { return faultyCalendar{s, f} }

// Routes wraps the route provider: lookups fail while it has a fault.
func (f *Faults) Routes(r routes.Router) routes.Router { return faultyRouter{r, f} }

type faultyEVCC struct {
	evcc.Client
	f *Faults
}

func (e faultyEVCC) State(ctx context.Context) (*evcc.State, error) {
	if err := e.f.fail(ctx, FaultEVCC); err != nil {
		return nil, err
	}
	return e.Client.State(ctx)
}

func (e faultyEVCC) PreviewSocPlan(ctx context.Context, loadpoint, soc int, at time.Time) (*evcc.Preview, error) {
	if err := e.f.fail(ctx, FaultEVCC); err != nil {
		return nil, err
	}
	return e.Client.PreviewSocPlan(ctx, loadpoint, soc, at)
}

func (e faultyEVCC) SetSocPlan(ctx context.Context, vehicle string, soc int, at time.Time) (*evcc.SocPlan, error) {
	if err := e.f.fail(ctx, FaultEVCC); err != nil {
		return nil, err
	}
	return e.Client.SetSocPlan(ctx, vehicle, soc, at)
}

func (e faultyEVCC) DeleteSocPlan(ctx context.Context, vehicle string) error {
	if err := e.f.fail(ctx, FaultEVCC); err != nil {
		return err
	}
	return e.Client.DeleteSocPlan(ctx, vehicle)
}

func (e faultyEVCC) SetSmartCostLimit(ctx context.Context, loadpoint int, limit *float64) error {
	if err := e.f.fail(ctx, FaultEVCC); err != nil {
		return err
	}
	return e.Client.SetSmartCostLimit(ctx, loadpoint, limit)
}

type faultyHA struct {
	homeassistant.Reader
	f *Faults
}

func (h faultyHA) SoC(ctx context.Context) (*homeassistant.Reading, error) {
	if err := h.f.fail(ctx, FaultHomeAssistant); err != nil {
		return nil, err
	}
	return h.Reader.SoC(ctx)
}

type faultyHASensors struct {
	faultyHA
	l homeassistant.Locator
}

func (h faultyHASensors) State(ctx context.Context, entityID string) (*homeassistant.EntityState, error) {
	if err := h.f.fail(ctx, FaultHomeAssistant); err != nil {
		return nil, err
	}
	return h.l.State(ctx, entityID)
}

type faultyCalendar struct {
	calendar.Source
	f *Faults
}

func (c faultyCalendar) Fetch(ctx context.Context, cal config.CalendarSource) ([]byte, error) {
	if err := c.f.fail(ctx, FaultCalendar); err != nil {
		return nil, err
	}
	return c.Source.Fetch(ctx, cal)
}

type faultyRouter struct {
	routes.Router
	f *Faults
}

func (r faultyRouter) Compute(ctx context.Context, points []string, departAt *time.Time) ([]routes.Route, error) {
	if err := r.f.fail(ctx, FaultRoutes); err != nil {
		return nil, err
	}
	return r.Router.Compute(ctx, points, departAt)
}
