package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	NewMux(http.HandlerFunc(Resource), Ready(nil)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok\n" {
		t.Fatalf("body = %q, want ok", rec.Body.String())
	}
}

func TestHealthIgnoresOtherMethods(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	NewMux(http.HandlerFunc(Resource), Ready(nil)).ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("POST /health returned 200")
	}
}

func TestReady(t *testing.T) {
	ok := httptest.NewRecorder()
	NewMux(http.HandlerFunc(Resource), Ready(nil)).ServeHTTP(ok, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ok.Code != http.StatusOK || ok.Body.String() != "ok\n" {
		t.Fatalf("ready status = %d body %q", ok.Code, ok.Body.String())
	}

	down := httptest.NewRecorder()
	NewMux(http.HandlerFunc(Resource), Ready(func(context.Context) error {
		return errors.New("redis down")
	})).ServeHTTP(down, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if down.Code != http.StatusServiceUnavailable || down.Body.String() != "not ready\n" {
		t.Fatalf("down status = %d body %q", down.Code, down.Body.String())
	}

	health := httptest.NewRecorder()
	NewMux(http.HandlerFunc(Resource), Ready(func(context.Context) error {
		return errors.New("redis down")
	})).ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK || health.Body.String() != "ok\n" {
		t.Fatalf("health status = %d body %q", health.Code, health.Body.String())
	}
}
