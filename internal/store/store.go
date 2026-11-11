// Package store persists personal planning data in SQLite.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/diogoap/evcc-planner/internal/model"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database. A Store given to an InTx callback runs
// every query in that transaction.
type Store struct {
	db *sql.DB
	q  querier // db, or the open transaction
	tx bool
}

type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Prepare(query string) (*sql.Stmt, error)
}

var migrations = []string{
	`CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE TABLE places (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE COLLATE NOCASE,
		address TEXT NOT NULL
	);
	CREATE TABLE recurring_templates (
		id INTEGER PRIMARY KEY,
		title TEXT NOT NULL,
		destination TEXT NOT NULL,
		weekdays TEXT NOT NULL,
		start_time TEXT NOT NULL,
		end_time TEXT NOT NULL,
		enabled INTEGER NOT NULL,
		exceptions TEXT NOT NULL DEFAULT '[]'
	);
	CREATE TABLE manual_trips (
		id INTEGER PRIMARY KEY,
		title TEXT NOT NULL,
		destination TEXT NOT NULL,
		start_at TEXT NOT NULL,
		end_at TEXT NOT NULL,
		enabled INTEGER NOT NULL,
		distance_override_km REAL
	);
	CREATE INDEX manual_trips_start ON manual_trips(start_at);
	CREATE TABLE trip_overrides (
		trip_key TEXT PRIMARY KEY,
		data TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE TABLE calendar_events (
		key TEXT PRIMARY KEY,
		calendar TEXT NOT NULL,
		uid TEXT NOT NULL,
		title TEXT NOT NULL,
		location TEXT NOT NULL,
		start_at TEXT NOT NULL,
		end_at TEXT NOT NULL
	);
	CREATE INDEX calendar_events_start ON calendar_events(start_at);
	CREATE TABLE calendar_sync (
		calendar TEXT PRIMARY KEY,
		last_attempt_at TEXT,
		last_success_at TEXT,
		last_error TEXT NOT NULL DEFAULT '',
		event_count INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE route_cache (
		origin TEXT NOT NULL,
		destination TEXT NOT NULL,
		slot TEXT NOT NULL,
		distance_m INTEGER NOT NULL,
		duration_s INTEGER NOT NULL,
		provider TEXT NOT NULL,
		fetched_at TEXT NOT NULL,
		PRIMARY KEY (origin, destination, slot)
	);
	CREATE TABLE vehicle_soc (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		soc REAL NOT NULL,
		observed_at TEXT NOT NULL,
		source TEXT NOT NULL
	);
	CREATE TABLE plan_writes (
		id INTEGER PRIMARY KEY,
		vehicle TEXT NOT NULL,
		soc INTEGER NOT NULL,
		target_time TEXT NOT NULL,
		trip_key TEXT NOT NULL DEFAULT '',
		written_at TEXT NOT NULL
	);`,
	// Calendars are identified by an ID derived from their URL; names come
	// from the feeds.
	`ALTER TABLE calendar_events ADD COLUMN calendar_name TEXT NOT NULL DEFAULT '';
	ALTER TABLE calendar_sync ADD COLUMN name TEXT NOT NULL DEFAULT '';`,
	// V2: daily forecasts (latest valid payload per source), dismissed
	// advice, and temporary EVCC adjustments made from advice.
	`CREATE TABLE forecast_sources (
		source TEXT PRIMARY KEY,
		last_attempt_at TEXT,
		last_success_at TEXT,
		last_error TEXT NOT NULL DEFAULT '',
		payload TEXT,
		content_hash TEXT NOT NULL DEFAULT '',
		detail TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE advice_dismissals (
		date TEXT NOT NULL,
		recommendation TEXT NOT NULL,
		dismissed_at TEXT NOT NULL,
		PRIMARY KEY (date, recommendation)
	);
	CREATE TABLE evcc_adjustments (
		id INTEGER PRIMARY KEY,
		kind TEXT NOT NULL,
		advice_date TEXT NOT NULL,
		status TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		data TEXT NOT NULL
	);
	CREATE INDEX evcc_adjustments_status ON evcc_adjustments(status);`,
	// The vehicle's range, read with the SoC.
	`ALTER TABLE vehicle_soc ADD COLUMN range_km REAL;`,
	// V3: the append-only activity log. Rows are never updated; only the
	// daily retention prune (and a demo reset) deletes them.
	`CREATE TABLE audit_events (
		id INTEGER PRIMARY KEY,
		at TEXT NOT NULL,
		type TEXT NOT NULL,
		source TEXT NOT NULL,
		action TEXT NOT NULL,
		subject_type TEXT NOT NULL,
		subject_id TEXT NOT NULL DEFAULT '',
		related TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL,
		before_json TEXT,
		after_json TEXT,
		detail_json TEXT,
		outcome TEXT NOT NULL,
		error TEXT NOT NULL DEFAULT '',
		service TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		bytes INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX audit_events_at ON audit_events(at);
	CREATE INDEX audit_events_subject ON audit_events(subject_type, subject_id);
	CREATE INDEX audit_events_related ON audit_events(related) WHERE related != '';
	CREATE INDEX audit_events_service ON audit_events(service, at) WHERE service != '';
	CREATE TRIGGER audit_events_append_only BEFORE UPDATE ON audit_events
	BEGIN SELECT RAISE(ABORT, 'audit events are append-only'); END;`,
	// V4: the last SoC reading of each source (EVCC, Home Assistant). The
	// demo's seeded reading stands for EVCC's.
	`CREATE TABLE soc_readings (
		source TEXT PRIMARY KEY,
		soc REAL NOT NULL,
		range_km REAL,
		observed_at TEXT NOT NULL
	);
	INSERT INTO soc_readings (source, soc, range_km, observed_at)
		SELECT CASE source WHEN 'demo' THEN 'evcc' ELSE source END, soc, range_km, observed_at FROM vehicle_soc;
	DROP TABLE vehicle_soc;`,
	// When a reading's value last changed (Home Assistant), so a value that
	// is reported again but predates EVCC's last reading does not replace it.
	`ALTER TABLE soc_readings ADD COLUMN changed_at TEXT;`,
}

