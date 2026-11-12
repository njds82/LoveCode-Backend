package routing

import (
	"errors"
	"sync"
	"time"

	"github.com/lovecode/lovecode/internal/providers"
)

// RateLimiter tracks provider-specific 429 rate limit events, cooldowns, and remaining quota hints.
type RateLimiter struct {
	mu        sync.RWMutex
	cooldowns map[string]time.Time         // provider_id -> cooldown_until
	limits    map[string]ProviderRateLimit // provider_id -> observed info
}

// ProviderRateLimit holds rate-limit status observed from response headers or 429s.
type ProviderRateLimit struct {
	ProviderID        string        `json:"provider_id"`
	CooldownUntil     time.Time     `json:"cooldown_until,omitempty"`
	RequestsRemaining *int          `json:"requests_remaining,omitempty"`
	TokensRemaining   *int          `json:"tokens_remaining,omitempty"`
	ResetDuration     time.Duration `json:"reset_duration,omitempty"`
	Last429At         time.Time     `json:"last_429_at,omitempty"`
}

// NewRateLimiter initializes a RateLimiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		cooldowns: make(map[string]time.Time),
		limits:    make(map[string]ProviderRateLimit),
	}
}

// RecordRateLimit records a 429 or rate limit event for a provider with optional retryAfter duration.
func (rl *RateLimiter) RecordRateLimit(providerID string, retryAfter time.Duration, now time.Time) {
	if providerID == "" {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	if retryAfter <= 0 {
		retryAfter = 5 * time.Second // default cooldown if not specified
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	until := now.Add(retryAfter)
	rl.cooldowns[providerID] = until

	info := rl.limits[providerID]
	info.ProviderID = providerID
	info.CooldownUntil = until
	info.Last429At = now
	info.ResetDuration = retryAfter
	rl.limits[providerID] = info
}

// RecordHeaders parses provider rate limit headers from response metadata if available.
func (rl *RateLimiter) RecordHeaders(providerID string, metadata map[string]string) {
	if providerID == "" || len(metadata) == 0 {
		return
	}
	// Note: We only record what the provider explicitly returned; we never invent remaining quota.
	// Common headers: x-ratelimit-remaining-requests, x-ratelimit-remaining-tokens
	rl.mu.Lock()
	defer rl.mu.Unlock()

	info := rl.limits[providerID]
	info.ProviderID = providerID

	if v, ok := metadata["x-ratelimit-remaining-requests"]; ok {
		var reqs int
		if _, err := parsePositiveInt(v, &reqs); err == nil {
			info.RequestsRemaining = &reqs
		}
	}
	if v, ok := metadata["x-ratelimit-remaining-tokens"]; ok {
		var toks int
		if _, err := parsePositiveInt(v, &toks); err == nil {
			info.TokensRemaining = &toks
		}
	}
	rl.limits[providerID] = info
}

func parsePositiveInt(s string, out *int) (bool, error) {
	var val int
	for _, c := range s {
		if c < '0' || c > '9' {
			return false, nil
		}
		val = val*10 + int(c-'0')
	}
	*out = val
	return true, nil
}

// IsCoolingDown checks if a provider is currently under an active rate-limit cooldown.
func (rl *RateLimiter) IsCoolingDown(providerID string, now time.Time) (bool, time.Duration) {
	if providerID == "" {
		return false, 0
	}
	if now.IsZero() {
		now = time.Now()
	}

	rl.mu.RLock()
	defer rl.mu.RUnlock()

	until, ok := rl.cooldowns[providerID]
	if !ok || now.After(until) {
		return false, 0
	}
	return true, until.Sub(now)
}

// Status returns a snapshot of all tracked provider rate limits.
func (rl *RateLimiter) Status() []ProviderRateLimit {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	out := make([]ProviderRateLimit, 0, len(rl.limits))
	for _, lim := range rl.limits {
		out = append(out, lim)
	}
	return out
}

// ErrorRetryAfter extracts RetryAfter duration from error if available.
func ErrorRetryAfter(err error) time.Duration {
	if err != nil {
		var pErr *providers.Error
		if errors.As(err, &pErr) {
			return pErr.RetryAfter
		}
	}
	return 0
}
