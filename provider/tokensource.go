package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	cf "github.com/plexusone/connectforge"
)

// TokenSource is an optional capability for a provider whose stored
// credential is not sent on requests but exchanged for a short-lived access
// token, such as a service account key signed into a token request.
//
// The broker detects it by type assertion. It mints a token with Token, keeps
// it until shortly before it expires, and sends it on each request instead of
// calling Authenticate, which such a provider may leave unsupported. A token
// is derived from the stored credential and never stored, so a refresh needs
// no lease and nothing is persisted, unlike a refresh token that the service
// rotates and the host must store.
type TokenSource interface {
	// Token exchanges the credential for an access token. hc is restricted to
	// the manifest's hosts, so the manifest must list the token endpoint's
	// origin as well as the service's. A rejection wraps a connectforge
	// provider error; an error never contains the credential.
	Token(ctx context.Context, hc *http.Client, m Material) (AccessToken, error)
}

// AccessToken is a short-lived credential minted from a stored one.
type AccessToken struct {
	// Type is the authorization scheme; "Bearer" when empty.
	Type  string
	Value cf.Secret
	// ExpiresAt is when the token stops working. A zero value means the
	// source does not say, and the token is used once and not kept.
	ExpiresAt time.Time
}

// TokenExpiryMargin is how long before a token expires it is replaced, so a
// request that starts with a kept token does not finish with an expired one.
const TokenExpiryMargin = time.Minute

// ErrTokenSourceOnly is returned by a provider's Authenticate when its
// credential is used through a [TokenSource].
var ErrTokenSourceOnly = errors.New("provider: credential is used through a token source")

// NewTokenTransport returns a RoundTripper that authenticates each request,
// on a copy of it, with a token from ts, and keeps the token until
// [TokenExpiryMargin] before it expires. Concurrent requests share one
// exchange. hc makes the exchange and next carries the requests; both must be
// restricted to the provider's hosts. A failed exchange is returned and not
// kept, so the next request tries again. clock may be nil.
func NewTokenTransport(ts TokenSource, m Material, hc *http.Client, next http.RoundTripper, clock cf.Clock) http.RoundTripper {
	if clock == nil {
		clock = cf.SystemClock{}
	}
	return &tokenTransport{ts: ts, m: m, hc: hc, next: next, clock: clock}
}

// NewTokenClient returns a copy of hc whose requests carry a token from ts.
// A provider uses it in Validate to call its service with the credential.
func NewTokenClient(ts TokenSource, m Material, hc *http.Client, clock cf.Clock) *http.Client {
	next := hc.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	c := *hc
	c.Transport = NewTokenTransport(ts, m, hc, next, clock)
	return &c
}

type tokenTransport struct {
	ts    TokenSource
	m     Material
	hc    *http.Client
	next  http.RoundTripper
	clock cf.Clock

	mu   sync.Mutex
	tok  AccessToken
	have bool
}

func (t *tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	typ, val, err := t.token(r.Context())
	if err != nil {
		return nil, err
	}
	r2 := r.Clone(r.Context())
	r2.Header.Set("Authorization", typ+" "+val)
	return t.next.RoundTrip(r2)
}

// token returns the kept token if it is still good, else mints one. Holding
// the lock across the exchange is what makes concurrent requests share it.
func (t *tokenTransport) token(ctx context.Context) (typ, val string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.have || !t.tok.ExpiresAt.After(t.clock.Now().Add(TokenExpiryMargin)) {
		tok, err := t.ts.Token(ctx, t.hc, t.m)
		if err != nil {
			return "", "", err
		}
		if tok.Value.IsZero() {
			return "", "", fmt.Errorf("%w: the token source returned no token", cf.ErrCredentialUnavailable)
		}
		t.tok, t.have = tok, !tok.ExpiresAt.IsZero()
		typ, val = tokenParts(tok)
		return typ, val, nil
	}
	typ, val = tokenParts(t.tok)
	return typ, val, nil
}

func tokenParts(tok AccessToken) (typ, val string) {
	b := tok.Value.Reveal()
	defer clear(b)
	typ = tok.Type
	if typ == "" {
		typ = "Bearer"
	}
	return typ, string(b)
}
