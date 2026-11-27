package middleware

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Lazzar19/distributed-rate-limiter/internal/limiter"
	"github.com/Lazzar19/distributed-rate-limiter/internal/storage"
)

func samplePolicy() limiter.Policy {
	return limiter.Policy{Capacity: 10, RefillRate: 2, RefillInterval: time.Second}
}

func TestMiddlewareBurstAndHeaders(t *testing.T) {
	epoch := time.Unix(0, 0).UTC()
	current := epoch
	store := storage.NewMemory(func() time.Time { return current })
	handler := New(store, samplePolicy(), false, nil, nil).Wrap(resource())

	for i := 1; i <= 10; i++ {
		rec := get(handler, "192.0.2.10:1234", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i, rec.Code)
		}
		if rec.Header().Get("X-RateLimit-Limit") != "10" {
			t.Fatalf("request %d limit = %s, want 10", i, rec.Header().Get("X-RateLimit-Limit"))
		}
		if rec.Header().Get("Retry-After") != "" {
			t.Fatalf("request %d set Retry-After", i)
		}
		wantRemaining := 10 - i
		if rec.Header().Get("X-RateLimit-Remaining") != strconv.Itoa(wantRemaining) {
			t.Fatalf("request %d remaining = %s, want %d", i, rec.Header().Get("X-RateLimit-Remaining"), wantRemaining)
		}
	}

	denied := get(handler, "192.0.2.10:1234", "")
	if denied.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", denied.Code)
	}
	if denied.Header().Get("X-RateLimit-Limit") != "10" || denied.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("denied headers = limit %s remaining %s", denied.Header().Get("X-RateLimit-Limit"), denied.Header().Get("X-RateLimit-Remaining"))
	}
	if denied.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %s, want 1", denied.Header().Get("Retry-After"))
	}
	if denied.Body.String() == "resource\n" {
		t.Fatal("denied request reached the handler")
	}

	current = epoch.Add(400 * time.Millisecond)
	partial := get(handler, "192.0.2.10:1234", "")
	if partial.Code != http.StatusTooManyRequests || partial.Header().Get("Retry-After") != "1" {
		t.Fatalf("400ms status = %d retry %s", partial.Code, partial.Header().Get("Retry-After"))
	}

	current = epoch.Add(500 * time.Millisecond)
	refilled := get(handler, "192.0.2.10:1234", "")
	if refilled.Code != http.StatusOK {
		t.Fatalf("500ms status = %d, want 200", refilled.Code)
	}
	if refilled.Header().Get("Retry-After") != "" {
		t.Fatal("allowed request set Retry-After")
	}
}

func TestMiddlewareClientsAreIndependent(t *testing.T) {
	store := storage.NewMemory(func() time.Time { return time.Unix(0, 0).UTC() })
	handler := New(store, samplePolicy(), false, nil, nil).Wrap(resource())

	for range 10 {
		if rec := get(handler, "192.0.2.10:1", ""); rec.Code != http.StatusOK {
			t.Fatalf("client a status = %d", rec.Code)
		}
	}
	if rec := get(handler, "192.0.2.10:1", ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("client a status = %d, want 429", rec.Code)
	}

	other := get(handler, "[2001:db8::5]:9", "")
	if other.Code != http.StatusOK {
		t.Fatalf("client b status = %d, want 200", other.Code)
	}
	if other.Header().Get("X-RateLimit-Limit") != "10" || other.Header().Get("X-RateLimit-Remaining") != "9" {
		t.Fatalf("client b headers = limit %s remaining %s", other.Header().Get("X-RateLimit-Limit"), other.Header().Get("X-RateLimit-Remaining"))
	}
}

func TestMiddlewareIgnoresUntrustedForwardedHeader(t *testing.T) {
	store := storage.NewMemory(func() time.Time { return time.Unix(0, 0).UTC() })
	handler := New(store, samplePolicy(), false, nil, nil).Wrap(resource())

	for i := range 10 {
		rec := get(handler, "192.0.2.10:1", "203.0.113."+strconv.Itoa(i+1))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	rec := get(handler, "192.0.2.10:1", "203.0.113.99")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rotated forwarded header status = %d, want 429", rec.Code)
	}
}

