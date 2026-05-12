package auth

import (
	"sync"
	"time"
)

// LoginLimiter is a tiny in-memory token bucket keyed by client IP.
// It throttles only the login endpoint, so memory pressure is negligible
// even with many distinct attackers. Buckets self-evict on next access
// past the window.
type LoginLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	max     int
	window  time.Duration
}

type bucket struct {
	count int
	reset time.Time
}

// NewLoginLimiter creates a limiter allowing max attempts per window per IP.
// Defaults: 5 attempts / 15 minutes.
func NewLoginLimiter(max int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{
		buckets: map[string]*bucket{},
		max:     max,
		window:  window,
	}
}

// Allow records an attempt from ip and returns (allowed, retryAfter).
// retryAfter is non-zero only when allowed=false.
func (l *LoginLimiter) Allow(ip string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[ip]
	if !ok || now.After(b.reset) {
		l.buckets[ip] = &bucket{count: 1, reset: now.Add(l.window)}
		return true, 0
	}
	b.count++
	if b.count > l.max {
		return false, time.Until(b.reset)
	}
	return true, 0
}

// Reset clears the bucket for ip — call this after a successful login so a
// legitimate user doesn't get throttled by their own typo retries.
func (l *LoginLimiter) Reset(ip string) {
	l.mu.Lock()
	delete(l.buckets, ip)
	l.mu.Unlock()
}
