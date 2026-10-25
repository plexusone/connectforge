package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/broker"
	"github.com/plexusone/connectforge/cryptotest"
	"github.com/plexusone/connectforge/egress"
	"github.com/plexusone/connectforge/provider"
	"github.com/plexusone/connectforge/service"
	"github.com/plexusone/connectforge/store/memstore"
)

// keyProvider's credential is a key that is exchanged for an access token at
// a token endpoint on the same host as the service.
type keyProvider struct {
	base      string
	exchanges atomic.Int32
}

func (p *keyProvider) Manifest() provider.Manifest {
	return provider.Manifest{
		Key: "keyed", DisplayName: "Keyed", BaseURLs: []string{p.base},
		AuthMethods: []provider.AuthMethod{{
			Key:    "service_account",
			Fields: []provider.Field{{Name: "key", Label: "Key", Secret: true, Required: true}},
		}},
	}
}

func (p *keyProvider) Token(ctx context.Context, hc *http.Client, m provider.Material) (provider.AccessToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/token", nil)
	if err != nil {
		return provider.AccessToken{}, err
	}
	b := m.Field("key").Reveal()
	req.Header.Set("X-Key", string(b))
	clear(b)
	resp, err := hc.Do(req)
	if err != nil {
		return provider.AccessToken{}, err
	}
	defer resp.Body.Close()
	if err := provider.Classify("keyed", resp); err != nil {
		return provider.AccessToken{}, err
	}
	p.exchanges.Add(1)
	return provider.AccessToken{Value: cf.SecretString("minted"), ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (p *keyProvider) Validate(ctx context.Context, hc *http.Client, m provider.Material) (provider.Result, error) {
	c := provider.NewTokenClient(p, m, hc, nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/me", nil)
	if err != nil {
		return provider.Result{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return provider.Result{}, err
	}
	defer resp.Body.Close()
	if err := provider.Classify("keyed", resp); err != nil {
		return provider.Result{}, err
	}
	return provider.Result{AccountID: "acct", AccountLabel: "Acct"}, nil
}

func (*keyProvider) Authenticate(*http.Request, provider.Material) error {
	return provider.ErrTokenSourceOnly
}

func TestBrokerUsesATokenSource(t *testing.T) {
	ctx := context.Background()
	var (
		mu      sync.Mutex
		apiAuth []string
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if r.Header.Get("X-Key") != "the-key" {
				http.Error(w, "no", http.StatusUnauthorized)
			}
		default:
			mu.Lock()
			apiAuth = append(apiAuth, r.Header.Get("Authorization"))
			mu.Unlock()
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	prov := &keyProvider{base: u.Scheme + "://" + u.Host}
	reg := provider.NewRegistry()
	if err := reg.Register(prov); err != nil {
		t.Fatal(err)
	}
	st := memstore.New()
	b, err := broker.New(broker.Options{
		Store: st, Crypto: cryptotest.New(), Providers: reg, Authorizer: authz{},
		Egress: egress.Policy{AllowPrefixes: loopback, Transport: srv.Client().Transport.(*http.Transport)}, //nolint:forcetypeassert // httptest's client uses *http.Transport
	})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Options{Store: st, Broker: b, Providers: reg, Authorizer: authz{}, Auditor: &recorder{}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Create(ctx, service.CreateInput{
		Owner: owner, Actor: admin, Provider: "keyed", AuthMethod: "service_account",
		Fields: map[string]cf.Secret{"key": cf.SecretString("wrong")},
	}); err == nil {
		t.Fatal("a key the token endpoint refuses was accepted")
	}

	c, err := svc.Create(ctx, service.CreateInput{
		Owner: owner, Actor: admin, Provider: "keyed", AuthMethod: "service_account",
		Fields: map[string]cf.Secret{"key": cf.SecretString("the-key")},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	hc, err := b.HTTPClient(ctx, owner, agent, c.ID, cf.Use{})
	if err != nil {
		t.Fatal(err)
	}
	before := prov.exchanges.Load()
	for range 3 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, prov.base+"/data", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if got := prov.exchanges.Load() - before; got != 1 {
		t.Fatalf("%d exchanges for three requests, want 1", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(apiAuth) < 3 || apiAuth[len(apiAuth)-1] != "Bearer minted" {
		t.Fatalf("service saw Authorization %q", apiAuth)
	}
}
