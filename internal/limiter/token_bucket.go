// Package limiter holds the token-bucket types shared by every storage backend.
package limiter

import (
	"math"
	"time"
)

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

// NewState returns a full bucket at now.
func NewState(now time.Time, policy Policy) State {
	return State{Tokens: policy.Capacity, LastRefill: now}
}

// Advance refills a bucket from elapsed time and consumes one token when
// the bucket has at least one token. now is supplied by the caller.
// The returned state keeps any fractional token and moves LastRefill to now.
func Advance(state State, now time.Time, policy Policy) (State, Decision) {
	elapsed := now.Sub(state.LastRefill)
	if elapsed < 0 {
		elapsed = 0
	}

	tokens := state.Tokens
	if elapsed > 0 && policy.RefillInterval > 0 && policy.RefillRate > 0 {
		tokens += float64(elapsed) / float64(policy.RefillInterval) * policy.RefillRate
	}
	if tokens > policy.Capacity {
		tokens = policy.Capacity
	}
	if tokens < 0 || math.IsNaN(tokens) {
		tokens = 0
	}

	var decision Decision
	if tokens >= 1 {
		tokens--
		decision.Allowed = true
	} else if policy.RefillRate > 0 && policy.RefillInterval > 0 {
		wait := (1 - tokens) * float64(policy.RefillInterval) / policy.RefillRate
		decision.RetryAfter = time.Duration(math.Round(wait))
	}
	decision.Remaining = int(math.Floor(tokens))

	state.Tokens = tokens
	state.LastRefill = now
	return state, decision
}
