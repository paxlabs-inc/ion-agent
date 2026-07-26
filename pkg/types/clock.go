// Package types contains shared, state-free types used across Ion.
package types

import "time"

// Clock supplies time to business logic.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production wall clock.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time {
	return time.Now()
}
