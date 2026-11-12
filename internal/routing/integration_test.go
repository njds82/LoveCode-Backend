package routing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/models"
	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/storage"
)

func newTestStorage(t *testing.T) *storage.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_routing.db")
	db, err := storage.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func setupTestRouter(t *testing.T) (*Router, *storage.DB, *providers.Manager) {
	t.Helper()
	db := newTestStorage(t)
	modelRegistry := models.NewRegistry()
	providerRegistry := providers.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pm := providers.NewManager(providerRegistry, modelRegistry, logger)

	r := NewWithOptions(pm, RouterOptions{
		DB: db.DB,
	})
	return r, db, pm
}

func TestPersistenceAcrossReconstructedRouter(t *testing.T) {
	r, db, pm := setupTestRouter(t)

	// Save profile
	p := Profile{
		ID:          "prod-profile",
		Policy:      Priority,
		MaxAttempts: 4,
		Timeout:     5 * time.Second,
		Candidates: []Candidate{
			{ProviderID: "mock1", ModelID: "model1", Priority: 10, Weight: 2, MaxConcurrent: 5},
			{ProviderID: "mock2", ModelID: "model2", Priority: 5, Weight: 1, MaxConcurrent: 2},
		},
	}
	if err := r.SetProfile(p); err != nil {
		t.Fatalf("SetProfile failed: %v", err)
	}

	// Save group
	g := Group{
		ID:     "smart-models",
		Models: []string{"model1", "model2"},
	}
	if err := r.SetGroup(g); err != nil {
		t.Fatalf("SetGroup failed: %v", err)
	}

	// Save breaker config
	if err := r.ConfigureBreaker("mock1", 5, 45*time.Second, 2); err != nil {
		t.Fatalf("ConfigureBreaker failed: %v", err)
	}

	// Reconstruct router with the same DB
	r2 := NewWithOptions(pm, RouterOptions{
		DB: db.DB,
	})

	profs := r2.Profiles()
	if len(profs) != 1 {
		t.Fatalf("expected 1 profile loaded, got %d", len(profs))
	}
	if profs[0].ID != "prod-profile" || profs[0].MaxAttempts != 4 || profs[0].Timeout != 5*time.Second {
		t.Fatalf("unexpected profile loaded: %+v", profs[0])
	}
	if len(profs[0].Candidates) != 2 || profs[0].Candidates[0].MaxConcurrent != 5 {
		t.Fatalf("unexpected candidates: %+v", profs[0].Candidates)
	}

	grps := r2.Groups()
	if len(grps) != 1 || grps[0].ID != "smart-models" || len(grps[0].Models) != 2 {
		t.Fatalf("unexpected group loaded: %+v", grps)
	}

	// Breakers
	b := r2.breakerFor("mock1")
	b.mu.Lock()
	if b.threshold != 5 || b.recovery != 45*time.Second || b.maxProbes != 2 {
		t.Fatalf("unexpected breaker config loaded: %+v", b)
	}
	b.mu.Unlock()
}

func TestRetryBackoffAndRetryAfter(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:    3,
		BaseDelay:      100 * time.Millisecond,
		MaxDelay:       1 * time.Second,
		JitterFraction: 0.1,
	}
	b := NewBackoff(cfg)

	// Attempt 0 (1st retry): ~100ms ± 10%
	d0 := b.Delay(0, 0)
	if d0 < 80*time.Millisecond || d0 > 120*time.Millisecond {
		t.Fatalf("delay 0 unexpected: %v", d0)
	}

	// Attempt 1 (2nd retry): ~200ms ± 10%
	d1 := b.Delay(1, 0)
	if d1 < 170*time.Millisecond || d1 > 230*time.Millisecond {
		t.Fatalf("delay 1 unexpected: %v", d1)
	}

	// Retry-After header present
	ra := 400 * time.Millisecond
	dRA := b.Delay(0, ra)
	if dRA != ra {
		t.Fatalf("expected Retry-After duration %v, got %v", ra, dRA)
	}

	// Retry-After exceeding maxDelay is capped
	dCapped := b.Delay(0, 10*time.Second)
	if dCapped != 1*time.Second {
		t.Fatalf("expected capped duration 1s, got %v", dCapped)
	}
}

