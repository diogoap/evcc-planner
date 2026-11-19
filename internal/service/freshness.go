package service

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// How old each source's data may be before the planner treats it as too
// old (AUTOMATION.md, "When data is too old"). Too old EVCC data stops
// automatic changes; the other sources then show as an error in Settings →
// Connections and on the Overview's health line. The SoC (socStaleAfter),
// the outlook's and the weather's contents (outlookMaxAge, weatherMaxAge),
// and AI plans (aiMaxAge) have their own limits where they are used.

// checksMissed is how many checks in a row may bring no answer before a
// source's data is too old.
const checksMissed = 3

// forecastFetchMaxAge is how long the daily forecast fetches may bring
// nothing: two days missed.
const forecastFetchMaxAge = 50 * time.Hour

// evccFresh reports whether EVCC's state is recent enough to act on: read
// without an error at one of the last three checks.
func (s *Service) evccFresh(snap evccSnapshot) bool {
	return snap.state != nil && snap.err == nil && !s.tooOld(snap.okAt, checksMissed*s.Conn().EVCCPollInterval())
}

// tooOld reports whether a success at is older than maxAge (never for none).
func (s *Service) tooOld(at time.Time, maxAge time.Duration) bool {
	return !at.IsZero() && s.now().Sub(at) > maxAge
}

// ---- background jobs ----

// jobRun is when a background job last started a run, and how often it
// runs.
type jobRun struct {
	at    time.Time
	every time.Duration
}

// jobGrace is the least time a job may go without a run before it counts
// as stopped: one run can take a while (an AI plan, slow answers).
const jobGrace = 10 * time.Minute

// JobStarted records that a background job starts a run (app.StartJobs).
// Jobs that never started (tests) never count as stopped.
func (s *Service) JobStarted(job string, every time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs == nil {
		s.jobs = map[string]jobRun{}
	}
	s.jobs[job] = jobRun{at: s.now(), every: every}
}

// StoppedJobs describes the background jobs that haven't started a run
// for three of their intervals (and at least jobGrace), such as "The EVCC
// check last ran at 10:12"; empty while all run.
func (s *Service) StoppedJobs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for job, r := range s.jobs {
		if s.now().Sub(r.at) > max(checksMissed*r.every, jobGrace) {
			out = append(out, fmt.Sprintf("%s%s last ran at %s", strings.ToUpper(job[:1]), job[1:], s.clockText(r.at)))
		}
	}
	slices.Sort(out)
	return out
}
