// Package config loads and validates LoveCode's TOML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Duration is a time.Duration that unmarshals from strings such as "10s".
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }
func (d Duration) Std() time.Duration           { return time.Duration(d) }

const (
	ModeLocal  = "local"
	ModeLAN    = "lan"
	ModeRemote = "remote"
)

type Config struct {
	DataDir   string           `toml:"data_dir"`
	Server    Server           `toml:"server"`
	Logging   Logging          `toml:"logging"`
	Features  map[string]bool  `toml:"features"`
	Providers []ProviderConfig `toml:"providers"`
	Agents    AgentConfig      `toml:"agents"`
}

type Server struct {
	Mode              string   `toml:"mode"`
	Listen            string   `toml:"listen"`
	TLSCert           string   `toml:"tls_cert"`
	TLSKey            string   `toml:"tls_key"`
	ReadHeaderTimeout Duration `toml:"read_header_timeout"`
	ShutdownTimeout   Duration `toml:"shutdown_timeout"`
	RateLimitPerSec   float64  `toml:"rate_limit_per_sec"`
	RateLimitBurst    int      `toml:"rate_limit_burst"`
	MaxBodyBytes      int64    `toml:"max_body_bytes"`
}

type Logging struct {
	Level  string `toml:"level"`  // debug|info|warn|error
	Format string `toml:"format"` // text|json
}

// AgentConfig configures Agent Engine scheduling and resource limits.
type AgentConfig struct {
	MaxConcurrentRuns    int      `toml:"max_concurrent_runs"`
	MaxConcurrentAgents  int      `toml:"max_concurrent_agents"`
	MaxParallelPerRun    int      `toml:"max_parallel_per_run"`
	MaxTeamSize          int      `toml:"max_team_size"`
	MaxDelegationDepth   int      `toml:"max_delegation_depth"`
	MaxDynamicAgents     int      `toml:"max_dynamic_agents"`
	MaxTasksPerRun       int      `toml:"max_tasks_per_run"`
	MaxExecutionDuration Duration `toml:"max_execution_duration"`
	MaxRequestsPerTask   int      `toml:"max_requests_per_task"`
	QueueLimit           int      `toml:"queue_limit"`
}

// ProviderConfig describes one configured provider. Credentials are never
// stored here: APIKeyEnv names an environment variable that is resolved at
// startup.
type ProviderConfig struct {
	ID             string            `toml:"id"`
	Type           string            `toml:"type"` // openai | ollama | mock (adapters may add more)
	DisplayName    string            `toml:"display_name"`
	Description    string            `toml:"description"`
	Enabled        *bool             `toml:"enabled"` // nil means enabled
	Required       bool              `toml:"required"`
	Endpoint       string            `toml:"endpoint"`
	APIKeyEnv      string            `toml:"api_key_env"`
	Timeout        Duration          `toml:"timeout"`
	HealthTimeout  Duration          `toml:"health_timeout"`
	DefaultHeaders map[string]string `toml:"default_headers"`
	DiscoverModels bool              `toml:"discover_models"`
	Models         []ModelConfig     `toml:"models"`
	Settings       map[string]any    `toml:"settings"`
}

// IsEnabled applies the enabled-by-default rule.
func (p ProviderConfig) IsEnabled() bool { return p.Enabled == nil || *p.Enabled }

func (p ProviderConfig) TimeoutOrDefault() time.Duration {
	if p.Timeout.Std() <= 0 {
		return 60 * time.Second
	}
	return p.Timeout.Std()
}

func (p ProviderConfig) HealthTimeoutOrDefault() time.Duration {
	if p.HealthTimeout.Std() <= 0 {
		return 5 * time.Second
	}
	return p.HealthTimeout.Std()
}

