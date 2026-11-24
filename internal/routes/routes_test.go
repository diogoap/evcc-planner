package routes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/rejected"
	"github.com/diogoap/evcc-planner/internal/store"
)

var ams, _ = time.LoadLocation("Europe/Amsterdam")

func TestGoogleRequestAndResponse(t *testing.T) {
	var body map[string]any
	var headers http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header
		data, _ := io.ReadAll(r.Body)
		body = map[string]any{}
		json.Unmarshal(data, &body)
		if r.URL.Path != "/directions/v2:computeRoutes" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"routes":[{"distanceMeters":38123,"duration":"2100s"}]}`))
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 9, 19, 0, 0, 0, ams)
	g := &Google{BaseURL: srv.URL, APIKey: "k", TrafficAware: true, HTTP: srv.Client(), Now: func() time.Time { return now }}

	future := now.Add(13 * time.Hour)
	r, err := g.Compute(context.Background(), []string{"Home 1", "Office 2"}, &future)
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 1 || r[0].DistanceM != 38123 || r[0].Duration != 35*time.Minute {
		t.Errorf("route = %+v", r)
	}
	if headers.Get("X-Goog-Api-Key") != "k" ||
		headers.Get("X-Goog-FieldMask") != "routes.distanceMeters,routes.duration,routes.legs.distanceMeters,routes.legs.duration" {
		t.Errorf("headers = %v", headers)
	}
	if body["routingPreference"] != "TRAFFIC_AWARE" || body["departureTime"] != "2026-11-10T07:00:00Z" || body["travelMode"] != "DRIVE" {
		t.Errorf("traffic-aware body = %v", body)
	}
	if _, ok := body["intermediates"]; ok {
		t.Errorf("a two-point route has no stops: %v", body)
	}

	past := now.Add(-time.Hour)
	g.Compute(context.Background(), []string{"Home 1", "Office 2"}, &past)
	if body["routingPreference"] != "TRAFFIC_UNAWARE" || body["departureTime"] != nil {
		t.Errorf("past departure must not be traffic-aware: %v", body)
	}
}

func TestGoogleStopsBecomeLegs(t *testing.T) {
	var body map[string]any
	legs := `{"routes":[{"distanceMeters":30000,"duration":"2400s","legs":[` +
		`{"distanceMeters":6000,"duration":"720s"},{"distanceMeters":9000,"duration":"900s"},{"distanceMeters":15000,"duration":"780s"}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body = map[string]any{}
		json.Unmarshal(data, &body)
		if len(body["intermediates"].([]any)) == 1 {
			w.Write([]byte(`{"routes":[{"distanceMeters":1,"duration":"1s","legs":[{"distanceMeters":1,"duration":"1s"}]}]}`))
			return
		}
		w.Write([]byte(legs))
	}))
	defer srv.Close()
	g := &Google{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	r, err := g.Compute(context.Background(), []string{"Home 1", "School", "Shop 3", "Home 1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Route{{6000, 12 * time.Minute}, {9000, 15 * time.Minute}, {15000, 13 * time.Minute}}
	if len(r) != 3 || r[0] != want[0] || r[1] != want[1] || r[2] != want[2] {
		t.Errorf("legs = %+v", r)
	}
	stops, _ := json.Marshal(body["intermediates"])
	if string(stops) != `[{"address":"School"},{"address":"Shop 3"}]` || body["destination"].(map[string]any)["address"] != "Home 1" {
		t.Errorf("body = %v", body)
	}
	// A response with the wrong number of legs is an error, not a guess.
	if _, err := g.Compute(context.Background(), []string{"a", "b", "c"}, nil); err == nil || err.Error() != "routes api: 1 legs for 3 points" {
		t.Errorf("leg count error = %v", err)
	}
	if _, err := g.Compute(context.Background(), []string{"a"}, nil); err == nil {
		t.Error("one point is not a route")
	}
}

func TestGoogleErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Goog-Api-Key") {
		case "bad":
			// Google's answer to a key that isn't valid.
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"code":400,"message":"API key not valid. Please pass a valid API key."}}`))
			return
		case "restricted":
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":{"code":403,"message":"Routes API has not been used in project 1"}}`))
			return
		case "busy":
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"code":429,"message":"Quota exceeded"}}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	g := &Google{BaseURL: srv.URL, APIKey: "bad", HTTP: srv.Client()}
	if _, err := g.Compute(context.Background(), []string{"a", "b"}, nil); !rejected.Is(err) ||
		err.Error() != "API key rejected (HTTP 400): API key not valid. Please pass a valid API key." {
		t.Errorf("invalid key error = %v", err)
	}
	g.APIKey = "restricted"
	if _, err := g.Compute(context.Background(), []string{"a", "b"}, nil); !rejected.Is(err) {
		t.Errorf("forbidden error = %v", err)
	}
	g.APIKey = "busy"
	if _, err := g.Compute(context.Background(), []string{"a", "b"}, nil); rejected.Is(err) || err.Error() != "routes api: Quota exceeded" {
		t.Errorf("quota error = %v", err)
	}
	g.APIKey = "ok"
	if _, err := g.Compute(context.Background(), []string{"a", "b"}, nil); !errors.Is(err, ErrNoRoute) {
		t.Errorf("empty response error = %v", err)
	}
}

