package middleware

import (
	"net"
	"strings"
)

type proxySet struct {
	ips  []net.IP
	nets []net.IPNet
}

func newProxySet(values []string) proxySet {
	var set proxySet
	for _, value := range values {
		if ip := net.ParseIP(value); ip != nil {
			set.ips = append(set.ips, ip)
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err == nil && network != nil {
			set.nets = append(set.nets, *network)
		}
	}
	return set
}

func (p proxySet) contains(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, trusted := range p.ips {
		if trusted.Equal(ip) {
			return true
		}
	}
	for _, network := range p.nets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// clientKey is the bucket identity for a request.
// Forwarded addresses are used only when the direct peer is a trusted proxy.
// The walk starts at the right of X-Forwarded-For and skips other trusted proxies.
func clientKey(remoteAddr, forwardedFor string, proxies proxySet) string {
	host := hostOnly(remoteAddr)
	if !proxies.contains(host) {
		return host
	}
	parts := strings.Split(forwardedFor, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := hostOnly(parts[i])
		if net.ParseIP(candidate) == nil || proxies.contains(candidate) {
			continue
		}
		return candidate
	}
	return host
}

func hostOnly(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	return strings.Trim(value, "[]")
}
