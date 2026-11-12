package routing

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// RetryConfig configures exponential backoff with jitter and retry limits.
type RetryConfig struct {
	MaxAttempts    int           `json:"max_attempts"`
	BaseDelay      time.Duration `json:"base_delay"`
	MaxDelay       time.Duration `json:"max_delay"`
	JitterFraction float64       `json:"jitter_fraction"` // e.g., 0.25 for ±25% or full jitter
}

// DefaultRetryConfig returns sensible production defaults for retries.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:    3,
		BaseDelay:      50 * time.Millisecond,
		MaxDelay:       2 * time.Second,
		JitterFraction: 0.25,
	}
}

// Sleeper defines an interface for waiting, enabling deterministic test mocks.
type Sleeper interface {
	Sleep(d time.Duration)
}

type realSleeper struct{}

func (realSleeper) Sleep(d time.Duration) {
	time.Sleep(d)
}

// Backoff computes exponential backoff with bounded delay and jitter.
type Backoff struct {
	cfg RetryConfig
	rng *rand.Rand
	mu  sync.Mutex
}

// NewBackoff creates a Backoff calculator with the given config.
func NewBackoff(cfg RetryConfig) *Backoff {
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 50 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 2 * time.Second
	}
	if cfg.MaxDelay < cfg.BaseDelay {
		cfg.MaxDelay = cfg.BaseDelay
	}
	if cfg.JitterFraction <= 0 || cfg.JitterFraction > 1.0 {
		cfg.JitterFraction = 0.25
	}
	return &Backoff{
		cfg: cfg,
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Delay computes the wait duration for a zero-indexed attempt (0 for 1st retry, 1 for 2nd, etc.).
// If retryAfter > 0, it bounds or respects retryAfter up to maxDelay.
func (b *Backoff) Delay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > b.cfg.MaxDelay {
			return b.cfg.MaxDelay
		}
		return retryAfter
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// 2^attempt
	mult := math.Pow(2, float64(attempt))
	delaySec := b.cfg.BaseDelay.Seconds() * mult
	if delaySec > b.cfg.MaxDelay.Seconds() {
		delaySec = b.cfg.MaxDelay.Seconds()
	}

	// Apply jitter: ±(delaySec * jitterFraction * rand)
	if b.cfg.JitterFraction > 0 {
		jitter := (b.rng.Float64()*2 - 1) * (delaySec * b.cfg.JitterFraction)
		delaySec += jitter
		if delaySec < 0 {
			delaySec = 0
		}
	}

	dur := time.Duration(delaySec * float64(time.Second))
	if dur > b.cfg.MaxDelay {
		dur = b.cfg.MaxDelay
	}
	return dur
}