// ModelConfig is a configured model mapping. ID is the stable LoveCode
// identity; ProviderModelID is the identifier sent to the provider. When ID is
// empty the manager derives "<provider-id>:<provider-model-id>".
type ModelConfig struct {
	ID               string           `toml:"id"`
	ProviderModelID  string           `toml:"provider_model_id"`
	DisplayName      string           `toml:"display_name"`
	Description      string           `toml:"description"`
	ContextWindow    *int64           `toml:"context_window"`
	MaxOutputTokens  *int64           `toml:"max_output_tokens"`
	InputModalities  []string         `toml:"input_modalities"`
	OutputModalities []string         `toml:"output_modalities"`
	Streaming        *bool            `toml:"streaming"`
	ToolCalls        *bool            `toml:"tool_calls"`
	StructuredOutput *bool            `toml:"structured_output"`
	Reasoning        *bool            `toml:"reasoning"`
	Tags             []string         `toml:"tags"`
	Pricing          *PricingConfig   `toml:"pricing"`
	RateLimits       *RateLimitConfig `toml:"rate_limits"`
}

// PricingConfig is optional pricing metadata, per million tokens.
type PricingConfig struct {
	InputPerMillion      *float64 `toml:"input_per_million"`
	OutputPerMillion     *float64 `toml:"output_per_million"`
	CacheReadPerMillion  *float64 `toml:"cache_read_per_million"`
	CacheWritePerMillion *float64 `toml:"cache_write_per_million"`
	Currency             string   `toml:"currency"`
}

// RateLimitConfig is optional declared rate metadata.
type RateLimitConfig struct {
	RequestsPerMinute  *int `toml:"requests_per_minute"`
	TokensPerMinute    *int `toml:"tokens_per_minute"`
	ConcurrentRequests *int `toml:"concurrent_requests"`
}

