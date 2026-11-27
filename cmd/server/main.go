package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
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

	handler, release, err := newHandler(cfg, nil)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shut); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("listening on %s backend=%s on_error=%s capacity=%g refill=%g/%s",
		addr, cfg.StorageBackend, cfg.LimiterOnError, cfg.Capacity, cfg.RefillRate, cfg.RefillInterval)

	err = server.ListenAndServe()
	release()
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func newHandler(cfg config.Config, now func() time.Time) (http.Handler, func(), error) {
	store, stopEviction, err := newStore(cfg, now)
	if err != nil {
		return nil, func() {}, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			stopEviction()
			closeStore(store)
		})
	}
	limit := middleware.New(store, limiter.Policy{
		Capacity:       cfg.Capacity,
		RefillRate:     cfg.RefillRate,
		RefillInterval: cfg.RefillInterval,
	}, cfg.LimiterOnError == config.FailOpen, cfg.TrustedProxies, nil)
	handler := api.NewMux(limit.Wrap(http.HandlerFunc(api.Resource)), api.Ready(store.Ping))
	return handler, release, nil
}

func newStore(cfg config.Config, now func() time.Time) (storage.Store, func(), error) {
	switch cfg.StorageBackend {
	case config.BackendMemory:
		mem := storage.NewMemory(now)
		return mem, mem.StartEviction(cfg.MemoryIdle), nil
	case config.BackendRedis:
		store, err := storage.NewRedis(storage.RedisConfig{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       cfg.RedisDB,
		})
		if err != nil {
			return nil, nil, err
		}
		return store, func() {}, nil
	default:
		return nil, nil, fmt.Errorf("STORAGE_BACKEND=%s is not available", cfg.StorageBackend)
	}
}

func closeStore(store storage.Store) {
	closer, ok := store.(io.Closer)
	if !ok {
		return
	}
	if err := closer.Close(); err != nil {
		log.Printf("close store: %v", err)
	}
}
