package providers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

const (
	maxErrorBodyBytes  = 64 << 10
	maxJSONResponse    = 16 << 20
	maxHeaderValueSize = 8 << 10
)

func newHTTPClient(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 16
	tr.MaxIdleConnsPerHost = 4
	tr.IdleConnTimeout = 90 * time.Second
	return &http.Client{Transport: tr, Timeout: timeout}
}

// httpClientFor returns the client an adapter should use. An injected client is
// shallow-copied so the caller's timeout configuration is not mutated. owned
// reports whether the adapter may close idle connections on shutdown.
func httpClientFor(injected *http.Client, timeout time.Duration) (client *http.Client, owned bool) {
	if injected == nil {
		return newHTTPClient(timeout), true
	}
	cp := *injected
	if timeout > 0 {
		cp.Timeout = timeout
	}
	return &cp, false
}

func closeHTTPClient(c *http.Client, owned bool) {
	if owned && c != nil {
		c.CloseIdleConnections()
	}
}

// joinURL appends a configured path to a base URL while preserving the base
// query string (for services that require an api-version parameter).
func joinURL(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	u.RawPath = ""
	u.Fragment = ""
	return u.String(), nil
}

// ValidateEndpoint checks an HTTP endpoint without retaining credentials.
func ValidateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("endpoint is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("endpoint scheme must be http or https")
	}
	if u.Host == "" {
		return errors.New("endpoint must include a host")
	}
	if u.User != nil {
		return errors.New("endpoint must not contain userinfo; use api_key_env")
	}
	if u.Fragment != "" {
		return errors.New("endpoint must not contain a fragment")
	}
	return nil
}

// SanitizeEndpoint returns scheme://host/path with userinfo, query and fragment
// removed. Query strings may contain credentials and are never exposed.
func SanitizeEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

// ValidateHeaders rejects headers that cannot be set safely or that would break
// HTTP framing.
func ValidateHeaders(h map[string]string) error {
	seen := make(map[string]bool, len(h))
	for k, v := range h {
		if k == "" || textproto.CanonicalMIMEHeaderKey(k) == "" {
			return fmt.Errorf("invalid header name %q", k)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(k)
		if seen[canonical] {
			return fmt.Errorf("duplicate header %q (header names are case-insensitive)", canonical)
		}
		seen[canonical] = true
		switch canonical {
		case "Host", "Content-Length", "Transfer-Encoding", "Connection", "Upgrade":
			return fmt.Errorf("default header %q is managed by the HTTP transport", canonical)
		}
		if len(v) > maxHeaderValueSize || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("invalid value for header %q", k)
		}
	}
	return nil
}

func applyHeaders(req *http.Request, headers map[string]string) {
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("response exceeds %d byte limit", limit)
	}
	return b, nil
}

func retryAfterFromResponse(resp *http.Response) time.Duration {
	return ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
}

func providerRequestID(resp *http.Response) string {
	for _, h := range []string{"X-Request-ID", "X-Request-Id", "Request-Id", "OpenAI-Request-ID"} {
		if v := resp.Header.Get(h); v != "" {
			return SanitizeText(v)
		}
	}
	return ""
}

// drainAndClose releases a response body after a limited read. Callers that
// return early must use it to keep connections reusable.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
	_ = resp.Body.Close()
}
