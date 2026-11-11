package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if cfg.ServerPort != 8080 {
		t.Errorf("ServerPort = %d, want 8080", cfg.ServerPort)
	}
	if cfg.StorageBackend != BackendMemory {
		t.Errorf("StorageBackend = %q, want memory", cfg.StorageBackend)
	}
	if cfg.LimiterOnError != FailClosed {
		t.Errorf("LimiterOnError = %q, want fail_closed", cfg.LimiterOnError)
	}
	if cfg.Capacity != 10 {
		t.Errorf("Capacity = %v, want 10", cfg.Capacity)
	}
	if cfg.RefillRate != 2 {
		t.Errorf("RefillRate = %v, want 2", cfg.RefillRate)
	}
	if cfg.RefillInterval != time.Second {
		t.Errorf("RefillInterval = %s, want 1s", cfg.RefillInterval)
	}
	if cfg.RedisAddr != "" || cfg.RedisPassword != "" || cfg.RedisDB != 0 {
		t.Errorf("redis defaults = addr %q password %q db %d", cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v, want none", cfg.TrustedProxies)
	}
}

func TestLoadSample(t *testing.T) {
	env := map[string]string{
		"SERVER_PORT":                "9090",
		"STORAGE_BACKEND":            "redis",
		"LIMITER_ON_ERROR":           "fail_open",
		"RATE_LIMIT_CAPACITY":        "10",
		"RATE_LIMIT_REFILL_RATE":     "2",
		"RATE_LIMIT_REFILL_INTERVAL": "1s",
		"TRUSTED_PROXIES":            "10.0.0.1, 10.0.0.0/8",
		"REDIS_ADDR":                 "redis:6379",
		"REDIS_PASSWORD":             "secret",
		"REDIS_DB":                   "2",
	}
	cfg, err := load(mapLookup(env))
	if err != nil {
		t.Fatalf("load sample: %v", err)
	}
	if cfg.ServerPort != 9090 || cfg.StorageBackend != BackendRedis || cfg.LimiterOnError != FailOpen {
		t.Fatalf("server settings = %+v", cfg)
	}
	if cfg.RedisAddr != "redis:6379" || cfg.RedisPassword != "secret" || cfg.RedisDB != 2 {
		t.Fatalf("redis settings = %+v", cfg)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0] != "10.0.0.1" || cfg.TrustedProxies[1] != "10.0.0.0/8" {
		t.Fatalf("TrustedProxies = %v", cfg.TrustedProxies)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "port zero", env: map[string]string{"SERVER_PORT": "0"}, want: "SERVER_PORT"},
		{name: "port text", env: map[string]string{"SERVER_PORT": "abc"}, want: "SERVER_PORT"},
		{name: "port blank", env: map[string]string{"SERVER_PORT": " "}, want: "SERVER_PORT"},
		{name: "backend", env: map[string]string{"STORAGE_BACKEND": "disk"}, want: "STORAGE_BACKEND"},
		{name: "on error", env: map[string]string{"LIMITER_ON_ERROR": "skip"}, want: "LIMITER_ON_ERROR"},
		{name: "capacity", env: map[string]string{"RATE_LIMIT_CAPACITY": "0.5"}, want: "RATE_LIMIT_CAPACITY"},
		{name: "rate", env: map[string]string{"RATE_LIMIT_REFILL_RATE": "0"}, want: "RATE_LIMIT_REFILL_RATE"},
		{name: "interval zero", env: map[string]string{"RATE_LIMIT_REFILL_INTERVAL": "0s"}, want: "RATE_LIMIT_REFILL_INTERVAL"},
		{name: "interval text", env: map[string]string{"RATE_LIMIT_REFILL_INTERVAL": "soon"}, want: "RATE_LIMIT_REFILL_INTERVAL"},
		{name: "redis addr empty", env: map[string]string{"STORAGE_BACKEND": "redis", "REDIS_ADDR": ""}, want: "REDIS_ADDR"},
		{name: "redis addr missing", env: map[string]string{"STORAGE_BACKEND": "redis"}, want: "REDIS_ADDR"},
		{name: "redis db", env: map[string]string{"REDIS_DB": "-1"}, want: "REDIS_DB"},
		{name: "proxy", env: map[string]string{"TRUSTED_PROXIES": "not-an-ip"}, want: "TRUSTED_PROXIES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(mapLookup(tc.env))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %s", err, tc.want)
			}
		})
	}
}

func TestLoadAllowsEmptyPasswordAndMemoryWithoutRedis(t *testing.T) {
	cfg, err := load(mapLookup(map[string]string{
		"STORAGE_BACKEND": "memory",
		"REDIS_PASSWORD":  "",
		"TRUSTED_PROXIES": "",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.StorageBackend != BackendMemory || cfg.RedisAddr != "" || cfg.RedisPassword != "" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func mapLookup(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}
}
