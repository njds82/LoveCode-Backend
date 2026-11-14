package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultMaxRedirects     = 3
	DefaultMaxResponseBytes = 2 * 1024 * 1024 // 2 MB
	DefaultTimeout          = 15 * time.Second
)

type RequestArgs struct {
	URL              string            `json:"url"`
	Method           string            `json:"method,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	Body             string            `json:"body,omitempty"`
	TimeoutSeconds   int               `json:"timeout_seconds,omitempty"`
	MaxResponseBytes int64             `json:"max_response_bytes,omitempty"`
}

type ResponseData struct {
	StatusCode    int               `json:"status_code"`
	Status        string            `json:"status"`
	Headers       map[string]string `json:"headers"`
	Body          string            `json:"body"`
	Truncated     bool              `json:"truncated"`
	ContentLength int64             `json:"content_length"`
	DurationMS    int64             `json:"duration_ms"`
}

type ClientOptions struct {
	MaxRedirects  int
	AllowLoopback bool
}

type Client struct {
	httpClient    *http.Client
	allowLoopback bool
}

func NewClient(maxRedirects int) *Client {
	return NewClientWithOptions(ClientOptions{MaxRedirects: maxRedirects})
}

func NewClientWithOptions(opts ClientOptions) *Client {
	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = DefaultMaxRedirects
	}

	transport := NewSafeTransport(SafeTransportOptions{
		AllowLoopback: opts.AllowLoopback,
	})

	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			// Verify protocol
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect scheme %q is not permitted", req.URL.Scheme)
			}
			// Verify host
			host := req.URL.Hostname()
			if host == "" {
				return errors.New("redirect URL missing hostname")
			}
			if !opts.AllowLoopback && IsBlockedHostname(host) {
				return fmt.Errorf("%w: redirect to blocked host %q", ErrSSRFBlocked, host)
			}
			if ip := net.ParseIP(host); ip != nil {
				if opts.AllowLoopback && (ip.IsLoopback() || ip.String() == "127.0.0.1") {
					return nil
				}
				if err := ValidateIP(ip); err != nil {
					return fmt.Errorf("%w: redirect to blocked IP: %v", ErrSSRFBlocked, err)
				}
			}
			return nil
		},
	}

	return &Client{
		httpClient:    client,
		allowLoopback: opts.AllowLoopback,
	}
}

func (c *Client) Execute(ctx context.Context, args RequestArgs) (*ResponseData, error) {
	if strings.TrimSpace(args.URL) == "" {
		return nil, errors.New("url parameter is required")
	}

	u, err := url.Parse(args.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported protocol scheme %q: only http and https are allowed", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return nil, errors.New("url missing hostname")
	}

	// Early host and IP check before request dispatch
	if !c.allowLoopback && IsBlockedHostname(host) {
		return nil, fmt.Errorf("%w: access to host %q is blocked", ErrSSRFBlocked, host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !c.allowLoopback || (!ip.IsLoopback() && ip.String() != "127.0.0.1") {
			if err := ValidateIP(ip); err != nil {
				return nil, err
			}
		}
	}

	method := strings.ToUpper(strings.TrimSpace(args.Method))
	if method == "" {
		method = http.MethodGet
	}

	var reqBody io.Reader
	if args.Body != "" {
		reqBody = strings.NewReader(args.Body)
	}

	timeout := DefaultTimeout
	if args.TimeoutSeconds > 0 {
		timeout = time.Duration(args.TimeoutSeconds) * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, method, args.URL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	for k, v := range args.Headers {
		// Prevent header injection / CRLF
		cleanK := strings.TrimSpace(k)
		cleanV := strings.TrimSpace(v)
		if cleanK != "" && !strings.EqualFold(cleanK, "Host") {
			httpReq.Header.Set(cleanK, cleanV)
		}
	}

	maxBytes := int64(DefaultMaxResponseBytes)
	if args.MaxResponseBytes > 0 {
		maxBytes = args.MaxResponseBytes
	}

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	duration := time.Since(start)

	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read bounded response using io.LimitReader
	limitReader := io.LimitReader(resp.Body, maxBytes+1)
	bodyBytes, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	truncated := false
	if int64(len(bodyBytes)) > maxBytes {
		bodyBytes = bodyBytes[:maxBytes]
		truncated = true
	}

	respHeaders := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		respHeaders[k] = strings.Join(v, ", ")
	}

	return &ResponseData{
		StatusCode:    resp.StatusCode,
		Status:        resp.Status,
		Headers:       respHeaders,
		Body:          string(bodyBytes),
		Truncated:     truncated,
		ContentLength: resp.ContentLength,
		DurationMS:    duration.Milliseconds(),
	}, nil
}
