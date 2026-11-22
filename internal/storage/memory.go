package storage

import (
	"context"
	"sync"
	"time"

	"github.com/Lazzar19/distributed-rate-limiter/internal/limiter"
)

// Memory keeps one bucket per client in the process.
// The map lock only finds or inserts a bucket. That bucket's lock covers Advance and the save.
type Memory struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	mu    sync.Mutex
	state limiter.State
}

// NewMemory returns an empty store.
// now is the clock passed to Advance. A nil clock uses time.Now.
// Concurrent Allow calls must be able to call now at the same time.
func NewMemory(now func() time.Time) *Memory {
	if now == nil {
		now = time.Now
	}
	return &Memory{
		buckets: make(map[string]*bucket),
		now:     now,
	}
}

// Allow applies the policy to key and stores the updated bucket.
func (m *Memory) Allow(ctx context.Context, key string, policy limiter.Policy) (limiter.Decision, error) {
	if err := ctx.Err(); err != nil {
		return limiter.Decision{}, err
	}
	now := m.now()
	b := m.loadOrCreate(key, now, policy)

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return limiter.Decision{}, err
	}
	var decision limiter.Decision
	b.state, decision = limiter.Advance(b.state, now, policy)
	return decision, nil
}

func (m *Memory) loadOrCreate(key string, now time.Time, policy limiter.Policy) *bucket {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.buckets[key]
	if !ok {
		b = &bucket{state: limiter.NewState(now, policy)}
		m.buckets[key] = b
	}
	return b
}

var _ Store = (*Memory)(nil)
