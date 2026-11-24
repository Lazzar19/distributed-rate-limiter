package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Lazzar19/distributed-rate-limiter/internal/config"
)

func TestHandlerBurstAndHealth(t *testing.T) {
	epoch := time.Unix(0, 0).UTC()
	handler, err := newHandler(config.Config{
		StorageBackend: config.BackendMemory,
		LimiterOnError: config.FailClosed,
		Capacity:       10,
		RefillRate:     2,
		RefillInterval: time.Second,
	}, func() time.Time { return epoch })
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}

	for i := 1; i <= 10; i++ {
		rec := request(handler, http.MethodGet, "/api/resource", "192.0.2.10:1")
		if rec.Code != http.StatusOK || rec.Body.String() != "resource\n" {
			t.Fatalf("request %d status = %d body %q", i, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-RateLimit-Limit") != "10" {
			t.Fatalf("request %d limit = %s", i, rec.Header().Get("X-RateLimit-Limit"))
		}
	}
	denied := request(handler, http.MethodGet, "/api/resource", "192.0.2.10:1")
	if denied.Code != http.StatusTooManyRequests || denied.Header().Get("Retry-After") != "1" {
		t.Fatalf("denied status = %d retry %s", denied.Code, denied.Header().Get("Retry-After"))
	}

	other := request(handler, http.MethodGet, "/api/resource", "198.51.100.20:9")
	if other.Code != http.StatusOK || other.Header().Get("X-RateLimit-Remaining") != "9" {
		t.Fatalf("other client status = %d remaining %s", other.Code, other.Header().Get("X-RateLimit-Remaining"))
	}

	health := request(handler, http.MethodGet, "/health", "192.0.2.10:1")
	if health.Code != http.StatusOK || health.Body.String() != "ok\n" {
		t.Fatalf("health status = %d body %q", health.Code, health.Body.String())
	}
	if health.Header().Get("X-RateLimit-Limit") != "" || health.Header().Get("Retry-After") != "" {
		t.Fatal("health response includes rate-limit headers")
	}

	post := request(handler, http.MethodPost, "/api/resource", "198.51.100.21:9")
	if post.Code == http.StatusOK {
		t.Fatal("POST /api/resource returned 200")
	}
}

func TestNewHandlerRejectsRedis(t *testing.T) {
	_, err := newHandler(config.Config{StorageBackend: config.BackendRedis}, nil)
	if err == nil || !strings.Contains(err.Error(), "redis") {
		t.Fatalf("error = %v", err)
	}
}

func request(handler http.Handler, method, path, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
