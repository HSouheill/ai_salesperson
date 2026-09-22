// Package netguard stops user-supplied hosts (website URLs, SMTP/IMAP servers,
// webhook URLs) from reaching internal networks (SSRF). The check runs on the
// resolved IP at connect time, so it also covers redirects and DNS rebinding.
package netguard

import (
	"fmt"
	"net"
	"syscall"
	"time"
)

func IsPublic(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified())
}

// Dialer returns a dialer that refuses non-public addresses unless allowPrivate
// is set (self-hosted deployments and tests).
func Dialer(allowPrivate bool) *net.Dialer {
	d := &net.Dialer{Timeout: 15 * time.Second}
	if allowPrivate {
		return d
	}
	d.Control = func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip == nil || !IsPublic(ip) {
			return fmt.Errorf("blocked non-public address %s", host)
		}
		return nil
	}
	return d
}
