package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimit is a tiny per-IP token bucket. Defaults: 5 attempts per minute.
// Suitable only for low-traffic admin endpoints (login).
type RateLimit struct {
	max     int
	window  time.Duration
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	count int
	reset time.Time
}

// NewRateLimit returns a limiter that allows max requests per window per IP.
func NewRateLimit(max int, window time.Duration) *RateLimit {
	return &RateLimit{
		max:     max,
		window:  window,
		buckets: make(map[string]*bucket),
	}
}

// Allow returns true if the IP has remaining quota and consumes one slot.
func (rl *RateLimit) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok || now.After(b.reset) {
		rl.buckets[ip] = &bucket{count: 1, reset: now.Add(rl.window)}
		return true
	}
	if b.count >= rl.max {
		return false
	}
	b.count++
	return true
}

// Middleware wraps a handler enforcing the limit on its IP.
func (rl *RateLimit) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only enforce on POST (login submission). GET (form display) is free.
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		ip := ClientIP(r)
		if !rl.Allow(ip) {
			http.Error(w, "Too many attempts. Try again in a minute.", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP extracts the best-guess client IP, honouring X-Forwarded-For when
// present (we sit behind nginx in production).
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry is the original client.
		for i, c := range xff {
			if c == ',' {
				return trimSpace(xff[:i])
			}
		}
		return trimSpace(xff)
	}
	if rip := r.Header.Get("X-Real-IP"); rip != "" {
		return trimSpace(rip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
