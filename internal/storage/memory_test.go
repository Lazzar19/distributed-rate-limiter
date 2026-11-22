package storage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lazzar19/distributed-rate-limiter/internal/limiter"
)

func samplePolicy() limiter.Policy {
	return limiter.Policy{Capacity: 10, RefillRate: 2, RefillInterval: time.Second}
}

func TestMemoryCanonicalRows(t *testing.T) {
	policy := samplePolicy()
	epoch := time.Unix(0, 0).UTC()

	t.Run("full bucket", func(t *testing.T) {
		store := NewMemory(func() time.Time { return epoch })
		decision, err := store.Allow(context.Background(), "client", policy)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, true, 9, 0)
	})

	t.Run("empty", func(t *testing.T) {
		store := NewMemory(func() time.Time { return epoch })
		drain(t, store, "client", policy, int(policy.Capacity))
		decision, err := store.Allow(context.Background(), "client", policy)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, false, 0, 500*time.Millisecond)
	})

	t.Run("partial refill", func(t *testing.T) {
		current := epoch
		store := NewMemory(func() time.Time { return current })
		drain(t, store, "client", policy, int(policy.Capacity))

		current = epoch.Add(400 * time.Millisecond)
		decision, err := store.Allow(context.Background(), "client", policy)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, false, 0, 100*time.Millisecond)

		current = epoch.Add(500 * time.Millisecond)
		decision, err = store.Allow(context.Background(), "client", policy)
		if err != nil {
			t.Fatalf("Allow remainder: %v", err)
		}
		assertDecision(t, decision, true, 0, 0)
	})

	t.Run("one token refilled", func(t *testing.T) {
		current := epoch
		store := NewMemory(func() time.Time { return current })
		drain(t, store, "client", policy, int(policy.Capacity))

		current = epoch.Add(500 * time.Millisecond)
		decision, err := store.Allow(context.Background(), "client", policy)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, true, 0, 0)
	})

	t.Run("long idle clamps", func(t *testing.T) {
		current := epoch
		store := NewMemory(func() time.Time { return current })
		drain(t, store, "client", policy, int(policy.Capacity))

		current = epoch.Add(100 * time.Second)
		decision, err := store.Allow(context.Background(), "client", policy)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, true, 9, 0)
	})

	t.Run("clock step backward", func(t *testing.T) {
		current := epoch.Add(time.Second)
		store := NewMemory(func() time.Time { return current })
		drain(t, store, "client", policy, 6)

		current = epoch
		decision, err := store.Allow(context.Background(), "client", policy)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, true, 3, 0)
	})
}

func TestMemoryClientsAreIndependent(t *testing.T) {
	policy := samplePolicy()
	store := NewMemory(func() time.Time { return time.Unix(0, 0).UTC() })
	drain(t, store, "a", policy, int(policy.Capacity))

	denied, err := store.Allow(context.Background(), "a", policy)
	if err != nil {
		t.Fatalf("Allow a: %v", err)
	}
	if denied.Allowed {
		t.Fatal("drained client was allowed")
	}

	other, err := store.Allow(context.Background(), "b", policy)
	if err != nil {
		t.Fatalf("Allow b: %v", err)
	}
	assertDecision(t, other, true, 9, 0)
}

func TestMemoryConcurrentOneKey(t *testing.T) {
	policy := limiter.Policy{Capacity: 10, RefillRate: 1, RefillInterval: time.Hour}
	store := NewMemory(func() time.Time { return time.Unix(0, 0).UTC() })

	const requests = 100
	var allowed atomic.Int64
	var wg sync.WaitGroup
	wg.Add(requests)
	for range requests {
		go func() {
			defer wg.Done()
			decision, err := store.Allow(context.Background(), "client", policy)
			if err != nil {
				t.Errorf("Allow: %v", err)
				return
			}
			if decision.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := allowed.Load(); got != int64(policy.Capacity) {
		t.Fatalf("allowed = %d, want %d", got, int64(policy.Capacity))
	}
}

func TestMemoryConcurrentDifferentKeys(t *testing.T) {
	policy := limiter.Policy{Capacity: 10, RefillRate: 1, RefillInterval: time.Hour}
	store := NewMemory(func() time.Time { return time.Unix(0, 0).UTC() })

	const requests = 40
	var allowedA, allowedB atomic.Int64
	var wg sync.WaitGroup
	wg.Add(requests * 2)
	for range requests {
		go func() {
			defer wg.Done()
			decision, err := store.Allow(context.Background(), "a", policy)
			if err != nil {
				t.Errorf("Allow a: %v", err)
				return
			}
			if decision.Allowed {
				allowedA.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			decision, err := store.Allow(context.Background(), "b", policy)
			if err != nil {
				t.Errorf("Allow b: %v", err)
				return
			}
			if decision.Allowed {
				allowedB.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := allowedA.Load(); got != int64(policy.Capacity) {
		t.Fatalf("client a allowed = %d, want %d", got, int64(policy.Capacity))
	}
	if got := allowedB.Load(); got != int64(policy.Capacity) {
		t.Fatalf("client b allowed = %d, want %d", got, int64(policy.Capacity))
	}
}

func TestMemoryCanceledContextDoesNotConsume(t *testing.T) {
	policy := samplePolicy()
	store := NewMemory(func() time.Time { return time.Unix(0, 0).UTC() })
	first, err := store.Allow(context.Background(), "client", policy)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	assertDecision(t, first, true, 9, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.Allow(ctx, "client", policy)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	next, err := store.Allow(context.Background(), "client", policy)
	if err != nil {
		t.Fatalf("Allow after cancel: %v", err)
	}
	assertDecision(t, next, true, 8, 0)
}

func drain(t *testing.T, store *Memory, key string, policy limiter.Policy, n int) {
	t.Helper()
	for i := range n {
		decision, err := store.Allow(context.Background(), key, policy)
		if err != nil {
			t.Fatalf("drain %d: %v", i+1, err)
		}
		if !decision.Allowed {
			t.Fatalf("drain %d denied", i+1)
		}
	}
}

func assertDecision(t *testing.T, decision limiter.Decision, allowed bool, remaining int, retryAfter time.Duration) {
	t.Helper()
	if decision.Allowed != allowed || decision.Remaining != remaining || decision.RetryAfter != retryAfter {
		t.Fatalf("decision = allowed %v remaining %d retry %s, want allowed %v remaining %d retry %s",
			decision.Allowed, decision.Remaining, decision.RetryAfter, allowed, remaining, retryAfter)
	}
}