func TestCircuitBreakerFullTransitions(t *testing.T) {
	r := New(nil)
	if err := r.ConfigureBreaker("p1", 2, 50*time.Millisecond, 1); err != nil {
		t.Fatal(err)
	}

	// Initially closed
	if !r.allow("p1") {
		t.Fatal("expected closed breaker to allow")
	}

	// 1 failure - still closed
	r.failure("p1", providers.NewError(providers.KindUnavailable, "p1", "err"))
	if !r.allow("p1") {
		t.Fatal("expected closed breaker to allow after 1 failure")
	}

	// 2nd failure - trips to open
	r.failure("p1", providers.NewError(providers.KindUnavailable, "p1", "err"))
	if r.allow("p1") {
		t.Fatal("expected open breaker to reject")
	}

	// Wait recovery period
	time.Sleep(60 * time.Millisecond)

	// 1st half-open probe allowed
	if !r.allow("p1") {
		t.Fatal("expected 1st half-open probe to be allowed")
	}

	// 2nd probe blocked
	if r.allow("p1") {
		t.Fatal("expected 2nd half-open probe to be blocked")
	}

	// Probe succeeds -> breaker closes
	r.success("p1")
	if !r.allow("p1") {
		t.Fatal("expected breaker to be closed after success")
	}

	// Next request allowed
	if !r.allow("p1") {
		t.Fatal("expected breaker to remain closed")
	}
}

func TestCircuitBreakerConcurrentAccess(t *testing.T) {
	r := New(nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx%3 == 0 {
				r.failure("cp", providers.NewError(providers.KindTimeout, "cp", "to"))
			} else if idx%3 == 1 {
				r.success("cp")
			} else {
				_ = r.allow("cp")
			}
		}(i)
	}
	wg.Wait()
	_ = r.Breakers()
}

func TestRateLimitTracking(t *testing.T) {
	rl := NewRateLimiter()

	// Initial
	cooling, _ := rl.IsCoolingDown("prov1", time.Now())
	if cooling {
		t.Fatal("expected not cooling down initially")
	}

	// Record 429
	now := time.Now()
	rl.RecordRateLimit("prov1", 200*time.Millisecond, now)

	cooling, rem := rl.IsCoolingDown("prov1", now.Add(50*time.Millisecond))
	if !cooling || rem <= 0 {
		t.Fatalf("expected cooling down, got cooling=%v rem=%v", cooling, rem)
	}

	// Cooldown expired
	cooling, _ = rl.IsCoolingDown("prov1", now.Add(250*time.Millisecond))
	if cooling {
		t.Fatal("expected cooldown to be finished")
	}

	// Parse headers
	meta := map[string]string{
		"x-ratelimit-remaining-requests": "45",
		"x-ratelimit-remaining-tokens":   "125000",
	}
	rl.RecordHeaders("prov1", meta)

	st := rl.Status()
	if len(st) != 1 || *st[0].RequestsRemaining != 45 || *st[0].TokensRemaining != 125000 {
		t.Fatalf("unexpected rate limit status: %+v", st)
	}
}

func TestUsageStoreAndSummary(t *testing.T) {
	db := newTestStorage(t)
	st := NewStore(db.DB)
	ctx := context.Background()

	pTokens := 15
	cTokens := 25
	totTokens := 40
	r1 := UsageRecord{
		RequestID:        "req-1",
		ProviderID:       "mock-p",
		ModelID:          "mock-m1",
		PromptTokens:     &pTokens,
		CompletionTokens: &cTokens,
		TotalTokens:      &totTokens,
		Duration:         120 * time.Millisecond,
		Success:          true,
		Stream:           false,
		CreatedAt:        time.Now().UTC(),
	}
	if err := st.RecordUsage(ctx, r1); err != nil {
		t.Fatalf("record usage 1 failed: %v", err)
	}

	// Record unknown token usage
	r2 := UsageRecord{
		RequestID:  "req-2",
		ProviderID: "mock-p",
		ModelID:    "mock-m2",
		Duration:   200 * time.Millisecond,
		Success:    false,
		ErrorKind:  "timeout",
		Stream:     true,
		CreatedAt:  time.Now().UTC(),
	}
	if err := st.RecordUsage(ctx, r2); err != nil {
		t.Fatalf("record usage 2 failed: %v", err)
	}

	// Query
	records, err := st.QueryUsage(ctx, UsageFilter{ProviderID: "mock-p"})
	if err != nil || len(records) != 2 {
		t.Fatalf("expected 2 records, got %d, err: %v", len(records), err)
	}

	// Filter by success
	trueVal := true
	successOnly, err := st.QueryUsage(ctx, UsageFilter{Success: &trueVal})
	if err != nil || len(successOnly) != 1 || successOnly[0].RequestID != "req-1" {
		t.Fatalf("filter by success failed: %v", successOnly)
	}

	// Summary
	sum, err := st.SummaryUsage(ctx, UsageFilter{ProviderID: "mock-p"})
	if err != nil {
		t.Fatalf("summary failed: %v", err)
	}
	if sum.TotalRequests != 2 || sum.Successful != 1 || sum.Failed != 1 {
		t.Fatalf("summary counts mismatch: %+v", sum)
	}
	if sum.TotalTokens != 40 || sum.UnknownUsageCount != 1 {
		t.Fatalf("summary tokens mismatch: %+v", sum)
	}
}

