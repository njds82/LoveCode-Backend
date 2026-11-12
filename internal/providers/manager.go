package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/models"
)

// Factory builds one adapter from configuration. apiKey is the resolved value
// of api_key_env (empty when no credential is configured).
type Factory func(cfg config.ProviderConfig, apiKey string) (Provider, error)

// Factories maps a provider type to its factory. Adding a provider adapter does
// not require changes to the registry, model registry or API handlers.
type Factories map[string]Factory

// DefaultFactories returns the Phase 2 adapters.
func DefaultFactories() Factories {
	return Factories{
		"openai": newOpenAIFromConfig,
		"ollama": newOllamaFromConfig,
		"mock":   newMockFromConfig,
	}
}

// ManagerOptions overrides defaults. It is mainly for embedding and tests.
type ManagerOptions struct {
	Factories Factories
	LookupEnv func(string) (string, bool)
}

// Manager is the integration layer between configuration, the provider
// registry and the model registry. The API depends on this type rather than
// constructing adapters itself.
type Manager struct {
	registry  *Registry
	models    *models.Registry
	log       *slog.Logger
	factories Factories
	lookupEnv func(string) (string, bool)

	refreshMu sync.Mutex

	mu   sync.RWMutex
	meta map[string]*providerMeta
}

type providerMeta struct {
	info          ProviderInfo
	endpoint      string
	healthTimeout time.Duration
	enabled       bool
	lastErr       string
	lastErrKind   Kind
}

// ProviderStatus is the management view of a configured provider. It never
// contains credentials or raw configuration.
type ProviderStatus struct {
	Status
	Endpoint      string `json:"endpoint,omitempty"`
	ModelCount    int    `json:"model_count"`
	LastError     string `json:"last_error,omitempty"`
	LastErrorKind Kind   `json:"last_error_kind,omitempty"`
}

// RefreshResult reports a model metadata refresh.
type RefreshResult struct {
	ProviderID string `json:"provider_id"`
	Added      int    `json:"added"`
	Updated    int    `json:"updated"`
	Total      int    `json:"total"`
}

func NewManager(registry *Registry, modelRegistry *models.Registry, log *slog.Logger) *Manager {
	return NewManagerWithOptions(registry, modelRegistry, log, ManagerOptions{})
}

