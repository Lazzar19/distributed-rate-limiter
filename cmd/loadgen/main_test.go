package main

import (
	"math"
	"testing"
	"time"
)

func TestExpectedAccepts(t *testing.T) {
	got := expectedAccepts(10, 20, time.Second, 500*time.Millisecond)
	if math.Abs(got-20) > 1e-9 {
		t.Fatalf("expected = %v, want 20", got)
	}
	if got := expectedAccepts(10, 20, time.Second, 0); got != 10 {
		t.Fatalf("zero span = %v, want 10", got)
	}
}
