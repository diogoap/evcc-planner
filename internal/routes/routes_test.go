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
	r, err := g.Compute(context.Background(), "Home 1", "Office 2", &future)
	if err != nil {
		t.Fatal(err)
	}
	if r.DistanceM != 38123 || r.Duration != 35*time.Minute {
		t.Errorf("route = %+v", r)
	}
	if headers.Get("X-Goog-Api-Key") != "k" || headers.Get("X-Goog-FieldMask") != "routes.distanceMeters,routes.duration" {
		t.Errorf("headers = %v", headers)
	}
	if body["routingPreference"] != "TRAFFIC_AWARE" || body["departureTime"] != "2026-11-10T07:00:00Z" || body["travelMode"] != "DRIVE" {
		t.Errorf("traffic-aware body = %v", body)
	}

	past := now.Add(-time.Hour)
	g.Compute(context.Background(), "Home 1", "Office 2", &past)
	if body["routingPreference"] != "TRAFFIC_UNAWARE" || body["departureTime"] != nil {
		t.Errorf("past departure must not be traffic-aware: %v", body)
	}
}

func TestGoogleErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Goog-Api-Key") == "bad" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":{"code":403,"message":"API key not valid"}}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	g := &Google{BaseURL: srv.URL, APIKey: "bad", HTTP: srv.Client()}
	if _, err := g.Compute(context.Background(), "a", "b", nil); err == nil || err.Error() != "routes api: API key not valid" {
		t.Errorf("forbidden error = %v", err)
	}
	g.APIKey = "ok"
	if _, err := g.Compute(context.Background(), "a", "b", nil); !errors.Is(err, ErrNoRoute) {
		t.Errorf("empty response error = %v", err)
	}
}

type countingRouter struct {
	calls int
	fail  bool
	name  string
}

func (c *countingRouter) Name() string { return c.name }
func (c *countingRouter) Compute(ctx context.Context, o, d string, at *time.Time) (Route, error) {
	c.calls++
	if c.fail {
		return Route{}, errors.New("quota exceeded")
	}
	return Route{DistanceM: 6000, Duration: 12 * time.Minute}, nil
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

func TestStubIsDeterministic(t *testing.T) {
	a, _ := Stub{}.Compute(context.Background(), "Home", "Schoolstraat 1", nil)
	b, _ := Stub{}.Compute(context.Background(), "Schoolstraat 1", "Home", nil)
	if a.DistanceM != 6000 || b.DistanceM != 6000 {
		t.Errorf("school legs = %d, %d", a.DistanceM, b.DistanceM)
	}
	x, _ := Stub{}.Compute(context.Background(), "Home", "Parkweg 20", nil)
	y, _ := Stub{}.Compute(context.Background(), "home", "parkweg  20", nil)
	if x != y || x.DistanceM < 4000 || x.DistanceM > 44000 {
		t.Errorf("hash routes = %+v %+v", x, y)
	}
}
