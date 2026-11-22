package limiter

import (
	"math"
	"testing"
	"time"
)

func samplePolicy() Policy {
	return Policy{Capacity: 10, RefillRate: 2, RefillInterval: time.Second}
}

func TestAdvanceCanonicalRows(t *testing.T) {
	policy := samplePolicy()
	epoch := time.Unix(0, 0).UTC()
	cases := []struct {
		name       string
		tokens     float64
		last       time.Duration
		now        time.Duration
		allowed    bool
		tokensOut  float64
		remaining  int
		retryAfter time.Duration
	}{
		{name: "full bucket", tokens: 10, last: 0, now: 0, allowed: true, tokensOut: 9, remaining: 9},
		{name: "empty", tokens: 0, last: 0, now: 0, allowed: false, tokensOut: 0, remaining: 0, retryAfter: 500 * time.Millisecond},
		{name: "partial refill", tokens: 0, last: 0, now: 400 * time.Millisecond, allowed: false, tokensOut: 0.8, remaining: 0, retryAfter: 100 * time.Millisecond},
		{name: "one token refilled", tokens: 0, last: 0, now: 500 * time.Millisecond, allowed: true, tokensOut: 0, remaining: 0},
		{name: "long idle clamps", tokens: 0, last: 0, now: 100 * time.Second, allowed: true, tokensOut: 9, remaining: 9},
		{name: "clock step backward", tokens: 4, last: time.Second, now: 0, allowed: true, tokensOut: 3, remaining: 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := epoch.Add(tc.now)
			state, decision := Advance(State{Tokens: tc.tokens, LastRefill: epoch.Add(tc.last)}, now, policy)

			if decision.Allowed != tc.allowed {
				t.Fatalf("Allowed = %v, want %v", decision.Allowed, tc.allowed)
			}
			if math.Abs(state.Tokens-tc.tokensOut) > 1e-9 {
				t.Fatalf("Tokens = %v, want %v", state.Tokens, tc.tokensOut)
			}
			if decision.Remaining != tc.remaining {
				t.Fatalf("Remaining = %d, want %d", decision.Remaining, tc.remaining)
			}
			if decision.RetryAfter != tc.retryAfter {
				t.Fatalf("RetryAfter = %s, want %s", decision.RetryAfter, tc.retryAfter)
			}
			if !state.LastRefill.Equal(now) {
				t.Fatalf("LastRefill = %s, want %s", state.LastRefill, now)
			}
			if state.Tokens < 0 || state.Tokens > policy.Capacity {
				t.Fatalf("Tokens %v outside 0…%v", state.Tokens, policy.Capacity)
			}
		})
	}
}

func TestNewStateStartsFull(t *testing.T) {
	policy := samplePolicy()
	now := time.Unix(20, 0).UTC()
	state := NewState(now, policy)

	if state.Tokens != policy.Capacity {
		t.Fatalf("Tokens = %v, want %v", state.Tokens, policy.Capacity)
	}
	if !state.LastRefill.Equal(now) {
		t.Fatalf("LastRefill = %s, want %s", state.LastRefill, now)
	}
}

func TestAdvanceBurstThenDeny(t *testing.T) {
	policy := samplePolicy()
	now := time.Unix(0, 0).UTC()
	state := NewState(now, policy)

	for i := 0; i < int(policy.Capacity); i++ {
		var decision Decision
		state, decision = Advance(state, now, policy)
		if !decision.Allowed {
			t.Fatalf("request %d denied", i+1)
		}
	}

	state, decision := Advance(state, now, policy)
	if decision.Allowed {
		t.Fatal("request past capacity was allowed")
	}
	if math.Abs(state.Tokens) > 1e-9 {
		t.Fatalf("Tokens = %v, want 0", state.Tokens)
	}
	if decision.RetryAfter != 500*time.Millisecond {
		t.Fatalf("RetryAfter = %s, want 500ms", decision.RetryAfter)
	}
}

func TestAdvanceKeepsFractionalRemainder(t *testing.T) {
	policy := samplePolicy()
	epoch := time.Unix(0, 0).UTC()
	state := State{Tokens: 0, LastRefill: epoch}

	var decision Decision
	state, decision = Advance(state, epoch.Add(400*time.Millisecond), policy)
	if decision.Allowed {
		t.Fatal("400ms refill was allowed")
	}
	if math.Abs(state.Tokens-0.8) > 1e-9 {
		t.Fatalf("Tokens after 400ms = %v, want 0.8", state.Tokens)
	}

	state, decision = Advance(state, epoch.Add(500*time.Millisecond), policy)
	if !decision.Allowed {
		t.Fatal("remainder plus 100ms was denied")
	}
	if math.Abs(state.Tokens) > 1e-9 {
		t.Fatalf("Tokens after consume = %v, want 0", state.Tokens)
	}
}

func TestAdvanceClampsIncomingTokens(t *testing.T) {
	policy := samplePolicy()
	now := time.Unix(0, 0).UTC()

	over, decision := Advance(State{Tokens: 100, LastRefill: now}, now, policy)
	if !decision.Allowed || math.Abs(over.Tokens-9) > 1e-9 {
		t.Fatalf("over capacity: tokens %v allowed %v", over.Tokens, decision.Allowed)
	}

	under, decision := Advance(State{Tokens: -3, LastRefill: now}, now, policy)
	if decision.Allowed || under.Tokens != 0 {
		t.Fatalf("negative tokens: tokens %v allowed %v", under.Tokens, decision.Allowed)
	}
	if under.Tokens > policy.Capacity || over.Tokens > policy.Capacity {
		t.Fatal("tokens exceeded capacity")
	}
}
