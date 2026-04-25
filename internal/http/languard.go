package http

import (
	"fmt"
	"net"
)

// CheckLANBind refuses any listen address that would expose the panel
// beyond the LAN. The rules, deliberately strict:
//
//  1. Wildcard binds (0.0.0.0, [::], empty host) are rejected: they would
//     also bind the WAN interface.
//  2. The host part must resolve to at least one address, and every such
//     address must be in one of the private/loopback/link-local ranges
//     (RFC1918, RFC4193, loopback, IPv4 link-local, IPv6 link-local).
//
// Lives in the http package because it runs at startup right before the
// HTTP listener binds the socket — failing loudly here is preferable to
// silently exposing a basic-auth panel to the internet.
func CheckLANBind(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", listen, err)
	}
	if port == "" {
		return fmt.Errorf("listen %q: empty port", listen)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return fmt.Errorf("listen %q: wildcard bind is not allowed (would expose WAN); use the LAN IP", listen)
	}

	ips, err := resolveAll(host)
	if err != nil {
		return fmt.Errorf("listen %q: resolve host: %w", listen, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("listen %q: host resolved to no addresses", listen)
	}
	for _, ip := range ips {
		if !isLAN(ip) {
			return fmt.Errorf("listen %q: %s is not a LAN address (private / loopback / link-local); refusing to bind", listen, ip)
		}
	}
	return nil
}

func resolveAll(host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	return addrs, nil
}

func isLAN(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	return false
}
