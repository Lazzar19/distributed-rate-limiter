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
)

const listenHost = "0.0.0.0"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	addr := net.JoinHostPort(listenHost, strconv.Itoa(cfg.ServerPort))
	server := &http.Server{
		Addr:              addr,
		Handler:           api.NewMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("listening on %s backend=%s on_error=%s capacity=%g refill=%g/%s",
		addr, cfg.StorageBackend, cfg.LimiterOnError, cfg.Capacity, cfg.RefillRate, cfg.RefillInterval)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
