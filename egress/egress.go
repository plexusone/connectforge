// Package egress restricts where a provider request may go: only to the hosts
// a provider's manifest names, over HTTPS, without following a redirect to
// another host, and never to a private, loopback or link-local address, which
// is checked after DNS resolution so a rebinding name cannot reach one.
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

func blocked(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() || cgnat.Contains(a)
}

// cgnat is shared address space (RFC 6598), not public.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")
