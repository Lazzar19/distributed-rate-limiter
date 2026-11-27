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
	idle    time.Duration
}

type bucket struct {
	mu       sync.Mutex
	state    limiter.State
	lastUsed time.Time
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
	b.lastUsed = now
	return decision, nil
}

// Ping reports that the in-memory store can answer. It does not check Redis.
func (m *Memory) Ping(context.Context) error {
	return nil
}

// StartEviction deletes buckets that have not been used for idle.
// One goroutine sweeps the map. Zero or negative idle leaves every bucket in place.
// The returned function stops that goroutine and is safe to call more than once.
func (m *Memory) StartEviction(idle time.Duration) func() {
	m.idle = idle
	if idle <= 0 {
		return func() {}
	}
	stop := make(chan struct{})
	interval := idle / 2
	if interval < time.Second {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				m.sweep(m.now())
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(stop) })
	}
}

// sweep removes buckets whose last use is at least idle before now.
// The map lock is held across the scan. A bucket lock that is already held is left in place.
func (m *Memory) sweep(now time.Time) {
	if m.idle <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, b := range m.buckets {
		if !b.mu.TryLock() {
			continue
		}
		idle := !b.lastUsed.IsZero() && !now.Before(b.lastUsed.Add(m.idle))
		b.mu.Unlock()
		if idle {
			delete(m.buckets, key)
		}
	}
}

func (m *Memory) loadOrCreate(key string, now time.Time, policy limiter.Policy) *bucket {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.buckets[key]
	if !ok {
		b = &bucket{state: limiter.NewState(now, policy), lastUsed: now}
		m.buckets[key] = b
	}
	return b
}

var _ Store = (*Memory)(nil)