// Open opens (creating if needed) the database at path and migrates it.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection serializes writes and avoids SQLITE_BUSY; load is tiny.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, q: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// InTx runs fn in one transaction, committed when fn returns nil. fn must use
// only the Store it is given: the database has a single connection, which
// the transaction holds until it ends. Nested calls join the open
// transaction.
func (s *Store) InTx(fn func(tx *Store) error) error {
	if s.tx {
		return fn(s)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(&Store{db: s.db, q: tx, tx: true}); err != nil {
		return err
	}
	return tx.Commit()
}

// Ping checks the database is usable.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- settings ----

// Settings rows hold JSON objects, keyed by kind.
const (
	settingsKey    = "planning"
	adviceKey      = "advice"
	connectionsKey = "connections"
)

// setting decodes a saved settings row over v, which holds the defaults, and
// reports whether the row exists. Fields added later keep their default.
func (s *Store) setting(key string, v any) (bool, error) {
	var raw string
	err := s.q.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return true, fmt.Errorf("decode %s settings: %w", key, err)
	}
	return true, nil
}

// putSetting stores a settings row.
func (s *Store) putSetting(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.q.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, string(raw), fmtTime(time.Now()))
	return err
}

// Settings returns saved settings merged onto defaults, and whether the user
// has saved settings at all.
func (s *Store) Settings(defaults model.Settings) (model.Settings, bool, error) {
	v := defaults
	saved, err := s.setting(settingsKey, &v)
	if err != nil {
		return defaults, false, err
	}
	return v, saved, nil
}

// SaveSettings stores the full settings object.
func (s *Store) SaveSettings(v model.Settings) error { return s.putSetting(settingsKey, v) }

// ConnectionSettings returns the saved connection settings merged onto
// defaults. Only fields that differ from the built-in values are saved, so
// the others follow later default changes.
func (s *Store) ConnectionSettings(defaults model.ConnectionSettings) (model.ConnectionSettings, error) {
	v := defaults
	if _, err := s.setting(connectionsKey, &v); err != nil {
		return defaults, err
	}
	return v, nil
}

// SaveConnectionSettings stores the fields that differ from the built-in values.
func (s *Store) SaveConnectionSettings(changed map[string]any) error {
	return s.putSetting(connectionsKey, changed)
}

// ---- places ----

