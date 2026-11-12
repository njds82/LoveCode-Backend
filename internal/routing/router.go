// Package routing selects eligible provider/model pairs and executes bounded failover.
package routing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/lovecode/lovecode/internal/models"
	"github.com/lovecode/lovecode/internal/providers"
)

type Policy string

const (
	Priority Policy = "priority"
	Weighted Policy = "weighted"
	Ordered  Policy = "ordered"
)

type Candidate struct {
	ProviderID    string `json:"provider_id"`
	ModelID       string `json:"model_id"`
	Priority      int    `json:"priority"`
	Weight        int    `json:"weight"`
	MaxConcurrent int    `json:"max_concurrent,omitempty"`
}

type Profile struct {
	ID          string        `json:"id"`
	Policy      Policy        `json:"policy"`
	Candidates  []Candidate   `json:"candidates"`
	MaxAttempts int           `json:"max_attempts"`
	Timeout     time.Duration `json:"timeout"`
}

type Group struct {
	ID     string   `json:"id"`
	Models []string `json:"models"`
}

type Requirements struct {
	ProviderID      string
	ModelID         string
	GroupID         string
	Streaming       bool
	ToolCalls       bool
	InputModalities []models.Modality
	MinContext      int64
}

type CandidateResult struct {
	Candidate Candidate    `json:"candidate"`
	Model     models.Model `json:"model"`
	Reason    string       `json:"reason,omitempty"`
}

type BreakerState string

const (
	Closed   BreakerState = "closed"
	Open     BreakerState = "open"
	HalfOpen BreakerState = "half_open"
)

type breaker struct {
	mu        sync.Mutex
	state     BreakerState
	failures  int
	opened    time.Time
	probes    int
	threshold int
	recovery  time.Duration
	maxProbes int
}

type breakerView struct {
	ProviderID string       `json:"provider_id"`
	State      BreakerState `json:"state"`
	Failures   int          `json:"failures"`
	OpenedAt   time.Time    `json:"opened_at,omitempty"`
}

type Router struct {
	providers *providers.Manager
	store     *Store
	rateLimit *RateLimiter
	health    *HealthMonitor
	backoff   *Backoff
	sleeper   Sleeper

	mu            sync.RWMutex
	profiles      map[string]Profile
	groups        map[string]Group
	breakers      map[string]*breaker
	modelSems     map[string]chan struct{}
	provSems      map[string]chan struct{}
	provLimits    map[string]int
	defaultThresh int
	defaultRecov  time.Duration
}

// RouterOptions configures optional dependencies for Router.
type RouterOptions struct {
	DB               *sql.DB
	RetryConfig      *RetryConfig
	Sleeper          Sleeper
	HealthInterval   time.Duration
	ProviderLimits   map[string]int
	DefaultThreshold int
	DefaultRecovery  time.Duration
}

func New(pm *providers.Manager) *Router {
	return NewWithOptions(pm, RouterOptions{})
}

func NewWithOptions(pm *providers.Manager, opts RouterOptions) *Router {
	var st *Store
	if opts.DB != nil {
		st = NewStore(opts.DB)
	}

	rc := DefaultRetryConfig()
	if opts.RetryConfig != nil {
		rc = *opts.RetryConfig
	}

	var sl Sleeper = realSleeper{}
	if opts.Sleeper != nil {
		sl = opts.Sleeper
	}

	thresh := 3
	if opts.DefaultThreshold > 0 {
		thresh = opts.DefaultThreshold
	}
	recov := 30 * time.Second
	if opts.DefaultRecovery > 0 {
		recov = opts.DefaultRecovery
	}

	r := &Router{
		providers:     pm,
		store:         st,
		rateLimit:     NewRateLimiter(),
		backoff:       NewBackoff(rc),
		sleeper:       sl,
		profiles:      make(map[string]Profile),
		groups:        make(map[string]Group),
		breakers:      make(map[string]*breaker),
		modelSems:     make(map[string]chan struct{}),
		provSems:      make(map[string]chan struct{}),
		provLimits:    make(map[string]int),
		defaultThresh: thresh,
		defaultRecov:  recov,
	}

	if opts.ProviderLimits != nil {
		for p, lim := range opts.ProviderLimits {
			r.provLimits[p] = lim
		}
	}

	if opts.HealthInterval > 0 && pm != nil {
		r.health = NewHealthMonitor(pm, opts.HealthInterval)
	}

	// Load persistent configuration if store available
	if st != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if profs, err := st.LoadProfiles(ctx); err == nil {
			for _, p := range profs {
				r.profiles[p.ID] = p
			}
		}
		if grps, err := st.LoadGroups(ctx); err == nil {
			for _, g := range grps {
				r.groups[g.ID] = g
			}
		}
		if bcfgs, err := st.LoadBreakerConfigs(ctx); err == nil {
			for pID, bcfg := range bcfgs {
				b := &breaker{
					state:     Closed,
					threshold: bcfg.Threshold,
					recovery:  bcfg.Recovery,
					maxProbes: bcfg.MaxProbes,
				}
				r.breakers[pID] = b
			}
		}
	}

	return r
}

