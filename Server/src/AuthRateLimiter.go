package main

import (
	"sync"
	"time"
)

type attemptRecord struct {
	failures    int
	firstFailed time.Time
	lastFailed  time.Time
	blockedTill time.Time
}

type AuthRateLimiter struct {
	mu              sync.RWMutex
	attempts        map[string]*attemptRecord
	maxAttempts     int
	windowDuration  time.Duration
	lockoutDuration time.Duration
	stopCleanup     chan struct{}
	stopOnce        sync.Once
}

func NewAuthRateLimiter(maxAttempts int, windowDuration, lockoutDuration time.Duration) *AuthRateLimiter {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if windowDuration <= 0 {
		windowDuration = 1 * time.Minute
	}
	if lockoutDuration <= 0 {
		lockoutDuration = 5 * time.Minute
	}
	rl := &AuthRateLimiter{
		attempts:        make(map[string]*attemptRecord),
		maxAttempts:     maxAttempts,
		windowDuration:  windowDuration,
		lockoutDuration: lockoutDuration,
		stopCleanup:     make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *AuthRateLimiter) IsBlocked(key string) (bool, time.Duration) {
	if rl == nil || key == "" {
		return false, 0
	}
	rl.mu.RLock()
	rec, ok := rl.attempts[key]
	if !ok || rec == nil {
		rl.mu.RUnlock()
		return false, 0
	}
	now := time.Now()
	if rec.blockedTill.After(now) {
		remaining := rec.blockedTill.Sub(now)
		rl.mu.RUnlock()
		return true, remaining
	}
	rl.mu.RUnlock()
	return false, 0
}

func (rl *AuthRateLimiter) RecordFailure(key string) (bool, int, time.Duration) {
	if rl == nil || key == "" {
		return false, 0, 0
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rec, ok := rl.attempts[key]
	if !ok || rec == nil {
		rec = &attemptRecord{
			failures:    1,
			firstFailed: now,
			lastFailed:  now,
		}
		rl.attempts[key] = rec
		return false, rl.maxAttempts - 1, 0
	}

	// If already locked out and still within lockout period
	if rec.blockedTill.After(now) {
		return true, 0, rec.blockedTill.Sub(now)
	}

	// If lockout expired or if the failure window has passed, reset failure counter
	if (!rec.blockedTill.IsZero() && !rec.blockedTill.After(now)) || now.Sub(rec.firstFailed) > rl.windowDuration {
		rec.failures = 1
		rec.firstFailed = now
		rec.lastFailed = now
		rec.blockedTill = time.Time{}
		return false, rl.maxAttempts - 1, 0
	}

	rec.failures++
	rec.lastFailed = now

	if rec.failures >= rl.maxAttempts {
		rec.blockedTill = now.Add(rl.lockoutDuration)
		return true, 0, rl.lockoutDuration
	}

	return false, rl.maxAttempts - rec.failures, 0
}

// CheckAndRecord evaluates whether an attempt for the given key is allowed under the rate limit.
// If the key is currently blocked or if the new attempt exceeds maxAttempts within windowDuration,
// it records the attempt, triggers lockout, and returns (false, remainingLockout).
// Otherwise, it increments the attempt count and returns (true, 0).
func (rl *AuthRateLimiter) CheckAndRecord(key string) (bool, time.Duration) {
	if rl == nil || key == "" {
		return true, 0
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rec, ok := rl.attempts[key]
	if !ok || rec == nil {
		rec = &attemptRecord{
			failures:    1,
			firstFailed: now,
			lastFailed:  now,
		}
		rl.attempts[key] = rec
		if rl.maxAttempts <= 0 {
			rec.blockedTill = now.Add(rl.lockoutDuration)
			return false, rl.lockoutDuration
		}
		return true, 0
	}

	// If already locked out and still within lockout period
	if rec.blockedTill.After(now) {
		return false, rec.blockedTill.Sub(now)
	}

	// If lockout expired or if the window duration has passed, reset counter
	if (!rec.blockedTill.IsZero() && !rec.blockedTill.After(now)) || now.Sub(rec.firstFailed) > rl.windowDuration {
		rec.failures = 1
		rec.firstFailed = now
		rec.lastFailed = now
		rec.blockedTill = time.Time{}
		return true, 0
	}

	rec.failures++
	rec.lastFailed = now

	if rec.failures > rl.maxAttempts {
		rec.blockedTill = now.Add(rl.lockoutDuration)
		return false, rl.lockoutDuration
	}

	return true, 0
}

func (rl *AuthRateLimiter) RecordSuccess(key string) {
	if rl == nil || key == "" {
		return
	}
	rl.mu.Lock()
	delete(rl.attempts, key)
	rl.mu.Unlock()
}

func (rl *AuthRateLimiter) Reset(key string) {
	rl.RecordSuccess(key)
}

func (rl *AuthRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for k, rec := range rl.attempts {
				if rec == nil {
					delete(rl.attempts, k)
					continue
				}
				if !rec.blockedTill.After(now) && now.Sub(rec.lastFailed) > rl.lockoutDuration {
					delete(rl.attempts, k)
				}
			}
			rl.mu.Unlock()
		case <-rl.stopCleanup:
			return
		}
	}
}

func (rl *AuthRateLimiter) Close() {
	if rl == nil {
		return
	}
	rl.stopOnce.Do(func() {
		close(rl.stopCleanup)
	})
}
