// Package config loads process settings from environment variables.
package config

import (
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// BackendMemory keeps buckets in the process.
	BackendMemory = "memory"
	// BackendRedis keeps buckets in Redis.
	BackendRedis = "redis"

	// FailClosed rejects requests when the store cannot decide.
	FailClosed = "fail_closed"
	// FailOpen allows requests when the store cannot decide.
	FailOpen = "fail_open"

	defaultPort           = 8080
	defaultBackend        = BackendMemory
	defaultOnError        = FailClosed
	defaultCapacity       = 10.0
	defaultRefillRate     = 2.0
	defaultRefillInterval = time.Second
)

// Config is the validated process configuration.
type Config struct {
	ServerPort     int
	StorageBackend string
	LimiterOnError string
	Capacity       float64
	RefillRate     float64
	RefillInterval time.Duration
	TrustedProxies []string
	RedisAddr      string
	RedisPassword  string
	RedisDB        int
}

// Load reads configuration from the environment.
// Unset values use the sample defaults. A variable that is set to an invalid value returns an error.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

func load(lookup func(string) (string, bool)) (Config, error) {
	var cfg Config
	var err error

	cfg.ServerPort, err = parsePort(lookup)
	if err != nil {
		return Config{}, err
	}
	cfg.StorageBackend, err = parseChoice(lookup, "STORAGE_BACKEND", defaultBackend, BackendMemory, BackendRedis)
	if err != nil {
		return Config{}, err
	}
	cfg.LimiterOnError, err = parseChoice(lookup, "LIMITER_ON_ERROR", defaultOnError, FailClosed, FailOpen)
	if err != nil {
		return Config{}, err
	}
	cfg.Capacity, err = parseFloat(lookup, "RATE_LIMIT_CAPACITY", defaultCapacity, atLeastOne)
	if err != nil {
		return Config{}, err
	}
	cfg.RefillRate, err = parseFloat(lookup, "RATE_LIMIT_REFILL_RATE", defaultRefillRate, greaterThanZero)
	if err != nil {
		return Config{}, err
	}
	cfg.RefillInterval, err = parseInterval(lookup)
	if err != nil {
		return Config{}, err
	}
	cfg.TrustedProxies, err = parseProxies(lookup)
	if err != nil {
		return Config{}, err
	}
	cfg.RedisAddr, err = parseRedisAddr(lookup, cfg.StorageBackend)
	if err != nil {
		return Config{}, err
	}
	cfg.RedisPassword = parsePassword(lookup)
	cfg.RedisDB, err = parseRedisDB(lookup)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parsePort(lookup func(string) (string, bool)) (int, error) {
	raw, set, err := optional(lookup, "SERVER_PORT")
	if err != nil {
		return 0, err
	}
	if !set {
		return defaultPort, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("SERVER_PORT is not a valid integer")
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("SERVER_PORT must be between 1 and 65535")
	}
	return port, nil
}

func parseChoice(lookup func(string) (string, bool), key, fallback string, allowed ...string) (string, error) {
	value, set, err := optional(lookup, key)
	if err != nil {
		return "", err
	}
	if !set {
		value = fallback
	}
	for _, candidate := range allowed {
		if value == candidate {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s must be %s", key, quotedList(allowed))
}

type floatRule func(float64) bool

func atLeastOne(v float64) bool {
	return v >= 1
}

func greaterThanZero(v float64) bool {
	return v > 0
}

func parseFloat(lookup func(string) (string, bool), key string, fallback float64, ok floatRule) (float64, error) {
	raw, set, err := optional(lookup, key)
	if err != nil {
		return 0, err
	}
	if !set {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%s is not a valid number", key)
	}
	if !ok(value) {
		if key == "RATE_LIMIT_CAPACITY" {
			return 0, fmt.Errorf("%s must be at least 1", key)
		}
		return 0, fmt.Errorf("%s must be greater than 0", key)
	}
	return value, nil
}

func parseInterval(lookup func(string) (string, bool)) (time.Duration, error) {
	raw, set, err := optional(lookup, "RATE_LIMIT_REFILL_INTERVAL")
	if err != nil {
		return 0, err
	}
	if !set {
		return defaultRefillInterval, nil
	}
	interval, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("RATE_LIMIT_REFILL_INTERVAL is not a valid duration")
	}
	if interval <= 0 {
		return 0, fmt.Errorf("RATE_LIMIT_REFILL_INTERVAL must be greater than 0")
	}
	return interval, nil
}

func parseProxies(lookup func(string) (string, bool)) ([]string, error) {
	raw, ok := lookup("TRUSTED_PROXIES")
	if !ok {
		return nil, nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	proxies := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if net.ParseIP(part) == nil {
			if _, _, err := net.ParseCIDR(part); err != nil {
				return nil, fmt.Errorf("TRUSTED_PROXIES contains invalid address %q", part)
			}
		}
		proxies = append(proxies, part)
	}
	return proxies, nil
}

func parseRedisAddr(lookup func(string) (string, bool), backend string) (string, error) {
	raw, ok := lookup("REDIS_ADDR")
	if !ok {
		raw = ""
	}
	addr := strings.TrimSpace(raw)
	if addr == "" {
		if backend == BackendRedis {
			return "", fmt.Errorf("REDIS_ADDR is required when STORAGE_BACKEND is redis")
		}
		return "", nil
	}
	return addr, nil
}

func parsePassword(lookup func(string) (string, bool)) string {
	raw, ok := lookup("REDIS_PASSWORD")
	if !ok {
		return ""
	}
	return strings.TrimSpace(raw)
}

func parseRedisDB(lookup func(string) (string, bool)) (int, error) {
	raw, set, err := optional(lookup, "REDIS_DB")
	if err != nil {
		return 0, err
	}
	if !set {
		return 0, nil
	}
	db, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("REDIS_DB is not a valid integer")
	}
	if db < 0 {
		return 0, fmt.Errorf("REDIS_DB must be zero or greater")
	}
	return db, nil
}

// optional reports whether key was present. A present empty value is an error.
func optional(lookup func(string) (string, bool), key string) (string, bool, error) {
	raw, ok := lookup(key)
	if !ok {
		return "", false, nil
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false, fmt.Errorf("%s is empty", key)
	}
	return value, true, nil
}

func quotedList(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " or " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
	}
}
