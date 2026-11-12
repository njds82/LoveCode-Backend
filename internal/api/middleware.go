package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/lovecode/lovecode/internal/audit"
	"github.com/lovecode/lovecode/internal/security"
)

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyPrincipal
)

func RequestID(ctx context.Context) string { s, _ := ctx.Value(keyRequestID).(string); return s }

func PrincipalFrom(ctx context.Context) *security.Principal {
	p, _ := ctx.Value(keyPrincipal).(*security.Principal)
	return p
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// statusRecorder captures the status code. Unwrap lets http.ResponseController
// reach the underlying writer for Flush (needed by SSE in Phase 2).
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(c int)           { s.status = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID() // never trust a client-supplied ID for log/audit correlation
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyRequestID, id)))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				s.log.Error("handler panic", "request_id", RequestID(r.Context()), "panic", p, "stack", string(debug.Stack()))
				writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// Path only, never the query string: queries may carry secrets.
		s.log.Info("request", "request_id", RequestID(r.Context()), "method", r.Method,
			"path", r.URL.Path, "status", rec.status, "dur_ms", time.Since(start).Milliseconds())
	})
}

func remoteIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// localOnly rejects non-loopback peers in local mode (defence in depth on top of
// the loopback bind validated in config).
func (s *Server) localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.localMode {
			if ip := net.ParseIP(remoteIP(r)); ip == nil || !ip.IsLoopback() {
				writeError(w, r, http.StatusForbidden, "forbidden", "local-only mode")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(remoteIP(r), time.Now()) {
			w.Header().Set("Retry-After", "1")
			writeError(w, r, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, s.maxBody)
		next.ServeHTTP(w, r)
	})
}

// protect enforces authentication and (when scope != "") authorization.
func (s *Server) protect(scope string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		secret, ok := strings.CutPrefix(authz, "Bearer ")
		if !ok || secret == "" {
			s.authFailed(r, "missing bearer token")
			w.Header().Set("WWW-Authenticate", `Bearer realm="lovecode"`)
			writeError(w, r, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		p, err := s.tokens.Verify(r.Context(), secret)
		if err != nil {
			s.authFailed(r, "invalid token")
			writeError(w, r, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if scope != "" && !p.Allows(scope) {
			_ = s.audit.Append(r.Context(), audit.Event{Actor: p.Name, Action: "auth.denied", Target: r.URL.Path,
				CorrelationID: RequestID(r.Context()), Detail: map[string]any{"required_scope": scope}})
			writeError(w, r, http.StatusForbidden, "forbidden", "insufficient scope")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), keyPrincipal, p)))
	})
}

func (s *Server) authFailed(r *http.Request, reason string) {
	// Bounded by the rate limiter, which runs before authentication.
	if err := s.audit.Append(r.Context(), audit.Event{Actor: "anonymous", Action: "auth.failure", Target: r.URL.Path,
		CorrelationID: RequestID(r.Context()), Detail: map[string]any{"reason": reason, "ip": remoteIP(r)}}); err != nil {
		s.log.Error("audit append failed", "err", err)
	}
}

// limiter is a per-key token bucket with a hard cap on tracked keys so memory
// stays bounded even under a flood of distinct source addresses.
type limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	maxKeys int
	m       map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(rate float64, burst int) *limiter {
	return &limiter{rate: rate, burst: float64(burst), maxKeys: 10000, m: make(map[string]*bucket)}
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.m[key]
	if !ok {
		if len(l.m) >= l.maxKeys {
			l.evictLocked(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.m[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *limiter) evictLocked(now time.Time) {
	for k, b := range l.m {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.m, k)
		}
	}
	for k := range l.m { // still full: drop arbitrary entries to honour the cap
		if len(l.m) < l.maxKeys {
			break
		}
		delete(l.m, k)
	}
}