func NewManagerWithOptions(registry *Registry, modelRegistry *models.Registry, log *slog.Logger, opts ManagerOptions) *Manager {
	if registry == nil {
		registry = NewRegistry()
	}
	if modelRegistry == nil {
		modelRegistry = models.NewRegistry()
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	factories := opts.Factories
	if factories == nil {
		factories = DefaultFactories()
	}
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	return &Manager{
		registry: registry, models: modelRegistry, log: log,
		factories: factories, lookupEnv: lookup,
		meta: make(map[string]*providerMeta),
	}
}

func (m *Manager) Registry() *Registry             { return m.registry }
func (m *Manager) ModelRegistry() *models.Registry { return m.models }

// Load registers every configured provider and its configured models. A
// failure in an optional provider is recorded and does not block unrelated
// providers. Required providers, duplicate identities and invalid model
// records fail startup.
func (m *Manager) Load(ctx context.Context, providers []config.ProviderConfig) error {
	var fatal []error
	seen := make(map[string]bool, len(providers))
	for _, cfg := range providers {
		if err := ctx.Err(); err != nil {
			fatal = append(fatal, err)
			break
		}
		if cfg.ID == "" || !ValidProviderID(cfg.ID) {
			err := fmt.Errorf("%w: provider id %q is invalid", ErrInvalidProvider, cfg.ID)
			fatal = append(fatal, err)
			continue
		}
		if seen[cfg.ID] {
			err := fmt.Errorf("%w: %s", ErrDuplicateProvider, cfg.ID)
			m.setError(cfg.ID, err)
			fatal = append(fatal, err)
			continue
		}
		seen[cfg.ID] = true

		m.setMeta(cfg)
		// Invalid model records are configuration errors, not provider
		// availability problems: fail startup even when the provider itself
		// is optional.
		var modelErr error
		for _, mc := range cfg.Models {
			if _, err := modelFromConfig(cfg.ID, mc); err != nil {
				modelErr = err
				break
			}
		}
		if modelErr != nil {
			err := fmt.Errorf("provider %q: %w", cfg.ID, modelErr)
			m.setError(cfg.ID, err)
			fatal = append(fatal, err)
			continue
		}
		err := m.loadOne(ctx, cfg)
		if err == nil {
			m.clearError(cfg.ID)
			continue
		}
		m.setError(cfg.ID, err)
		kind := KindInvalidConfig
		var pe *Error
		if errors.As(err, &pe) {
			kind = pe.Kind
		}
		m.log.Warn("provider could not be fully loaded", "provider", cfg.ID, "kind", kind, "err", SanitizeText(err.Error()))
		if cfg.Required || isFatalConfigError(err) {
			fatal = append(fatal, fmt.Errorf("provider %q: %w", cfg.ID, err))
		}
	}
	return errors.Join(fatal...)
}

func (m *Manager) loadOne(ctx context.Context, cfg config.ProviderConfig) error {
	if len(cfg.Models) > 0 {
		for _, mc := range cfg.Models {
			if _, err := modelFromConfig(cfg.ID, mc); err != nil {
				return err
			}
		}
	}
	if cfg.Endpoint != "" {
		if err := ValidateEndpoint(cfg.Endpoint); err != nil {
			return NewError(KindInvalidConfig, cfg.ID, err.Error())
		}
	}
	var apiKey string
	if cfg.APIKeyEnv != "" {
		v, ok := m.lookupEnv(cfg.APIKeyEnv)
		if !ok || strings.TrimSpace(v) == "" {
			return NewError(KindInvalidConfig, cfg.ID,
				fmt.Sprintf("environment variable %s is not set or is empty", cfg.APIKeyEnv))
		}
		apiKey = v
	}
	factory, ok := m.factories[cfg.Type]
	if !ok {
		return NewError(KindInvalidConfig, cfg.ID, fmt.Sprintf("unknown provider type %q", cfg.Type))
	}
	p, err := factory(cfg, apiKey)
	if err != nil {
		return err
	}
	if err := m.registry.Register(ctx, p); err != nil {
		return err
	}
	if !cfg.IsEnabled() {
		if err := m.registry.SetEnabled(cfg.ID, false); err != nil {
			return err
		}
	}
	m.updateInfo(cfg.ID, p.Info(), cfg)

	for _, mc := range cfg.Models {
		model, err := modelFromConfig(cfg.ID, mc)
		if err != nil {
			return err
		}
		if err := m.models.Add(model); err != nil {
			return err
		}
	}
	if cfg.DiscoverModels && cfg.IsEnabled() {
		if _, err := m.RefreshModels(ctx, cfg.ID); err != nil {
			return err
		}
	}
	return nil
}

// TestProvider runs a bounded health check against a provider, including a
// disabled one. It does not modify the provider's stored initialization error.
func (m *Manager) TestProvider(ctx context.Context, id string) error {
	lease, err := m.registry.AcquireAny(id)
	if err != nil {
		return err
	}
	defer lease.Release()
	timeout := m.healthTimeout(id)
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return lease.Provider().Health(cctx)
}

// RefreshModels asks a provider for its current model list and merges it into
// the model registry. Configured values are preserved when discovery reports
// unknown metadata; discovered models never overwrite operator tags or the
// enabled flag. Models absent from the provider response are not deleted.
func (m *Manager) RefreshModels(ctx context.Context, id string) (RefreshResult, error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	lease, err := m.registry.AcquireAny(id)
	if err != nil {
		return RefreshResult{ProviderID: id}, err
	}
	defer lease.Release()
	p := lease.Provider()
	if p.Capabilities().ModelListing == models.SupportUnsupported {
		err := NewError(KindUnsupported, id, "provider does not support model listing")
		m.setError(id, err)
		return RefreshResult{ProviderID: id}, err
	}
	cctx, cancel := context.WithTimeout(ctx, m.healthTimeout(id))
	defer cancel()
	infos, err := p.ListModels(cctx)
	if err != nil {
		m.setError(id, err)
		return RefreshResult{ProviderID: id}, err
	}
	res, err := m.mergeDiscovered(id, infos)
	if err != nil {
		m.setError(id, err)
		return res, err
	}
	m.clearError(id)
	return res, nil
}

func (m *Manager) mergeDiscovered(providerID string, infos []ModelInfo) (RefreshResult, error) {
	res := RefreshResult{ProviderID: providerID}
	existing := make(map[string]models.Model)
	for _, model := range m.models.List(models.Filter{ProviderID: providerID}) {
		existing[model.ProviderModelID] = model
	}
	seen := make(map[string]bool, len(infos))
	for _, info := range infos {
		upstream := strings.TrimSpace(info.ProviderModelID)
		if upstream == "" {
			return res, NewError(KindMalformedResponse, providerID, "provider returned a model with an empty id")
		}
		if seen[upstream] {
			return res, NewError(KindMalformedResponse, providerID, fmt.Sprintf("provider returned duplicate model id %q", upstream))
		}
		seen[upstream] = true
		if prev, ok := existing[upstream]; ok {
			merged := mergeModelInfo(prev, info)
			merged.Availability = models.AvailabilityAvailable
			if err := m.models.Update(merged); err != nil {
				return res, err
			}
			res.Updated++
			continue
		}
		modelID, err := deriveModelID(providerID, upstream)
		if err != nil {
			return res, err
		}
		display := strings.TrimSpace(info.DisplayName)
		if display == "" {
			display = upstream
		}
		record := models.Model{
			ID:               modelID,
			ProviderID:       providerID,
			ProviderModelID:  upstream,
			DisplayName:      display,
			Description:      SanitizeText(info.Description),
			ContextWindow:    info.ContextWindow,
			MaxOutputTokens:  info.MaxOutputTokens,
			InputModalities:  append([]models.Modality(nil), info.InputModalities...),
			OutputModalities: append([]models.Modality(nil), info.OutputModalities...),
			Streaming:        supportOrUnknown(info.Streaming),
			ToolCalls:        supportOrUnknown(info.ToolCalls),
			StructuredOutput: supportOrUnknown(info.StructuredOutput),
			Reasoning:        supportOrUnknown(info.Reasoning),
			Availability:     models.AvailabilityAvailable,
			Pricing:          info.Pricing,
			RateLimits:       info.RateLimits,
			Enabled:          true,
		}
		if err := m.models.Add(record); err != nil {
			return res, err
		}
		res.Added++
	}
	res.Total = len(m.models.List(models.Filter{ProviderID: providerID}))
	return res, nil
}

func mergeModelInfo(existing models.Model, info ModelInfo) models.Model {
	out := existing
	if strings.TrimSpace(info.DisplayName) != "" && (out.DisplayName == "" || out.DisplayName == out.ProviderModelID) {
		out.DisplayName = info.DisplayName
	}
	if out.Description == "" {
		out.Description = SanitizeText(info.Description)
	}
	if out.ContextWindow == nil {
		out.ContextWindow = info.ContextWindow
	}
	if out.MaxOutputTokens == nil {
		out.MaxOutputTokens = info.MaxOutputTokens
	}
	if len(out.InputModalities) == 0 {
		out.InputModalities = append([]models.Modality(nil), info.InputModalities...)
	}
	if len(out.OutputModalities) == 0 {
		out.OutputModalities = append([]models.Modality(nil), info.OutputModalities...)
	}
	if out.Streaming == models.SupportUnknown {
		out.Streaming = supportOrUnknown(info.Streaming)
	}
	if out.ToolCalls == models.SupportUnknown {
		out.ToolCalls = supportOrUnknown(info.ToolCalls)
	}
	if out.StructuredOutput == models.SupportUnknown {
		out.StructuredOutput = supportOrUnknown(info.StructuredOutput)
	}
	if out.Reasoning == models.SupportUnknown {
		out.Reasoning = supportOrUnknown(info.Reasoning)
	}
	if out.Pricing == nil {
		out.Pricing = info.Pricing
	}
	if out.RateLimits == nil {
		out.RateLimits = info.RateLimits
	}
	return out
}

func supportOrUnknown(s models.Support) models.Support {
	if s.Valid() {
		return s
	}
	return models.SupportUnknown
}

// ProviderStatuses returns configured providers, including providers whose
// initialization failed before they could be registered.
func (m *Manager) ProviderStatuses() []ProviderStatus {
	registered := make(map[string]Status)
	var order []string
	for _, st := range m.registry.List() {
		registered[st.ID] = st
		order = append(order, st.ID)
	}
	m.mu.RLock()
	for id := range m.meta {
		if _, ok := registered[id]; !ok {
			order = append(order, id)
		}
	}
	metas := make(map[string]providerMeta, len(m.meta))
	for id, meta := range m.meta {
		metas[id] = *meta
	}
	m.mu.RUnlock()

	// Include runtime-registered providers without configuration metadata.
	sort.Strings(order)
	out := make([]ProviderStatus, 0, len(order))
	for _, id := range order {
		st, ok := registered[id]
		meta := metas[id]
		if !ok {
			st = Status{
				ID:           id,
				Kind:         meta.info.Kind,
				DisplayName:  meta.info.DisplayName,
				Description:  meta.info.Description,
				Capabilities: meta.info.Capabilities,
				Enabled:      meta.enabled,
			}
			if st.DisplayName == "" {
				st.DisplayName = id
			}
			if st.Capabilities.Streaming == "" {
				st.Capabilities = unknownCapabilities()
			}
		}
		out = append(out, ProviderStatus{
			Status:        st,
			Endpoint:      meta.endpoint,
			ModelCount:    len(m.models.List(models.Filter{ProviderID: id})),
			LastError:     meta.lastErr,
			LastErrorKind: meta.lastErrKind,
		})
	}
	return out
}

func (m *Manager) Status(id string) (ProviderStatus, error) {
	for _, st := range m.ProviderStatuses() {
		if st.ID == id {
			return st, nil
		}
	}
	return ProviderStatus{}, fmt.Errorf("%w: %s", ErrProviderNotFound, id)
}

// SetProviderEnabled toggles a registered provider. The registry refuses the
// change while requests are in flight so a provider is never closed underneath
// active work.
func (m *Manager) SetProviderEnabled(id string, enabled bool) error {
	if err := m.registry.SetEnabled(id, enabled); err != nil {
		return err
	}
	m.mu.Lock()
	if meta := m.meta[id]; meta != nil {
		meta.enabled = enabled
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) Close(ctx context.Context) error { return m.registry.Close(ctx) }

func (m *Manager) setMeta(cfg config.ProviderConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meta := &providerMeta{
		info: ProviderInfo{
			ID:           cfg.ID,
			Kind:         cfg.Type,
			DisplayName:  cfg.DisplayName,
			Description:  cfg.Description,
			Capabilities: unknownCapabilities(),
		},
		endpoint:      SanitizeEndpoint(cfg.Endpoint),
		healthTimeout: cfg.HealthTimeoutOrDefault(),
		enabled:       cfg.IsEnabled(),
	}
	if meta.info.DisplayName == "" {
		meta.info.DisplayName = cfg.ID
	}
	m.meta[cfg.ID] = meta
}

func (m *Manager) updateInfo(id string, info ProviderInfo, cfg config.ProviderConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meta := m.meta[id]
	if meta == nil {
		meta = &providerMeta{}
		m.meta[id] = meta
	}
	meta.info = info
	if meta.info.DisplayName == "" {
		meta.info.DisplayName = cfg.DisplayName
	}
	if meta.info.DisplayName == "" {
		meta.info.DisplayName = id
	}
	meta.endpoint = SanitizeEndpoint(cfg.Endpoint)
	meta.healthTimeout = cfg.HealthTimeoutOrDefault()
}

func (m *Manager) setError(id string, err error) {
	if err == nil {
		return
	}
	kind := KindInvalidConfig
	var pe *Error
	if errors.As(err, &pe) {
		kind = pe.Kind
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	meta := m.meta[id]
	if meta == nil {
		meta = &providerMeta{info: ProviderInfo{ID: id, Kind: "", DisplayName: id, Capabilities: unknownCapabilities()}}
		m.meta[id] = meta
	}
	meta.lastErr = SanitizeText(err.Error())
	meta.lastErrKind = kind
}

func (m *Manager) clearError(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if meta := m.meta[id]; meta != nil {
		meta.lastErr = ""
		meta.lastErrKind = ""
	}
}

func (m *Manager) healthTimeout(id string) time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if meta := m.meta[id]; meta != nil && meta.healthTimeout > 0 {
		return meta.healthTimeout
	}
	return 5 * time.Second
}

func unknownCapabilities() ProviderCapabilities {
	return ProviderCapabilities{
		Streaming:        models.SupportUnknown,
		ToolCalls:        models.SupportUnknown,
		StructuredOutput: models.SupportUnknown,
		ModelListing:     models.SupportUnknown,
		HealthCheck:      models.SupportUnknown,
	}
}

func isFatalConfigError(err error) bool {
	return errors.Is(err, ErrDuplicateProvider) ||
		errors.Is(err, ErrInvalidProvider) ||
		errors.Is(err, models.ErrDuplicate)
}

func modelFromConfig(providerID string, mc config.ModelConfig) (models.Model, error) {
	id := mc.ID
	if id == "" {
		var err error
		id, err = deriveModelID(providerID, mc.ProviderModelID)
		if err != nil {
			return models.Model{}, err
		}
	}
	if !models.ValidID(id) {
		return models.Model{}, fmt.Errorf("model id %q is invalid", id)
	}
	display := mc.DisplayName
	if display == "" {
		display = mc.ProviderModelID
	}
	record := models.Model{
		ID:               id,
		ProviderID:       providerID,
		ProviderModelID:  mc.ProviderModelID,
		DisplayName:      display,
		Description:      mc.Description,
		ContextWindow:    mc.ContextWindow,
		MaxOutputTokens:  mc.MaxOutputTokens,
		InputModalities:  convertModalities(mc.InputModalities),
		OutputModalities: convertModalities(mc.OutputModalities),
		Streaming:        models.SupportFromBool(mc.Streaming),
		ToolCalls:        models.SupportFromBool(mc.ToolCalls),
		StructuredOutput: models.SupportFromBool(mc.StructuredOutput),
		Reasoning:        models.SupportFromBool(mc.Reasoning),
		Availability:     models.AvailabilityUnknown,
		Tags:             append([]string(nil), mc.Tags...),
		Enabled:          true,
	}
	if mc.Pricing != nil {
		record.Pricing = &models.Pricing{
			InputPerMillion:      mc.Pricing.InputPerMillion,
			OutputPerMillion:     mc.Pricing.OutputPerMillion,
			CacheReadPerMillion:  mc.Pricing.CacheReadPerMillion,
			CacheWritePerMillion: mc.Pricing.CacheWritePerMillion,
			Currency:             mc.Pricing.Currency,
		}
	}
	if mc.RateLimits != nil {
		record.RateLimits = &models.RateLimits{
			RequestsPerMinute:  mc.RateLimits.RequestsPerMinute,
			TokensPerMinute:    mc.RateLimits.TokensPerMinute,
			ConcurrentRequests: mc.RateLimits.ConcurrentRequests,
		}
	}
	if err := record.Validate(); err != nil {
		return models.Model{}, fmt.Errorf("model %q: %w", id, err)
	}
	return record, nil
}

func modelInfoFromConfig(mc config.ModelConfig) ModelInfo {
	display := mc.DisplayName
	if display == "" {
		display = mc.ProviderModelID
	}
	info := ModelInfo{
		ProviderModelID:  mc.ProviderModelID,
		DisplayName:      display,
		Description:      mc.Description,
		ContextWindow:    mc.ContextWindow,
		MaxOutputTokens:  mc.MaxOutputTokens,
		InputModalities:  convertModalities(mc.InputModalities),
		OutputModalities: convertModalities(mc.OutputModalities),
		Streaming:        models.SupportFromBool(mc.Streaming),
		ToolCalls:        models.SupportFromBool(mc.ToolCalls),
		StructuredOutput: models.SupportFromBool(mc.StructuredOutput),
		Reasoning:        models.SupportFromBool(mc.Reasoning),
	}
	if mc.Pricing != nil {
		info.Pricing = &models.Pricing{
			InputPerMillion:      mc.Pricing.InputPerMillion,
			OutputPerMillion:     mc.Pricing.OutputPerMillion,
			CacheReadPerMillion:  mc.Pricing.CacheReadPerMillion,
			CacheWritePerMillion: mc.Pricing.CacheWritePerMillion,
			Currency:             mc.Pricing.Currency,
		}
	}
	if mc.RateLimits != nil {
		info.RateLimits = &models.RateLimits{
			RequestsPerMinute:  mc.RateLimits.RequestsPerMinute,
			TokensPerMinute:    mc.RateLimits.TokensPerMinute,
			ConcurrentRequests: mc.RateLimits.ConcurrentRequests,
		}
	}
	return info
}

func convertModalities(in []string) []models.Modality {
	if len(in) == 0 {
		return nil
	}
	out := make([]models.Modality, 0, len(in))
	for _, m := range in {
		out = append(out, models.Modality(m))
	}
	return out
}

// deriveModelID builds a stable LoveCode identity. If the provider model ID is
// not URL-safe, it is sanitized and a short hash preserves uniqueness.
func deriveModelID(providerID, providerModelID string) (string, error) {
	upstream := strings.TrimSpace(providerModelID)
	if upstream == "" {
		return "", errors.New("provider_model_id must not be empty")
	}
	candidate := providerID + ":" + upstream
	if models.ValidID(candidate) {
		return candidate, nil
	}
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '_', r == ':', r == '/', r == '-':
			return r
		default:
			return '_'
		}
	}, upstream)
	clean = strings.Trim(clean, "._:/-")
	if clean == "" {
		clean = "model"
	}
	if clean[0] < 'A' || (clean[0] > 'Z' && clean[0] < 'a') || clean[0] > 'z' {
		if clean[0] < '0' || clean[0] > '9' {
			clean = "m" + clean
		}
	}
	sum := sha256.Sum256([]byte(upstream))
	suffix := hex.EncodeToString(sum[:4])
	if len(clean) > 180 {
		clean = clean[:180]
	}
	candidate = providerID + ":" + clean + "-" + suffix
	if !models.ValidID(candidate) {
		return "", fmt.Errorf("cannot derive a valid model id from provider model id %q", providerModelID)
	}
	return candidate, nil
}
