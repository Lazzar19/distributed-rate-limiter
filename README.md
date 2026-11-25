# Distributed Rate Limiter

A Go service that limits how often each client can call an HTTP API. Every client has its own token bucket. Every bucket uses one policy. With the Redis backend, multiple processes share those buckets.

## Architecture

```
HTTP request
    -> middleware (client identity, status codes, headers)
        -> Store.Allow
            -> memory: Advance under that client's lock
            -> redis: one Lua script, clock from Redis TIME
```

| Package | Responsibility |
| --- | --- |
| `internal/limiter` | Policy, bucket state, and the pure `Advance` function |
| `internal/storage` | `Allow` for the in-memory map and for Redis |
| `internal/middleware` | Client address, 200 / 429 / 503, rate-limit headers |
| `internal/api` | `GET /health` and the sample `GET /api/resource` |
| `cmd/server` | Load configuration, choose the store, listen on `0.0.0.0` |
| `cmd/loadgen` | Send a fixed set of clients and compare accepts with the formula |

`Advance` does not import HTTP or Redis. The memory store calls it. The Lua script is a second copy of the same steps. The HTTP handlers do not know which store is in use.

## Token bucket

A bucket stores a token count, which may be fractional, and the time of the last update. A new bucket starts full. There is no per-client timer. Each decision refills from elapsed time:

```
elapsed = max(0, now - lastRefill)
tokens  = min(capacity, tokens + elapsed * refillRate / refillInterval)

if tokens >= 1:
    allow
    tokens = tokens - 1
else:
    deny
    retryAfter = (1 - tokens) * refillInterval / refillRate

lastRefill = now
```

Fractional tokens are the unused remainder, so `lastRefill` moves forward even when the request is denied. A clock step backward adds no tokens. The count stays inside `0…capacity`.

The sample policy is a burst of 10 and a sustained rate of 2 requests per second. Ten requests can proceed immediately. The next one waits 0.5s. `Retry-After` is that delay rounded up to whole seconds, so the header is `1`. At 0.4s the bucket has 0.8 tokens and the request is still denied. At 0.5s one token is available and the request is allowed.

`X-RateLimit-Limit` is the capacity. `X-RateLimit-Remaining` is the whole tokens left after the decision. Those are different numbers on purpose: capacity 10 and a remaining count of 9 cannot be swapped without a test failing.

## HTTP API

The process listens on `0.0.0.0:$SERVER_PORT`.

| Route | Behavior |
| --- | --- |
| `GET /health` | 200 when the process is up. It does not check Redis or consume a token. |
| `GET /api/resource` | Sample route behind the limiter. |

| Result | HTTP | Headers |
| --- | --- | --- |
| Token consumed | 200 | `X-RateLimit-Limit`, `X-RateLimit-Remaining` |
| Fewer than one token | 429 | those headers plus `Retry-After` |
| Store error, fail-closed | 503 | no rate-limit headers; the error is logged |
| Store error, fail-open | 200 | no rate-limit headers; the error is logged |

429 means the bucket is empty. A Redis failure is never reported as 429. The default is fail-closed, so an outage does not silently turn the limit off. Fail-open is available when the protected API should keep serving if the limiter cannot be reached.

A token stays consumed if the client disconnects after Redis has stored the update. A retry is a new request and costs another token.

## Client identity

The client is the host in `RemoteAddr`, without the port. IPv4 and IPv6 are both accepted.

`X-Forwarded-For` is ignored unless the direct peer is listed in `TRUSTED_PROXIES`. Addresses may be IPs or CIDR blocks. When the peer is trusted, the middleware walks `X-Forwarded-For` from right to left, skips other trusted proxies, and uses the first remaining address. If none remains, it uses the peer. `X-Real-IP` is not a client identity.

API keys are not accepted. A new string would be a new bucket and would bypass the limit.

## Storage

The memory backend keeps one bucket per client. One mutex covers map lookup and insert. Each bucket has its own mutex, held across refill and save.

The Redis backend stores each client as one hash (`tokens` and `last`) under the prefix `rl:`. Refill and consumption run in one Lua script. The script reads Redis `TIME`, so two machines with different clocks still agree. A missing key, or a timestamp that cannot be read, starts a full bucket. It is not treated as timestamp zero.

The key TTL is the time to refill from empty plus one refill interval:

```
ttl = capacity * refillInterval / refillRate + refillInterval
```

For the sample policy that is 6s. After that idle period the client would have been full anyway. A shorter TTL would hand the burst back early. Restarting Redis drops buckets unless persistence is enabled. This project does not enable persistence.

All instances in one deployment must load the same capacity, refill rate, and refill interval. Each update still clamps the stored count to capacity.

## Configuration

Unset values use the defaults below. A variable that is set to an invalid value stops the process before it serves traffic. `REDIS_ADDR` is required when the backend is `redis`.

```
SERVER_PORT=8080
STORAGE_BACKEND=memory
LIMITER_ON_ERROR=fail_closed

RATE_LIMIT_CAPACITY=10
RATE_LIMIT_REFILL_RATE=2
RATE_LIMIT_REFILL_INTERVAL=1s

TRUSTED_PROXIES=

REDIS_ADDR=127.0.0.1:6379
REDIS_PASSWORD=
REDIS_DB=0
```

`STORAGE_BACKEND` is `memory` or `redis`. `LIMITER_ON_ERROR` is `fail_closed` or `fail_open`.

```bash
go run ./cmd/server
```

With Redis:

```bash
STORAGE_BACKEND=redis REDIS_ADDR=127.0.0.1:6379 go run ./cmd/server
```

Inside a container, `REDIS_ADDR` must be the Redis service name, not `localhost`. `localhost` is only for a server started on the host.

## Tests

```bash
go test ./...
```

Redis integration tests and the two-process test are skipped unless `REDIS_ADDR` is set:

```bash
REDIS_ADDR=127.0.0.1:6379 go test ./internal/storage/ ./tests/integration/ -count=1
```

The two-process test builds the server, starts two processes against the same Redis, and checks that 10 requests spread across both are accepted and the 11th is denied.

## Load evaluation

`cmd/loadgen` sends concurrent requests from a fixed set of client addresses and compares the accepted count with `capacity + refill over the run`. Use a new `-base` for each run so the buckets start full. The recorded samples, the machine, and the commands are in [docs/load-test.md](docs/load-test.md). Two runs are not a ranking of the backends.
