// Package server exposes the planner over HTTP: a JSON API under /api,
// health endpoints, and the compiled UI.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

func init() {
	// Not in Go's built-in table, and the container has no mime.types file.
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// Server holds HTTP dependencies.
type Server struct {
	Svc  *service.Service
	Log  *slog.Logger
	Demo *evcc.Stub // set in demo mode to expose vehicle controls
	// Reset reseeds demo data (demo mode only); browser tests use it.
	Reset func(context.Context) error
}

// Handler returns the full HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", s.ready)

	mux.HandleFunc("GET /api/meta", s.meta)
	mux.HandleFunc("GET /api/plan", s.plan)
	mux.HandleFunc("GET /api/status", s.status)

	mux.HandleFunc("POST /api/trips", s.createTrip)
	mux.HandleFunc("PATCH /api/trips/{key}", s.updateTrip)
	mux.HandleFunc("DELETE /api/trips/{key}", s.deleteTrip)
	mux.HandleFunc("POST /api/trips/{key}/reset", s.resetTrip)

	mux.HandleFunc("GET /api/recurring", s.listTemplates)
	mux.HandleFunc("POST /api/recurring", s.createTemplate)
	mux.HandleFunc("PUT /api/recurring/{id}", s.updateTemplate)
	mux.HandleFunc("DELETE /api/recurring/{id}", s.deleteTemplate)

	mux.HandleFunc("GET /api/places", s.listPlaces)
	mux.HandleFunc("POST /api/places", s.createPlace)
	mux.HandleFunc("PUT /api/places/{id}", s.updatePlace)
	mux.HandleFunc("DELETE /api/places/{id}", s.deletePlace)

	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)

	mux.HandleFunc("GET /api/evcc/preview", s.preview)
	mux.HandleFunc("POST /api/evcc/plan", s.sendPlan)
	mux.HandleFunc("POST /api/evcc/refresh", s.refreshEVCC)

	mux.HandleFunc("GET /api/connections", s.connections)
	mux.HandleFunc("POST /api/calendar/sync", s.syncCalendar)

	if s.Demo != nil {
		mux.HandleFunc("POST /api/demo/vehicle", s.demoVehicle)
	}
	if s.Reset != nil {
		mux.HandleFunc("POST /api/demo/reset", func(w http.ResponseWriter, r *http.Request) {
			if err := s.Reset(r.Context()); err != nil {
				s.fail(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "unknown API endpoint")
	})
	mux.Handle("/", s.static())
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h := rec.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		defer func() {
			if p := recover(); p != nil {
				s.Log.Error("panic", "path", r.URL.Path, "panic", p)
				writeError(rec, http.StatusInternalServerError, "internal error")
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				s.Log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
					"ms", time.Since(start).Milliseconds())
			}
		}()
		// POSTs must be JSON: browsers cannot send that cross-site without a
		// CORS preflight, which this server never approves. PUT, PATCH, and
		// DELETE always need a preflight.
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/") {
			if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
				writeError(rec, http.StatusUnsupportedMediaType, "requests must use Content-Type: application/json")
				return
			}
			r.Body = http.MaxBytesReader(rec, r.Body, 1<<20)
		}
		next.ServeHTTP(rec, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// static serves the compiled UI with a fallback to index.html for client-side
// routes.
func (s *Server) static() http.Handler {
	dir := s.Svc.Cfg.Server.WebDir
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := filepath.Join(dir, "index.html")
		if _, err := os.Stat(index); err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintln(w, "UI not built. Run `make dev` for development or `make build` to compile it.")
			return
		}
		clean := filepath.Clean("/" + r.URL.Path)
		if info, err := os.Stat(filepath.Join(dir, clean)); err == nil && !info.IsDir() {
			if strings.HasPrefix(clean, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Warn("encode response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	var ve service.ValidationError
	var api *evcc.APIError
	switch {
	case errors.As(err, &ve):
		writeError(w, http.StatusBadRequest, ve.Msg)
	case service.IsNotFound(err):
		writeError(w, http.StatusNotFound, "not found")
	case errors.As(err, &api):
		writeError(w, http.StatusBadGateway, api.Error())
	default:
		s.Log.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return service.ValidationError{Msg: "request body is empty"}
		}
		return service.ValidationError{Msg: "invalid JSON: " + err.Error()}
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, service.ValidationError{Msg: "invalid id"}
	}
	return id, nil
}

// ---- handlers ----

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Svc.Store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	cfg := s.Svc.Cfg
	stub := cfg.EVCC.Mode == config.ModeStub || cfg.Calendar.Mode == config.ModeStub ||
		s.Svc.Routes.Router.Name() == "stub"
	writeJSON(w, http.StatusOK, map[string]any{
		"demo": cfg.Demo, "stubs": stub, "timezone": cfg.Timezone, "now": s.Svc.Clock.Now(),
		"windowDays": service.WindowDays,
	})
}

func (s *Server) plan(w http.ResponseWriter, r *http.Request) {
	p, err := s.Svc.Plan(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Svc.Status())
}

func (s *Server) createTrip(w http.ResponseWriter, r *http.Request) {
	var in service.TripInput
	if err := decode(r, &in); err != nil {
		s.fail(w, err)
		return
	}
	key, err := s.Svc.CreateTrip(in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"key": key})
}

