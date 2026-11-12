package providers

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// settingsParser reads a provider-specific TOML settings table. It is strict:
// unknown keys and wrong types are configuration errors rather than silently
// ignored values. This keeps provider-specific options from bypassing
// validation.
type settingsParser struct {
	providerID string
	values     map[string]any
	used       map[string]bool
	errs       []error
}

func newSettingsParser(providerID string, values map[string]any) *settingsParser {
	return &settingsParser{providerID: providerID, values: values, used: make(map[string]bool)}
}

func (p *settingsParser) raw(key string) (any, bool) {
	if p.values == nil {
		return nil, false
	}
	v, ok := p.values[key]
	if ok {
		p.used[key] = true
	}
	return v, ok
}

func (p *settingsParser) fail(key, want string, got any) {
	p.errs = append(p.errs, fmt.Errorf("provider %q settings.%s must be %s, got %T", p.providerID, key, want, got))
}

func (p *settingsParser) string(key, def string) string {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	s, ok := v.(string)
	if !ok {
		p.fail(key, "a string", v)
		return def
	}
	return s
}

func (p *settingsParser) optionalString(key string) (string, bool) {
	v, ok := p.raw(key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		p.fail(key, "a string", v)
		return "", false
	}
	return s, true
}

func (p *settingsParser) boolean(key string, def bool) bool {
	if b, ok := p.optionalBool(key); ok {
		return *b
	}
	return def
}

func (p *settingsParser) optionalBool(key string) (*bool, bool) {
	v, ok := p.raw(key)
	if !ok {
		return nil, false
	}
	b, ok := v.(bool)
	if !ok {
		p.fail(key, "a boolean", v)
		return nil, false
	}
	return &b, true
}

func (p *settingsParser) integer(key string, def int) int {
	if v, ok := p.optionalInt(key); ok {
		return *v
	}
	return def
}

func (p *settingsParser) optionalInt(key string) (*int, bool) {
	v, ok := p.raw(key)
	if !ok {
		return nil, false
	}
	var n int
	switch x := v.(type) {
	case int64:
		n = int(x)
	case int:
		n = x
	default:
		p.fail(key, "an integer", v)
		return nil, false
	}
	return &n, true
}

func (p *settingsParser) duration(key string, def time.Duration) time.Duration {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	s, ok := v.(string)
	if !ok {
		p.fail(key, "a duration string such as \"500ms\"", v)
		return def
	}
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("provider %q settings.%s: %w", p.providerID, key, err))
		return def
	}
	return d
}

func (p *settingsParser) done() error {
	unknown := make([]string, 0)
	for k := range p.values {
		if !p.used[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		p.errs = append(p.errs, fmt.Errorf("provider %q has unknown settings: %s", p.providerID, strings.Join(unknown, ", ")))
	}
	return errors.Join(p.errs...)
}
