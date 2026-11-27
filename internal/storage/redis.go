package storage

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Lazzar19/distributed-rate-limiter/internal/limiter"
)

const defaultRedisTimeout = 2 * time.Second

// bucketLua is the same refill-and-consume sequence as limiter.Advance.
// ARGV is capacity, refill rate, refill interval nanoseconds, and TTL milliseconds.
// A fifth argument, test-only, is now in Unix milliseconds. The server never sends it.
// Without that argument the script reads Redis TIME.
const bucketLua = `
if redis.replicate_commands then
  redis.replicate_commands()
end

local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local interval_ns = tonumber(ARGV[3])
local ttl_ms = tonumber(ARGV[4])

local now_ms = tonumber(ARGV[5])
if now_ms == nil then
  local clock = redis.call("TIME")
  now_ms = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
end

local tokens = capacity
local last_ms = now_ms
local current = redis.call("HMGET", key, "tokens", "last")
if current[1] ~= false and current[2] ~= false then
  local parsed_tokens = tonumber(current[1])
  local parsed_last = tonumber(current[2])
  if parsed_tokens ~= nil and parsed_last ~= nil then
    tokens = parsed_tokens
    last_ms = parsed_last
  end
end

local elapsed_ms = now_ms - last_ms
if elapsed_ms < 0 then
  elapsed_ms = 0
end
if elapsed_ms > 0 and interval_ns > 0 and rate > 0 then
  local elapsed_ns = elapsed_ms * 1000000
  tokens = tokens + (elapsed_ns / interval_ns) * rate
end
if tokens > capacity then
  tokens = capacity
end
if tokens < 0 or tokens ~= tokens then
  tokens = 0
end

local allowed = 0
local retry_ns = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
elseif rate > 0 and interval_ns > 0 then
  retry_ns = math.floor((1 - tokens) * interval_ns / rate + 0.5)
end

local remaining = math.floor(tokens)
if remaining < 0 then
  remaining = 0
end
if ttl_ms < 1 then
  ttl_ms = 1
end

redis.call("HSET", key, "tokens", string.format("%.17g", tokens), "last", string.format("%.0f", now_ms))
redis.call("PEXPIRE", key, ttl_ms)
return {allowed, remaining, retry_ns}
`

var bucketScript = redis.NewScript(bucketLua)

// RedisConfig selects the Redis server used by Redis.
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
	Timeout  time.Duration
	// Prefix namespaces bucket keys. The default is "rl:".
	Prefix string
}

// Redis stores one hash per client and updates it in a single script.
type Redis struct {
	client  *redis.Client
	timeout time.Duration
	prefix  string
}

// NewRedis pings Redis before returning. The server uses this constructor and never passes a test clock.
func NewRedis(cfg RedisConfig) (*Redis, error) {
	store, err := openRedis(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), store.timeout)
	defer cancel()
	if err := store.client.Ping(ctx).Err(); err != nil {
		_ = store.client.Close()
		return nil, fmt.Errorf("redis: %w", err)
	}
	return store, nil
}

func openRedis(cfg RedisConfig) (*Redis, error) {
	if cfg.Addr == "" {
		return nil, fmt.Errorf("redis address is empty")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultRedisTimeout
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "rl:"
	}
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.Timeout,
		ReadTimeout:  cfg.Timeout,
		WriteTimeout: cfg.Timeout,
		MaxRetries:   -1,
	})
	return &Redis{client: client, timeout: cfg.Timeout, prefix: cfg.Prefix}, nil
}

// Close releases the Redis connection.
func (r *Redis) Close() error {
	return r.client.Close()
}

// Ping checks that Redis answers within the configured timeout.
func (r *Redis) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	if err := r.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	return nil
}

// Allow applies the policy to key. It takes the clock from Redis and bounds the call by the configured timeout.
func (r *Redis) Allow(ctx context.Context, key string, policy limiter.Policy) (limiter.Decision, error) {
	return r.allow(ctx, key, policy, nil)
}

// allowAt is for tests that replay the canonical rows with a chosen clock.
// Allow does not call it, so the server cannot send its own time.
func (r *Redis) allowAt(ctx context.Context, key string, policy limiter.Policy, now time.Time) (limiter.Decision, error) {
	return r.allow(ctx, key, policy, &now)
}

func (r *Redis) allow(ctx context.Context, key string, policy limiter.Policy, now *time.Time) (limiter.Decision, error) {
	if err := ctx.Err(); err != nil {
		return limiter.Decision{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	args := []any{
		policy.Capacity,
		policy.RefillRate,
		policy.RefillInterval.Nanoseconds(),
		bucketTTL(policy).Milliseconds(),
	}
	if now != nil {
		args = append(args, now.UnixMilli())
	}
	raw, err := bucketScript.Run(ctx, r.client, []string{r.prefix + key}, args...).Result()
	if err != nil {
		return limiter.Decision{}, err
	}
	return parseScriptResult(raw)
}

// bucketTTL is how long an idle bucket stays stored.
// It is the time to refill from empty plus one refill interval.
func bucketTTL(policy limiter.Policy) time.Duration {
	if policy.RefillRate <= 0 || policy.RefillInterval <= 0 {
		return time.Millisecond
	}
	ttl := time.Duration(float64(policy.RefillInterval)*policy.Capacity/policy.RefillRate) + policy.RefillInterval
	if ttl < time.Millisecond {
		return time.Millisecond
	}
	return ttl
}

func parseScriptResult(raw any) (limiter.Decision, error) {
	values, ok := raw.([]any)
	if !ok || len(values) != 3 {
		return limiter.Decision{}, fmt.Errorf("redis script returned %T", raw)
	}
	allowed, err := asInt64(values[0])
	if err != nil {
		return limiter.Decision{}, err
	}
	remaining, err := asInt64(values[1])
	if err != nil {
		return limiter.Decision{}, err
	}
	retryNs, err := asInt64(values[2])
	if err != nil {
		return limiter.Decision{}, err
	}
	if remaining < 0 {
		remaining = 0
	}
	return limiter.Decision{
		Allowed:    allowed == 1,
		Remaining:  int(remaining),
		RetryAfter: time.Duration(retryNs),
	}, nil
}

func asInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case float64:
		return int64(typed), nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("redis script returned %T", value)
	}
}

var _ Store = (*Redis)(nil)
