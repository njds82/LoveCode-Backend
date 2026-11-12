// Package security holds token management, scope checks and secret redaction.
package security

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"sync"
)

const Redacted = "[REDACTED]"

// Redactor encapsulates secret scanning regular expressions and redaction logic.
type Redactor struct {
	bearerRe       *regexp.Regexp
	awsSecretRe    *regexp.Regexp
	urlBasicAuthRe *regexp.Regexp
	pemRe          *regexp.Regexp
	secretRes      []*regexp.Regexp
}

// NewRedactor constructs a Redactor with all standard secret patterns compiled.
func NewRedactor() *Redactor {
	return &Redactor{
		// Bearer must run before raw token patterns so "Bearer <secret>" keeps its prefix.
		bearerRe: regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`),
		// AWS Secret Access Key: key value pair pattern
		awsSecretRe: regexp.MustCompile(`(?i)(aws[_\-\s]?secret[_\-\s]?(?:access[_\-\s]?)?key[\s:="']+)[A-Za-z0-9/+=]{40}`),
		// URL basic authentication credentials (e.g. https://user:pass@host)
		urlBasicAuthRe: regexp.MustCompile(`(?i)(https?://)[^:\s/@]+:[^@\s/]+@`),
		// PEM private keys (multiline)
		pemRe: regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9_\- ]*PRIVATE KEY-----.*?-----END [A-Z0-9_\- ]*PRIVATE KEY-----`),
		secretRes: []*regexp.Regexp{
			// JWT tokens: eyJ...
			regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_=-]+`),
			// AWS Access Key ID (AKIA or ASIA prefix)
			regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
			// GitHub Personal Access Tokens (new format)
			regexp.MustCompile(`github_pat_[A-Za-z0-9_]{82,}`),
			// GitHub tokens (classic / oauth / user / refresh: ghp_, gho_, ghu_, ghs_, ghr_)
			regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{36,}\b`),
			// Slack tokens (xoxb-, xoxa-, xoxp-, xoxr-, xoxs-)
			regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`),
			// LoveCode tokens
			regexp.MustCompile(`lc_[A-Za-z0-9_-]{20,}`),
			// OpenAI / OpenRouter style
			regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),
			// Google API keys
			regexp.MustCompile(`AIza[0-9A-Za-z_-]{30,}`),
			// Groq API keys
			regexp.MustCompile(`gsk_[A-Za-z0-9]{20,}`),
			// Hugging Face tokens
			regexp.MustCompile(`hf_[A-Za-z0-9]{20,}`),
		},
	}
}

var defaultRedactor = NewRedactor()

var sensitiveKeyParts = []string{
	"key", "token", "secret", "password", "passwd",
	"authorization", "credential", "cookie",
}

// RedactString masks known secret formats using the default redactor.
// Pattern-based redaction is best-effort: it cannot catch secrets of unknown
// format. Structured fields with sensitive names are masked by RedactValue.
func RedactString(s string) string {
	return defaultRedactor.RedactString(s)
}

