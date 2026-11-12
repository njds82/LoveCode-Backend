// Package security holds token management, scope checks and secret redaction.
package security

import (
	"regexp"
	"strings"
)

const Redacted = "[REDACTED]"

// Bearer must run first so "Bearer <secret>" keeps its prefix.
var bearerRe = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)

var secretRes = []*regexp.Regexp{
	regexp.MustCompile(`lc_[A-Za-z0-9_-]{20,}`),  // LoveCode tokens
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),  // OpenAI / OpenRouter style
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{30,}`), // Google API keys
	regexp.MustCompile(`gsk_[A-Za-z0-9]{20,}`),   // Groq
	regexp.MustCompile(`hf_[A-Za-z0-9]{20,}`),    // Hugging Face
}

var sensitiveKeyParts = []string{"key", "token", "secret", "password", "passwd", "authorization", "credential", "cookie"}

// RedactString masks known secret formats. Pattern-based redaction is
// best-effort: it cannot catch secrets of unknown format. Structured fields
// with sensitive names are masked by RedactValue regardless of content.
func RedactString(s string) string {
	s = bearerRe.ReplaceAllString(s, "${1}"+Redacted)
	for _, re := range secretRes {
		s = re.ReplaceAllString(s, Redacted)
	}
	return s
}

func sensitiveKey(k string) bool {
	k = strings.ToLower(k)
	for _, p := range sensitiveKeyParts {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

// RedactValue returns a redacted deep copy of strings, maps and slices.
func RedactValue(v any) any {
	switch x := v.(type) {
	case string:
		return RedactString(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if sensitiveKey(k) {
				out[k] = Redacted
			} else {
				out[k] = RedactValue(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = RedactValue(val)
		}
		return out
	default:
		return v
	}
}
