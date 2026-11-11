// Package clientip resolves the client address of an HTTP request for rate
// limiting. X-Forwarded-For is honoured only when the direct peer is a trusted
// proxy (SEMIDX_TRUSTED_PROXIES); otherwise it is client-controlled and ignored.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver derives the client IP. A nil *Resolver (or one with no trusted
// proxies) uses the RemoteAddr host only.
type Resolver struct {
	trusted []netip.Prefix
}

// New parses trusted proxy entries: CIDRs ("10.0.0.0/8") or bare IPs. Blank
// entries are skipped; any other unparseable entry is an error.
func New(entries []string) (*Resolver, error) {
	r := &Resolver{}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		p, err := parsePrefix(e)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: want an IP or CIDR", e)
		}
		r.trusted = append(r.trusted, p)
	}
	return r, nil
}

func parsePrefix(e string) (netip.Prefix, error) {
	if strings.Contains(e, "/") {
		p, err := netip.ParsePrefix(e)
		if err != nil {
			return netip.Prefix{}, err
		}
		return netip.PrefixFrom(p.Addr().Unmap(), unmappedBits(p)).Masked(), nil
	}
	a, err := netip.ParseAddr(e)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// unmappedBits converts a ::ffff:a.b.c.d/N prefix length to its IPv4 length.
func unmappedBits(p netip.Prefix) int {
	if p.Addr().Is4In6() {
		return max(p.Bits()-96, 0)
	}
	return p.Bits()
}

// ClientIP returns the client address for req. When the direct peer is a
// trusted proxy, X-Forwarded-For is walked right to left and the first valid
// address outside the trusted ranges wins (never blindly the leftmost, which
// the client controls). Malformed entries are skipped; if nothing qualifies,
// the RemoteAddr host is returned.
func (r *Resolver) ClientIP(req *http.Request) string {
	remote := remoteHost(req.RemoteAddr)
	if r == nil || len(r.trusted) == 0 {
		return remote
	}
	peer, err := netip.ParseAddr(remote)
	if err != nil || !r.isTrusted(peer) {
		return remote
	}
	hops := forwardedHops(req.Header.Values("X-Forwarded-For"))
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parseHop(hops[i])
		if !ok {
			continue
		}
		if !r.isTrusted(a) {
			return a.String()
		}
	}
	return remote
}

func (r *Resolver) isTrusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// forwardedHops flattens every X-Forwarded-For header, in order.
func forwardedHops(values []string) []string {
	var hops []string
	for _, v := range values {
		hops = append(hops, strings.Split(v, ",")...)
	}
	return hops
}

// parseHop accepts "ip", "ip:port" or "[v6]:port".
func parseHop(h string) (netip.Addr, bool) {
	h = strings.TrimSpace(h)
	if a, err := netip.ParseAddr(h); err == nil {
		return a.Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(h); err == nil {
		return ap.Addr().Unmap(), true
	}
	return netip.Addr{}, false
}

// remoteHost strips the port from RemoteAddr and unmaps IPv4-in-IPv6.
func remoteHost(remoteAddr string) string {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return a.Unmap().String()
	}
	return host
}
