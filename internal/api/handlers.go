// Package api contains the HTTP handlers for the rate limiter.
package api

import "net/http"

// NewMux returns the routes served by this process.
// Health reports that the process is up. It does not check Redis or the limiter.
func NewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	return mux
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
