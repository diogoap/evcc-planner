package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/routes"
	"github.com/diogoap/evcc-planner/internal/store"
)

// Integrations are the clients built from the connection settings.
type Integrations struct {
	EVCC evcc.Client
	// HA reads the car's SoC while it is unplugged; nil when Home Assistant
	// is not configured (HANote says why). HASensors lists sensors to choose
	// from once the token is set.
	HA        homeassistant.Reader
	HASensors homeassistant.SensorLister
	HANote    string
	// Notify sends phone notifications and Actions delivers their button
	// presses; nil when not configured (NotifyNote says why). The demo has
	// Notify without Actions.
	Notify     homeassistant.Notifier
	Actions    homeassistant.ActionListener
	NotifyNote string
	Routes     *routes.Resolver
	RoutesNote string // explains a stub fallback (e.g. missing API key)
	Forecasts  Forecasts
}

// connState is one generation of connection settings and their clients.
// It is never changed once stored; a save stores a new one.
type connState struct {
	gen      uint64
	settings model.ConnectionSettings
	Integrations
}

func (s *Service) state() *connState { return s.conn.Load() }

// Conn returns the connection settings in effect.
func (s *Service) Conn() model.ConnectionSettings { return s.state().settings }

// Clients returns the integrations in effect. An operation reads them once,
// so a save in the meantime cannot mix old and new clients.
func (s *Service) Clients() Integrations { return s.state().Integrations }

// UseIntegrations replaces clients without a settings change (tests).
func (s *Service) UseIntegrations(f func(*Integrations)) {
	next := *s.state()
	f(&next.Integrations)
	s.conn.Store(&next)
}

// resolveVehicle picks the vehicle to plan for (see evcc.State.ResolveVehicle).
func (s *Service) resolveVehicle(st *evcc.State) (evcc.Vehicle, bool) {
	c := s.Conn()
	return st.ResolveVehicle(c.EVCCVehicle, c.EVCCLoadpoint)
}

// loadpoint returns the configured loadpoint.
func (s *Service) loadpoint(st *evcc.State) (*evcc.Loadpoint, bool) {
	return st.Loadpoint(s.Conn().EVCCLoadpoint)
}

// LoadSettings reads the saved settings (at start and after a demo reset)
// and builds the integrations; cached EVCC and Home Assistant readings are
// forgotten. Saved connection settings that no longer validate fall back to
// the defaults, so a deploy never stops the planner.
func (s *Service) LoadSettings() error {
	s.cacheMu.Lock()
	s.planning, s.advice = nil, nil
	s.cacheMu.Unlock()
	s.mu.Lock()
	s.evccState, s.evccErr, s.evccAt, s.evccOKAt = nil, nil, time.Time{}, time.Time{}
	s.haReading, s.haErr, s.haOKAt = nil, nil, time.Time{}
	s.mu.Unlock()
	s.notifyMu.Lock()
	s.notifyPrev = nil
	s.notifyMu.Unlock()
	defaults := model.DefaultConnectionSettings()
	v, err := s.Store.ConnectionSettings(defaults)
	if err != nil {
		return err
	}
	if err := v.Validate(); err != nil {
		s.Log.Warn("saved connection settings are invalid; using the defaults", "err", err)
		v = defaults
	}
	s.apply(v)
	return nil
}

var errSettingsChanged = errors.New("connection settings changed; checking again")

// apply builds the clients for v and makes them current. Readings from the
// previous EVCC loadpoint or Home Assistant sensors are dropped.
func (s *Service) apply(v model.ConnectionSettings) {
	next := &connState{settings: v, Integrations: s.Build(v)}
	prev := s.conn.Load()
	if prev != nil {
		next.gen = prev.gen + 1
	}
	s.conn.Store(next)
	if prev == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if evccTarget(prev.settings) != evccTarget(v) {
		s.evccState, s.evccErr, s.evccOKAt = nil, errSettingsChanged, time.Time{}
	}
	if haTarget(prev.settings) != haTarget(v) {
		s.haReading, s.haErr, s.haOKAt = nil, nil, time.Time{}
	}
}

type target struct {
	url, vehicle string
	loadpoint    int
}

// evccTarget identifies the charger and vehicle the planner acts on.
func evccTarget(c model.ConnectionSettings) target {
	return target{url: c.EVCCURL, vehicle: c.EVCCVehicle, loadpoint: c.EVCCLoadpoint}
}

// haTarget identifies what a Home Assistant reading comes from.
func haTarget(c model.ConnectionSettings) [4]string {
	return [4]string{c.HAURL, c.HASoCEntity, c.HARangeEntity, c.HAUpdatedEntity}
}

// ConnectionSettingsView is what Settings → Connections edits.
type ConnectionSettingsView struct {
	Settings model.ConnectionSettings `json:"settings"`
	Defaults model.ConnectionSettings `json:"defaults"`
	// Env lists the secrets that stay in environment variables, only whether
	// each is set.
	Env []EnvView `json:"env"`
	// Connections is the status after a save checked EVCC and Home Assistant.
	Connections []ConnectionView `json:"connections,omitempty"`
}

