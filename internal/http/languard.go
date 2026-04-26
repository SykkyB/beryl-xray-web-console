package http

import (
	"fmt"
	"net"
	"os"
)

// CheckLANBind refuses any listen address that would expose the panel
// beyond the LAN. The rules:
//
//  1. The host part must be either:
//     - a wildcard (0.0.0.0 / ::) — accepted with a warning to stderr,
//       on the assumption that the router's firewall (fw3 zone_wan_*)
//       drops WAN-side traffic to the panel port. This is the SAFE
//       default on stock GL.iNet firmware. We allow it because binding
//       to a specific LAN IP triggers a kernel-routing quirk on this
//       hardware where SYN-ACK replies leave via lo with cleared
//       headers, making the panel unreachable from LAN clients.
//     - a private / loopback / link-local IP (RFC1918, RFC4193,
//       loopback, IPv4/IPv6 link-local).
//  2. The host part must resolve to at least one address.
//
// Lives in the http package because it runs at startup right before the
// HTTP listener binds the socket — failing loudly here (or warning
// loudly) is preferable to silently exposing a basic-auth panel to the
// internet.
func CheckLANBind(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", listen, err)
	}
	if port == "" {
		return fmt.Errorf("listen %q: empty port", listen)
	}
	if host == "" {
		return fmt.Errorf("listen %q: empty host (use 0.0.0.0, the LAN IP, or 127.0.0.1)", listen)
	}
	if host == "0.0.0.0" || host == "::" {
		fmt.Fprintf(os.Stderr, "WARNING: listen %q is a wildcard bind. Relying on the system firewall (fw3 / zone_wan_input DROP) to keep the panel off the WAN. Verify with: iptables -L zone_wan_input -n -v\n", listen)
		return nil
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