var (
	providerIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	modelIDRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`)
	envNameRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

var validModalities = map[string]bool{
	"text": true, "image": true, "audio": true, "video": true,
}

// KnownFeatures lists feature flags from the specification. Unknown flags are
// rejected so that typos cannot silently leave a feature in an unexpected state.
// A flag being known does NOT mean the feature exists yet; see docs/STATUS.md.
var KnownFeatures = map[string]bool{
	"providers": true, "router": true, "agents": true, "parallel_agents": true,
	"web_search": true, "filesystem_tools": true, "terminal_tools": true,
	"process_tools": true, "media": true, "mcp": true, "skills": true,
	"memory": true, "semantic_cache": true, "automation": true,
	"device_nodes": true, "remote_access": true, "analytics": true,
}

func defaultDataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "lovecode")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "share", "lovecode")
	}
	return "./data"
}

// Defaults returns the secure-by-default configuration: loopback only.
func Defaults() Config {
	return Config{
		DataDir: defaultDataDir(),
		Server: Server{
			Mode:              ModeLocal,
			Listen:            "127.0.0.1:7878",
			ReadHeaderTimeout: Duration(10 * time.Second),
			ShutdownTimeout:   Duration(15 * time.Second),
			RateLimitPerSec:   20,
			RateLimitBurst:    40,
			MaxBodyBytes:      4 << 20,
		},
		Logging:  Logging{Level: "info", Format: "text"},
		Features: map[string]bool{},
		Agents: AgentConfig{
			MaxConcurrentRuns:    8,
			MaxConcurrentAgents:  4,
			MaxParallelPerRun:    4,
			MaxTeamSize:          16,
			MaxDelegationDepth:   3,
			MaxDynamicAgents:     4,
			MaxTasksPerRun:       50,
			MaxExecutionDuration: Duration(10 * time.Minute),
			MaxRequestsPerTask:   5,
			QueueLimit:           32,
		},
	}
}

// Load reads path (if non-empty) over the defaults, then validates.
// Unknown keys are an error.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read config: %w", err)
		}
		dec := toml.NewDecoder(bytes.NewReader(raw)).DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return cfg, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if cfg.Features == nil {
		cfg.Features = map[string]bool{}
	}
	return cfg, cfg.Validate()
}

func (c Config) DBPath() string { return filepath.Join(c.DataDir, "lovecode.db") }

func (c Config) FeatureEnabled(name string) bool { return c.Features[name] }

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (c Config) Validate() error {
	var errs []error
	if c.DataDir == "" {
		errs = append(errs, errors.New("data_dir must not be empty"))
	}
	host, _, err := net.SplitHostPort(c.Server.Listen)
	if err != nil {
		errs = append(errs, fmt.Errorf("server.listen %q: %w", c.Server.Listen, err))
	}
	switch c.Server.Mode {
	case ModeLocal:
		if err == nil && !isLoopbackHost(host) {
			errs = append(errs, fmt.Errorf("server.mode=local requires a loopback listen address, got %q", host))
		}
	case ModeLAN:
	case ModeRemote:
		if c.Server.TLSCert == "" || c.Server.TLSKey == "" {
			errs = append(errs, errors.New("server.mode=remote requires tls_cert and tls_key"))
		}
	default:
		errs = append(errs, fmt.Errorf("server.mode must be local, lan or remote, got %q", c.Server.Mode))
	}
	if (c.Server.TLSCert == "") != (c.Server.TLSKey == "") {
		errs = append(errs, errors.New("tls_cert and tls_key must be set together"))
	}
	if c.Server.RateLimitPerSec <= 0 || c.Server.RateLimitBurst <= 0 {
		errs = append(errs, errors.New("rate limit values must be positive"))
	}
	if c.Server.MaxBodyBytes <= 0 {
		errs = append(errs, errors.New("server.max_body_bytes must be positive"))
	}
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("logging.level invalid: %q", c.Logging.Level))
	}
	switch c.Logging.Format {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("logging.format invalid: %q", c.Logging.Format))
	}
	for k := range c.Features {
		if !KnownFeatures[k] {
			errs = append(errs, fmt.Errorf("unknown feature flag %q", k))
		}
	}
	if c.Agents.MaxConcurrentRuns < 0 || c.Agents.MaxConcurrentAgents < 0 ||
		c.Agents.MaxParallelPerRun < 0 || c.Agents.MaxTeamSize < 0 ||
		c.Agents.MaxDelegationDepth < 0 || c.Agents.MaxDynamicAgents < 0 ||
		c.Agents.MaxTasksPerRun < 0 || c.Agents.MaxRequestsPerTask < 0 ||
		c.Agents.QueueLimit < 0 {
		errs = append(errs, errors.New("agents limits must be non-negative"))
	}

	providerIDs := make(map[string]bool, len(c.Providers))
	modelIDs := make(map[string]bool)
	for i := range c.Providers {
		p := c.Providers[i]
		prefix := fmt.Sprintf("providers[%d]", i)
		if err := p.validate(prefix); err != nil {
			errs = append(errs, err)
		}
		if p.ID != "" && providerIDs[p.ID] {
			errs = append(errs, fmt.Errorf("%s: duplicate provider id %q", prefix, p.ID))
		}
		providerIDs[p.ID] = true
		for j := range p.Models {
			m := p.Models[j]
			if m.ID != "" && modelIDs[m.ID] {
				errs = append(errs, fmt.Errorf("%s.models[%d]: duplicate model id %q", prefix, j, m.ID))
			}
			if m.ID != "" {
				modelIDs[m.ID] = true
			}
		}
	}
	return errors.Join(errs...)
}

func (p ProviderConfig) validate(prefix string) error {
	if prefix == "" {
		prefix = "provider"
	}
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", prefix, fmt.Sprintf(format, args...)))
	}
	if !providerIDRe.MatchString(p.ID) {
		add("id %q must match %s", p.ID, providerIDRe)
	}
	if strings.TrimSpace(p.Type) == "" {
		add("type must not be empty")
	}
	if len(p.DisplayName) > 256 || len(p.Description) > 4096 {
		add("display_name or description is too long")
	}
	if p.APIKeyEnv != "" && !envNameRe.MatchString(p.APIKeyEnv) {
		add("api_key_env %q is not a valid environment variable name", p.APIKeyEnv)
	}
	if p.Timeout.Std() < 0 {
		add("timeout must not be negative")
	}
	if p.HealthTimeout.Std() < 0 {
		add("health_timeout must not be negative")
	}
	if p.Endpoint != "" {
		u, err := url.Parse(p.Endpoint)
		if err != nil {
			add("endpoint is not a valid URL: %v", err)
		} else {
			if u.Scheme != "http" && u.Scheme != "https" {
				add("endpoint scheme must be http or https")
			}
			if u.Host == "" {
				add("endpoint must include a host")
			}
			if u.User != nil {
				add("endpoint must not contain userinfo; use api_key_env")
			}
			if u.Fragment != "" {
				add("endpoint must not contain a fragment")
			}
		}
	} else if p.Type == "openai" {
		add("endpoint is required for an openai-compatible provider")
	}
	if err := validateHeaders(p.DefaultHeaders); err != nil {
		add("%v", err)
	}

	providerModels := make(map[string]bool, len(p.Models))
	for j := range p.Models {
		m := p.Models[j]
		if err := m.validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s.models[%d]: %w", prefix, j, err))
		}
		if m.ProviderModelID != "" && providerModels[m.ProviderModelID] {
			errs = append(errs, fmt.Errorf("%s.models[%d]: duplicate provider_model_id %q", prefix, j, m.ProviderModelID))
		}
		providerModels[m.ProviderModelID] = true
	}
	return errors.Join(errs...)
}

func (m ModelConfig) validate() error {
	var errs []error
	if strings.TrimSpace(m.ProviderModelID) == "" {
		errs = append(errs, errors.New("provider_model_id must not be empty"))
	}
	if m.ID != "" && !modelIDRe.MatchString(m.ID) {
		errs = append(errs, fmt.Errorf("id %q is invalid", m.ID))
	}
	if m.ContextWindow != nil && *m.ContextWindow <= 0 {
		errs = append(errs, errors.New("context_window must be positive"))
	}
	if m.MaxOutputTokens != nil && *m.MaxOutputTokens <= 0 {
		errs = append(errs, errors.New("max_output_tokens must be positive"))
	}
	for name, list := range map[string][]string{"input_modalities": m.InputModalities, "output_modalities": m.OutputModalities} {
		seen := map[string]bool{}
		for _, mod := range list {
			if !validModalities[mod] {
				errs = append(errs, fmt.Errorf("%s contains invalid modality %q", name, mod))
			}
			if seen[mod] {
				errs = append(errs, fmt.Errorf("%s contains duplicate modality %q", name, mod))
			}
			seen[mod] = true
		}
	}
	for _, tag := range m.Tags {
		if strings.TrimSpace(tag) == "" {
			errs = append(errs, errors.New("tags must not contain empty strings"))
		}
	}
	if m.Pricing != nil {
		for name, v := range map[string]*float64{
			"input_per_million":       m.Pricing.InputPerMillion,
			"output_per_million":      m.Pricing.OutputPerMillion,
			"cache_read_per_million":  m.Pricing.CacheReadPerMillion,
			"cache_write_per_million": m.Pricing.CacheWritePerMillion,
		} {
			if v != nil && *v < 0 {
				errs = append(errs, fmt.Errorf("pricing.%s must not be negative", name))
			}
		}
		if m.Pricing.Currency != "" && len(m.Pricing.Currency) != 3 {
			errs = append(errs, errors.New("pricing.currency must be a three-letter code"))
		}
	}
	if m.RateLimits != nil {
		for name, v := range map[string]*int{
			"requests_per_minute": m.RateLimits.RequestsPerMinute,
			"tokens_per_minute":   m.RateLimits.TokensPerMinute,
			"concurrent_requests": m.RateLimits.ConcurrentRequests,
		} {
			if v != nil && *v <= 0 {
				errs = append(errs, fmt.Errorf("rate_limits.%s must be positive", name))
			}
		}
	}
	return errors.Join(errs...)
}

func validateHeaders(h map[string]string) error {
	seen := make(map[string]bool, len(h))
	for k, v := range h {
		if k == "" || textproto.CanonicalMIMEHeaderKey(k) == "" {
			return fmt.Errorf("invalid default header name %q", k)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(k)
		if seen[canonical] {
			return fmt.Errorf("duplicate default header %q", canonical)
		}
		seen[canonical] = true
		switch canonical {
		case "Host", "Content-Length", "Transfer-Encoding", "Connection", "Upgrade":
			return fmt.Errorf("default header %q is managed by the HTTP transport", canonical)
		}
		if len(v) > 8<<10 || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("invalid value for default header %q", k)
		}
	}
	return nil
}
