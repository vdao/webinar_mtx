package auth

import (
	"sync"
	"time"
)

// Limiter is a small in-memory rate limiter keyed by arbitrary strings
// (e.g. "ip|email"). Enough for a single-instance app; no external state.
type Limiter struct {
	mu     sync.Mutex
	events map[string][]time.Time // sliding window
	last   map[string]time.Time   // minimal interval
	limit  int
	window time.Duration
}

// NewLimiter allows max events per window per key.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{
		events: map[string][]time.Time{},
		last:   map[string]time.Time{},
		limit:  max,
		window: window,
	}
}

// Allow records an event for key and reports whether it fits the window.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	events := l.events[key]
	kept := events[:0]
	for _, t := range events {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.events[key] = kept
		return false
	}
	l.events[key] = append(kept, now)
	return true
}

// AllowAtMostOncePer enforces a minimal interval between events per key.
func (l *Limiter) AllowAtMostOncePer(key string, minInterval time.Duration) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.last[key]; ok && now.Sub(t) < minInterval {
		return false
	}
	l.last[key] = now
	return true
}
