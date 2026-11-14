package network

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"
)

type SafeTransportOptions struct {
	DialTimeout         time.Duration
	KeepAlive           time.Duration
	TLSHandshakeTimeout time.Duration
	Resolver            *net.Resolver
	AllowLoopback       bool
}

// NewSafeTransport constructs an http.Transport with direct IP dialing and TLS SNI preservation.
func NewSafeTransport(opts SafeTransportOptions) *http.Transport {
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 10 * time.Second
	}
	if opts.TLSHandshakeTimeout <= 0 {
		opts.TLSHandshakeTimeout = 10 * time.Second
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	dialer := &net.Dialer{
		Timeout:   opts.DialTimeout,
		KeepAlive: opts.KeepAlive,
	}

	return &http.Transport{
		Proxy: nil, // Prohibit proxy redirection from bypassing SSRF guards
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid destination address %q: %w", addr, err)
			}

			// Hostname early check
			if !opts.AllowLoopback && IsBlockedHostname(host) {
				return nil, fmt.Errorf("%w: access to host %q is blocked", ErrSSRFBlocked, host)
			}

			// Pre-dial DNS resolution: resolve host to all IPs
			ips, err := resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("dns resolution failed for %q: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no IP addresses found for host %q", host)
			}

			// Validate ALL returned IPs against SSRF rules
			for _, ipAddr := range ips {
				if opts.AllowLoopback && (ipAddr.IP.IsLoopback() || ipAddr.IP.String() == "127.0.0.1") {
					continue
				}
				if err := ValidateIP(ipAddr.IP); err != nil {
					return nil, fmt.Errorf("%w: host %q: %v", ErrSSRFBlocked, host, err)
				}
			}

			// Direct IP dialing: connect to the first safe IP
			// This eliminates DNS rebinding TOCTOU because no second DNS query is performed.
			var lastErr error
			for _, ipAddr := range ips {
				target := net.JoinHostPort(ipAddr.IP.String(), port)
				conn, err := dialer.DialContext(ctx, network, target)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, fmt.Errorf("failed connecting to host %q: %w", host, lastErr)
		},
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// ServerName is automatically set by Transport from req.URL.Hostname()
			// ensuring SNI and certificate verification succeed against the original domain.
		},
		TLSHandshakeTimeout:   opts.TLSHandshakeTimeout,
		DisableKeepAlives:     true, // Prevent connection pooling reuse across varied requests
		MaxIdleConns:          0,
		ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
