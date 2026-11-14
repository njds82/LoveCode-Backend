package network

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

var ErrSSRFBlocked = errors.New("request blocked by SSRF protection")

var blockedCIDRs = []*net.IPNet{
	// IPv4 Loopback & Unspecified
	mustParseCIDR("0.0.0.0/8"),
	mustParseCIDR("127.0.0.0/8"),
	mustParseCIDR("255.255.255.255/32"),

	// RFC 1918 Private
	mustParseCIDR("10.0.0.0/8"),
	mustParseCIDR("172.16.0.0/12"),
	mustParseCIDR("192.168.0.0/16"),

	// RFC 3927 Link-Local (Includes AWS/GCP/Azure IMDS 169.254.169.254)
	mustParseCIDR("169.254.0.0/16"),

	// RFC 6598 Carrier-Grade NAT (Includes Alibaba Cloud IMDS 100.100.100.200)
	mustParseCIDR("100.64.0.0/10"),

	// RFC 5771 Multicast & Reserved
	mustParseCIDR("224.0.0.0/4"),
	mustParseCIDR("240.0.0.0/4"),

	// RFC 5737 / 2544 Documentation & Benchmarking
	mustParseCIDR("192.0.0.0/24"),
	mustParseCIDR("192.0.2.0/24"),
	mustParseCIDR("192.88.99.0/24"),
	mustParseCIDR("198.18.0.0/15"),
	mustParseCIDR("198.51.100.0/24"),
	mustParseCIDR("203.0.113.0/24"),

	// IPv6 Loopback & Unspecified
	mustParseCIDR("::1/128"),
	mustParseCIDR("::/128"),

	// IPv6 Link-Local
	mustParseCIDR("fe80::/10"),

	// IPv6 Unique Local Address (RFC 4193) & IMDS (fd00:ec2::254/128)
	mustParseCIDR("fc00::/7"),

	// IPv6 Multicast
	mustParseCIDR("ff00::/8"),

	// IPv6 Documentation & Discard
	mustParseCIDR("100::/64"),
	mustParseCIDR("2001:db8::/32"),
	mustParseCIDR("2001:2::/48"),
	mustParseCIDR("64:ff9b::/96"),
}

var blockedHostnames = []string{
	"localhost",
	"instance-data",
	"metadata.google.internal",
}

func mustParseCIDR(s string) *net.IPNet {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		panic(fmt.Sprintf("invalid CIDR %s: %v", s, err))
	}
	return ipnet
}

// ValidateIP returns ErrSSRFBlocked if the IP is private, loopback, link-local,
// cloud metadata, or reserved.
func ValidateIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("%w: invalid nil IP", ErrSSRFBlocked)
	}

	// Normalize IPv4-mapped IPv6 addresses (e.g. ::ffff:127.0.0.1)
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}

	// Standard library checks
	if ip.IsLoopback() {
		return fmt.Errorf("%w: destination resolves to loopback IP %s", ErrSSRFBlocked, ip)
	}
	if ip.IsPrivate() {
		return fmt.Errorf("%w: destination resolves to private IP %s", ErrSSRFBlocked, ip)
	}
	if ip.IsLinkLocalUnicast() {
		return fmt.Errorf("%w: destination resolves to link-local unicast IP %s", ErrSSRFBlocked, ip)
	}
	if ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return fmt.Errorf("%w: destination resolves to multicast IP %s", ErrSSRFBlocked, ip)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("%w: destination resolves to unspecified IP %s", ErrSSRFBlocked, ip)
	}

	// Explicit CIDR blocklist check (covers Carrier-Grade NAT 100.64.0.0/10, Alibaba IMDS 100.100.100.200, 0.0.0.0/8, etc.)
	for _, block := range blockedCIDRs {
		if block.Contains(ip) {
			return fmt.Errorf("%w: destination IP %s belongs to blocked subnet %s", ErrSSRFBlocked, ip, block.String())
		}
	}

	return nil
}

// IsBlockedHostname returns true if the hostname is a known local/metadata hostname.
func IsBlockedHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	for _, b := range blockedHostnames {
		if h == b {
			return true
		}
	}
	if strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return true
	}
	return false
}
