package auth

import (
	"sync"
	"time"
)

// RateLimiter guards the login endpoint two ways (H2):
//   - per-IP token bucket: caps request volume from a single source, but a
//     shared office NAT means many humans share one IP — not sufficient alone.
//   - per-account failure counter: locks out repeated wrong-password attempts
//     against one username regardless of which IP they come from.
// Both are in-memory (volatile across restarts) — acceptable for a small
// team login endpoint; no persistence requirement.
type RateLimiter struct {
	mu       sync.RWMutex
	buckets  map[string]*tokenBucket
	failures map[string]*failureCounter
}

const (
	bucketCapacity  = 5
	bucketRefill    = 30 * time.Second // 1 token per 30s
	maxAccountFails = 5
	lockoutWindow   = 5 * time.Minute
	gcAge           = time.Hour
)

type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

type failureCounter struct {
	count      int
	lockedUnil time.Time
	lastSeen   time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		buckets:  make(map[string]*tokenBucket),
		failures: make(map[string]*failureCounter),
	}
}

// AllowIP reports whether ip may attempt another login right now, consuming
// one token if so.
func (rl *RateLimiter) AllowIP(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok {
		b = &tokenBucket{tokens: bucketCapacity, lastRefill: now}
		rl.buckets[ip] = b
	}
	elapsed := now.Sub(b.lastRefill)
	refill := elapsed.Seconds() / bucketRefill.Seconds()
	if refill > 0 {
		b.tokens += refill
		if b.tokens > bucketCapacity {
			b.tokens = bucketCapacity
		}
		b.lastRefill = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// AllowAccount reports whether username may attempt another login (not
// currently locked out from prior failures).
func (rl *RateLimiter) AllowAccount(username string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	f, ok := rl.failures[username]
	if !ok {
		return true
	}
	return !time.Now().Before(f.lockedUnil)
}

// RecordFailure increments username's failure count; once it reaches
// maxAccountFails, the account is locked for lockoutWindow regardless of IP.
func (rl *RateLimiter) RecordFailure(username string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	f, ok := rl.failures[username]
	if !ok {
		f = &failureCounter{}
		rl.failures[username] = f
	}
	f.count++
	f.lastSeen = now
	if f.count >= maxAccountFails {
		f.lockedUnil = now.Add(lockoutWindow)
		f.count = 0
	}
}

// ResetAccount clears failure state after a successful login.
func (rl *RateLimiter) ResetAccount(username string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.failures, username)
}

// GC drops idle bucket/failure entries older than gcAge to bound memory
// growth. Call periodically from a background goroutine.
func (rl *RateLimiter) GC() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for ip, b := range rl.buckets {
		if now.Sub(b.lastRefill) > gcAge {
			delete(rl.buckets, ip)
		}
	}
	for user, f := range rl.failures {
		if now.Sub(f.lastSeen) > gcAge && now.After(f.lockedUnil) {
			delete(rl.failures, user)
		}
	}
}