func (s *Server) updateTrip(w http.ResponseWriter, r *http.Request) {
	var in service.TripInput
	if err := decode(r, &in); err != nil {
		s.fail(w, err)
		return
	}
	key := r.PathValue("key")
	if err := s.Svc.UpdateTrip(key, in); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key})
}

func (s *Server) deleteTrip(w http.ResponseWriter, r *http.Request) {
	if err := s.Svc.DeleteTrip(r.PathValue("key")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resetTrip(w http.ResponseWriter, r *http.Request) {
	if err := s.Svc.ResetTrip(r.PathValue("key")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.Svc.Templates()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	var t model.Template
	if err := decode(r, &t); err != nil {
		s.fail(w, err)
		return
	}
	t.ID = 0
	created, err := s.Svc.CreateTemplate(t)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	var t model.Template
	if err := decode(r, &t); err != nil {
		s.fail(w, err)
		return
	}
	t.ID = id
	updated, err := s.Svc.UpdateTemplate(t)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Svc.DeleteTemplate(id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPlaces(w http.ResponseWriter, r *http.Request) {
	list, err := s.Svc.Places()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createPlace(w http.ResponseWriter, r *http.Request) {
	var p model.Place
	if err := decode(r, &p); err != nil {
		s.fail(w, err)
		return
	}
	p.ID = 0
	created, err := s.Svc.CreatePlace(p)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) updatePlace(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	var p model.Place
	if err := decode(r, &p); err != nil {
		s.fail(w, err)
		return
	}
	p.ID = id
	updated, err := s.Svc.UpdatePlace(p)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deletePlace(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Svc.DeletePlace(id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.Svc.Settings()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var v model.Settings
	if err := decode(r, &v); err != nil {
		s.fail(w, err)
		return
	}
	saved, err := s.Svc.SaveSettings(v)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	soc, err := strconv.Atoi(r.URL.Query().Get("soc"))
	if err != nil {
		s.fail(w, service.ValidationError{Msg: "soc must be a whole percentage"})
		return
	}
	at, err := time.Parse(time.RFC3339, r.URL.Query().Get("time"))
	if err != nil {
		s.fail(w, service.ValidationError{Msg: "time must be RFC3339"})
		return
	}
	p, err := s.Svc.Preview(r.Context(), soc, at)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) sendPlan(w http.ResponseWriter, r *http.Request) {
	var in service.SendPlanInput
	if err := decode(r, &in); err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.Svc.SendPlan(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) refreshEVCC(w http.ResponseWriter, r *http.Request) {
	if err := s.Svc.PollEVCC(r.Context()); err != nil {
		s.Log.Warn("evcc refresh failed", "err", err)
	}
	writeJSON(w, http.StatusOK, s.Svc.Status())
}

func (s *Server) connections(w http.ResponseWriter, r *http.Request) {
	list, err := s.Svc.Connections()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) syncCalendar(w http.ResponseWriter, r *http.Request) {
	if err := s.Svc.Calendar.SyncAll(r.Context()); err != nil {
		s.Log.Warn("calendar sync failed", "err", err)
	}
	s.connections(w, r)
}

func (s *Server) demoVehicle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Connected bool    `json:"connected"`
		SoC       float64 `json:"soc"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, err)
		return
	}
	if in.SoC < 0 || in.SoC > 100 {
		s.fail(w, service.ValidationError{Msg: "soc must be 0 to 100"})
		return
	}
	s.Demo.SetVehicle(in.Connected, in.SoC)
	if err := s.Svc.PollEVCC(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Svc.Status())
}
