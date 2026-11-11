// Package storage defines the rate-limit store shared by the memory and Redis backends.
package storage

import (
	"context"

	"github.com/Lazzar19/distributed-rate-limiter/internal/limiter"
)

// Store decides whether one client key may proceed and updates that client's bucket.
//
// Allowed false with a nil error means the bucket has fewer than one token.
// A non-nil error means the store could not make a decision.
type Store interface {
	Allow(ctx context.Context, key string, policy limiter.Policy) (limiter.Decision, error)
}