func (r *Router) Store() *Store {
	return r.store
}

func (r *Router) RateLimiter() *RateLimiter {
	return r.rateLimit
}

func (r *Router) HealthMonitor() *HealthMonitor {
	return r.health
}

func (r *Router) SetProviderConcurrencyLimit(providerID string, limit int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.provLimits[providerID] = limit
	// Reset semaphore if exists
	delete(r.provSems, providerID)
}

func (r *Router) ConfigureBreaker(providerID string, threshold int, recovery time.Duration, maxProbes int) error {
	if providerID == "" {
		return errors.New("provider_id required")
	}
	if threshold <= 0 {
		threshold = 3
	}
	if recovery <= 0 {
		recovery = 30 * time.Second
	}
	if maxProbes <= 0 {
		maxProbes = 1
	}

	r.mu.Lock()
	b := r.breakers[providerID]
	if b == nil {
		b = &breaker{state: Closed}
		r.breakers[providerID] = b
	}
	b.mu.Lock()
	b.threshold = threshold
	b.recovery = recovery
	b.maxProbes = maxProbes
	b.mu.Unlock()
	r.mu.Unlock()

	if r.store != nil {
		cfg := BreakerConfig{
			ProviderID: providerID,
			Threshold:  threshold,
			Recovery:   recovery,
			MaxProbes:  maxProbes,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = r.store.SaveBreakerConfig(ctx, cfg)
	}
	return nil
}

func (r *Router) SetProfile(p Profile) error {
	if p.ID == "" {
		return errors.New("profile id required")
	}
	if p.Policy == "" {
		p.Policy = Priority
	}
	if p.Policy != Priority && p.Policy != Weighted && p.Policy != Ordered {
		return fmt.Errorf("invalid routing policy %q", p.Policy)
	}
	if len(p.Candidates) == 0 {
		return errors.New("profile requires candidates")
	}
	if p.MaxAttempts <= 0 || p.MaxAttempts > 20 {
		p.MaxAttempts = 3
	}
	for _, c := range p.Candidates {
		if c.ProviderID == "" || c.ModelID == "" {
			return errors.New("candidate provider_id and model_id required")
		}
		if c.Weight < 0 {
			return errors.New("candidate weight cannot be negative")
		}
	}

	r.mu.Lock()
	p.Candidates = append([]Candidate(nil), p.Candidates...)
	r.profiles[p.ID] = p
	r.mu.Unlock()

	if r.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := r.store.SaveProfile(ctx, p); err != nil {
			return fmt.Errorf("persist profile: %w", err)
		}
	}
	return nil
}

func (r *Router) Profiles() []Profile {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Profile, 0, len(r.profiles))
	for _, p := range r.profiles {
		p.Candidates = append([]Candidate(nil), p.Candidates...)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Router) SetGroup(g Group) error {
	if g.ID == "" || len(g.Models) == 0 {
		return errors.New("group id and models required")
	}
	seen := map[string]bool{}
	for _, id := range g.Models {
		if id == "" || seen[id] {
			return errors.New("group model ids must be unique and non-empty")
		}
		seen[id] = true
	}

	r.mu.Lock()
	g.Models = append([]string(nil), g.Models...)
	r.groups[g.ID] = g
	r.mu.Unlock()

	if r.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := r.store.SaveGroup(ctx, g); err != nil {
			return fmt.Errorf("persist group: %w", err)
		}
	}
	return nil
}

func (r *Router) Groups() []Group {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Group, 0, len(r.groups))
	for _, g := range r.groups {
		g.Models = append([]string(nil), g.Models...)
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Router) group(id string) (Group, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.groups[id]
	return g, ok
}

func (r *Router) breakerFor(id string) *breaker {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.breakers[id]
	if b == nil {
		b = &breaker{
			state:     Closed,
			threshold: r.defaultThresh,
			recovery:  r.defaultRecov,
			maxProbes: 1,
		}
		r.breakers[id] = b
	}
	return b
}