// EnvView is one environment-managed value.
type EnvView struct {
	Name string `json:"name"`
	Env  string `json:"env"`
	Set  bool   `json:"set"`
}

// ConnectionSettings returns the settings, their defaults, and the
// environment-managed values.
func (s *Service) ConnectionSettings() ConnectionSettingsView {
	c := s.Cfg
	return ConnectionSettingsView{
		Settings: s.Conn(), Defaults: model.DefaultConnectionSettings(),
		Env: []EnvView{
			{Name: "Calendar addresses (secret iCal)", Env: c.Calendar.URLEnv, Set: len(c.Calendar.Sources) > 0},
			{Name: "Google Routes API key", Env: c.Routes.APIKeyEnv, Set: c.Routes.APIKey != ""},
			{Name: "OpenRouter API key", Env: c.OpenRouter.APIKeyEnv, Set: c.OpenRouter.APIKey != ""},
			{Name: "KNMI API key", Env: c.KNMI.APIKeyEnv, Set: c.KNMI.APIKey != ""},
			{Name: "Home Assistant access token", Env: c.HomeAssistant.TokenEnv, Set: c.HomeAssistant.Token != ""},
		},
	}
}

// SaveConnections validates and stores connection settings, applies them
// at once, and checks EVCC and Home Assistant with them. Fields that differ
// from the defaults are stored; the others keep following them.
func (s *Service) SaveConnections(ctx context.Context, v model.ConnectionSettings) (ConnectionSettingsView, error) {
	for _, f := range []*string{&v.EVCCURL, &v.EVCCVehicle, &v.HAURL, &v.HASoCEntity, &v.HARangeEntity, &v.HAUpdatedEntity,
		&v.TibberURL, &v.OpenRouterModel, &v.KNMIDataset, &v.KNMIVersion, &v.ForecastDailyAt} {
		*f = strings.TrimSpace(*f)
	}
	if err := v.Validate(); err != nil {
		return ConnectionSettingsView{}, ValidationError{Msg: err.Error()}
	}
	// The plug-in check and temporary EVCC changes must not run half on the
	// old settings and half on the new ones.
	s.autoPlanMu.Lock()
	s.adjustMu.Lock()
	err := s.saveConnections(ctx, v)
	s.adjustMu.Unlock()
	s.autoPlanMu.Unlock()
	if err != nil {
		return ConnectionSettingsView{}, err
	}
	// Best effort: the status table shows any error.
	_ = s.PollEVCC(ctx)
	_ = s.PollHomeAssistant(ctx)
	view := s.ConnectionSettings()
	view.Connections, err = s.Connections()
	return view, err
}

func (s *Service) saveConnections(ctx context.Context, v model.ConnectionSettings) error {
	prev := s.Conn()
	newTarget := evccTarget(prev) != evccTarget(v)
	if newTarget {
		list, err := s.Store.Adjustments(localtime.Date(s.now(), s.loc()))
		if err != nil {
			return err
		}
		for _, a := range list {
			if a.Status == model.AdjustActive {
				return invalid("End the active %s first: it restores EVCC's previous setting at the current EVCC address and loadpoint.", kindText(a.Kind))
			}
		}
	}
	defaults := model.DefaultConnectionSettings()
	ev := model.AuditEvent{Action: "settings.update", SubjectType: "settings", SubjectID: "connections"}
	err := s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		before, err := tx.ConnectionSettings(defaults)
		if err != nil {
			return err
		}
		if err := tx.SaveConnectionSettings(differences(defaults, v)); err != nil {
			return err
		}
		// Another loadpoint or EVCC: learn its plug state before acting on
		// it, so a car already plugged in there is not a new plug-in.
		if newTarget {
			if err := tx.SetMeta(connectedMeta, ""); err != nil {
				return err
			}
		}
		e.Before, e.After = changes(before, v)
		e.Summary = settingsSummary("connection settings", e.After)
		return nil
	})
	if err != nil {
		return err
	}
	s.apply(v)
	return nil
}

// differences returns v's top-level JSON fields that differ from defaults.
func differences(defaults, v any) map[string]any {
	d, a := toMap(defaults), toMap(v)
	out := map[string]any{}
	for k, val := range a {
		if !reflect.DeepEqual(d[k], val) {
			out[k] = val
		}
	}
	return out
}

// HomeAssistantSensors lists the battery and range sensors to choose from.
func (s *Service) HomeAssistantSensors(ctx context.Context) ([]homeassistant.Sensor, error) {
	lister := s.Clients().HASensors
	if lister == nil {
		return nil, invalid("Set %s to list Home Assistant's sensors", s.Cfg.HomeAssistant.TokenEnv)
	}
	return lister.Sensors(ctx)
}
