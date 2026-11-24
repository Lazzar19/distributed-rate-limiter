package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Lazzar19/distributed-rate-limiter/internal/api"
	"github.com/Lazzar19/distributed-rate-limiter/internal/config"
	"github.com/Lazzar19/distributed-rate-limiter/internal/limiter"
	"github.com/Lazzar19/distributed-rate-limiter/internal/middleware"
	"github.com/Lazzar19/distributed-rate-limiter/internal/storage"
)

const listenHost = "0.0.0.0"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	handler, err := newHandler(cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	addr := net.JoinHostPort(listenHost, strconv.Itoa(cfg.ServerPort))
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("listening on %s backend=%s on_error=%s capacity=%g refill=%g/%s",
		addr, cfg.StorageBackend, cfg.LimiterOnError, cfg.Capacity, cfg.RefillRate, cfg.RefillInterval)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func newHandler(cfg config.Config, now func() time.Time) (http.Handler, error) {
	if cfg.StorageBackend != config.BackendMemory {
		return nil, fmt.Errorf("STORAGE_BACKEND=%s is not available yet", cfg.StorageBackend)
	}
	store := storage.NewMemory(now)
	limit := middleware.New(store, limiter.Policy{
		Capacity:       cfg.Capacity,
		RefillRate:     cfg.RefillRate,
		RefillInterval: cfg.RefillInterval,
	}, cfg.LimiterOnError == config.FailOpen, cfg.TrustedProxies, nil)
	return api.NewMux(limit.Wrap(http.HandlerFunc(api.Resource))), nil
}