func TestConcurrencyLimitEnforcement(t *testing.T) {
	r := New(nil)
	r.SetProviderConcurrencyLimit("p_limit", 2)

	cand := Candidate{
		ProviderID:    "p_limit",
		ModelID:       "m_limit",
		MaxConcurrent: 1,
	}

	ctx := context.Background()
	rel1, err := r.acquire(ctx, cand)
	if err != nil {
		t.Fatalf("acquire 1 failed: %v", err)
	}

	// Model limit is 1, so 2nd acquire should block until timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()

	_, err2 := r.acquire(timeoutCtx, cand)
	if !errors.Is(err2, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got: %v", err2)
	}

	// Release first and acquire second
	rel1()
	rel2, err3 := r.acquire(ctx, cand)
	if err3 != nil {
		t.Fatalf("acquire after release failed: %v", err3)
	}
	rel2()
}

func TestFullGenerationWithFailoverAndMock(t *testing.T) {
	r, _, pm := setupTestRouter(t)
	ctx := context.Background()

	// Load 2 mock providers: mock1 fails with transient KindUnavailable, mock2 succeeds
	p1Cfg := config.ProviderConfig{
		ID:       "mock-fail",
		Type:     "mock",
		Settings: map[string]any{"unavailable": true},
		Models: []config.ModelConfig{
			{ID: "fail:chat", ProviderModelID: "fail-1"},
		},
	}
	reply := "Success from mock-ok"
	p2Cfg := config.ProviderConfig{
		ID:       "mock-ok",
		Type:     "mock",
		Settings: map[string]any{"reply": reply},
		Models: []config.ModelConfig{
			{ID: "ok:chat", ProviderModelID: "ok-1"},
		},
	}
	if err := pm.Load(ctx, []config.ProviderConfig{p1Cfg, p2Cfg}); err != nil {
		t.Fatalf("pm.Load failed: %v", err)
	}

	// Profile with failover order: mock-fail first, then mock-ok
	prof := Profile{
		ID:          "failover-test",
		Policy:      Ordered,
		MaxAttempts: 2,
		Candidates: []Candidate{
			{ProviderID: "mock-fail", ModelID: "fail:chat"},
			{ProviderID: "mock-ok", ModelID: "ok:chat"},
		},
	}
	if err := r.SetProfile(prof); err != nil {
		t.Fatalf("SetProfile failed: %v", err)
	}

	req := providers.ChatRequest{
		Model: "auto",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: []providers.ContentPart{providers.TextPart("hello")}},
		},
	}

	res, err := r.Generate(ctx, req, "failover-test", "", "")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if res.ProviderID != "mock-ok" || res.ModelID != "ok:chat" {
		t.Fatalf("failover did not select mock-ok: %+v", res)
	}
	if res.Attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", res.Attempts)
	}
	if res.Response.Message.Text() != reply {
		t.Fatalf("unexpected reply: %s", res.Response.Message.Text())
	}
}

func TestStreamingFailoverBeforeFirstOutput(t *testing.T) {
	r, _, pm := setupTestRouter(t)
	ctx := context.Background()

	tVal := true
	p1Cfg := config.ProviderConfig{
		ID:       "mock-stream-fail",
		Type:     "mock",
		Settings: map[string]any{"unavailable": true},
		Models: []config.ModelConfig{
			{ID: "sf:chat", ProviderModelID: "sf-1", Streaming: &tVal},
		},
	}
	p2Cfg := config.ProviderConfig{
		ID:       "mock-stream-ok",
		Type:     "mock",
		Settings: map[string]any{"reply": "streamed-hello"},
		Models: []config.ModelConfig{
			{ID: "so:chat", ProviderModelID: "so-1", Streaming: &tVal},
		},
	}
	if err := pm.Load(ctx, []config.ProviderConfig{p1Cfg, p2Cfg}); err != nil {
		t.Fatalf("pm.Load: %v", err)
	}

	prof := Profile{
		ID:          "stream-profile",
		Policy:      Ordered,
		MaxAttempts: 2,
		Candidates: []Candidate{
			{ProviderID: "mock-stream-fail", ModelID: "sf:chat"},
			{ProviderID: "mock-stream-ok", ModelID: "so:chat"},
		},
	}
	if err := r.SetProfile(prof); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}

	req := providers.ChatRequest{
		Model:  "auto",
		Stream: true,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: []providers.ContentPart{providers.TextPart("stream please")}},
		},
	}

	sRes, err := r.Stream(ctx, req, "stream-profile", "", "")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer sRes.Stream.Close()

	if sRes.ProviderID != "mock-stream-ok" {
		t.Fatalf("expected failover to mock-stream-ok, got %s", sRes.ProviderID)
	}
	if sRes.Attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", sRes.Attempts)
	}
}
