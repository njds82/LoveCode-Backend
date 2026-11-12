package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/models"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func boolPtr(v bool) *bool { return &v }

func int64Ptr(v int64) *int64 { return &v }

func testManager(opts ManagerOptions) *Manager {
	if opts.Factories == nil {
		opts.Factories = DefaultFactories()
	}
	if opts.LookupEnv == nil {
		opts.LookupEnv = func(string) (string, bool) { return "", false }
	}
	return NewManagerWithOptions(NewRegistry(), models.NewRegistry(), testLogger(), opts)
}

func TestManagerLoadConfiguredProviderAndModels(t *testing.T) {
	m := testManager(ManagerOptions{})
	cfg := config.ProviderConfig{
		ID:          "mock",
		Type:        "mock",
		DisplayName: "Mock Provider",
		Endpoint:    "http://example.test/v1?token=top-secret",
		Models: []config.ModelConfig{{
			ID:               "mock:chat",
			ProviderModelID:  "chat-1",
			DisplayName:      "Chat One",
			Description:      "primary chat model",
			ContextWindow:    int64Ptr(8192),
			MaxOutputTokens:  int64Ptr(1024),
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Streaming:        boolPtr(true),
			ToolCalls:        boolPtr(true),
			StructuredOutput: boolPtr(false),
			Reasoning:        boolPtr(false),
			Tags:             []string{"fast", "chat"},
			Pricing: &config.PricingConfig{
				InputPerMillion:  floatPtr(1.5),
				OutputPerMillion: floatPtr(2.5),
				Currency:         "USD",
			},
			RateLimits: &config.RateLimitConfig{
				RequestsPerMinute:  intPtr(60),
				ConcurrentRequests: intPtr(4),
			},
		}},
		Settings: map[string]any{
			"reply":             "hello from mock",
			"finish_reason":     "stop",
			"prompt_tokens":     3,
			"completion_tokens": 4,
		},
	}
	if err := m.Load(context.Background(), []config.ProviderConfig{cfg}); err != nil {
		t.Fatal(err)
	}
	if m.Registry().Count() != 1 {
		t.Fatalf("provider count: %d", m.Registry().Count())
	}
	p, err := m.Registry().Get("mock")
	if err != nil || p.ID() != "mock" || p.Kind() != "mock" {
		t.Fatalf("get provider: %v %v", p, err)
	}
	model, err := m.ModelRegistry().Get("mock:chat")
	if err != nil {
		t.Fatal(err)
	}
	if model.ProviderID != "mock" || model.ProviderModelID != "chat-1" {
		t.Fatalf("identity: %+v", model)
	}
	if model.Streaming != models.SupportSupported || model.StructuredOutput != models.SupportUnsupported {
		t.Fatalf("capabilities: %+v", model)
	}
	if len(model.InputModalities) != 2 || model.InputModalities[0] != models.ModalityText {
		t.Fatalf("modalities: %+v", model.InputModalities)
	}
	if model.Tags[0] != "fast" || model.Pricing == nil || *model.Pricing.InputPerMillion != 1.5 {
		t.Fatalf("metadata: %+v", model)
	}
	if model.RateLimits == nil || *model.RateLimits.RequestsPerMinute != 60 {
		t.Fatalf("rate limits: %+v", model.RateLimits)
	}
	if model.Availability != models.AvailabilityUnknown {
		t.Fatalf("availability before refresh: %s", model.Availability)
	}

	st, err := m.Status("mock")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || st.ModelCount != 1 || st.Kind != "mock" {
		t.Fatalf("status: %+v", st)
	}
	if st.Endpoint != "http://example.test/v1" {
		t.Fatalf("endpoint must be sanitized: %q", st.Endpoint)
	}
	raw, _ := json.Marshal(m.ProviderStatuses())
	if strings.Contains(string(raw), "top-secret") || strings.Contains(string(raw), "token=") {
		t.Fatalf("status leaked endpoint query: %s", raw)
	}

	resp, err := m.Registry().Chat(context.Background(), "mock", validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Text() != "hello from mock" || resp.Usage == nil || *resp.Usage.PromptTokens != 3 {
		t.Fatalf("chat: %+v", resp)
	}

	res, err := m.RefreshModels(context.Background(), "mock")
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || res.Added != 0 || res.Total != 1 {
		t.Fatalf("refresh: %+v", res)
	}
	model, err = m.ModelRegistry().Get("mock:chat")
	if err != nil {
		t.Fatal(err)
	}
	if model.Availability != models.AvailabilityAvailable {
		t.Fatalf("availability after refresh: %s", model.Availability)
	}
	if model.Tags[0] != "fast" || model.Streaming != models.SupportSupported {
		t.Fatalf("refresh overwrote operator metadata: %+v", model)
	}
}

func TestManagerRefreshAddsModelsAndPreservesState(t *testing.T) {
	factories := Factories{
		"stub": func(cfg config.ProviderConfig, _ string) (Provider, error) {
			return NewMock(MockConfig{
				ID: cfg.ID,
				Models: []ModelInfo{
					{
						ProviderModelID:  "alpha",
						DisplayName:      "Discovered Alpha",
						ContextWindow:    int64Ptr(4096),
						InputModalities:  []models.Modality{models.ModalityText},
						OutputModalities: []models.Modality{models.ModalityText},
						Streaming:        models.SupportSupported,
						ToolCalls:        models.SupportSupported,
					},
					{
						ProviderModelID: "beta/model 1",
						DisplayName:     "Beta",
						Streaming:       models.SupportUnknown,
					},
				},
			})
		},
	}
	m := testManager(ManagerOptions{Factories: factories})
	cfg := config.ProviderConfig{
		ID:   "stub",
		Type: "stub",
		Models: []config.ModelConfig{{
			ID:              "alpha-id",
			ProviderModelID: "alpha",
			ContextWindow:   int64Ptr(8192),
			DisplayName:     "Configured Alpha",
			Streaming:       boolPtr(false),
			Tags:            []string{"operator"},
		}},
	}
	if err := m.Load(context.Background(), []config.ProviderConfig{cfg}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ModelRegistry().SetEnabled("alpha-id", false); err != nil {
		t.Fatal(err)
	}

	res, err := m.RefreshModels(context.Background(), "stub")
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Updated != 1 || res.Total != 2 {
		t.Fatalf("refresh: %+v", res)
	}
	alpha, err := m.ModelRegistry().Get("alpha-id")
	if err != nil {
		t.Fatal(err)
	}
	if alpha.Enabled {
		t.Fatal("refresh re-enabled a disabled model")
	}
	if alpha.DisplayName != "Configured Alpha" || *alpha.ContextWindow != 8192 {
		t.Fatalf("configured metadata overwritten: %+v", alpha)
	}
	if alpha.Streaming != models.SupportUnsupported {
		t.Fatalf("known capability overwritten: %+v", alpha)
	}
	if alpha.Availability != models.AvailabilityAvailable || len(alpha.Tags) != 1 {
		t.Fatalf("refresh state: %+v", alpha)
	}

	discovered := m.ModelRegistry().List(models.Filter{ProviderID: "stub"})
	if len(discovered) != 2 {
		t.Fatalf("models: %+v", discovered)
	}
	var beta models.Model
	for _, model := range discovered {
		if model.ProviderModelID == "beta/model 1" {
			beta = model
		}
	}
	if beta.ID == "" || !strings.HasPrefix(beta.ID, "stub:") {
		t.Fatalf("derived id: %+v", beta)
	}
	if beta.Enabled != true || beta.Availability != models.AvailabilityAvailable {
		t.Fatalf("discovered model state: %+v", beta)
	}

	res, err = m.RefreshModels(context.Background(), "stub")
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 || res.Updated != 2 || res.Total != 2 {
		t.Fatalf("second refresh: %+v", res)
	}
}

func TestManagerOptionalFailureIsolation(t *testing.T) {
	m := testManager(ManagerOptions{})
	configs := []config.ProviderConfig{
		{ID: "broken", Type: "does-not-exist"},
		{ID: "working", Type: "mock", Settings: map[string]any{"reply": "ok"}},
	}
	if err := m.Load(context.Background(), configs); err != nil {
		t.Fatalf("optional failure must not be fatal: %v", err)
	}
	bad, err := m.Status("broken")
	if err != nil {
		t.Fatal(err)
	}
	if bad.LastError == "" || bad.LastErrorKind != KindInvalidConfig {
		t.Fatalf("broken status: %+v", bad)
	}
	if _, err := m.Registry().Get("broken"); err == nil {
		t.Fatal("failed provider must not be registered")
	}
	good, err := m.Status("working")
	if err != nil {
		t.Fatal(err)
	}
	if good.LastError != "" || !good.Enabled {
		t.Fatalf("working status: %+v", good)
	}
	if _, err := m.Registry().Chat(context.Background(), "working", validRequest()); err != nil {
		t.Fatalf("working provider unusable: %v", err)
	}
}

func TestManagerRequiredProviderFailures(t *testing.T) {
	t.Run("required provider", func(t *testing.T) {
		m := testManager(ManagerOptions{})
		err := m.Load(context.Background(), []config.ProviderConfig{{ID: "broken", Type: "does-not-exist", Required: true}})
		if err == nil || !strings.Contains(err.Error(), "broken") {
			t.Fatalf("expected required failure: %v", err)
		}
	})
	t.Run("missing env", func(t *testing.T) {
		m := testManager(ManagerOptions{})
		cfg := config.ProviderConfig{
			ID:        "remote",
			Type:      "openai",
			Endpoint:  "https://example.test/v1",
			APIKeyEnv: "LOVECODE_TEST_MISSING_KEY",
			Required:  true,
		}
		err := m.Load(context.Background(), []config.ProviderConfig{cfg})
		if err == nil || !strings.Contains(err.Error(), "LOVECODE_TEST_MISSING_KEY") {
			t.Fatalf("expected env failure: %v", err)
		}
		if strings.Contains(err.Error(), "not-the-key") {
			t.Fatalf("secret leaked: %v", err)
		}
	})
	t.Run("duplicate ids", func(t *testing.T) {
		m := testManager(ManagerOptions{})
		err := m.Load(context.Background(), []config.ProviderConfig{
			{ID: "same", Type: "mock"},
			{ID: "same", Type: "mock"},
		})
		if !errors.Is(err, ErrDuplicateProvider) {
			t.Fatalf("expected duplicate provider: %v", err)
		}
	})
	t.Run("invalid model record", func(t *testing.T) {
		m := testManager(ManagerOptions{})
		cfg := config.ProviderConfig{
			ID: "mock", Type: "mock",
			Models: []config.ModelConfig{{ID: "mock:bad", ProviderModelID: ""}},
		}
		if err := m.Load(context.Background(), []config.ProviderConfig{cfg}); err == nil {
			t.Fatal("invalid model record must fail startup")
		}
		if m.Registry().Count() != 0 {
			t.Fatal("provider was registered despite invalid model configuration")
		}
	})
}

func TestManagerEnvResolutionAndDisabledProvider(t *testing.T) {
	lookup := func(name string) (string, bool) {
		if name == "LOVECODE_TEST_KEY" {
			return "super-secret-value", true
		}
		return "", false
	}
	m := testManager(ManagerOptions{LookupEnv: lookup})
	enabled := false
	cfg := config.ProviderConfig{
		ID:        "remote",
		Type:      "openai",
		Endpoint:  "https://example.test/v1",
		APIKeyEnv: "LOVECODE_TEST_KEY",
		Enabled:   &enabled,
		Settings:  map[string]any{"skip_health_check": true},
	}
	if err := m.Load(context.Background(), []config.ProviderConfig{cfg}); err != nil {
		t.Fatal(err)
	}
	st, err := m.Status("remote")
	if err != nil {
		t.Fatal(err)
	}
	if st.Enabled {
		t.Fatal("provider should be disabled")
	}
	if st.Endpoint != "https://example.test/v1" {
		t.Fatalf("endpoint: %q", st.Endpoint)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "super-secret-value") {
		t.Fatalf("status leaked api key: %s", raw)
	}
	if _, err := m.Registry().Chat(context.Background(), "remote", validRequest()); !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("disabled provider chat: %v", err)
	}
	if err := m.TestProvider(context.Background(), "remote"); err != nil {
		t.Fatalf("test must work while disabled: %v", err)
	}
}

func TestManagerRefreshUnsupportedAndUnknown(t *testing.T) {
	m := testManager(ManagerOptions{})
	if _, err := m.RefreshModels(context.Background(), "missing"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("missing provider: %v", err)
	}
	if err := m.Load(context.Background(), []config.ProviderConfig{{ID: "mock", Type: "mock"}}); err != nil {
		t.Fatal(err)
	}
	res, err := m.RefreshModels(context.Background(), "mock")
	if err != nil || res.Total != 1 {
		t.Fatalf("refresh default mock: %+v %v", res, err)
	}
}
