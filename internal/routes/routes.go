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
	"time"

	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/store"
)

// Route is one leg's road distance and duration.
type Route struct {
	DistanceM int
	Duration  time.Duration
}

// Router computes a single driving route.
type Router interface {
	Name() string
	Compute(ctx context.Context, origin, destination string, departAt *time.Time) (Route, error)
}

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

// Compute requests a driving route. Traffic-aware routing (billed as Compute
// Routes Pro) needs a future departure time; otherwise traffic is ignored.
func (g *Google) Compute(ctx context.Context, origin, destination string, departAt *time.Time) (Route, error) {
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	body := map[string]any{
		"origin":      map[string]any{"address": origin},
		"destination": map[string]any{"address": destination},
		"travelMode":  "DRIVE",
		"units":       "METRIC",
	}
	if g.TrafficAware && departAt != nil && departAt.After(now().Add(time.Minute)) {
		body["routingPreference"] = "TRAFFIC_AWARE"
		body["departureTime"] = departAt.UTC().Format(time.RFC3339)
	} else {
		body["routingPreference"] = "TRAFFIC_UNAWARE"
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Route{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(g.BaseURL, "/")+"/directions/v2:computeRoutes", bytes.NewReader(payload))
	if err != nil {
		return Route{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", g.APIKey)
	req.Header.Set("X-Goog-FieldMask", "routes.distanceMeters,routes.duration")
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return Route{}, fmt.Errorf("routes api unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Route{}, err
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
		return Route{}, fmt.Errorf("routes api: %s", msg)
	}
	var out struct {
		Routes []struct {
			DistanceMeters int    `json:"distanceMeters"`
			Duration       string `json:"duration"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Route{}, fmt.Errorf("decode routes response: %w", err)
	}
	if len(out.Routes) == 0 {
		return Route{}, ErrNoRoute
	}
	d, err := time.ParseDuration(out.Routes[0].Duration)
	if err != nil {
		return Route{}, fmt.Errorf("decode route duration %q: %w", out.Routes[0].Duration, err)
	}
	return Route{DistanceM: out.Routes[0].DistanceMeters, Duration: d}, nil
}

// ---- stub ----

// Stub returns plausible fixed routes without network access. Demo places get
// fixed distances; other addresses get a stable pseudo-random distance.
type Stub struct{}

// Name identifies the provider in the cache and UI.
func (Stub) Name() string { return "stub" }

var stubKnown = map[string]float64{"school": 6, "office": 38, "station": 4, "supermarket": 3}

// Compute returns a deterministic route.
func (Stub) Compute(ctx context.Context, origin, destination string, departAt *time.Time) (Route, error) {
	key := Normalize(destination)
	if key == Normalize(origin) {
		return Route{}, nil
	}
	km := 0.0
	for name, d := range stubKnown {
		if strings.Contains(key, name) || strings.Contains(Normalize(origin), name) {
			km = d
			break
		}
	}
	if km == 0 {
		h := fnv.New32a()
		h.Write([]byte(Normalize(origin) + "|" + key))
		km = 4 + float64(h.Sum32()%400)/10 // 4.0 to 43.9 km
	}
	minutes := math.Round(km/45*60) + 4
	return Route{DistanceM: int(km * 1000), Duration: time.Duration(minutes) * time.Minute}, nil
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
	o, d := Normalize(origin), Normalize(destination)
	if o == "" || d == "" {
		return Leg{}, errors.New("missing address")
	}
	if o == d {
		return Leg{Provider: r.Router.Name()}, nil
	}
	provider := r.Router.Name()
	slot := r.slot(departAt)
	now := r.Clock.Now()
	if c, err := r.Cache.Route(o, d, slot, provider); err == nil && c != nil && now.Sub(c.FetchedAt) < r.TTL {
		return legFrom(c, true, false), nil
	}

	timeout := r.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	route, err := r.Router.Compute(cctx, origin, destination, &departAt)
	if err != nil {
		if c, cerr := r.Cache.AnyRoute(o, d, provider); cerr == nil && c != nil {
			return legFrom(c, true, true), nil
		}
		return Leg{}, err
	}
	cached := store.CachedRoute{
		DistanceM: route.DistanceM, DurationS: int(route.Duration.Seconds()), Provider: provider, FetchedAt: now,
	}
	if err := r.Cache.PutRoute(o, d, slot, cached); err != nil {
		return Leg{}, err
	}
	return legFrom(&cached, false, false), nil
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
