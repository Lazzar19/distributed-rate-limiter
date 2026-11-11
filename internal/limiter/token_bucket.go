// Package limiter holds the token-bucket types shared by every storage backend.
package limiter

import "time"

// State is one client's bucket. Tokens may be fractional so refill can keep a remainder.
type State struct {
	Tokens     float64
	LastRefill time.Time
}

// Policy is the single rate limit applied to every client.
// Capacity is the maximum token count. RefillRate tokens are added over each RefillInterval.
type Policy struct {
	Capacity       float64
	RefillRate     float64
	RefillInterval time.Duration
}

// Decision is the result of applying the policy once.
// Remaining is the whole tokens left after the decision.
// RetryAfter is zero when the request is allowed.
type Decision struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
}

// Advance refills a bucket from elapsed time and consumes one token when
// the bucket has at least one token. Callers supply now.
//
// The formula is implemented in Milestone 1. Until then Advance panics.
func Advance(state State, now time.Time, policy Policy) (State, Decision) {
	panic("limiter.Advance is implemented in Milestone 1")
}
