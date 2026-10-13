// Package broker is the only path from a stored credential to an
// authenticated request. It checks authorization and state, unseals the
// credential, and returns an HTTP client that authenticates each request and
// may reach only the provider's hosts. Callers never receive the credential.
package broker

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	cf "github.com/plexusone/connectionforge"
	"github.com/plexusone/connectionforge/egress"
	"github.com/plexusone/connectionforge/provider"
)

// Options configures a [Broker].
type Options struct {
	Store      cf.Store         // required
	Crypto     cf.Cryptographer // required
	Providers  *provider.Registry
	Authorizer cf.Authorizer // required
	// Auditor records refusals. Optional.
	Auditor cf.Auditor
	// Egress is the policy template; its Hosts are replaced by each
	// provider's manifest hosts.
	Egress egress.Policy
	// OnUse, if set, is called for each client handed out, for use counters.
	OnUse func(ctx context.Context, c cf.Connection, u cf.Use)
	Clock cf.Clock
}

// Broker hands out authenticated clients.
type Broker struct {
	o Options
}

// New returns a broker.
func New(o Options) (*Broker, error) {
	switch {
	case o.Store == nil:
		return nil, errors.New("broker: Options.Store is required")
	case o.Crypto == nil:
		return nil, errors.New("broker: Options.Crypto is required")
	case o.Providers == nil:
		return nil, errors.New("broker: Options.Providers is required")
	case o.Authorizer == nil:
		return nil, errors.New("broker: Options.Authorizer is required")
	}
	if o.Clock == nil {
		o.Clock = cf.SystemClock{}
	}
	return &Broker{o: o}, nil
}

// HTTPClient returns a client whose requests carry the connection's
// credential. The principal must be allowed to use the connection, and the
// connection must be connected. The credential is read once, when the client
// is made: make a client per unit of work, so a disconnect or a refresh
// applies to the next one.
func (b *Broker) HTTPClient(ctx context.Context, owner cf.OwnerID, p cf.Principal, connID string, use cf.Use) (*http.Client, error) {
	c, err := b.o.Store.GetConnection(ctx, owner, connID)
	if err != nil {
		return nil, err
	}
	if err := b.o.Authorizer.Allowed(ctx, p, cf.ActionUse, c); err != nil {
		return nil, b.denied(ctx, c, p, use.OnBehalfOf, err)
	}
	if !c.State.Usable() {
		return nil, fmt.Errorf("%w: %s is %s", cf.ErrNotUsable, c.ID, c.State)
	}
	prov, err := b.o.Providers.Get(c.Provider)
	if err != nil {
		return nil, err
	}
	m, err := b.Unseal(ctx, c)
	if err != nil {
		return nil, err
	}
	base, err := b.ProviderClient(prov.Manifest())
	if err != nil {
		return nil, err
	}
	if b.o.OnUse != nil {
		b.o.OnUse(ctx, c, use)
	}
	return &http.Client{
		Transport:     authTransport{prov: prov, m: m, next: base.Transport},
		CheckRedirect: base.CheckRedirect,
		Timeout:       base.Timeout,
	}, nil
}

// ProviderClient returns an unauthenticated client restricted to the
// manifest's hosts, for validation.
func (b *Broker) ProviderClient(m provider.Manifest) (*http.Client, error) {
	hosts, err := m.Hosts()
	if err != nil {
		return nil, err
	}
	pol := b.o.Egress
	pol.Hosts = hosts
	return egress.Client(pol), nil
}

// Seal encrypts material as version v of c's credential.
func (b *Broker) Seal(ctx context.Context, c cf.Connection, v int, m provider.Material) (cf.Credential, error) {
	pt, err := provider.EncodeMaterial(m)
	if err != nil {
		return cf.Credential{}, err
	}
	defer clear(pt)
	ct, err := b.o.Crypto.Seal(ctx, cf.CredentialPurpose, pt, cf.CredentialAAD(c.Owner, c.ID, v))
	if err != nil {
		return cf.Credential{}, fmt.Errorf("broker: seal: %w", err)
	}
	return cf.Credential{
		Owner: c.Owner, ConnectionID: c.ID, Version: v, Kind: m.Kind,
		Ciphertext: ct, ExpiresAt: m.ExpiresAt, CreatedAt: b.o.Clock.Now(),
	}, nil
}

// Unseal reads and decrypts c's active credential. Any failure is
// [cf.ErrCredentialUnavailable]: there is no fallback.
func (b *Broker) Unseal(ctx context.Context, c cf.Connection) (provider.Material, error) {
	cr, err := b.o.Store.ActiveCredential(ctx, c.Owner, c.ID)
	if err != nil {
		return provider.Material{}, fmt.Errorf("%w: %w", cf.ErrCredentialUnavailable, err)
	}
	pt, err := b.o.Crypto.Open(ctx, cf.CredentialPurpose, cr.Ciphertext, cf.CredentialAAD(c.Owner, c.ID, cr.Version))
	if err != nil {
		return provider.Material{}, fmt.Errorf("%w: %w", cf.ErrCredentialUnavailable, err)
	}
	m, err := provider.DecodeMaterial(pt)
	if err != nil {
		return provider.Material{}, fmt.Errorf("%w: %w", cf.ErrCredentialUnavailable, err)
	}
	return m, nil
}

func (b *Broker) denied(ctx context.Context, c cf.Connection, p cf.Principal, obo *cf.Principal, cause error) error {
	if b.o.Auditor == nil {
		return cause
	}
	err := b.o.Auditor.Record(ctx, cf.Event{
		Time: b.o.Clock.Now(), Type: cf.EventAccessDenied, Owner: c.Owner, Actor: p, OnBehalfOf: obo,
		ConnectionID: c.ID, Provider: c.Provider, Reason: string(cf.ActionUse),
	})
	if err != nil {
		return errors.Join(cause, fmt.Errorf("broker: recording the refusal: %w", err))
	}
	return cause
}

// authTransport authenticates each request on a copy of it.
type authTransport struct {
	prov provider.Provider
	m    provider.Material
	next http.RoundTripper
}

func (t authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	if err := t.prov.Authenticate(r2, t.m); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(r2)
}
