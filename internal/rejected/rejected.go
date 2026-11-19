// Package rejected marks answers from outside services that refuse the
// planner's credentials: an API key, an access token, or a secret address.
// Settings → Connections and the Overview then say which environment
// variable to renew instead of showing a plain error.
package rejected

import (
	"errors"
	"fmt"
	"regexp"
)

// Error is a refusal of the credentials. Its text has a fixed form, so a
// refusal is recognised in an error stored as text too (In).
type Error struct {
	What    string // "API key", "access token", "calendar address"
	Status  int    // the HTTP status; 0 without one (a websocket)
	Message string // what the service said, if anything
}

func (e *Error) Error() string {
	s := e.What + " rejected"
	if e.Status != 0 {
		s += fmt.Sprintf(" (HTTP %d)", e.Status)
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// Is reports whether err is, or wraps, a refusal.
func Is(err error) bool {
	var r *Error
	return errors.As(err, &r)
}

// marker is how a refusal with an HTTP status reads in an error's text.
var marker = regexp.MustCompile(` rejected \(HTTP \d{3}\)`)

// In reports whether an error stored as text came from a refusal with an
// HTTP status.
func In(text string) bool { return marker.MatchString(text) }
