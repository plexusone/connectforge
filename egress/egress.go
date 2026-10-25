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
	// AllowPrefixes are ranges a request may reach although they are not
	// public, for a development or test destination: 127.0.0.0/8 for a local
	// fake, say, or the one private range a staging mock lives in. It is for
	// development and tests only. Link-local and cloud metadata addresses
	// are refused even when listed.
	AllowPrefixes []netip.Prefix
	// AllowPrivate permits every address that is not public except
	// link-local and cloud metadata addresses, which are always refused. It
	// is for development and tests only.
	//
	// Deprecated: list only the ranges a development setup needs in
	// AllowPrefixes.
	AllowPrivate bool
	// AllowHTTP permits plain HTTP. It is for development and tests only.
	AllowHTTP bool
	// Transport is the base transport, cloned. The default is a clone of
	// http.DefaultTransport. Its proxy and its dial functions (DialContext,
	// DialTLSContext, Dial and DialTLS) are replaced, so every connection is
	// made by the policy's dialer and checked; configure TLS, including
	// client certificates, through TLSClientConfig.
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
	// A custom TLS or legacy dialer would be used instead of DialContext and
	// skip the address check, so TLS is done over the checked connection.
	tr.DialTLSContext = nil
	tr.DialTLS = nil //nolint:staticcheck // SA1019: cleared because a caller's transport may still set it
	tr.Dial = nil    //nolint:staticcheck // SA1019: as above
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
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: address %s", ErrRefused, address)
	}
	a := ap.Addr().Unmap()
	if neverAllowed(a) {
		return fmt.Errorf("%w: %s is a link-local or metadata address", ErrRefused, a)
	}
	if p.AllowPrivate || p.allowed(a) {
		return nil
	}
	if blocked(a) {
		return fmt.Errorf("%w: %s is not a public address", ErrRefused, a)
	}
	return nil
}

// allowed reports whether a is in one of the policy's AllowPrefixes.
func (p Policy) allowed(a netip.Addr) bool {
	return slices.ContainsFunc(p.AllowPrefixes, func(pr netip.Prefix) bool { return pr.Contains(a) })
}

// neverAllowed reports whether a is a link-local or cloud metadata address,
// or an IPv6 form that embeds one, which no policy may reach: a provider API
// never lives there, and reaching one from a server can expose its cloud
// credentials. Teredo and local-use NAT64 addresses, whose embedded address
// cannot be checked reliably, are refused too.
func neverAllowed(a netip.Addr) bool {
	a = a.Unmap()
	if v4, ok := embeddedIPv4(a); ok && neverAllowed(v4) {
		return true
	}
	return slices.ContainsFunc(never, func(p netip.Prefix) bool { return p.Contains(a) })
}

// never are the ranges no policy may reach.
var never = []netip.Prefix{
	netip.MustParsePrefix("169.254.0.0/16"),     // link-local, including 169.254.169.254
	netip.MustParsePrefix("100.100.100.200/32"), // a cloud metadata service in shared address space
	netip.MustParsePrefix("168.63.129.16/32"),   // a cloud platform's host endpoint
	netip.MustParsePrefix("fe80::/10"),          // link-local
	netip.MustParsePrefix("fd00:ec2::254/128"),  // a cloud metadata service over IPv6
	netip.MustParsePrefix("2001::/32"),          // Teredo
	netip.MustParsePrefix("64:ff9b:1::/48"),     // local-use NAT64
}

// embeddedIPv4 returns the IPv4 address carried by a well-known-prefix NAT64,
// an IPv4-compatible or a 6to4 address.
func embeddedIPv4(a netip.Addr) (netip.Addr, bool) {
	if v4, ok := nat64(a); ok {
		return v4, true
	}
	if !a.Is6() {
		return netip.Addr{}, false
	}
	b := a.As16()
	switch {
	case ipv4Compatible.Contains(a):
		return netip.AddrFrom4([4]byte(b[12:])), true
	case sixToFour.Contains(a):
		return netip.AddrFrom4([4]byte(b[2:6])), true
	}
	return netip.Addr{}, false
}

var (
	ipv4Compatible = netip.MustParsePrefix("::/96")
	sixToFour      = netip.MustParsePrefix("2002::/16")
)

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