// Places lists saved places by name.
func (s *Store) Places() ([]model.Place, error) {
	rows, err := s.q.Query(`SELECT id, name, address FROM places ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	places := []model.Place{}
	for rows.Next() {
		var p model.Place
		if err := rows.Scan(&p.ID, &p.Name, &p.Address); err != nil {
			return nil, err
		}
		places = append(places, p)
	}
	return places, rows.Err()
}

// Place returns one saved place.
func (s *Store) Place(id int64) (model.Place, error) {
	var p model.Place
	err := s.q.QueryRow(`SELECT id, name, address FROM places WHERE id = ?`, id).Scan(&p.ID, &p.Name, &p.Address)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// CreatePlace inserts a place and returns it with its ID.
func (s *Store) CreatePlace(p model.Place) (model.Place, error) {
	res, err := s.q.Exec(`INSERT INTO places (name, address) VALUES (?, ?)`, p.Name, p.Address)
	if err != nil {
		return p, uniqueErr(err, "a place with this name already exists")
	}
	p.ID, err = res.LastInsertId()
	return p, err
}

// UpdatePlace replaces a place's name and address.
func (s *Store) UpdatePlace(p model.Place) error {
	res, err := s.q.Exec(`UPDATE places SET name = ?, address = ? WHERE id = ?`, p.Name, p.Address, p.ID)
	if err != nil {
		return uniqueErr(err, "a place with this name already exists")
	}
	return expectRow(res)
}

// DeletePlace removes a place.
func (s *Store) DeletePlace(id int64) error {
	res, err := s.q.Exec(`DELETE FROM places WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectRow(res)
}

// ---- recurring templates ----

// Templates lists recurring schedules.
func (s *Store) Templates() ([]model.Template, error) {
	rows, err := s.q.Query(`SELECT id, title, destination, weekdays, start_time, end_time, enabled, exceptions
		FROM recurring_templates ORDER BY start_time, title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []model.Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// Template returns one recurring schedule.
func (s *Store) Template(id int64) (model.Template, error) {
	row := s.q.QueryRow(`SELECT id, title, destination, weekdays, start_time, end_time, enabled, exceptions
		FROM recurring_templates WHERE id = ?`, id)
	t, err := scanTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

type scanner interface{ Scan(...any) error }

func scanTemplate(r scanner) (model.Template, error) {
	var t model.Template
	var weekdays, exceptions string
	var enabled int
	if err := r.Scan(&t.ID, &t.Title, &t.Destination, &weekdays, &t.Start, &t.End, &enabled, &exceptions); err != nil {
		return t, err
	}
	t.Enabled = enabled == 1
	if err := json.Unmarshal([]byte(weekdays), &t.Weekdays); err != nil {
		return t, fmt.Errorf("decode weekdays: %w", err)
	}
	if err := json.Unmarshal([]byte(exceptions), &t.Exceptions); err != nil {
		return t, fmt.Errorf("decode exceptions: %w", err)
	}
	if t.Exceptions == nil {
		t.Exceptions = []model.DateRange{}
	}
	return t, nil
}

// CreateTemplate inserts a recurring schedule.
func (s *Store) CreateTemplate(t model.Template) (model.Template, error) {
	weekdays, exceptions, err := templateJSON(t)
	if err != nil {
		return t, err
	}
	res, err := s.q.Exec(`INSERT INTO recurring_templates (title, destination, weekdays, start_time, end_time, enabled, exceptions)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, t.Title, t.Destination, weekdays, t.Start, t.End, boolInt(t.Enabled), exceptions)
	if err != nil {
		return t, err
	}
	t.ID, err = res.LastInsertId()
	return t, err
}

// UpdateTemplate replaces a recurring schedule.
func (s *Store) UpdateTemplate(t model.Template) error {
	weekdays, exceptions, err := templateJSON(t)
	if err != nil {
		return err
	}
	res, err := s.q.Exec(`UPDATE recurring_templates SET title = ?, destination = ?, weekdays = ?, start_time = ?,
		end_time = ?, enabled = ?, exceptions = ? WHERE id = ?`,
		t.Title, t.Destination, weekdays, t.Start, t.End, boolInt(t.Enabled), exceptions, t.ID)
	if err != nil {
		return err
	}
	return expectRow(res)
}

func templateJSON(t model.Template) (string, string, error) {
	if t.Exceptions == nil {
		t.Exceptions = []model.DateRange{}
	}
	w, err := json.Marshal(t.Weekdays)
	if err != nil {
		return "", "", err
	}
	e, err := json.Marshal(t.Exceptions)
	return string(w), string(e), err
}

// DeleteTemplate removes a schedule and the local edits of its occurrences.
func (s *Store) DeleteTemplate(id int64) error {
	return s.InTx(func(tx *Store) error {
		res, err := tx.q.Exec(`DELETE FROM recurring_templates WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if err := expectRow(res); err != nil {
			return err
		}
		prefix := fmt.Sprintf("recurring:%d:", id)
		_, err = tx.q.Exec(`DELETE FROM trip_overrides WHERE substr(trip_key, 1, ?) = ?`, len(prefix), prefix)
		return err
	})
}

// ---- manual trips ----

// ManualTrips lists manual trips overlapping [from, to).
func (s *Store) ManualTrips(from, to time.Time) ([]model.ManualTrip, error) {
	rows, err := s.q.Query(`SELECT id, title, destination, start_at, end_at, enabled, distance_override_km
		FROM manual_trips WHERE start_at < ? AND end_at >= ? ORDER BY start_at`, fmtTime(to), fmtTime(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []model.ManualTrip
	for rows.Next() {
		m, err := scanManual(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// ManualTrip returns one manual trip.
func (s *Store) ManualTrip(id int64) (model.ManualTrip, error) {
	row := s.q.QueryRow(`SELECT id, title, destination, start_at, end_at, enabled, distance_override_km
		FROM manual_trips WHERE id = ?`, id)
	m, err := scanManual(row)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func scanManual(r scanner) (model.ManualTrip, error) {
	var m model.ManualTrip
	var start, end string
	var enabled int
	var dist sql.NullFloat64
	if err := r.Scan(&m.ID, &m.Title, &m.Destination, &start, &end, &enabled, &dist); err != nil {
		return m, err
	}
	var err error
	if m.Start, err = parseTime(start); err != nil {
		return m, err
	}
	if m.End, err = parseTime(end); err != nil {
		return m, err
	}
	m.Enabled = enabled == 1
	if dist.Valid {
		m.DistanceOverrideKm = &dist.Float64
	}
	return m, nil
}

// CreateManualTrip inserts a manual trip.
func (s *Store) CreateManualTrip(m model.ManualTrip) (model.ManualTrip, error) {
	res, err := s.q.Exec(`INSERT INTO manual_trips (title, destination, start_at, end_at, enabled, distance_override_km)
		VALUES (?, ?, ?, ?, ?, ?)`, m.Title, m.Destination, fmtTime(m.Start), fmtTime(m.End), boolInt(m.Enabled), m.DistanceOverrideKm)
	if err != nil {
		return m, err
	}
	m.ID, err = res.LastInsertId()
	return m, err
}

// UpdateManualTrip replaces a manual trip.
func (s *Store) UpdateManualTrip(m model.ManualTrip) error {
	res, err := s.q.Exec(`UPDATE manual_trips SET title = ?, destination = ?, start_at = ?, end_at = ?, enabled = ?,
		distance_override_km = ? WHERE id = ?`,
		m.Title, m.Destination, fmtTime(m.Start), fmtTime(m.End), boolInt(m.Enabled), m.DistanceOverrideKm, m.ID)
	if err != nil {
		return err
	}
	return expectRow(res)
}

// DeleteManualTrip removes a manual trip.
func (s *Store) DeleteManualTrip(id int64) error {
	res, err := s.q.Exec(`DELETE FROM manual_trips WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectRow(res)
}

// ---- overrides ----

type overrideJSON struct {
	Title              *string    `json:"title,omitempty"`
	Destination        *string    `json:"destination,omitempty"`
	Start              *time.Time `json:"start,omitempty"`
	End                *time.Time `json:"end,omitempty"`
	Enabled            *bool      `json:"enabled,omitempty"`
	DistanceOverrideKm *float64   `json:"distanceOverrideKm,omitempty"`
	Reviewed           *bool      `json:"reviewed,omitempty"`
}

// Overrides returns all local trip edits keyed by trip key.
func (s *Store) Overrides() (map[string]model.Override, error) {
	rows, err := s.q.Query(`SELECT trip_key, data FROM trip_overrides`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.Override{}
	for rows.Next() {
		var key, data string
		if err := rows.Scan(&key, &data); err != nil {
			return nil, err
		}
		var o overrideJSON
		if err := json.Unmarshal([]byte(data), &o); err != nil {
			return nil, fmt.Errorf("decode override %s: %w", key, err)
		}
		out[key] = model.Override{
			TripKey: key, Title: o.Title, Destination: o.Destination, Start: o.Start, End: o.End,
			Enabled: o.Enabled, DistanceOverrideKm: o.DistanceOverrideKm, Reviewed: o.Reviewed,
		}
	}
	return out, rows.Err()
}

// SaveOverride stores a trip's local edits, or deletes them when empty.
func (s *Store) SaveOverride(o model.Override) error {
	data := overrideJSON{
		Title: o.Title, Destination: o.Destination, Start: o.Start, End: o.End,
		Enabled: o.Enabled, DistanceOverrideKm: o.DistanceOverrideKm, Reviewed: o.Reviewed,
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if string(raw) == "{}" {
		return s.DeleteOverride(o.TripKey)
	}
	_, err = s.q.Exec(`INSERT INTO trip_overrides (trip_key, data, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(trip_key) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		o.TripKey, string(raw), fmtTime(time.Now()))
	return err
}

// DeleteOverride removes a trip's local edits.
func (s *Store) DeleteOverride(key string) error {
	_, err := s.q.Exec(`DELETE FROM trip_overrides WHERE trip_key = ?`, key)
	return err
}

// ---- calendar ----

// ReplaceCalendarEvents swaps the stored events of one calendar.
func (s *Store) ReplaceCalendarEvents(calendarID, name string, events []model.CalendarEvent) error {
	return s.InTx(func(tx *Store) error {
		if _, err := tx.q.Exec(`DELETE FROM calendar_events WHERE calendar = ?`, calendarID); err != nil {
			return err
		}
		stmt, err := tx.q.Prepare(`INSERT OR REPLACE INTO calendar_events (key, calendar, calendar_name, uid, title,
			location, start_at, end_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, e := range events {
			if _, err := stmt.Exec(e.Key, calendarID, name, e.UID, e.Title, e.Location, fmtTime(e.Start), fmtTime(e.End)); err != nil {
				return err
			}
		}
		return nil
	})
}

// RemoveCalendarsExcept deletes events and sync state of calendars that are
// no longer configured.
func (s *Store) RemoveCalendarsExcept(ids []string) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, n := range ids {
		args[i] = n
	}
	where := "1 = 1"
	if len(ids) > 0 {
		where = "calendar NOT IN (" + placeholders + ")"
	}
	if _, err := s.q.Exec(`DELETE FROM calendar_events WHERE `+where, args...); err != nil {
		return err
	}
	_, err := s.q.Exec(`DELETE FROM calendar_sync WHERE `+where, args...)
	return err
}

// CalendarEvents lists stored events overlapping [from, to).
func (s *Store) CalendarEvents(from, to time.Time) ([]model.CalendarEvent, error) {
	rows, err := s.q.Query(`SELECT key, calendar, calendar_name, uid, title, location, start_at, end_at FROM calendar_events
		WHERE start_at < ? AND end_at >= ? ORDER BY start_at`, fmtTime(to), fmtTime(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []model.CalendarEvent
	for rows.Next() {
		var e model.CalendarEvent
		var start, end string
		if err := rows.Scan(&e.Key, &e.CalendarID, &e.Calendar, &e.UID, &e.Title, &e.Location, &start, &end); err != nil {
			return nil, err
		}
		if e.Start, err = parseTime(start); err != nil {
			return nil, err
		}
		if e.End, err = parseTime(end); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// SaveSyncStatus records a sync attempt.
func (s *Store) SaveSyncStatus(st model.CalendarSyncStatus) error {
	var attempt, success any
	if st.LastAttemptAt != nil {
		attempt = fmtTime(*st.LastAttemptAt)
	}
	if st.LastSuccessAt != nil {
		success = fmtTime(*st.LastSuccessAt)
	}
	_, err := s.q.Exec(`INSERT INTO calendar_sync (calendar, name, last_attempt_at, last_success_at, last_error, event_count)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(calendar) DO UPDATE SET name = excluded.name,
		last_attempt_at = excluded.last_attempt_at, last_success_at = excluded.last_success_at,
		last_error = excluded.last_error, event_count = excluded.event_count`,
		st.CalendarID, st.Name, attempt, success, st.LastError, st.EventCount)
	return err
}

// SyncStatuses returns the sync state per calendar ID.
func (s *Store) SyncStatuses() (map[string]model.CalendarSyncStatus, error) {
	rows, err := s.q.Query(`SELECT calendar, name, last_attempt_at, last_success_at, last_error, event_count FROM calendar_sync`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.CalendarSyncStatus{}
	for rows.Next() {
		var st model.CalendarSyncStatus
		var attempt, success sql.NullString
		if err := rows.Scan(&st.CalendarID, &st.Name, &attempt, &success, &st.LastError, &st.EventCount); err != nil {
			return nil, err
		}
		if attempt.Valid {
			if t, err := parseTime(attempt.String); err == nil {
				st.LastAttemptAt = &t
			}
		}
		if success.Valid {
			if t, err := parseTime(success.String); err == nil {
				st.LastSuccessAt = &t
			}
		}
		out[st.CalendarID] = st
	}
	return out, rows.Err()
}

// ---- route cache ----

// CachedRoute is a stored route lookup.
type CachedRoute struct {
	DistanceM int
	DurationS int
	Provider  string
	FetchedAt time.Time
}

// Route returns a cached route from provider for the exact slot.
func (s *Store) Route(origin, destination, slot, provider string) (*CachedRoute, error) {
	return s.route(`SELECT distance_m, duration_s, provider, fetched_at FROM route_cache
		WHERE origin = ? AND destination = ? AND slot = ? AND provider = ?`, origin, destination, slot, provider)
}

// AnyRoute returns the most recently fetched route from provider for the
// pair, in any slot.
func (s *Store) AnyRoute(origin, destination, provider string) (*CachedRoute, error) {
	return s.route(`SELECT distance_m, duration_s, provider, fetched_at FROM route_cache
		WHERE origin = ? AND destination = ? AND provider = ? ORDER BY fetched_at DESC LIMIT 1`, origin, destination, provider)
}

func (s *Store) route(query string, args ...any) (*CachedRoute, error) {
	var r CachedRoute
	var fetched string
	err := s.q.QueryRow(query, args...).Scan(&r.DistanceM, &r.DurationS, &r.Provider, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.FetchedAt, err = parseTime(fetched)
	return &r, err
}

// PutRoute stores a route lookup.
func (s *Store) PutRoute(origin, destination, slot string, r CachedRoute) error {
	_, err := s.q.Exec(`INSERT INTO route_cache (origin, destination, slot, distance_m, duration_s, provider, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(origin, destination, slot) DO UPDATE SET
		distance_m = excluded.distance_m, duration_s = excluded.duration_s, provider = excluded.provider,
		fetched_at = excluded.fetched_at`,
		origin, destination, slot, r.DistanceM, r.DurationS, r.Provider, fmtTime(r.FetchedAt))
	return err
}

// ---- vehicle SoC ----

// SoCReadings returns each source's last reading, newest first.
func (s *Store) SoCReadings() ([]model.SoCReading, error) {
	rows, err := s.q.Query(`SELECT source, soc, range_km, observed_at, changed_at FROM soc_readings
		ORDER BY observed_at DESC, source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SoCReading
	for rows.Next() {
		var r model.SoCReading
		var observed string
		var rangeKm sql.NullFloat64
		var changed sql.NullString
		if err := rows.Scan(&r.Source, &r.Percent, &rangeKm, &observed, &changed); err != nil {
			return nil, err
		}
		if rangeKm.Valid {
			r.RangeKm = &rangeKm.Float64
		}
		if r.ObservedAt, err = parseTime(observed); err != nil {
			return nil, err
		}
		if changed.Valid {
			if r.ChangedAt, err = parseTime(changed.String); err != nil {
				return nil, err
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SoCReading returns a source's last reading, or nil if it has none.
func (s *Store) SoCReading(source string) (*model.SoCReading, error) {
	list, err := s.SoCReadings()
	if err != nil {
		return nil, err
	}
	for _, r := range list {
		if r.Source == source {
			return &r, nil
		}
	}
	return nil, nil
}

// SaveSoC records a source's latest reading, replacing its previous one.
func (s *Store) SaveSoC(r model.SoCReading) error {
	var changed any
	if !r.ChangedAt.IsZero() {
		changed = fmtTime(r.ChangedAt)
	}
	_, err := s.q.Exec(`INSERT INTO soc_readings (source, soc, range_km, observed_at, changed_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(source) DO UPDATE SET soc = excluded.soc, range_km = excluded.range_km,
		observed_at = excluded.observed_at, changed_at = excluded.changed_at`,
		r.Source, r.Percent, r.RangeKm, fmtTime(r.ObservedAt), changed)
	return err
}

// ---- plan writes ----

// AddPlanWrite records a plan sent to EVCC.
func (s *Store) AddPlanWrite(w model.PlanWrite) (model.PlanWrite, error) {
	res, err := s.q.Exec(`INSERT INTO plan_writes (vehicle, soc, target_time, trip_key, written_at) VALUES (?, ?, ?, ?, ?)`,
		w.Vehicle, w.SoC, fmtTime(w.TargetTime), w.TripKey, fmtTime(w.WrittenAt))
	if err != nil {
		return w, err
	}
	w.ID, err = res.LastInsertId()
	return w, err
}

// LastPlanWrite returns the most recent plan sent to EVCC, if any.
func (s *Store) LastPlanWrite() (*model.PlanWrite, error) {
	var w model.PlanWrite
	var target, written string
	err := s.q.QueryRow(`SELECT id, vehicle, soc, target_time, trip_key, written_at FROM plan_writes
		ORDER BY id DESC LIMIT 1`).Scan(&w.ID, &w.Vehicle, &w.SoC, &target, &w.TripKey, &written)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if w.TargetTime, err = parseTime(target); err != nil {
		return nil, err
	}
	w.WrittenAt, err = parseTime(written)
	return &w, err
}

// ---- forecasts ----

// ForecastRecord is the latest state of one forecast source.
type ForecastRecord struct {
	Source        string
	LastAttemptAt *time.Time
	LastSuccessAt *time.Time
	LastError     string
	Payload       []byte // latest valid payload; kept when later attempts fail
	ContentHash   string
	Detail        string
}

// Forecast returns a source's record, or nil if it was never fetched.
func (s *Store) Forecast(source string) (*ForecastRecord, error) {
	r := ForecastRecord{Source: source}
	var attempt, success, payload sql.NullString
	err := s.q.QueryRow(`SELECT last_attempt_at, last_success_at, last_error, payload, content_hash, detail
		FROM forecast_sources WHERE source = ?`, source).Scan(&attempt, &success, &r.LastError, &payload, &r.ContentHash, &r.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.LastAttemptAt, r.LastSuccessAt = nullTime(attempt), nullTime(success)
	if payload.Valid {
		r.Payload = []byte(payload.String)
	}
	return &r, nil
}

// SaveForecastFailure records a failed attempt and keeps the last payload.
func (s *Store) SaveForecastFailure(source string, at time.Time, msg string) error {
	_, err := s.q.Exec(`INSERT INTO forecast_sources (source, last_attempt_at, last_error) VALUES (?, ?, ?)
		ON CONFLICT(source) DO UPDATE SET last_attempt_at = excluded.last_attempt_at, last_error = excluded.last_error`,
		source, fmtTime(at), msg)
	return err
}

// SaveForecast stores a new valid payload.
func (s *Store) SaveForecast(source string, at time.Time, payload []byte, hash, detail string) error {
	_, err := s.q.Exec(`INSERT INTO forecast_sources (source, last_attempt_at, last_success_at, last_error, payload, content_hash, detail)
		VALUES (?, ?, ?, '', ?, ?, ?) ON CONFLICT(source) DO UPDATE SET last_attempt_at = excluded.last_attempt_at,
		last_success_at = excluded.last_success_at, last_error = '', payload = excluded.payload,
		content_hash = excluded.content_hash, detail = excluded.detail`,
		source, fmtTime(at), fmtTime(at), string(payload), hash, detail)
	return err
}

// TouchForecast records a successful check that found unchanged content.
func (s *Store) TouchForecast(source string, at time.Time) error {
	_, err := s.q.Exec(`UPDATE forecast_sources SET last_attempt_at = ?, last_success_at = ?, last_error = '' WHERE source = ?`,
		fmtTime(at), fmtTime(at), source)
	return err
}

func nullTime(v sql.NullString) *time.Time {
	if !v.Valid {
		return nil
	}
	t, err := parseTime(v.String)
	if err != nil {
		return nil
	}
	return &t
}

// ---- advice ----

// AdviceSettings returns saved advice settings merged onto defaults.
func (s *Store) AdviceSettings(defaults model.AdviceSettings) (model.AdviceSettings, error) {
	v := defaults
	if _, err := s.setting(adviceKey, &v); err != nil {
		return defaults, err
	}
	return v, nil
}

// SaveAdviceSettings stores the advice settings.
func (s *Store) SaveAdviceSettings(v model.AdviceSettings) error { return s.putSetting(adviceKey, v) }

// Dismissals returns dismissed advice as date -> recommendation.
func (s *Store) Dismissals() (map[string]string, error) {
	rows, err := s.q.Query(`SELECT date, recommendation FROM advice_dismissals`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var date, rec string
		if err := rows.Scan(&date, &rec); err != nil {
			return nil, err
		}
		out[date] = rec
	}
	return out, rows.Err()
}

// Dismiss records that the user dismissed a day's advice.
func (s *Store) Dismiss(date, recommendation string, at time.Time) error {
	_, err := s.q.Exec(`INSERT OR REPLACE INTO advice_dismissals (date, recommendation, dismissed_at) VALUES (?, ?, ?)`,
		date, recommendation, fmtTime(at))
	return err
}

// Undismiss removes a dismissal.
func (s *Store) Undismiss(date string) error {
	_, err := s.q.Exec(`DELETE FROM advice_dismissals WHERE date = ?`, date)
	return err
}

// ---- EVCC adjustments ----

// AddAdjustment records a temporary EVCC change.
func (s *Store) AddAdjustment(a model.Adjustment) (model.Adjustment, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return a, err
	}
	res, err := s.q.Exec(`INSERT INTO evcc_adjustments (kind, advice_date, status, expires_at, data) VALUES (?, ?, ?, ?, ?)`,
		a.Kind, a.AdviceDate, a.Status, fmtTime(a.ExpiresAt), string(raw))
	if err != nil {
		return a, err
	}
	a.ID, err = res.LastInsertId()
	return a, err
}

// UpdateAdjustment replaces a stored adjustment.
func (s *Store) UpdateAdjustment(a model.Adjustment) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	res, err := s.q.Exec(`UPDATE evcc_adjustments SET status = ?, expires_at = ?, data = ? WHERE id = ?`,
		a.Status, fmtTime(a.ExpiresAt), string(raw), a.ID)
	if err != nil {
		return err
	}
	return expectRow(res)
}

// Adjustment returns one adjustment.
func (s *Store) Adjustment(id int64) (model.Adjustment, error) {
	list, err := s.adjustments(`WHERE id = ?`, id)
	if err != nil {
		return model.Adjustment{}, err
	}
	if len(list) == 0 {
		return model.Adjustment{}, ErrNotFound
	}
	return list[0], nil
}

// Adjustments lists active adjustments and those for advice dates from
// `since` on, newest first.
func (s *Store) Adjustments(since string) ([]model.Adjustment, error) {
	return s.adjustments(`WHERE status = ? OR advice_date >= ? ORDER BY id DESC`, model.AdjustActive, since)
}

func (s *Store) adjustments(where string, args ...any) ([]model.Adjustment, error) {
	rows, err := s.q.Query(`SELECT id, data FROM evcc_adjustments `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []model.Adjustment{}
	for rows.Next() {
		var id int64
		var data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		var a model.Adjustment
		if err := json.Unmarshal([]byte(data), &a); err != nil {
			return nil, fmt.Errorf("decode adjustment %d: %w", id, err)
		}
		a.ID = id
		list = append(list, a)
	}
	return list, rows.Err()
}

// ---- retention ----

// Cleanup deletes history older than cutoff in one transaction: activity
// log entries, manual trips that have ended, per-date edits of past dates
// (and of calendar events that are gone), cached routes, plan records
// (keeping the latest), advice dismissals, and ended EVCC changes.
// Schedules, places, and settings are kept. It returns the number of rows
// deleted per kind, omitting kinds with none.
func (s *Store) Cleanup(cutoff time.Time, loc *time.Location) (map[string]int64, error) {
	at, date := fmtTime(cutoff), cutoff.In(loc).Format("2006-01-02")
	steps := []struct {
		kind  string
		query string
		args  []any
	}{
		{"activity", `DELETE FROM audit_events WHERE at < ?`, []any{at}},
		{"manual_trips", `DELETE FROM manual_trips WHERE end_at < ?`, []any{at}},
		{"trip_edits", `DELETE FROM trip_overrides WHERE (trip_key LIKE 'recurring:%' AND substr(trip_key, -10) < ?)
			OR (trip_key LIKE 'calendar:%' AND updated_at < ?
				AND substr(trip_key, 10) NOT IN (SELECT key FROM calendar_events))`, []any{date, at}},
		{"routes", `DELETE FROM route_cache WHERE fetched_at < ?`, []any{at}},
		{"plan_writes", `DELETE FROM plan_writes WHERE written_at < ? AND id < (SELECT MAX(id) FROM plan_writes)`, []any{at}},
		{"dismissals", `DELETE FROM advice_dismissals WHERE date < ?`, []any{date}},
		{"adjustments", `DELETE FROM evcc_adjustments WHERE status != ? AND advice_date < ?`, []any{model.AdjustActive, date}},
	}
	out := map[string]int64{}
	err := s.InTx(func(tx *Store) error {
		for _, st := range steps {
			res, err := tx.q.Exec(st.query, st.args...)
			if err != nil {
				return fmt.Errorf("clean up %s: %w", st.kind, err)
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				out[st.kind] = n
			}
		}
		return nil
	})
	return out, err
}

// ---- helpers ----

// Reset deletes all personal data (demo mode only). The route cache is kept.
func (s *Store) Reset() error {
	for _, table := range []string{"settings", "places", "recurring_templates", "manual_trips", "trip_overrides",
		"calendar_events", "calendar_sync", "soc_readings", "plan_writes", "forecast_sources", "advice_dismissals",
		"evcc_adjustments", "audit_events"} {
		if _, err := s.q.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	return nil
}

// IsEmpty reports whether no personal data has been saved yet.
func (s *Store) IsEmpty() (bool, error) {
	var n int
	err := s.q.QueryRow(`SELECT (SELECT COUNT(*) FROM settings WHERE key = ?) + (SELECT COUNT(*) FROM places)
		+ (SELECT COUNT(*) FROM recurring_templates) + (SELECT COUNT(*) FROM manual_trips)`, settingsKey).Scan(&n)
	return n == 0, err
}

// Meta returns an internal flag stored alongside settings.
func (s *Store) Meta(key string) (string, error) {
	var v string
	err := s.q.QueryRow(`SELECT value FROM settings WHERE key = ?`, "meta."+key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetMeta stores an internal flag.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.q.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		"meta."+key, value, fmtTime(time.Now()))
	return err
}

func expectRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ValidationError is a user-facing input problem.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

func uniqueErr(err error, msg string) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ValidationError{Msg: msg}
	}
	return err
}
