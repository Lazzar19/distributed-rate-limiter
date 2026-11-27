package storage

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lazzar19/distributed-rate-limiter/internal/limiter"
)

// Redis integration tests are skipped unless REDIS_ADDR is set.
// Run them with:
//
//	REDIS_ADDR=localhost:6379 go test ./internal/storage/ -run Redis -count=1

func TestBucketTTL(t *testing.T) {
	policy := samplePolicy()
	if got := bucketTTL(policy); got != 6*time.Second {
		t.Fatalf("ttl = %s, want 6s", got)
	}
}

func TestRedisAllowReturnsConnectionError(t *testing.T) {
	store, err := openRedis(RedisConfig{Addr: "127.0.0.1:1", Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("openRedis: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	decision, err := store.Allow(context.Background(), "client", samplePolicy())
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if decision.Allowed {
		t.Fatal("connection error was returned as an allowed request")
	}
	if err := store.Ping(context.Background()); err == nil {
		t.Fatal("Ping succeeded against a closed port")
	}
}

func TestRedisCanceledContextDoesNotCallRedis(t *testing.T) {
	store, err := openRedis(RedisConfig{Addr: "127.0.0.1:1", Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("openRedis: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err = store.Allow(ctx, "client", samplePolicy())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("canceled context waited on Redis")
	}
}

func TestRedisCanonicalRows(t *testing.T) {
	policy := samplePolicy()
	epoch := time.Unix(0, 0).UTC()

	t.Run("full bucket", func(t *testing.T) {
		store := newTestRedis(t)
		decision, err := store.allowAt(context.Background(), "client", policy, epoch)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, true, 9, 0)
	})

	t.Run("empty", func(t *testing.T) {
		store := newTestRedis(t)
		drainAt(t, store, "client", policy, epoch, int(policy.Capacity))
		decision, err := store.allowAt(context.Background(), "client", policy, epoch)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, false, 0, 500*time.Millisecond)
	})

	t.Run("partial refill", func(t *testing.T) {
		store := newTestRedis(t)
		drainAt(t, store, "client", policy, epoch, int(policy.Capacity))
		decision, err := store.allowAt(context.Background(), "client", policy, epoch.Add(400*time.Millisecond))
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, false, 0, 100*time.Millisecond)

		decision, err = store.allowAt(context.Background(), "client", policy, epoch.Add(500*time.Millisecond))
		if err != nil {
			t.Fatalf("Allow remainder: %v", err)
		}
		assertDecision(t, decision, true, 0, 0)
	})

	t.Run("one token refilled", func(t *testing.T) {
		store := newTestRedis(t)
		drainAt(t, store, "client", policy, epoch, int(policy.Capacity))
		decision, err := store.allowAt(context.Background(), "client", policy, epoch.Add(500*time.Millisecond))
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, true, 0, 0)
	})

	t.Run("long idle clamps", func(t *testing.T) {
		store := newTestRedis(t)
		drainAt(t, store, "client", policy, epoch, int(policy.Capacity))
		decision, err := store.allowAt(context.Background(), "client", policy, epoch.Add(100*time.Second))
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, true, 9, 0)
	})

	t.Run("clock step backward", func(t *testing.T) {
		store := newTestRedis(t)
		start := epoch.Add(time.Second)
		drainAt(t, store, "client", policy, start, 6)
		decision, err := store.allowAt(context.Background(), "client", policy, epoch)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		assertDecision(t, decision, true, 3, 0)
	})
}

func TestRedisMissingKeyAndUnreadableTimestampStartFull(t *testing.T) {
	store := newTestRedis(t)
	policy := samplePolicy()
	now := time.Unix(0, 0).UTC()

	missing, err := store.allowAt(context.Background(), "missing", policy, now)
	if err != nil {
		t.Fatalf("missing: %v", err)
	}
	assertDecision(t, missing, true, 9, 0)

	key := store.prefix + "broken"
	if err := store.client.HSet(context.Background(), key, "tokens", "0", "last", "nope").Err(); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	broken, err := store.allowAt(context.Background(), "broken", policy, now)
	if err != nil {
		t.Fatalf("broken: %v", err)
	}
	assertDecision(t, broken, true, 9, 0)
	last, err := store.client.HGet(context.Background(), key, "last").Result()
	if err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if last != "0" {
		t.Fatalf("last = %q, want 0", last)
	}
}

