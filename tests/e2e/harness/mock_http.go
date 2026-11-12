package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// MockHTTPRequestRecord logs requests received by the mock HTTP server.
type MockHTTPRequestRecord struct {
	Method  string
	Path    string
	Headers http.Header
	Body    string
}

// MockHTTPServer provides endpoints for network tool testing.
type MockHTTPServer struct {
	Server   *httptest.Server
	URL      string
	Requests []MockHTTPRequestRecord
	mu       sync.Mutex
	closed   bool
}

// NewMockHTTPServer starts a new mock HTTP server.
func NewMockHTTPServer(t testing.TB) *MockHTTPServer {
	t.Helper()

	s := &MockHTTPServer{}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /get", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"method":  "GET",
			"headers": r.Header,
		})
	})

	mux.HandleFunc("POST /post", func(w http.ResponseWriter, r *http.Request) {
		rec := s.record(r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"echo":   rec.Body,
		})
	})

	mux.HandleFunc("GET /delay", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		msStr := r.URL.Query().Get("ms")
		ms, _ := strconv.Atoi(msStr)
		if ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"delayed_ok"}`))
	})

	mux.HandleFunc("GET /large", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		sizeStr := r.URL.Query().Get("size")
		size, _ := strconv.Atoi(sizeStr)
		if size <= 0 {
			size = 1024
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		buf := bytes.Repeat([]byte("A"), size)
		_, _ = w.Write(buf)
	})

	mux.HandleFunc("GET /redirect", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		hopsStr := r.URL.Query().Get("hops")
		hops, _ := strconv.Atoi(hopsStr)
		target := r.URL.Query().Get("target")
		if target == "" {
			target = "/get"
		}

		if hops > 1 {
			nextURL := fmt.Sprintf("/redirect?hops=%d&target=%s", hops-1, target)
			http.Redirect(w, r, nextURL, http.StatusFound)
			return
		}
		http.Redirect(w, r, target, http.StatusFound)
	})

	mux.HandleFunc("GET /secret", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":  "ghp_123456789012345678901234567890123456",
			"jwt":    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.doNotLeakThisJWTSecretSignature1234567890",
			"status": "classified",
		})
	})

	s.Server = httptest.NewServer(mux)
	s.URL = s.Server.URL

	t.Cleanup(func() {
		s.Close()
	})

	return s
}

func (s *MockHTTPServer) record(r *http.Request) MockHTTPRequestRecord {
	bodyBytes, _ := io.ReadAll(r.Body)
	rec := MockHTTPRequestRecord{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: r.Header.Clone(),
		Body:    string(bodyBytes),
	}
	s.mu.Lock()
	s.Requests = append(s.Requests, rec)
	s.mu.Unlock()
	return rec
}

// Close stops the mock HTTP server.
func (s *MockHTTPServer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.Server != nil {
		s.Server.Close()
	}
}

// GetRequests returns a copy of recorded requests.
func (s *MockHTTPServer) GetRequests() []MockHTTPRequestRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]MockHTTPRequestRecord, len(s.Requests))
	copy(out, s.Requests)
	return out
}