func (r *Router) allow(id string) bool {
	b := r.breakerFor(id)
	b.mu.Lock()
	defer b.mu.Unlock()

	thresh := b.threshold
	if thresh <= 0 {
		thresh = r.defaultThresh
	}
	recov := b.recovery
	if recov <= 0 {
		recov = r.defaultRecov
	}
	maxProbes := b.maxProbes
	if maxProbes <= 0 {
		maxProbes = 1
	}

	if b.state == Open {
		if time.Since(b.opened) < recov {
			return false
		}
		b.state = HalfOpen
		b.probes = 0
	}
	if b.state == HalfOpen {
		if b.probes >= maxProbes {
			return false
		}
		b.probes++
	}
	return true
}

func (r *Router) success(id string) {
	b := r.breakerFor(id)
	b.mu.Lock()
	b.state = Closed
	b.failures = 0
	b.probes = 0
	b.mu.Unlock()
	if r.health != nil {
		r.health.RecordObservedResult(id, nil)
	}
}

func (r *Router) failure(id string, err error) {
	b := r.breakerFor(id)
	if !retryable(err) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	thresh := b.threshold
	if thresh <= 0 {
		thresh = r.defaultThresh
	}

	b.failures++
	if b.state == HalfOpen || b.failures >= thresh {
		b.state = Open
		b.opened = time.Now()
		b.probes = 0
	}
	if r.health != nil {
		r.health.RecordObservedResult(id, err)
	}
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	var pe *providers.Error
	if errors.As(err, &pe) {
		switch pe.Kind {
		case providers.KindConnection, providers.KindTimeout, providers.KindUnavailable, providers.KindRateLimited, providers.KindInternal:
			return true
		default:
			return false
		}
	}
	return true
}

func (r *Router) Breakers() []breakerView {
	r.mu.RLock()
	ids := make([]string, 0, len(r.breakers))
	for id := range r.breakers {
		ids = append(ids, id)
	}
	r.mu.RUnlock()
	sort.Strings(ids)
	out := []breakerView{}
	for _, id := range ids {
		b := r.breakerFor(id)
		b.mu.Lock()
		out = append(out, breakerView{id, b.state, b.failures, b.opened})
		b.mu.Unlock()
	}
	return out
}

func (r *Router) Discover(req Requirements, profileID string) ([]CandidateResult, error) {
	if r.providers == nil {
		return nil, errors.New("provider manager unavailable")
	}
	modelsList := r.providers.ModelRegistry().List(models.Filter{})
	providerEnabled := map[string]bool{}
	for _, p := range r.providers.ProviderStatuses() {
		providerEnabled[p.ID] = p.Enabled && p.LastError == ""
	}
	allowed := map[string]bool{}
	var candidates []Candidate
	r.mu.RLock()
	p, ok := r.profiles[profileID]
	r.mu.RUnlock()
	if profileID != "" && !ok {
		return nil, fmt.Errorf("routing profile %q not found", profileID)
	}
	if ok {
		for _, c := range p.Candidates {
			candidates = append(candidates, c)
			allowed[c.ModelID] = true
		}
	}
	if req.GroupID != "" {
		g, found := r.group(req.GroupID)
		if !found {
			return nil, fmt.Errorf("model group %q not found", req.GroupID)
		}
		allowed = map[string]bool{}
		for _, id := range g.Models {
			allowed[id] = true
		}
	}
	if req.ModelID != "" {
		allowed = map[string]bool{req.ModelID: true}
	}
	byModel := map[string]Candidate{}
	for _, c := range candidates {
		byModel[c.ModelID] = c
	}
	out := []CandidateResult{}
	for _, m := range modelsList {
		reason := ""
		if !m.Usable() {
			reason = "model disabled or unavailable"
		} else if req.ProviderID != "" && m.ProviderID != req.ProviderID {
			reason = "provider preference mismatch"
		} else if req.ModelID != "" && m.ID != req.ModelID {
			reason = "model mismatch"
		} else if len(allowed) > 0 && !allowed[m.ID] {
			reason = "not in requested group/profile"
		} else if ok && len(candidates) > 0 && !allowed[m.ID] {
			reason = "not in profile"
		}
		if reason == "" && !providerEnabled[m.ProviderID] {
			reason = "provider disabled or initialization failed"
		}
		if reason == "" && req.Streaming && m.Streaming != models.SupportSupported {
			reason = "streaming capability not confirmed"
		}
		if reason == "" && req.ToolCalls && m.ToolCalls != models.SupportSupported {
			reason = "tool calling capability not confirmed"
		}
		if reason == "" && req.MinContext > 0 && (m.ContextWindow == nil || *m.ContextWindow < req.MinContext) {
			reason = "context window unknown or insufficient"
		}
		if reason == "" {
			for _, mod := range req.InputModalities {
				found := false
				for _, v := range m.InputModalities {
					if v == mod {
						found = true
					}
				}
				if !found {
					reason = "required input modality unknown or unsupported"
					break
				}
			}
		}
		c, has := byModel[m.ID]
		if !has {
			c = Candidate{ProviderID: m.ProviderID, ModelID: m.ID, Weight: 1}
		}
		if reason == "" && !r.allow(m.ProviderID) {
			reason = "provider circuit breaker open"
		}
		if reason == "" && r.rateLimit != nil {
			if cooling, _ := r.rateLimit.IsCoolingDown(m.ProviderID, time.Now()); cooling {
				reason = "provider rate limited / cooldown active"
			}
		}
		if reason == "" {
			out = append(out, CandidateResult{Candidate: c, Model: m})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no eligible provider/model candidates")
	}
	if ok {
		switch p.Policy {
		case Priority:
			sort.SliceStable(out, func(i, j int) bool { return out[i].Candidate.Priority > out[j].Candidate.Priority })
		case Ordered:
			rank := map[string]int{}
			for i, c := range p.Candidates {
				rank[c.ModelID] = i
			}
			sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Model.ID] < rank[out[j].Model.ID] })
		case Weighted:
			out = weightedOrder(out)
		}
	}
	return out, nil
}

