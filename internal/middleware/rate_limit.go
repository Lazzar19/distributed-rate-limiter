// Package middleware limits HTTP requests by client address.
package middleware

import (
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Lazzar19/distributed-rate-limiter/internal/limiter"
	"github.com/Lazzar19/distributed-rate-limiter/internal/storage"
)

// Logger records store failures. *log.Logger implements it.
type Logger interface {
	Printf(format string, args ...any)
}

// Limiter checks one shared policy before the next handler runs.
type Limiter struct {
	store    storage.Store
	policy   limiter.Policy
	failOpen bool
	proxies  proxySet
	logger   Logger
}

// New returns middleware for store.
// failOpen allows the request when the store returns an error. The default is to reject it.
// proxies are the trusted proxy addresses and CIDR blocks from configuration.
func New(store storage.Store, policy limiter.Policy, failOpen bool, proxies []string, logger Logger) *Limiter {
	if logger == nil {
		logger = log.Default()
	}
	return &Limiter{
		store:    store,
		policy:   policy,
		failOpen: failOpen,
		proxies:  newProxySet(proxies),
		logger:   logger,
	}
}

// Wrap limits requests to next. It does not refill buckets itself.
func (l *Limiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientKey(r.RemoteAddr, r.Header.Get("X-Forwarded-For"), l.proxies)
		decision, err := l.store.Allow(r.Context(), key, l.policy)
		if err != nil {
			l.logger.Printf("rate limit store error: %v", err)
			if l.failOpen {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "rate limiter unavailable", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("X-RateLimit-Limit", strconv.FormatFloat(l.policy.Capacity, 'f', -1, 64))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(decision.Remaining))
		if decision.Allowed {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(decision.RetryAfter)))
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
	})
}

func retryAfterSeconds(wait time.Duration) int {
	seconds := math.Ceil(wait.Seconds())
	if seconds < 0 {
		return 0
	}
	return int(seconds)
}