func TestRedisTTLMatchesFormula(t *testing.T) {
	store := newTestRedis(t)
	policy := samplePolicy()
	if _, err := store.allowAt(context.Background(), "client", policy, time.Unix(0, 0).UTC()); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	ttl, err := store.client.PTTL(context.Background(), store.prefix+"client").Result()
	if err != nil {
		t.Fatalf("PTTL: %v", err)
	}
	want := bucketTTL(policy)
	if ttl <= 0 || ttl > want || want-ttl > time.Second {
		t.Fatalf("ttl = %s, want about %s", ttl, want)
	}
}

func TestRedisClientsDoNotShareAHash(t *testing.T) {
	store := newTestRedis(t)
	policy := samplePolicy()
	now := time.Unix(0, 0).UTC()
	drainAt(t, store, "a", policy, now, int(policy.Capacity))

	denied, err := store.allowAt(context.Background(), "a", policy, now)
	if err != nil {
		t.Fatalf("Allow a: %v", err)
	}
	if denied.Allowed {
		t.Fatal("drained client was allowed")
	}
	other, err := store.allowAt(context.Background(), "b", policy, now)
	if err != nil {
		t.Fatalf("Allow b: %v", err)
	}
	assertDecision(t, other, true, 9, 0)

	count, err := store.client.Exists(context.Background(), store.prefix+"a", store.prefix+"b").Result()
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if count != 2 {
		t.Fatalf("hashes = %d, want 2", count)
	}
}

func TestRedisAllowUsesRedisTime(t *testing.T) {
	store := newTestRedis(t)
	before := time.Now().Add(-2 * time.Second)
	if _, err := store.Allow(context.Background(), "client", samplePolicy()); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	last, err := store.client.HGet(context.Background(), store.prefix+"client", "last").Result()
	if err != nil {
		t.Fatalf("HGet: %v", err)
	}
	millis, err := strconv.ParseInt(last, 10, 64)
	if err != nil {
		t.Fatalf("last %q: %v", last, err)
	}
	got := time.UnixMilli(millis)
	after := time.Now().Add(2 * time.Second)
	if got.Before(before) || got.After(after) {
		t.Fatalf("last refill %s is outside %s … %s", got, before, after)
	}
}

func TestRedisConcurrentOneKey(t *testing.T) {
	store := newTestRedis(t)
	policy := limiter.Policy{Capacity: 10, RefillRate: 1, RefillInterval: time.Hour}

	const requests = 40
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

func newTestRedis(t *testing.T) *Redis {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR is unset; run: REDIS_ADDR=localhost:6379 go test ./internal/storage/ -run Redis -count=1")
	}
	store, err := NewRedis(RedisConfig{
		Addr:    addr,
		Timeout: 2 * time.Second,
		Prefix:  "rltest:" + t.Name() + ":",
	})
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	t.Cleanup(func() {
		deletePrefix(store)
		_ = store.Close()
	})
	deletePrefix(store)
	return store
}

func deletePrefix(store *Redis) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var cursor uint64
	for {
		keys, next, err := store.client.Scan(ctx, cursor, store.prefix+"*", 100).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			_ = store.client.Del(ctx, keys...).Err()
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}

func drainAt(t *testing.T, store *Redis, key string, policy limiter.Policy, now time.Time, n int) {
	t.Helper()
	for i := range n {
		decision, err := store.allowAt(context.Background(), key, policy, now)
		if err != nil {
			t.Fatalf("drain %d: %v", i+1, err)
		}
		if !decision.Allowed {
			t.Fatalf("drain %d denied", i+1)
		}
	}
}