type countingRouter struct {
	calls  int
	fail   bool
	name   string
	points [][]string
}

func (c *countingRouter) Name() string { return c.name }
func (c *countingRouter) Compute(ctx context.Context, points []string, at *time.Time) ([]Route, error) {
	c.calls++
	c.points = append(c.points, points)
	if c.fail {
		return nil, errors.New("quota exceeded")
	}
	out := make([]Route, len(points)-1)
	for i := range out {
		out[i] = Route{DistanceM: 6000 * (i + 1), Duration: time.Duration(12*(i+1)) * time.Minute}
	}
	return out, nil
}

func TestResolverCachesPerSlotAndFallsBack(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 9, 19, 0, 0, 0, ams)
	router := &countingRouter{name: "google"}
	r := &Resolver{Router: router, Cache: st, Clock: clock.Fixed{T: now}, Location: ams, TTL: 7 * 24 * time.Hour, TrafficAware: true}

	tue0750 := time.Date(2026, 11, 10, 7, 50, 0, 0, ams)
	leg, err := r.Leg(context.Background(), "Home  1", "School", tue0750)
	if err != nil || leg.DistanceKm != 6 || leg.Cached || router.calls != 1 {
		t.Fatalf("first lookup = %+v %v calls=%d", leg, err, router.calls)
	}
	// Same weekday and half hour next week, different spacing/case: cache hit.
	leg, _ = r.Leg(context.Background(), "home 1", "school", tue0750.AddDate(0, 0, 7).Add(-5*time.Minute))
	if !leg.Cached || router.calls != 1 {
		t.Fatalf("expected cache hit, calls=%d leg=%+v", router.calls, leg)
	}
	// Different slot: provider called again.
	r.Leg(context.Background(), "Home 1", "School", tue0750.Add(2*time.Hour))
	if router.calls != 2 {
		t.Fatalf("new slot should call provider, calls=%d", router.calls)
	}
	// Provider failure falls back to any cached route, marked stale.
	router.fail = true
	leg, err = r.Leg(context.Background(), "Home 1", "School", tue0750.Add(5*time.Hour))
	if err != nil || !leg.Stale || leg.DistanceKm != 6 {
		t.Fatalf("fallback = %+v %v", leg, err)
	}
	if _, err := r.Leg(context.Background(), "Home 1", "Elsewhere", tue0750); err == nil {
		t.Fatal("no cache and failing provider should error")
	}
	if d, ok := r.Typical("Home 1", "School"); !ok || d != 12*time.Minute {
		t.Errorf("typical = %s %v", d, ok)
	}
	if leg, err := r.Leg(context.Background(), "School", "school", tue0750); err != nil || leg.DistanceKm != 0 {
		t.Errorf("same place = %+v %v", leg, err)
	}
}

