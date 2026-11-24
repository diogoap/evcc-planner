// Package routes computes road distance and travel time between addresses,
// using the Google Routes API or a deterministic stub, with a SQLite cache.
package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/rejected"
	"github.com/diogoap/evcc-planner/internal/store"
)

// Route is one leg's road distance and duration.
type Route struct {
	DistanceM int
	Duration  time.Duration
}

// Router computes a driving route through two or more points, stopping at
// each point between the first and the last. It returns one Route per leg.
type Router interface {
	Name() string
	Compute(ctx context.Context, points []string, departAt *time.Time) ([]Route, error)
}

// MaxPoints is the most points a route may have: Google bills up to 10
// stops between start and end at the normal rate, and trips have at most 5
// points.
const MaxPoints = 12

// ErrNoRoute means the provider found no route between the addresses.
var ErrNoRoute = errors.New("no route found")

// ---- Google Routes API ----

// Google calls routes.googleapis.com computeRoutes with an API key.
type Google struct {
	BaseURL      string
	APIKey       string
	TrafficAware bool
	HTTP         *http.Client
	Now          func() time.Time
}

// Name identifies the provider in the cache and UI.
func (g *Google) Name() string { return "google" }

// Compute requests a driving route, with the points between the first and
// the last as stops (one leg each). Traffic-aware routing (billed as Compute
// Routes Pro) needs a future departure time; otherwise traffic is ignored.
func (g *Google) Compute(ctx context.Context, points []string, departAt *time.Time) ([]Route, error) {
	if len(points) < 2 || len(points) > MaxPoints {
		return nil, fmt.Errorf("a route needs 2 to %d points", MaxPoints)
	}
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	body := map[string]any{
		"origin":      map[string]any{"address": points[0]},
		"destination": map[string]any{"address": points[len(points)-1]},
		"travelMode":  "DRIVE",
		"units":       "METRIC",
	}
	if via := points[1 : len(points)-1]; len(via) > 0 {
		stops := make([]any, len(via))
		for i, p := range via {
			stops[i] = map[string]any{"address": p}
		}
		body["intermediates"] = stops
	}
	if g.TrafficAware && departAt != nil && departAt.After(now().Add(time.Minute)) {
		body["routingPreference"] = "TRAFFIC_AWARE"
		body["departureTime"] = departAt.UTC().Format(time.RFC3339)
	} else {
		body["routingPreference"] = "TRAFFIC_UNAWARE"
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(g.BaseURL, "/")+"/directions/v2:computeRoutes", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", g.APIKey)
	req.Header.Set("X-Goog-FieldMask", "routes.distanceMeters,routes.duration,routes.legs.distanceMeters,routes.legs.duration")
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("routes api unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := resp.Status
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		// Google answers a key that isn't valid with 400.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
			(resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(msg), "api key not valid")) {
			return nil, &rejected.Error{What: "API key", Status: resp.StatusCode, Message: msg}
		}
		return nil, fmt.Errorf("routes api: %s", msg)
	}
	type leg struct {
		DistanceMeters int    `json:"distanceMeters"`
		Duration       string `json:"duration"`
	}
	var out struct {
		Routes []struct {
			leg
			Legs []leg `json:"legs"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode routes response: %w", err)
	}
	if len(out.Routes) == 0 {
		return nil, ErrNoRoute
	}
	legs := out.Routes[0].Legs
	if len(legs) == 0 && len(points) == 2 {
		legs = []leg{out.Routes[0].leg}
	}
	if len(legs) != len(points)-1 {
		return nil, fmt.Errorf("routes api: %d legs for %d points", len(legs), len(points))
	}
	routes := make([]Route, len(legs))
	for i, l := range legs {
		d := time.Duration(0)
		if l.Duration != "" {
			if d, err = time.ParseDuration(l.Duration); err != nil {
				return nil, fmt.Errorf("decode route duration %q: %w", l.Duration, err)
			}
		}
		routes[i] = Route{DistanceM: l.DistanceMeters, Duration: d}
	}
	return routes, nil
}

// ---- stub ----

// Stub returns plausible fixed routes without network access. Demo places get
// fixed distances; other addresses get a stable pseudo-random distance.
type Stub struct{}

// Name identifies the provider in the cache and UI.
func (Stub) Name() string { return "stub" }

// stubKnown are the demo places' distances, matched in this order, the
// destination before the origin, so a leg between two of them always gets
// the same distance.
var stubKnown = []struct {
	name string
	km   float64
}{{"school", 6}, {"office", 38}, {"station", 4}, {"supermarket", 3}}

// Compute returns a deterministic route, leg by leg.
func (Stub) Compute(ctx context.Context, points []string, departAt *time.Time) ([]Route, error) {
	if len(points) < 2 {
		return nil, fmt.Errorf("a route needs at least 2 points")
	}
	out := make([]Route, len(points)-1)
	for i := range out {
		out[i] = stubLeg(points[i], points[i+1])
	}
	return out, nil
}

func stubLeg(origin, destination string) Route {
	key := Normalize(destination)
	if key == Normalize(origin) {
		return Route{}
	}
	km := 0.0
	for _, place := range []string{key, Normalize(origin)} {
		for _, k := range stubKnown {
			if km == 0 && strings.Contains(place, k.name) {
				km = k.km
			}
		}
	}
	if km == 0 {
		h := fnv.New32a()
		h.Write([]byte(Normalize(origin) + "|" + key))
		km = 4 + float64(h.Sum32()%400)/10 // 4.0 to 43.9 km
	}
	minutes := math.Round(km/45*60) + 4
	return Route{DistanceM: int(km * 1000), Duration: time.Duration(minutes) * time.Minute}
}

// ---- resolver with cache ----

// Leg is a resolved route with provenance.
type Leg struct {
	DistanceKm float64
	Duration   time.Duration
	Provider   string
	Cached     bool
	Stale      bool // provider failed; using an older cached route
}

// Cache stores route lookups.
type Cache interface {
	Route(origin, destination, slot, provider string) (*store.CachedRoute, error)
	AnyRoute(origin, destination, provider string) (*store.CachedRoute, error)
	PutRoute(origin, destination, slot string, r store.CachedRoute) error
}

// Resolver caches routes per origin, destination, and departure slot so the
// provider is called rarely: recurring trips reuse the same weekly slot.
type Resolver struct {
	Router       Router
	Cache        Cache
	Clock        clock.Clock
	Location     *time.Location
	TTL          time.Duration
	TrafficAware bool
	Timeout      time.Duration

	// How the provider's lookups went (see Health).
	mu         sync.Mutex
	okAt       time.Time
	failedAt   time.Time
	lastFailed error
}

// Health reports the provider's last successful lookup, and the last
// failed one while no lookup has worked since (err nil otherwise).
func (r *Resolver) Health() (okAt, failedAt time.Time, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.okAt, r.failedAt, r.lastFailed
}

func (r *Resolver) noteLookup(err error, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.failedAt, r.lastFailed = at, err
		return
	}
	r.okAt, r.lastFailed = at, nil
}

// Typical returns any cached duration for the pair, used to guess the
// departure time before the traffic-aware lookup.
func (r *Resolver) Typical(origin, destination string) (time.Duration, bool) {
	c, err := r.Cache.AnyRoute(Normalize(origin), Normalize(destination), r.Router.Name())
	if err != nil || c == nil {
		return 0, false
	}
	return time.Duration(c.DurationS) * time.Second, true
}

// Leg resolves one direction for a departure time.
func (r *Resolver) Leg(ctx context.Context, origin, destination string, departAt time.Time) (Leg, error) {
	legs, err := r.Route(ctx, []string{origin, destination}, departAt)
	if err != nil {
		return Leg{}, err
	}
	return legs[0], nil
}

// Route resolves a drive through points (at least two) leaving at
// departAt, with one Leg per pair of points. Legs are cached one by one
// under the departure's slot; when any is missing, one provider request
// covers the whole drive, with the points between as stops. When that
// request fails, older cached legs are used, marked stale.
func (r *Resolver) Route(ctx context.Context, points []string, departAt time.Time) ([]Leg, error) {
	if len(points) < 2 || len(points) > MaxPoints {
		return nil, fmt.Errorf("a route needs 2 to %d points", MaxPoints)
	}
	norm := make([]string, len(points))
	for i, p := range points {
		if norm[i] = Normalize(p); norm[i] == "" {
			return nil, errors.New("missing address")
		}
	}
	provider := r.Router.Name()
	slot := r.slot(departAt)
	now := r.Clock.Now()
	legs := make([]Leg, len(points)-1)
	var missing []int
	for i := range legs {
		o, d := norm[i], norm[i+1]
		if o == d {
			legs[i] = Leg{Provider: provider}
			continue
		}
		if c, err := r.Cache.Route(o, d, slot, provider); err == nil && c != nil && now.Sub(c.FetchedAt) < r.TTL {
			legs[i] = legFrom(c, true, false)
			continue
		}
		missing = append(missing, i)
	}
	if len(missing) == 0 {
		return legs, nil
	}

	// The request skips a point that repeats the one before (no leg).
	req := []string{points[0]}
	var reqLeg []int // the leg each requested leg fills
	for i := 1; i < len(points); i++ {
		if norm[i] != norm[i-1] {
			req = append(req, points[i])
			reqLeg = append(reqLeg, i-1)
		}
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	detail := map[string]any{
		"from": req[0], "to": req[len(req)-1], "slot": slot,
		"departAt": departAt.In(r.Location).Format("2006-01-02 15:04"),
	}
	if len(req) > 2 {
		detail["via"] = req[1 : len(req)-1]
	}
	cctx = audit.WithCall(cctx, audit.Call{Summary: "Route " + strings.Join(req, " → "), Detail: detail})
	routes, err := r.Router.Compute(cctx, req, &departAt)
	if err == nil && len(routes) != len(req)-1 {
		err = fmt.Errorf("%s returned %d legs for %d points", provider, len(routes), len(req))
	}
	r.noteLookup(err, now)
	if err != nil {
		for _, i := range missing {
			c, cerr := r.Cache.AnyRoute(norm[i], norm[i+1], provider)
			if cerr != nil || c == nil {
				return nil, err
			}
			legs[i] = legFrom(c, true, true)
		}
		return legs, nil
	}
	for j, route := range routes {
		i := reqLeg[j]
		cached := store.CachedRoute{
			DistanceM: route.DistanceM, DurationS: int(route.Duration.Seconds()), Provider: provider, FetchedAt: now,
		}
		if err := r.Cache.PutRoute(norm[i], norm[i+1], slot, cached); err != nil {
			return nil, err
		}
		legs[i] = legFrom(&cached, false, false)
	}
	return legs, nil
}

// Check looks one route up with the provider now, without the cache, to
// see whether lookups work (Settings → Connections: Check now). The
// outcome counts for Health.
func (r *Resolver) Check(ctx context.Context, origin, destination string) error {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cctx = audit.WithCall(cctx, audit.Call{
		Summary: fmt.Sprintf("Route %s → %s (check)", origin, destination),
		Detail:  map[string]any{"from": origin, "to": destination},
	})
	_, err := r.Router.Compute(cctx, []string{origin, destination}, nil)
	r.noteLookup(err, r.Clock.Now())
	return err
}

func legFrom(c *store.CachedRoute, cached, stale bool) Leg {
	return Leg{
		DistanceKm: float64(c.DistanceM) / 1000, Duration: time.Duration(c.DurationS) * time.Second,
		Provider: c.Provider, Cached: cached, Stale: stale,
	}
}

// slot groups departures by weekday and half hour so traffic-aware results
// are reused week to week. Without traffic, one entry per pair suffices.
func (r *Resolver) slot(t time.Time) string {
	if !r.TrafficAware || r.Router.Name() == "stub" {
		return "any"
	}
	l := t.In(r.Location)
	return fmt.Sprintf("%s-%02d%02d", strings.ToLower(l.Weekday().String()[:3]), l.Hour(), l.Minute()/30*30)
}

// Normalize makes addresses comparable: lower case, single spaces.
func Normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
