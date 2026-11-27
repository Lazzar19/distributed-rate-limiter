// Package api contains the HTTP handlers for the rate limiter.
package api

import (
	"context"
	"net/http"
)

// NewMux returns the routes served by this process.
// Health reports that the process is up. It does not check Redis or the limiter.
// ready is GET /ready. It may check the store. resource is the already limited handler.
func NewMux(resource http.Handler, ready http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.Handle("GET /ready", ready)
	mux.Handle("GET /api/resource", resource)
	return mux
}

// Ready returns 200 when ping succeeds and 503 when it fails.
// A nil ping is ready. Health does not use this check.
func Ready(ping func(context.Context) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ping != nil {
			if err := ping(r.Context()); err != nil {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("not ready\n"))
				return
			}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

// Resource is the sample route protected by the rate-limit middleware.
func Resource(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("resource\n"))
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
