package store

import (
	"database/sql"

	"github.com/diogoap/evcc-planner/internal/model"
)

// ---- day history ----

const dayStatsColumns = `date, price_min, price_avg, price_max, price_hours, distance_km, energy_kwh,
	wind_bft, sunshine_pct, updated_at`

// SaveDayStats inserts or replaces a day's figures.
func (s *Store) SaveDayStats(d model.DayStats) error {
	_, err := s.q.Exec(`INSERT INTO day_stats (`+dayStatsColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(date) DO UPDATE SET price_min = excluded.price_min, price_avg = excluded.price_avg,
		price_max = excluded.price_max, price_hours = excluded.price_hours, distance_km = excluded.distance_km,
		energy_kwh = excluded.energy_kwh, wind_bft = excluded.wind_bft, sunshine_pct = excluded.sunshine_pct,
		updated_at = excluded.updated_at`,
		d.Date, d.PriceMin, d.PriceAvg, d.PriceMax, d.PriceHours, d.DistanceKm, d.EnergyKWh,
		d.WindBft, d.SunshinePct, fmtTime(d.UpdatedAt))
	return err
}

// DayStat returns one day's figures, or nil if none are saved.
func (s *Store) DayStat(date string) (*model.DayStats, error) {
	list, err := s.DayStats(date, date)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// DayStats returns the saved days from `from` through `to` (local dates,
// inclusive; empty means unbounded), oldest first.
func (s *Store) DayStats(from, to string) ([]model.DayStats, error) {
	rows, err := s.q.Query(`SELECT `+dayStatsColumns+` FROM day_stats
		WHERE (? = '' OR date >= ?) AND (? = '' OR date <= ?) ORDER BY date`, from, from, to, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.DayStats{}
	for rows.Next() {
		var d model.DayStats
		var minP, avgP, maxP, dist, energy sql.NullFloat64
		var wind, sun sql.NullInt64
		var updated string
		if err := rows.Scan(&d.Date, &minP, &avgP, &maxP, &d.PriceHours, &dist, &energy, &wind, &sun, &updated); err != nil {
			return nil, err
		}
		d.PriceMin, d.PriceAvg, d.PriceMax = nullFloat(minP), nullFloat(avgP), nullFloat(maxP)
		d.DistanceKm, d.EnergyKWh = nullFloat(dist), nullFloat(energy)
		d.WindBft, d.SunshinePct = nullInt(wind), nullInt(sun)
		if d.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// FirstDayStats returns the earliest saved date, or "" when there is none.
func (s *Store) FirstDayStats() (string, error) {
	var first sql.NullString
	err := s.q.QueryRow(`SELECT MIN(date) FROM day_stats`).Scan(&first)
	return first.String, err
}

func nullFloat(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

func nullInt(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}
