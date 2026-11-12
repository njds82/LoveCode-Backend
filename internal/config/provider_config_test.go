package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "lovecode.toml")
	content := "data_dir = " + quoteTOML(dir) + "\n" + body
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func quoteTOML(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

func TestLoadProviderConfiguration(t *testing.T) {
	path := writeConfigFile(t, `
[[providers]]
id = "openai-main"
type = "openai"
display_name = "Main OpenAI"
description = "Primary cloud provider"
endpoint = "https://api.example.test/v1"
api_key_env = "OPENAI_API_KEY"
timeout = "30s"
health_timeout = "3s"
discover_models = true
required = true
[providers.default_headers]
X-Tenant = "lovecode"
[[providers.models]]
id = "main:chat"
provider_model_id = "gpt-x"
display_name = "Chat"
description = "chat model"
context_window = 128000
max_output_tokens = 4096
input_modalities = ["text", "image"]
output_modalities = ["text"]
streaming = true
tool_calls = true
structured_output = false
reasoning = false
tags = ["fast", "vision"]
[providers.models.pricing]
input_per_million = 1.25
output_per_million = 2.5
cache_read_per_million = 0.25
cache_write_per_million = 0.5
currency = "USD"
[providers.models.rate_limits]
requests_per_minute = 60
tokens_per_minute = 100000
concurrent_requests = 4

[[providers]]
id = "local-ollama"
type = "ollama"
display_name = "Local Ollama"
enabled = false
endpoint = "http://127.0.0.1:11434"
[[providers.models]]
provider_model_id = "llama3:latest"
display_name = "Llama 3"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("providers: %+v", cfg.Providers)
	}
	p := cfg.Providers[0]
	if p.ID != "openai-main" || p.Type != "openai" || !p.Required || !p.DiscoverModels {
		t.Fatalf("provider: %+v", p)
	}
	if p.Timeout.Std() != 30*time.Second || p.HealthTimeout.Std() != 3*time.Second {
		t.Fatalf("timeouts: %+v", p)
	}
	if p.APIKeyEnv != "OPENAI_API_KEY" || p.DefaultHeaders["X-Tenant"] != "lovecode" {
		t.Fatalf("env/headers: %+v", p)
	}
	if !p.IsEnabled() {
		t.Fatal("provider should default to enabled")
	}
	if len(p.Models) != 1 {
		t.Fatalf("models: %+v", p.Models)
	}
	m := p.Models[0]
	if m.ID != "main:chat" || m.ProviderModelID != "gpt-x" || m.ContextWindow == nil || *m.ContextWindow != 128000 {
		t.Fatalf("model: %+v", m)
	}
	if m.Streaming == nil || !*m.Streaming || m.StructuredOutput == nil || *m.StructuredOutput {
		t.Fatalf("capabilities: %+v", m)
	}
	if len(m.InputModalities) != 2 || m.Tags[0] != "fast" {
		t.Fatalf("metadata: %+v", m)
	}
	if m.Pricing == nil || m.Pricing.InputPerMillion == nil || *m.Pricing.InputPerMillion != 1.25 || m.Pricing.Currency != "USD" {
		t.Fatalf("pricing: %+v", m.Pricing)
	}
	if m.RateLimits == nil || m.RateLimits.RequestsPerMinute == nil || *m.RateLimits.RequestsPerMinute != 60 {
		t.Fatalf("rate limits: %+v", m.RateLimits)
	}

	second := cfg.Providers[1]
	if second.IsEnabled() {
		t.Fatal("explicit enabled=false was not applied")
	}
	if second.APIKeyEnv != "" || second.Models[0].ID != "" {
		t.Fatalf("second provider: %+v", second)
	}
	if second.TimeoutOrDefault() != 60*time.Second {
		t.Fatalf("default timeout: %v", second.TimeoutOrDefault())
	}
}

func TestProviderConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "invalid provider id",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "Bad ID", Type: "mock"}}
			},
			wantErr: "must match",
		},
		{
			name: "duplicate provider id",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "same", Type: "mock"}, {ID: "same", Type: "mock"}}
			},
			wantErr: "duplicate provider id",
		},
		{
			name: "missing type",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p"}}
			},
			wantErr: "type must not be empty",
		},
		{
			name: "openai endpoint required",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "openai"}}
			},
			wantErr: "endpoint is required",
		},
		{
			name: "invalid endpoint scheme",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "openai", Endpoint: "ftp://example.test"}}
			},
			wantErr: "scheme must be http or https",
		},
		{
			name: "endpoint userinfo",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "openai", Endpoint: "https://user:pass@example.test"}}
			},
			wantErr: "userinfo",
		},
		{
			name: "endpoint fragment",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "openai", Endpoint: "https://example.test/#frag"}}
			},
			wantErr: "fragment",
		},
		{
			name: "invalid env name",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", APIKeyEnv: "BAD-NAME"}}
			},
			wantErr: "environment variable name",
		},
		{
			name: "negative timeout",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", Timeout: Duration(-time.Second)}}
			},
			wantErr: "timeout must not be negative",
		},
		{
			name: "managed header",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", DefaultHeaders: map[string]string{"Host": "x"}}}
			},
			wantErr: "managed by the HTTP transport",
		},
		{
			name: "newline header",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", DefaultHeaders: map[string]string{"X-Test": "a\r\nb"}}}
			},
			wantErr: "invalid value",
		},
		{
			name: "duplicate default header",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", DefaultHeaders: map[string]string{"X-Test": "a", "x-test": "b"}}}
			},
			wantErr: "duplicate default header",
		},
		{
			name: "empty provider model id",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", Models: []ModelConfig{{ID: "m"}}}}
			},
			wantErr: "provider_model_id must not be empty",
		},
		{
			name: "invalid model id",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", Models: []ModelConfig{{ID: "-bad", ProviderModelID: "x"}}}}
			},
			wantErr: "id \"-bad\" is invalid",
		},
		{
			name: "duplicate model id",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{
					{ID: "a", Type: "mock", Models: []ModelConfig{{ID: "m", ProviderModelID: "x"}}},
					{ID: "b", Type: "mock", Models: []ModelConfig{{ID: "m", ProviderModelID: "y"}}},
				}
			},
			wantErr: "duplicate model id",
		},
		{
			name: "duplicate provider model id",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", Models: []ModelConfig{
					{ProviderModelID: "x"}, {ProviderModelID: "x"},
				}}}
			},
			wantErr: "duplicate provider_model_id",
		},
		{
			name: "bad context window",
			mutate: func(c *Config) {
				zero := int64(0)
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", Models: []ModelConfig{{ProviderModelID: "x", ContextWindow: &zero}}}}
			},
			wantErr: "context_window must be positive",
		},
		{
			name: "bad modality",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", Models: []ModelConfig{{ProviderModelID: "x", InputModalities: []string{"smell"}}}}}
			},
			wantErr: "invalid modality",
		},
		{
			name: "duplicate modality",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", Models: []ModelConfig{{ProviderModelID: "x", OutputModalities: []string{"text", "text"}}}}}
			},
			wantErr: "duplicate modality",
		},
		{
			name: "empty tag",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", Models: []ModelConfig{{ProviderModelID: "x", Tags: []string{" "}}}}}
			},
			wantErr: "tags must not contain empty strings",
		},
		{
			name: "negative price",
			mutate: func(c *Config) {
				v := -1.0
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", Models: []ModelConfig{{ProviderModelID: "x", Pricing: &PricingConfig{InputPerMillion: &v}}}}}
			},
			wantErr: "must not be negative",
		},
		{
			name: "bad currency",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", Models: []ModelConfig{{ProviderModelID: "x", Pricing: &PricingConfig{Currency: "US"}}}}}
			},
			wantErr: "three-letter code",
		},
		{
			name: "bad rate limit",
			mutate: func(c *Config) {
				zero := 0
				c.Providers = []ProviderConfig{{ID: "p", Type: "mock", Models: []ModelConfig{{ProviderModelID: "x", RateLimits: &RateLimitConfig{RequestsPerMinute: &zero}}}}}
			},
			wantErr: "must be positive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestProviderValidationAcceptsMultipleOpenAICompatible(t *testing.T) {
	cfg := Defaults()
	cfg.Providers = []ProviderConfig{
		{ID: "openai", Type: "openai", Endpoint: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY"},
		{ID: "together", Type: "openai", Endpoint: "https://api.together.xyz/v1", APIKeyEnv: "TOGETHER_API_KEY"},
		{ID: "ollama", Type: "ollama"},
		{ID: "mock", Type: "mock"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid provider mix rejected: %v", err)
	}
}

func TestLoadRejectsUnknownProviderFields(t *testing.T) {
	path := writeConfigFile(t, `
[[providers]]
id = "mock"
type = "mock"
api_key = "must-not-be-stored-here"
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "strict mode") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestProviderDefaultsAndEnabled(t *testing.T) {
	var p ProviderConfig
	if !p.IsEnabled() || p.TimeoutOrDefault() != 60*time.Second || p.HealthTimeoutOrDefault() != 5*time.Second {
		t.Fatalf("defaults: %+v", p)
	}
	no := false
	p.Enabled = &no
	if p.IsEnabled() {
		t.Fatal("enabled=false ignored")
	}
	p.Timeout = Duration(2 * time.Second)
	p.HealthTimeout = Duration(time.Second)
	if p.TimeoutOrDefault() != 2*time.Second || p.HealthTimeoutOrDefault() != time.Second {
		t.Fatal("explicit timeout ignored")
	}
}