// RedactString masks known secret formats using this Redactor instance.
func (r *Redactor) RedactString(s string) string {
	if s == "" {
		return ""
	}
	// Prefix-preserving replacements first
	s = r.bearerRe.ReplaceAllString(s, "${1}"+Redacted)
	s = r.awsSecretRe.ReplaceAllString(s, "${1}"+Redacted)
	s = r.urlBasicAuthRe.ReplaceAllString(s, "${1}"+Redacted+"@")
	s = r.pemRe.ReplaceAllString(s, Redacted)

	// Direct secret matches
	for _, re := range r.secretRes {
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

// RedactValue returns a redacted deep copy of strings, maps and slices using the default redactor.
func RedactValue(v any) any {
	return defaultRedactor.RedactValue(v)
}

// RedactValue returns a redacted deep copy of strings, maps and slices using this Redactor instance.
func (r *Redactor) RedactValue(v any) any {
	switch x := v.(type) {
	case string:
		return r.RedactString(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if sensitiveKey(k) {
				out[k] = Redacted
			} else {
				out[k] = r.RedactValue(val)
			}
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, val := range x {
			if sensitiveKey(k) {
				out[k] = Redacted
			} else {
				out[k] = r.RedactString(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = r.RedactValue(val)
		}
		return out
	case []string:
		out := make([]string, len(x))
		for i, val := range x {
			out[i] = r.RedactString(val)
		}
		return out
	default:
		return v
	}
}

// RedactingWriter is an io.Writer that redacts secrets from streaming data.
// It buffers a sliding tail window to prevent secrets spanning chunk boundaries
// from being written unredacted.
type RedactingWriter struct {
	mu         sync.Mutex
	w          io.Writer
	redactor   *Redactor
	buf        []byte
	tailWindow int
	maxBuffer  int
	closed     bool
}

// NewRedactingWriter wraps an io.Writer with streaming secret redaction.
func NewRedactingWriter(w io.Writer) *RedactingWriter {
	return NewRedactingWriterWithRedactor(w, defaultRedactor)
}

// NewRedactingWriterWithRedactor wraps an io.Writer with a custom Redactor.
func NewRedactingWriterWithRedactor(w io.Writer, r *Redactor) *RedactingWriter {
	if r == nil {
		r = defaultRedactor
	}
	return &RedactingWriter{
		w:          w,
		redactor:   r,
		buf:        make([]byte, 0, 4096),
		tailWindow: 1024,
		maxBuffer:  64 * 1024,
	}
}

// SetBufferLimits overrides the default tailWindow (1024) and maxBuffer (64KB).
func (rw *RedactingWriter) SetBufferLimits(tailWindow, maxBuffer int) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if tailWindow > 0 {
		rw.tailWindow = tailWindow
	}
	if maxBuffer > rw.tailWindow {
		rw.maxBuffer = maxBuffer
	}
}

// Write buffers incoming bytes and flushes safe, redacted prefixes to the underlying writer.
func (rw *RedactingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.closed {
		return 0, io.ErrClosedPipe
	}
	rw.buf = append(rw.buf, p...)
	if err := rw.drain(false); err != nil {
		return 0, err
	}
	return len(p), nil
}

// drain processes buffered data. If force is true, all buffered data is redacted and written.
// If force is false, a tail window is retained to preserve tokens across chunk boundaries.
func (rw *RedactingWriter) drain(force bool) error {
	if len(rw.buf) == 0 {
		return nil
	}
	if force {
		redacted := rw.redactor.RedactString(string(rw.buf))
		rw.buf = rw.buf[:0]
		_, err := rw.w.Write([]byte(redacted))
		return err
	}

	if len(rw.buf) <= rw.tailWindow {
		return nil
	}

	safeEnd := len(rw.buf) - rw.tailWindow

	// PEM blocks can span multiple lines. Protect unclosed PEM blocks or PEM blocks
	// that would be bisected by safeEnd.
	beginIdx := bytes.LastIndex(rw.buf, []byte("-----BEGIN"))
	if beginIdx >= 0 && len(rw.buf) < rw.maxBuffer {
		endSub := bytes.Index(rw.buf[beginIdx:], []byte("-----END"))
		if endSub == -1 {
			// Unclosed BEGIN: do not flush past BEGIN
			if beginIdx < safeEnd {
				safeEnd = beginIdx
			}
		} else {
			// Find end of line after -----END ... KEY-----
			endMarker := beginIdx + endSub
			lineEnd := bytes.IndexByte(rw.buf[endMarker:], '\n')
			var pemEnd int
			if lineEnd == -1 {
				pemEnd = len(rw.buf)
			} else {
				pemEnd = endMarker + lineEnd + 1
			}
			// If safeEnd falls inside the PEM block, cut before the block so it stays intact
			if safeEnd < pemEnd && beginIdx < safeEnd {
				safeEnd = beginIdx
			}
		}
	}

	if safeEnd <= 0 {
		if len(rw.buf) >= rw.maxBuffer {
			// Buffer limit reached: must flush even if unclosed PEM block
			safeEnd = len(rw.buf) - rw.tailWindow
		} else {
			return nil
		}
	}

	// Try to split on a natural token delimiter
	flushEnd := safeEnd
	lastDelim := bytes.LastIndexAny(rw.buf[:safeEnd], "\r\n \t,;\"'=:")
	if lastDelim >= 0 {
		flushEnd = lastDelim + 1
	} else if len(rw.buf) < rw.maxBuffer {
		// No delimiter in safe window and not at max buffer: wait for delimiter
		return nil
	}

	if flushEnd <= 0 {
		return nil
	}

	redacted := rw.redactor.RedactString(string(rw.buf[:flushEnd]))
	if _, err := rw.w.Write([]byte(redacted)); err != nil {
		return err
	}

	copy(rw.buf, rw.buf[flushEnd:])
	rw.buf = rw.buf[:len(rw.buf)-flushEnd]
	return nil
}

// Flush flushes all remaining buffered bytes to the underlying writer with redaction.
func (rw *RedactingWriter) Flush() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.drain(true)
}

// Close flushes remaining data and closes the underlying writer if it implements io.Closer.
func (rw *RedactingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.closed {
		return nil
	}
	rw.closed = true
	if err := rw.drain(true); err != nil {
		return err
	}
	if closer, ok := rw.w.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