func weightedOrder(in []CandidateResult) []CandidateResult {
	pool := append([]CandidateResult(nil), in...)
	out := make([]CandidateResult, 0, len(pool))
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for len(pool) > 0 {
		total := 0
		for _, c := range pool {
			w := c.Candidate.Weight
			if w <= 0 {
				w = 1
			}
			total += w
		}
		n := rng.Intn(total)
		idx := 0
		for i, c := range pool {
			w := c.Candidate.Weight
			if w <= 0 {
				w = 1
			}
			n -= w
			if n < 0 {
				idx = i
				break
			}
		}
		out = append(out, pool[idx])
		pool = append(pool[:idx], pool[idx+1:]...)
	}
	return out
}

// acquire handles two-level concurrency: provider limit, then candidate/model limit.
func (r *Router) acquire(ctx context.Context, c Candidate) (func(), error) {
	r.mu.Lock()
	var provSem chan struct{}
	if plim, ok := r.provLimits[c.ProviderID]; ok && plim > 0 {
		provSem = r.provSems[c.ProviderID]
		if provSem == nil {
			provSem = make(chan struct{}, plim)
			r.provSems[c.ProviderID] = provSem
		}
	}

	var modelSem chan struct{}
	if c.MaxConcurrent > 0 {
		key := c.ProviderID + "/" + c.ModelID
		modelSem = r.modelSems[key]
		if modelSem == nil {
			modelSem = make(chan struct{}, c.MaxConcurrent)
			r.modelSems[key] = modelSem
		}
	}
	r.mu.Unlock()

	// Acquire provider semaphore first
	if provSem != nil {
		select {
		case provSem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Acquire model semaphore
	if modelSem != nil {
		select {
		case modelSem <- struct{}{}:
		case <-ctx.Done():
			if provSem != nil {
				<-provSem
			}
			return nil, ctx.Err()
		}
	}

	release := func() {
		if modelSem != nil {
			select {
			case <-modelSem:
			default:
			}
		}
		if provSem != nil {
			select {
			case <-provSem:
			default:
			}
		}
	}
	return release, nil
}

type Result struct {
	Response   *providers.ChatResponse
	ModelID    string
	ProviderID string
	Attempts   int
}

func (r *Router) Generate(ctx context.Context, req providers.ChatRequest, profileID, groupID, providerID string) (Result, error) {
	start := time.Now()
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	modelID := req.Model
	if _, err := r.providers.ModelRegistry().Get(modelID); err != nil {
		if modelID != "" && modelID != "auto" {
			return Result{}, err
		}
		modelID = ""
	}
	cr := Requirements{ProviderID: providerID, ModelID: modelID, GroupID: groupID}
	p, has := r.profile(profileID)
	if has && p.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Timeout)
		defer cancel()
	}
	cands, err := r.Discover(cr, profileID)
	if err != nil {
		return Result{}, err
	}
	max := len(cands)
	if has && p.MaxAttempts < max {
		max = p.MaxAttempts
	}
	var last error
	for i := 0; i < max; i++ {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		// If retrying (i > 0), calculate backoff and wait
		if i > 0 {
			var retryAfter time.Duration
			var pe *providers.Error
			if errors.As(last, &pe) {
				retryAfter = pe.RetryAfter
			}
			waitDur := r.backoff.Delay(i-1, retryAfter)
			if waitDur > 0 {
				select {
				case <-time.After(waitDur):
				case <-ctx.Done():
					return Result{}, ctx.Err()
				}
			}
		}

		c := cands[i].Candidate
		release, err := r.acquire(ctx, c)
		if err != nil {
			return Result{}, err
		}
		pr, err := r.providers.Registry().Chat(ctx, c.ProviderID, providers.ChatRequest{
			Model:           cands[i].Model.ProviderModelID,
			Messages:        req.Messages,
			Temperature:     req.Temperature,
			TopP:            req.TopP,
			MaxTokens:       req.MaxTokens,
			Stop:            req.Stop,
			Seed:            req.Seed,
			Tools:           req.Tools,
			ToolChoice:      req.ToolChoice,
			ResponseFormat:  req.ResponseFormat,
			Stream:          false,
			ProviderOptions: req.ProviderOptions,
		})
		release()

		if err == nil {
			r.success(c.ProviderID)
			pr.Model = c.ModelID
			pr.ProviderID = c.ProviderID

			// Track rate limit headers if metadata present
			if r.rateLimit != nil && len(pr.Metadata) > 0 {
				r.rateLimit.RecordHeaders(c.ProviderID, pr.Metadata)
			}

			// Record usage if store available
			if r.store != nil {
				reqID := ""
				if pr.ProviderRequestID != "" {
					reqID = pr.ProviderRequestID
				}
				rec := UsageRecord{
					RequestID:  reqID,
					ProviderID: c.ProviderID,
					ModelID:    c.ModelID,
					Duration:   time.Since(start),
					Success:    true,
					Stream:     false,
					CreatedAt:  time.Now().UTC(),
				}
				if pr.Usage != nil {
					rec.PromptTokens = pr.Usage.PromptTokens
					rec.CompletionTokens = pr.Usage.CompletionTokens
					rec.TotalTokens = pr.Usage.TotalTokens
				}
				_ = r.store.RecordUsage(context.Background(), rec)
			}

			return Result{pr, c.ModelID, c.ProviderID, i + 1}, nil
		}

		last = err
		r.failure(c.ProviderID, err)

		// Record 429 rate limit cooldown if applicable
		var pe *providers.Error
		if errors.As(err, &pe) && pe.Kind == providers.KindRateLimited && r.rateLimit != nil {
			r.rateLimit.RecordRateLimit(c.ProviderID, pe.RetryAfter, time.Now())
		}

		if !retryable(err) {
			// Record failed attempt
			if r.store != nil {
				_ = r.store.RecordUsage(context.Background(), UsageRecord{
					ProviderID: c.ProviderID,
					ModelID:    c.ModelID,
					Duration:   time.Since(start),
					Success:    false,
					ErrorKind:  safeKind(err),
					Stream:     false,
					CreatedAt:  time.Now().UTC(),
				})
			}
			return Result{}, err
		}
	}

	if last == nil {
		last = errors.New("all routing candidates exhausted")
	}
	if r.store != nil && len(cands) > 0 {
		_ = r.store.RecordUsage(context.Background(), UsageRecord{
			ProviderID: cands[0].Candidate.ProviderID,
			ModelID:    cands[0].Candidate.ModelID,
			Duration:   time.Since(start),
			Success:    false,
			ErrorKind:  safeKind(last),
			Stream:     false,
			CreatedAt:  time.Now().UTC(),
		})
	}
	return Result{}, fmt.Errorf("generation failed after %d attempts: %w", max, last)
}

