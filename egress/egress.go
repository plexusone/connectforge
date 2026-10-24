// Package egress restricts where a provider request may go: only to the hosts
// a provider's manifest names, over HTTPS, without following a redirect to
// another host, and never to an address that is not public: private,
// loopback, link-local, shared, reserved and documentation ranges, and IPv6
// forms that embed such an IPv4 address. The address is checked after DNS
// resolution, so a rebinding name cannot reach one.
package egress

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"syscall"
	"time"
)

// ErrRefused is returned for a request the policy does not allow.
var ErrRefused = errors.New("egress: refused")

// Policy is where requests may go.
type Policy struct {
	// Hosts are the allowed hosts, with a port when not the default.
	Hosts []string
	// AllowPrivate permits private, loopback and link-local addresses. It is
	// for development and tests only.
	AllowPrivate bool
	// AllowHTTP permits plain HTTP. It is for development and tests only.
	AllowHTTP bool
	// Transport is the base transport, cloned. The default is a clone of
	// http.DefaultTransport.
	Transport *http.Transport
	// Timeout bounds a whole request. Default 30 seconds.
	Timeout time.Duration
}

// Client returns an HTTP client that enforces the policy.
func Client(p Policy) *http.Client {
	base := p.Transport
	if base == nil {
		base = http.DefaultTransport.(*http.Transport) //nolint:forcetypeassert // the standard library's default is always *http.Transport
	}
	tr := base.Clone()
	tr.Proxy = nil // a proxy would hide the destination from the dial check
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: p.control}
	tr.DialContext = dialer.DialContext
	timeout := p.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Transport: checkTransport{p: p, next: tr},
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("%w: too many redirects", ErrRefused)
			}
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("%w: redirect to another host %s", ErrRefused, req.URL.Host)
			}
			return nil
		},
	}
}

// checkTransport refuses requests to hosts or schemes outside the policy.
type checkTransport struct {
	p    Policy
	next http.RoundTripper
}

func (t checkTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" && (r.URL.Scheme != "http" || !t.p.AllowHTTP) {
		return nil, fmt.Errorf("%w: scheme %s", ErrRefused, r.URL.Scheme)
	}
	if !slices.Contains(t.p.Hosts, r.URL.Host) {
		return nil, fmt.Errorf("%w: host %s is not in the provider's manifest", ErrRefused, r.URL.Host)
	}
	return t.next.RoundTrip(r)
}

// control runs on the resolved address just before connecting.
func (p Policy) control(_, address string, _ syscall.RawConn) error {
	if p.AllowPrivate {
		return nil
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: address %s", ErrRefused, address)
	}
	if blocked(ap.Addr()) {
		return fmt.Errorf("%w: %s is not a public address", ErrRefused, ap.Addr())
	}
	return nil
}

// blocked reports whether a is not a public unicast address. Besides the
// loopback, private, link-local, multicast and unspecified addresses, it
// refuses the IANA special-purpose ranges that are not globally reachable,
// and the IPv6 forms that carry an IPv4 address a gateway may translate: a
// NAT64 address is judged by the IPv4 address it embeds, and the other
// transition forms are refused outright.
func blocked(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return true
	}
	if v4, ok := nat64(a); ok {
		return blocked(v4)
	}
	for _, p := range special {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// nat64 returns the IPv4 address embedded in a well-known-prefix NAT64
// address (RFC 6052).
func nat64(a netip.Addr) (netip.Addr, bool) {
	if !wellKnownNAT64.Contains(a) {
		return netip.Addr{}, false
	}
	b := a.As16()
	return netip.AddrFrom4([4]byte(b[12:])), true
}

var wellKnownNAT64 = netip.MustParsePrefix("64:ff9b::/96")

// special are special-purpose ranges (RFC 6890 and the IANA registries) that
// are not public destinations, beyond those the netip predicates cover.
var special = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // shared address space (RFC 6598)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, and the broadcast address
	netip.MustParsePrefix("::/96"),           // IPv4-compatible (deprecated)
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("100::/64"),        // discard-only
	netip.MustParsePrefix("2001::/23"),       // IETF protocol assignments, including Teredo
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("2002::/16"),       // 6to4
	netip.MustParsePrefix("3fff::/20"),       // documentation
	netip.MustParsePrefix("5f00::/16"),       // segment routing
	netip.MustParsePrefix("fec0::/10"),       // site-local (deprecated)
}
