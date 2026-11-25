# Load evaluation

Each run uses a fresh range of client addresses, so every bucket starts full. The expected accepts for one client are `capacity + refill rate * span / interval`, where span is the time from that client's first request to its last response. The run passes when every client is within `max(3, 15% of expected)` and the error rate is zero.

These are two samples on one machine. They are not a ranking of the memory and Redis backends.

## Environment

- Date: 2026-11-25
- Machine: Linux 6.18.33.2-microsoft-standard-WSL2, x86_64, 12 CPUs
- Go: go1.27.1 linux/amd64
- Redis: 7.4.2 on 127.0.0.1:16379, persistence off
- Server policy: capacity 10, refill 100 per second
- Load: 4 clients, 400 requests each, 16 workers
- The server trusted `127.0.0.1` and `::1`, and the load generator sent each client in `X-Forwarded-For`

Start the server with:

```bash
SERVER_PORT=8080 STORAGE_BACKEND=memory \
  RATE_LIMIT_CAPACITY=10 RATE_LIMIT_REFILL_RATE=100 RATE_LIMIT_REFILL_INTERVAL=1s \
  TRUSTED_PROXIES=127.0.0.1,::1 \
  go run ./cmd/server
```

For Redis, set `STORAGE_BACKEND=redis` and `REDIS_ADDR`. Repeat a run with a new `-base` so the clients do not reuse an existing bucket.

```bash
go run ./cmd/loadgen -label memory-run-1 -url http://127.0.0.1:8080/api/resource \
  -base 10 -requests 400 -concurrency 16 -capacity 10 -rate 100 -interval 1s
```

## Memory

| Run | Accepted | Rejected | Errors | Average | p50 | p95 | p99 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | 68 | 1532 | 0 | 720µs | 440µs | 2.511ms | 4.520ms |
| 2 | 69 | 1531 | 0 | 737µs | 429µs | 2.488ms | 5.466ms |

Per client, run 1 accepted 17 against an expected 17.8 over about 78ms. Run 2 accepted 17 or 18 against an expected 18.0–18.2 over about 80ms. Both runs were inside the tolerance.

## Redis

| Run | Accepted | Rejected | Errors | Average | p50 | p95 | p99 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | 97 | 1503 | 0 | 1.577ms | 1.193ms | 3.599ms | 9.122ms |
| 2 | 99 | 1501 | 0 | 1.494ms | 1.234ms | 3.051ms | 6.094ms |

Per client, run 1 accepted 24 or 25 against an expected 26.2–26.3 over about 163ms. Run 2 accepted 24 or 25 against an expected 25.5–25.6 over about 155ms. Both runs were inside the tolerance. The Redis runs took longer, so more tokens refilled. That is why they accepted more requests than the memory runs.