type StreamResult struct {
	Stream     providers.Stream
	First      providers.StreamEvent
	ModelID    string
	ProviderID string
	Attempts   int
}

func (r *Router) Stream(ctx context.Context, req providers.ChatRequest, profileID, groupID, providerID string) (StreamResult, error) {
	start := time.Now()
	if err := req.Validate(); err != nil {
		return StreamResult{}, err
	}
	modelID := req.Model
	if _, err := r.providers.ModelRegistry().Get(modelID); err != nil {
		if modelID != "" && modelID != "auto" {
			return StreamResult{}, err
		}
		modelID = ""
	}
	cr := Requirements{ProviderID: providerID, ModelID: modelID, GroupID: groupID, Streaming: true, ToolCalls: len(req.Tools) > 0}
	p, has := r.profile(profileID)
	if has && p.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Timeout)
		defer cancel()
	}
	cands, err := r.Discover(cr, profileID)
	if err != nil {
		return StreamResult{}, err
	}
	max := len(cands)
	if has && p.MaxAttempts < max {
		max = p.MaxAttempts
	}
	var last error
	for i := 0; i < max; i++ {
		if err := ctx.Err(); err != nil {
			return StreamResult{}, err
		}
		if i > 0 {
			var retryAfter time.Duration
			var pe *providers.Error
			if errors.As(last, &pe) {
				retryAfter = pe.RetryAfter
			}
			waitDur := r.backoff.Delay(i-1, retryAfter)
			if waitDur > 0 {
				select {
				case <-time.After(waitDur):
				case <-ctx.Done():
					return StreamResult{}, ctx.Err()
				}
			}
		}

		c := cands[i].Candidate
		release, err := r.acquire(ctx, c)
		if err != nil {
			return StreamResult{}, err
		}
		stream, err := r.providers.Registry().ChatStream(ctx, c.ProviderID, providers.ChatRequest{
			Model:           cands[i].Model.ProviderModelID,
			Messages:        req.Messages,
			Temperature:     req.Temperature,
			TopP:            req.TopP,
			MaxTokens:       req.MaxTokens,
			Stop:            req.Stop,
			Seed:            req.Seed,
			Tools:           req.Tools,
			ToolChoice:      req.ToolChoice,
			ResponseFormat:  req.ResponseFormat,
			Stream:          true,
			ProviderOptions: req.ProviderOptions,
		})
		if err == nil {
			first, recvErr := stream.Recv(ctx)
			if recvErr == nil {
				wrappedStream := &acquiredStream{Stream: stream, release: release}
				r.success(c.ProviderID)
				if r.store != nil {
					_ = r.store.RecordUsage(context.Background(), UsageRecord{
						ProviderID: c.ProviderID,
						ModelID:    c.ModelID,
						Duration:   time.Since(start),
						Success:    true,
						Stream:     true,
						CreatedAt:  time.Now().UTC(),
					})
				}
				return StreamResult{wrappedStream, first, c.ModelID, c.ProviderID, i + 1}, nil
			}
			_ = stream.Close()
			err = recvErr
		}
		release()

		last = err
		r.failure(c.ProviderID, err)

		var pe *providers.Error
		if errors.As(err, &pe) && pe.Kind == providers.KindRateLimited && r.rateLimit != nil {
			r.rateLimit.RecordRateLimit(c.ProviderID, pe.RetryAfter, time.Now())
		}

		if !retryable(err) {
			if r.store != nil {
				_ = r.store.RecordUsage(context.Background(), UsageRecord{
					ProviderID: c.ProviderID,
					ModelID:    c.ModelID,
					Duration:   time.Since(start),
					Success:    false,
					ErrorKind:  safeKind(err),
					Stream:     true,
					CreatedAt:  time.Now().UTC(),
				})
			}
			return StreamResult{}, err
		}
	}
	if last == nil {
		last = errors.New("all streaming candidates exhausted")
	}
	if r.store != nil && len(cands) > 0 {
		_ = r.store.RecordUsage(context.Background(), UsageRecord{
			ProviderID: cands[0].Candidate.ProviderID,
			ModelID:    cands[0].Candidate.ModelID,
			Duration:   time.Since(start),
			Success:    false,
			ErrorKind:  safeKind(last),
			Stream:     true,
			CreatedAt:  time.Now().UTC(),
		})
	}
	return StreamResult{}, fmt.Errorf("stream failed before first event after %d attempts: %w", max, last)
}

type acquiredStream struct {
	providers.Stream
	releaseOnce sync.Once
	release     func()
}

func (s *acquiredStream) Close() error {
	s.releaseOnce.Do(func() {
		if s.release != nil {
			s.release()
		}
	})
	return s.Stream.Close()
}

func (r *Router) profile(id string) (Profile, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.profiles[id]
	return p, ok
}

func safeKind(err error) string {
	var pe *providers.Error
	if errors.As(err, &pe) {
		return string(pe.Kind)
	}
	return "internal"
}

var _ = io.EOF
