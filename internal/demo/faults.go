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

// How a service fails: unreachable, or refusing its key (or token, or
// address); "" means it works.
const (
	FaultDown = "down"
	FaultKey  = "key"
)

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
// also fails the demo phone.
type Faults struct {
	Phone *homeassistant.NotifyStub
	mu    sync.Mutex
	set   map[string]string
}

// Set makes a service fail, or work again with fault "".
func (f *Faults) Set(service, fault string) error {
	i := slices.IndexFunc(faultNames, func(s faultService) bool { return s.key == service })
	switch {
	case i < 0:
		return fmt.Errorf("unknown service %q", service)
	case fault != "" && fault != FaultDown && fault != FaultKey,
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
		f.Phone.SetFail(f.err(service))
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
		out[i] = Fault{Service: s.key, Name: s.name, Fault: f.set[s.key], Faults: []string{FaultDown}}
		if s.refusal != nil {
			out[i].Faults = append(out[i].Faults, FaultKey)
		}
	}
	return out
}

func (f *Faults) err(service string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.set[service] {
	case FaultDown:
		return errDown
	case FaultKey:
		return faultNames[slices.IndexFunc(faultNames, func(s faultService) bool { return s.key == service })].refusal
	}
	return nil
}

// EVCC wraps an EVCC client: every call fails while EVCC is down.
func (f *Faults) EVCC(c evcc.Client) evcc.Client { return faultyEVCC{c, f} }

// HomeAssistant wraps the SoC reader, and its sensor reads when it has
// them: they fail while Home Assistant is down.
func (f *Faults) HomeAssistant(r homeassistant.Reader) homeassistant.Reader {
	if l, ok := r.(homeassistant.Locator); ok {
		return faultyHASensors{faultyHA{r, f}, l}
	}
	return faultyHA{r, f}
}

// Calendar wraps the calendar source: feeds fail while it is down.
func (f *Faults) Calendar(s calendar.Source) calendar.Source { return faultyCalendar{s, f} }

// Routes wraps the route provider: lookups fail while it is down.
func (f *Faults) Routes(r routes.Router) routes.Router { return faultyRouter{r, f} }

type faultyEVCC struct {
	evcc.Client
	f *Faults
}

func (e faultyEVCC) State(ctx context.Context) (*evcc.State, error) {
	if err := e.f.err(FaultEVCC); err != nil {
		return nil, err
	}
	return e.Client.State(ctx)
}

func (e faultyEVCC) PreviewSocPlan(ctx context.Context, loadpoint, soc int, at time.Time) (*evcc.Preview, error) {
	if err := e.f.err(FaultEVCC); err != nil {
		return nil, err
	}
	return e.Client.PreviewSocPlan(ctx, loadpoint, soc, at)
}

func (e faultyEVCC) SetSocPlan(ctx context.Context, vehicle string, soc int, at time.Time) (*evcc.SocPlan, error) {
	if err := e.f.err(FaultEVCC); err != nil {
		return nil, err
	}
	return e.Client.SetSocPlan(ctx, vehicle, soc, at)
}

func (e faultyEVCC) DeleteSocPlan(ctx context.Context, vehicle string) error {
	if err := e.f.err(FaultEVCC); err != nil {
		return err
	}
	return e.Client.DeleteSocPlan(ctx, vehicle)
}

func (e faultyEVCC) SetSmartCostLimit(ctx context.Context, loadpoint int, limit *float64) error {
	if err := e.f.err(FaultEVCC); err != nil {
		return err
	}
	return e.Client.SetSmartCostLimit(ctx, loadpoint, limit)
}

type faultyHA struct {
	homeassistant.Reader
	f *Faults
}

func (h faultyHA) SoC(ctx context.Context) (*homeassistant.Reading, error) {
	if err := h.f.err(FaultHomeAssistant); err != nil {
		return nil, err
	}
	return h.Reader.SoC(ctx)
}

type faultyHASensors struct {
	faultyHA
	l homeassistant.Locator
}

func (h faultyHASensors) State(ctx context.Context, entityID string) (*homeassistant.EntityState, error) {
	if err := h.f.err(FaultHomeAssistant); err != nil {
		return nil, err
	}
	return h.l.State(ctx, entityID)
}

type faultyCalendar struct {
	calendar.Source
	f *Faults
}

func (c faultyCalendar) Fetch(ctx context.Context, cal config.CalendarSource) ([]byte, error) {
	if err := c.f.err(FaultCalendar); err != nil {
		return nil, err
	}
	return c.Source.Fetch(ctx, cal)
}

type faultyRouter struct {
	routes.Router
	f *Faults
}

func (r faultyRouter) Compute(ctx context.Context, origin, destination string, departAt *time.Time) (routes.Route, error) {
	if err := r.f.err(FaultRoutes); err != nil {
		return routes.Route{}, err
	}
	return r.Router.Compute(ctx, origin, destination, departAt)
}