func TestResolverRoutesThroughStops(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 9, 19, 0, 0, 0, ams)
	router := &countingRouter{name: "google"}
	r := &Resolver{Router: router, Cache: st, Clock: clock.Fixed{T: now}, Location: ams, TTL: 7 * 24 * time.Hour, TrafficAware: true}
	tue0750 := time.Date(2026, 11, 10, 7, 50, 0, 0, ams)

	// One request for the whole drive, one leg per pair of points.
	legs, err := r.Route(context.Background(), []string{"Home 1", "School", "Shop", "Office"}, tue0750)
	if err != nil || len(legs) != 3 || router.calls != 1 {
		t.Fatalf("route = %+v %v calls=%d", legs, err, router.calls)
	}
	if legs[0].DistanceKm != 6 || legs[1].DistanceKm != 12 || legs[2].Duration != 36*time.Minute || legs[1].Cached {
		t.Errorf("legs = %+v", legs)
	}
	// Each leg is cached on its own: a trip with the first leg reuses it.
	if leg, _ := r.Leg(context.Background(), "home 1", "school", tue0750); !leg.Cached || leg.DistanceKm != 6 || router.calls != 1 {
		t.Errorf("cached leg = %+v calls=%d", leg, router.calls)
	}
	// A repeated point is no leg and is left out of the request.
	legs, err = r.Route(context.Background(), []string{"Home 1", "Home 1", "Gym", "Home 1"}, tue0750)
	if err != nil || len(legs) != 3 || legs[0].DistanceKm != 0 || router.calls != 2 {
		t.Fatalf("repeated point = %+v %v calls=%d", legs, err, router.calls)
	}
	if got := router.points[1]; len(got) != 3 || got[0] != "Home 1" || got[1] != "Gym" {
		t.Errorf("request points = %v", got)
	}
	// When the provider fails, older legs are used, marked stale, but only
	// if every missing leg has one.
	router.fail = true
	later := tue0750.Add(3 * time.Hour)
	legs, err = r.Route(context.Background(), []string{"Home 1", "School", "Shop", "Office"}, later)
	if err != nil || !legs[0].Stale || !legs[2].Stale || legs[1].DistanceKm != 12 {
		t.Errorf("stale route = %+v %v", legs, err)
	}
	if _, err := r.Route(context.Background(), []string{"Home 1", "School", "Nowhere"}, later); err == nil {
		t.Error("a leg without any cached route must fail")
	}
	if _, err := r.Route(context.Background(), []string{"Home 1", " "}, later); err == nil {
		t.Error("an empty point must fail")
	}
}

func TestStubIsDeterministic(t *testing.T) {
	leg := func(o, d string) Route {
		r, err := Stub{}.Compute(context.Background(), []string{o, d}, nil)
		if err != nil || len(r) != 1 {
			t.Fatalf("stub %s → %s: %v %v", o, d, r, err)
		}
		return r[0]
	}
	a, b := leg("Home", "Schoolstraat 1"), leg("Schoolstraat 1", "Home")
	if a.DistanceM != 6000 || b.DistanceM != 6000 {
		t.Errorf("school legs = %d, %d", a.DistanceM, b.DistanceM)
	}
	x, y := leg("Home", "Parkweg 20"), leg("home", "parkweg  20")
	if x != y || x.DistanceM < 4000 || x.DistanceM > 44000 {
		t.Errorf("hash routes = %+v %+v", x, y)
	}
	multi, _ := Stub{}.Compute(context.Background(), []string{"Home", "Schoolstraat 1", "Parkweg 20"}, nil)
	if len(multi) != 2 || multi[0] != a || multi[1] != leg("Schoolstraat 1", "Parkweg 20") {
		t.Errorf("stub legs = %+v", multi)
	}
}
