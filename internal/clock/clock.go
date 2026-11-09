// Package clock abstracts the current time so planning code and tests can use
// a fixed instant.
package clock

import "time"

// Clock reports the current time.
type Clock interface {
	Now() time.Time
}

// Real is the system clock.
type Real struct{}

// Now returns the system time.
func (Real) Now() time.Time { return time.Now() }

// Fixed always reports the same instant. Used by tests and demo screenshots.
type Fixed struct{ T time.Time }

// Now returns the fixed instant.
func (f Fixed) Now() time.Time { return f.T }
