package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lovecode/lovecode/internal/security"
)

// Kind is a normalized provider error category.
type Kind string

const (
	KindAuthentication    Kind = "authentication"
	KindInvalidConfig     Kind = "invalid_configuration"
	KindInvalidRequest    Kind = "invalid_request"
	KindUnsupported       Kind = "unsupported_feature"
	KindModelNotFound     Kind = "model_not_found"
	KindUnavailable       Kind = "provider_unavailable"
	KindRateLimited       Kind = "rate_limited"
	KindQuotaExceeded     Kind = "quota_exceeded"
	KindTimeout           Kind = "timeout"
	KindConnection        Kind = "connection_failure"
	KindCanceled          Kind = "canceled"
	KindInternal          Kind = "internal_provider_error"
	KindMalformedResponse Kind = "malformed_response"
)

func (k Kind) Valid() bool {
	switch k {
	case KindAuthentication, KindInvalidConfig, KindInvalidRequest, KindUnsupported,
		KindModelNotFound, KindUnavailable, KindRateLimited, KindQuotaExceeded,
		KindTimeout, KindConnection, KindCanceled, KindInternal, KindMalformedResponse:
		return true
	default:
		return false
	}
}

const maxErrorMessage = 512

// Error is the normalized error returned by every adapter. Provider-specific
// SDK errors never escape the provider layer.
//
// The unexported cause is available through errors.Unwrap for diagnostics, but
// is never serialized. Message is redacted and truncated before construction.
type Error struct {
	Kind              Kind          `json:"kind"`
	Message           string        `json:"message"`
	ProviderID        string        `json:"provider_id,omitempty"`
	HTTPStatus        int           `json:"http_status,omitempty"`
	ProviderCode      string        `json:"provider_code,omitempty"`
	ProviderRequestID string        `json:"provider_request_id,omitempty"`
	RetryAfter        time.Duration `json:"-"`
	cause             error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Kind))
	if e.ProviderID != "" {
		b.WriteString(" provider=")
		b.WriteString(e.ProviderID)
	}
	if e.HTTPStatus != 0 {
		fmt.Fprintf(&b, " status=%d", e.HTTPStatus)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.cause }

// IsKind reports whether err (or an error it wraps) is a provider error of kind.
func IsKind(err error, kind Kind) bool {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Kind == kind
	}
	return false
}

// NewError constructs a normalized error with a sanitized message.
func NewError(kind Kind, providerID, message string) *Error {
	if !kind.Valid() {
		kind = KindInternal
	}
	return &Error{Kind: kind, ProviderID: providerID, Message: SanitizeText(message)}
}

// KindForStatus maps an HTTP status and provider error code to a Kind.
func KindForStatus(status int, providerCode string) Kind {
	code := strings.ToLower(providerCode)
	switch status {
	case 400, 409, 413, 422:
		return KindInvalidRequest
	case 401, 403:
		return KindAuthentication
	case 404:
		return KindModelNotFound
	case 405, 501:
		return KindUnsupported
	case 408, 504:
		return KindTimeout
	case 429:
		if strings.Contains(code, "quota") || strings.Contains(code, "billing") || strings.Contains(code, "insufficient_quota") {
			return KindQuotaExceeded
		}
		return KindRateLimited
	case 502, 503, 529:
		return KindUnavailable
	case 500:
		return KindInternal
	default:
		if status >= 500 {
			return KindInternal
		}
		if status >= 400 {
			return KindInvalidRequest
		}
		return KindInternal
	}
}

// FromStatus builds an error from an HTTP response. secret, when non-empty, is
// removed from the provider message so a provider that echoes credentials
// cannot leak them through LoveCode.
func FromStatus(providerID string, status int, providerCode, message string, retryAfter time.Duration, secret string) *Error {
	kind := KindForStatus(status, providerCode)
	if strings.TrimSpace(message) == "" {
		message = fmt.Sprintf("provider returned HTTP %d", status)
	}
	return &Error{
		Kind:         kind,
		Message:      sanitizeWithSecret(message, secret),
		ProviderID:   providerID,
		HTTPStatus:   status,
		ProviderCode: SanitizeText(providerCode),
		RetryAfter:   retryAfter,
	}
}

// FromTransport normalizes failures that occur before or while reading a
// response. It deliberately does not include raw request details.
func FromTransport(ctx context.Context, providerID string, err error) *Error {
	kind := KindConnection
	message := "provider connection failed"
	var cause error = errors.New(sanitizeTransportCause(err))

	switch {
	case ctx != nil && errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		kind = KindCanceled
		message = "request canceled"
		cause = context.Canceled
	case ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		kind = KindTimeout
		message = "request timed out"
		cause = context.DeadlineExceeded
	default:
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			kind = KindTimeout
			message = "request timed out"
			cause = context.DeadlineExceeded
		}
	}
	return &Error{Kind: kind, Message: message, ProviderID: providerID, cause: cause}
}

// FromMalformed builds an error for an invalid or unexpected provider payload.
func FromMalformed(providerID, detail string) *Error {
	return &Error{Kind: KindMalformedResponse, ProviderID: providerID, Message: SanitizeText(detail)}
}

// SanitizeText redacts known secret formats, URL query strings (which may carry
// credentials in custom endpoints), control characters and truncates the result.
func SanitizeText(s string) string {
	s = security.RedactString(s)
	s = urlQueryRe.ReplaceAllString(s, "${1}?[REDACTED]")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	return truncateUTF8(s, maxErrorMessage)
}

func sanitizeWithSecret(s, secret string) string {
	if secret != "" {
		s = strings.ReplaceAll(s, secret, security.Redacted)
	}
	return SanitizeText(s)
}

func sanitizeTransportCause(err error) string {
	if err == nil {
		return "unknown transport error"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		// url.Error includes the full URL, possibly with a query string.
		if ue.Op != "" {
			return ue.Op + ": " + SanitizeText(ue.Err.Error())
		}
	}
	return SanitizeText(err.Error())
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}

var urlQueryRe = regexp.MustCompile(`(?i)(https?://[^\s?#]+)\?[^\s#]*`)

// ParseRetryAfter parses a Retry-After header (seconds or HTTP date).
func ParseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := time.ParseDuration(value + "s"); err == nil && secs >= 0 {
		return secs
	}
	// time.ParseDuration("120s") works; fall back to an integer parse for very
	// large values that ParseDuration rejects.
	var n int64
	if _, err := fmt.Sscanf(value, "%d", &n); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := httpParseTime(value); err == nil {
		d := t.Sub(now)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

// httpParseTime is split out to keep the import list in this file focused.
func httpParseTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("invalid HTTP time")
}