func TestMiddlewareUsesForwardedClientFromTrustedProxy(t *testing.T) {
	store := storage.NewMemory(func() time.Time { return time.Unix(0, 0).UTC() })
	handler := New(store, samplePolicy(), false, []string{"10.0.0.0/8"}, nil).Wrap(resource())

	for range 10 {
		rec := get(handler, "10.1.2.3:5000", "203.0.113.10, 10.0.0.1")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	denied := get(handler, "10.9.9.9:5000", "203.0.113.10")
	if denied.Code != http.StatusTooManyRequests {
		t.Fatalf("same forwarded client status = %d, want 429", denied.Code)
	}
	other := get(handler, "10.9.9.9:5000", "203.0.113.11, 10.0.0.1")
	if other.Code != http.StatusOK || other.Header().Get("X-RateLimit-Remaining") != "9" {
		t.Fatalf("other client status = %d remaining %s", other.Code, other.Header().Get("X-RateLimit-Remaining"))
	}
}

func TestMiddlewareStoreError(t *testing.T) {
	policy := samplePolicy()
	store := &scriptedStore{err: errors.New("redis down")}
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("resource\n"))
	})

	closed := New(store, policy, false, nil, logger).Wrap(next)
	rec := get(closed, "192.0.2.10:1", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("fail-closed status = %d, want 503", rec.Code)
	}
	if called {
		t.Fatal("fail-closed called the handler")
	}
	if rec.Header().Get("Retry-After") != "" || rec.Header().Get("X-RateLimit-Limit") != "" || rec.Header().Get("X-RateLimit-Remaining") != "" {
		t.Fatalf("fail-closed headers = %v", rec.Header())
	}
	if !bytes.Contains(buf.Bytes(), []byte("redis down")) {
		t.Fatalf("log = %q", buf.String())
	}

	called = false
	buf.Reset()
	open := New(store, policy, true, nil, logger).Wrap(next)
	rec = get(open, "192.0.2.10:1", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "resource\n" || !called {
		t.Fatalf("fail-open status = %d body %q called %v", rec.Code, rec.Body.String(), called)
	}
	if rec.Header().Get("Retry-After") != "" || rec.Header().Get("X-RateLimit-Limit") != "" || rec.Header().Get("X-RateLimit-Remaining") != "" {
		t.Fatalf("fail-open headers = %v", rec.Header())
	}
	if !bytes.Contains(buf.Bytes(), []byte("redis down")) {
		t.Fatalf("log = %q", buf.String())
	}
}

func TestMiddlewareHeaderValuesComeFromDifferentFields(t *testing.T) {
	store := &scriptedStore{decision: limiter.Decision{Allowed: true, Remaining: 4}}
	handler := New(store, samplePolicy(), false, nil, nil).Wrap(resource())
	rec := get(handler, "192.0.2.10:1", "")
	if rec.Header().Get("X-RateLimit-Limit") != "10" || rec.Header().Get("X-RateLimit-Remaining") != "4" {
		t.Fatalf("headers = limit %s remaining %s", rec.Header().Get("X-RateLimit-Limit"), rec.Header().Get("X-RateLimit-Remaining"))
	}

	store.decision = limiter.Decision{Allowed: false, Remaining: 0, RetryAfter: 1500 * time.Millisecond}
	rec = get(handler, "192.0.2.10:1", "")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("status = %d retry %s", rec.Code, rec.Header().Get("Retry-After"))
	}

	store.decision.RetryAfter = 2 * time.Second
	rec = get(handler, "192.0.2.10:1", "")
	if rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("Retry-After = %s, want 2", rec.Header().Get("Retry-After"))
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	if got := retryAfterSeconds(500 * time.Millisecond); got != 1 {
		t.Fatalf("500ms = %d, want 1", got)
	}
	if got := retryAfterSeconds(time.Second); got != 1 {
		t.Fatalf("1s = %d, want 1", got)
	}
	if got := retryAfterSeconds(2 * time.Second); got != 2 {
		t.Fatalf("2s = %d, want 2", got)
	}
}

type scriptedStore struct {
	decision limiter.Decision
	err      error
}

func (s *scriptedStore) Allow(context.Context, string, limiter.Policy) (limiter.Decision, error) {
	return s.decision, s.err
}

func (s *scriptedStore) Ping(context.Context) error {
	return nil
}

func resource() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("resource\n"))
	})
}

func get(handler http.Handler, remoteAddr, forwarded string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	req.RemoteAddr = remoteAddr
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	req.Header.Set("X-Real-IP", forwarded)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
