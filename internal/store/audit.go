package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

// AddAuditEvent appends an event to the activity log. Inside InTx it
// commits or rolls back together with the change it describes.
func (s *Store) AddAuditEvent(e model.AuditEvent) (model.AuditEvent, error) {
	res, err := s.q.Exec(`INSERT INTO audit_events (at, type, source, action, subject_type, subject_id, related, summary,
		before_json, after_json, detail_json, outcome, error, service, status_code, duration_ms, bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fmtTime(e.At), e.Type, e.Source, e.Action, e.SubjectType, e.SubjectID, e.Related, e.Summary,
		rawJSON(e.Before), rawJSON(e.After), rawJSON(e.Detail), e.Outcome, e.Error,
		e.Service, e.StatusCode, e.DurationMs, e.Bytes)
	if err != nil {
		return e, err
	}
	e.ID, err = res.LastInsertId()
	return e, err
}

func rawJSON(v json.RawMessage) any {
	if len(v) == 0 || string(v) == "null" {
		return nil
	}
	return string(v)
}

// AuditFilter selects activity log events. Zero values match everything.
type AuditFilter struct {
	SubjectTypes []string
	Source       string
	From, To     time.Time // [From, To)
	// Subject is "type" or "type:id"; it also matches events that name it
	// as their related subject (e.g. a schedule's per-date edits).
	Subject  string
	BeforeID int64 // only events older than this ID (paging)
	Limit    int   // 0 = no limit
}

// AuditEvents lists matching events, newest first.
func (s *Store) AuditEvents(f AuditFilter) ([]model.AuditEvent, error) {
	var where []string
	var args []any
	if len(f.SubjectTypes) > 0 {
		where = append(where, "subject_type IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.SubjectTypes)), ",")+")")
		for _, t := range f.SubjectTypes {
			args = append(args, t)
		}
	}
	if f.Source != "" {
		where = append(where, "source = ?")
		args = append(args, f.Source)
	}
	if !f.From.IsZero() {
		where = append(where, "at >= ?")
		args = append(args, fmtTime(f.From))
	}
	if !f.To.IsZero() {
		where = append(where, "at < ?")
		args = append(args, fmtTime(f.To))
	}
	if f.Subject != "" {
		typ, id, _ := strings.Cut(f.Subject, ":")
		if id == "" {
			where = append(where, "(subject_type = ? OR related = ? OR related LIKE ?)")
			args = append(args, typ, typ, typ+":%")
		} else {
			where = append(where, "((subject_type = ? AND subject_id = ?) OR related = ?)")
			args = append(args, typ, id, f.Subject)
		}
	}
	if f.BeforeID > 0 {
		where = append(where, "id < ?")
		args = append(args, f.BeforeID)
	}
	query := `SELECT id, at, type, source, action, subject_type, subject_id, related, summary, before_json, after_json,
		detail_json, outcome, error, service, status_code, duration_ms, bytes FROM audit_events`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id DESC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []model.AuditEvent{}
	for rows.Next() {
		var e model.AuditEvent
		var at string
		var before, after, detail sql.NullString
		if err := rows.Scan(&e.ID, &at, &e.Type, &e.Source, &e.Action, &e.SubjectType, &e.SubjectID, &e.Related,
			&e.Summary, &before, &after, &detail, &e.Outcome, &e.Error, &e.Service, &e.StatusCode, &e.DurationMs,
			&e.Bytes); err != nil {
			return nil, err
		}
		if e.At, err = parseTime(at); err != nil {
			return nil, err
		}
		e.Before, e.After, e.Detail = nullRaw(before), nullRaw(after), nullRaw(detail)
		list = append(list, e)
	}
	return list, rows.Err()
}

func nullRaw(v sql.NullString) json.RawMessage {
	if !v.Valid {
		return nil
	}
	return json.RawMessage(v.String)
}

// ServiceUsage counts the calls to one external service.
type ServiceUsage struct {
	Service    string
	Calls      int
	Failures   int
	Bytes      int64
	DurationMs int64 // total
}

// APIUsage counts external API calls per service in [from, to).
func (s *Store) APIUsage(from, to time.Time) (map[string]ServiceUsage, error) {
	rows, err := s.q.Query(`SELECT service, COUNT(*), SUM(outcome = ?), SUM(bytes), SUM(duration_ms) FROM audit_events
		WHERE type = ? AND at >= ? AND at < ? GROUP BY service`,
		model.OutcomeFailure, model.EventAPICall, fmtTime(from), fmtTime(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ServiceUsage{}
	for rows.Next() {
		var u ServiceUsage
		if err := rows.Scan(&u.Service, &u.Calls, &u.Failures, &u.Bytes, &u.DurationMs); err != nil {
			return nil, err
		}
		out[u.Service] = u
	}
	return out, rows.Err()
}
