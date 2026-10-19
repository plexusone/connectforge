package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/broker"
	"github.com/plexusone/connectforge/cryptotest"
	"github.com/plexusone/connectforge/egress"
	"github.com/plexusone/connectforge/internal/fakeapi"
	"github.com/plexusone/connectforge/provider"
	"github.com/plexusone/connectforge/service"
	"github.com/plexusone/connectforge/store/memstore"
)

const owner cf.OwnerID = "org-a"

var (
	admin  = cf.Principal{Kind: cf.PrincipalUser, ID: "admin"}
	member = cf.Principal{Kind: cf.PrincipalUser, ID: "member"}
	agent  = cf.Principal{Kind: cf.PrincipalAgent, ID: "agent"}
)

// authz lets admin do anything, member view, and agent view and use.
type authz struct{}

func (authz) Allowed(_ context.Context, p cf.Principal, a cf.Action, _ cf.Connection) error {
	switch {
	case p == admin,
		p == member && a == cf.ActionView,
		p == agent && (a == cf.ActionView || a == cf.ActionUse):
		return nil
	}
	return fmt.Errorf("%w: %s may not %s", cf.ErrDenied, p.ID, a)
}

type recorder struct {
	mu     sync.Mutex
	events []cf.Event
	fail   error
}

func (r *recorder) Record(_ context.Context, e cf.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.events = append(r.events, e)
	return nil
}

func (r *recorder) types() []cf.EventType {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []cf.EventType
	for _, e := range r.events {
		out = append(out, e.Type)
	}
	return out
}

type env struct {
	api    *fakeapi.API
	store  *memstore.Store
	broker *broker.Broker
	svc    *service.Service
	audit  *recorder
}

func setup(t *testing.T) *env {
	t.Helper()
	api := fakeapi.Start(t)
	reg := provider.NewRegistry()
	if err := reg.Register(api.Provider(t)); err != nil {
		t.Fatal(err)
	}
	e := &env{api: api, store: memstore.New(), audit: &recorder{}}
	b, err := broker.New(broker.Options{
		Store: e.store, Crypto: cryptotest.New(), Providers: reg, Authorizer: authz{}, Auditor: e.audit,
		Egress: egress.Policy{AllowPrivate: true, Transport: api.Client().Transport.(*http.Transport)}, //nolint:forcetypeassert // httptest's client uses *http.Transport
	})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Options{Store: e.store, Broker: b, Providers: reg, Authorizer: authz{}, Auditor: e.audit})
	if err != nil {
		t.Fatal(err)
	}
	e.broker, e.svc = b, svc
	return e
}

func (e *env) create(t *testing.T, tok string) (cf.Connection, error) {
	t.Helper()
	return e.svc.Create(context.Background(), service.CreateInput{
		Owner: owner, Actor: admin, Provider: "fakecdn", AuthMethod: "api_token",
		Fields: map[string]cf.Secret{"token": cf.SecretString(tok)},
	})
}

func TestCreateValidatesAndStoresSealed(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != cf.StateConnected || c.ExternalAccountID != fakeapi.AccountID || c.ExternalLabel != fakeapi.Label ||
		c.DisplayName != "Fake CDN" || len(c.Scopes) != 2 || c.Health.LastValidation != cf.OutcomeOK || c.CreatedBy != admin {
		t.Fatalf("connection: %+v", c)
	}
	cr, err := e.store.ActiveCredential(ctx, owner, c.ID)
	if err != nil || cr.Version != 1 || bytes.Contains(cr.Ciphertext, []byte(fakeapi.Good)) {
		t.Fatalf("credential: %v, %v", cr.Version, err)
	}
	if got := e.audit.types(); len(got) != 1 || got[0] != cf.EventConnectionCreated {
		t.Fatalf("events: %v", got)
	}
}

func TestCreateRefusals(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	if _, err := e.create(t, "wrong"); !errors.Is(err, cf.ErrInvalidCredential) {
		t.Fatalf("rejected token: %v", err)
	}
	if list, err := e.store.ListConnections(ctx, owner, cf.Filter{}); err != nil || len(list) != 0 {
		t.Fatalf("a rejected connection was stored: %v, %v", list, err)
	}
	if ev := e.audit.events[len(e.audit.events)-1]; ev.Success || ev.Type != cf.EventConnectionCreated {
		t.Fatalf("failure not audited: %+v", ev)
	}
	_, err := e.svc.Create(ctx, service.CreateInput{
		Owner: owner, Actor: member, Provider: "fakecdn", AuthMethod: "api_token",
		Fields: map[string]cf.Secret{"token": cf.SecretString(fakeapi.Good)},
	})
	if !errors.Is(err, cf.ErrDenied) {
		t.Fatalf("member created a connection: %v", err)
	}
	for name, in := range map[string]service.CreateInput{
		"missing field": {Owner: owner, Actor: admin, Provider: "fakecdn", AuthMethod: "api_token"},
		"unknown field": {Owner: owner, Actor: admin, Provider: "fakecdn", AuthMethod: "api_token",
			Fields: map[string]cf.Secret{"token": cf.SecretString("x"), "extra": cf.SecretString("y")}},
		"unknown method": {Owner: owner, Actor: admin, Provider: "fakecdn", AuthMethod: "oauth2"},
	} {
		if _, err := e.svc.Create(ctx, in); !errors.Is(err, service.ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.svc.Create(ctx, service.CreateInput{Provider: "nope"}); !errors.Is(err, provider.ErrUnknownProvider) {
		t.Fatalf("unknown provider: %v", err)
	}
}

func TestBrokerAuthenticatesRequests(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	use := cf.Use{Consumer: cf.Consumer{Kind: "application", ID: "dash"}, Purpose: "traffic"}
	hc, err := e.broker.HTTPClient(ctx, owner, agent, c.ID, use)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.api.URL+"/data", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	if err != nil || resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("authenticated request: %d %q %v", resp.StatusCode, body, err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("the broker modified the caller's request")
	}
	if seen := e.api.Seen(); len(seen) != 1 || seen[0] != "Bearer "+fakeapi.Good {
		t.Fatalf("server saw %v", seen)
	}
	if _, err := hc.Get("https://api.example.com/data"); !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("request off the manifest's hosts: %v", err)
	}
	if _, err := e.broker.HTTPClient(ctx, owner, member, c.ID, use); !errors.Is(err, cf.ErrDenied) {
		t.Fatalf("member used a connection: %v", err)
	}
	if got := e.audit.types(); got[len(got)-1] != cf.EventAccessDenied {
		t.Fatalf("refusal not audited: %v", got)
	}
}

func TestTestTracksHealthAndState(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}

	e.api.Revoke(fakeapi.Good)
	c, err = e.svc.Test(ctx, owner, agent, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != cf.StateNeedsAttention || c.Health.LastValidation != cf.OutcomeFailed || c.Health.ConsecutiveFails != 1 {
		t.Fatalf("revoked upstream: %+v", c)
	}
	if strings.Contains(c.Health.LastError, fakeapi.Good) || !strings.Contains(c.Health.LastError, "req-123") {
		t.Fatalf("last error: %q", c.Health.LastError)
	}
	if _, err := e.broker.HTTPClient(ctx, owner, agent, c.ID, cf.Use{}); !errors.Is(err, cf.ErrNotUsable) {
		t.Fatalf("used a connection that needs attention: %v", err)
	}

	e.api.Restore(fakeapi.Good)
	if c, err = e.svc.Test(ctx, owner, agent, c.ID); err != nil || c.State != cf.StateConnected || c.Health.ConsecutiveFails != 0 {
		t.Fatalf("restored: %+v, %v", c, err)
	}
}

func TestTransientFailureChangesOnlyHealth(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	// Swap in a credential the API rate-limits, as if limits began.
	cred, err := e.broker.Seal(ctx, c, 2, provider.Material{Kind: "api_token", Fields: map[string]cf.Secret{"token": cf.SecretString(fakeapi.Limited)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SwapCredential(ctx, 1, cred); err != nil {
		t.Fatal(err)
	}
	c, err = e.svc.Test(ctx, owner, admin, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != cf.StateConnected || c.Health.LastValidation != cf.OutcomeFailed {
		t.Fatalf("rate limited: %+v", c)
	}
}

func TestDisconnectDestroysCredentials(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Disconnect(ctx, owner, member, c.ID); !errors.Is(err, cf.ErrDenied) {
		t.Fatalf("member disconnected: %v", err)
	}
	c, err = e.svc.Disconnect(ctx, owner, admin, c.ID)
	if err != nil || c.State != cf.StateDisconnected {
		t.Fatalf("disconnect: %+v, %v", c, err)
	}
	if _, err := e.store.ActiveCredential(ctx, owner, c.ID); !errors.Is(err, cf.ErrNotFound) {
		t.Fatalf("credential survived: %v", err)
	}
	if _, err := e.broker.HTTPClient(ctx, owner, agent, c.ID, cf.Use{}); !errors.Is(err, cf.ErrNotUsable) {
		t.Fatalf("used after disconnect: %v", err)
	}
	if _, err := e.svc.Test(ctx, owner, admin, c.ID); !errors.Is(err, cf.ErrNotUsable) {
		t.Fatalf("tested after disconnect: %v", err)
	}
}

func TestGetListRename(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, owner, member, c.ID); err != nil {
		t.Fatalf("member view: %v", err)
	}
	stranger := cf.Principal{Kind: cf.PrincipalUser, ID: "stranger"}
	if _, err := e.svc.Get(ctx, owner, stranger, c.ID); !errors.Is(err, cf.ErrDenied) {
		t.Fatalf("stranger view: %v", err)
	}
	if list, err := e.svc.List(ctx, owner, stranger, cf.Filter{}); err != nil || len(list) != 0 {
		t.Fatalf("stranger list: %v, %v", list, err)
	}
	if list, err := e.svc.List(ctx, owner, member, cf.Filter{}); err != nil || len(list) != 1 {
		t.Fatalf("member list: %v, %v", list, err)
	}
	if _, err := e.svc.Rename(ctx, owner, member, c.ID, "x"); !errors.Is(err, cf.ErrDenied) {
		t.Fatalf("member rename: %v", err)
	}
	r, err := e.svc.Rename(ctx, owner, admin, c.ID, "Production")
	if err != nil || r.DisplayName != "Production" || r.Revision != c.Revision+1 {
		t.Fatalf("rename: %+v, %v", r, err)
	}
	if _, err := e.svc.Rename(ctx, owner, admin, c.ID, ""); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("empty name: %v", err)
	}
}

func TestAuditFailureIsReported(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	e.audit.fail = errors.New("audit store down")
	r, err := e.svc.Rename(ctx, owner, admin, c.ID, "Renamed")
	if !errors.Is(err, service.ErrNotAudited) || r.DisplayName != "Renamed" {
		t.Fatalf("unaudited change: %+v, %v", r, err)
	}
}

// TestNoSecretLeaks puts recognizable tokens through every path and checks
// that none appears in a connection, an event or an error.
func TestNoSecretLeaks(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	var outputs []string
	add := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(b), fmt.Sprintf("%+v", v))
	}
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	add(c)
	_, err = e.create(t, "fake-secret-wrong-token")
	outputs = append(outputs, err.Error())
	e.api.Revoke(fakeapi.Good)
	tested, err := e.svc.Test(ctx, owner, admin, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	add(tested)
	add(e.audit.events)
	for _, o := range outputs {
		for _, tok := range []string{fakeapi.Good, "fake-secret-wrong-token"} {
			if strings.Contains(o, tok) {
				t.Fatalf("token leaked: %s", o)
			}
		}
	}
}

func (e *env) reconnect(t *testing.T, as cf.Principal, id, tok string) (cf.Connection, error) {
	t.Helper()
	return e.svc.Reconnect(context.Background(), service.ReconnectInput{
		Owner: owner, Actor: as, ID: id,
		Fields: map[string]cf.Secret{"token": cf.SecretString(tok)},
	})
}

// seen reports whether the fake API received a request with this token.
func (e *env) seen(tok string) bool {
	return slices.Contains(e.api.Seen(), "Bearer "+tok)
}

func TestReconnectReplacesTheCredential(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	use := cf.Use{Consumer: cf.Consumer{Kind: "application", ID: "dash"}, Purpose: "traffic"}
	if err := e.svc.Bind(ctx, owner, agent, c.ID, use.Consumer, use.Purpose); err != nil {
		t.Fatal(err)
	}

	// The token is revoked upstream, and a check finds it.
	e.api.Revoke(fakeapi.Good)
	if c, err = e.svc.Test(ctx, owner, admin, c.ID); err != nil || c.State != cf.StateNeedsAttention {
		t.Fatalf("after revocation: %+v, %v", c, err)
	}

	// A new token for the same account puts it right without a new
	// connection, so the binding is untouched.
	r, err := e.reconnect(t, admin, c.ID, fakeapi.Rotated)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if r.ID != c.ID || r.State != cf.StateConnected || r.Health.LastValidation != cf.OutcomeOK || r.Health.ConsecutiveFails != 0 {
		t.Fatalf("reconnected: %+v", r)
	}
	cr, err := e.store.ActiveCredential(ctx, owner, c.ID)
	if err != nil || cr.Version != 2 {
		t.Fatalf("credential version: %+v, %v", cr, err)
	}
	if bs, err := e.svc.Bindings(ctx, owner, member, c.ID); err != nil || len(bs) != 1 {
		t.Fatalf("bindings after reconnect: %v, %v", bs, err)
	}

	// Requests now carry the new token.
	hc, err := e.broker.HTTPClient(ctx, owner, agent, c.ID, use)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Get(e.api.URL + "/data")
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if !e.seen(fakeapi.Rotated) {
		t.Fatalf("the new token was not used: %v", e.api.Seen())
	}
	if got := e.audit.types(); got[len(got)-1] != cf.EventCredentialReplaced {
		t.Fatalf("not audited: %v", got)
	}
}

func TestReconnectRefusals(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	before, err := e.store.ActiveCredential(ctx, owner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := func(why string) {
		t.Helper()
		after, err := e.store.ActiveCredential(ctx, owner, c.ID)
		if err != nil || after.Version != before.Version {
			t.Fatalf("%s changed the credential: %+v, %v", why, after, err)
		}
		got, err := e.store.GetConnection(ctx, owner, c.ID)
		if err != nil || got.Revision != c.Revision {
			t.Fatalf("%s changed the connection: %+v, %v", why, got, err)
		}
	}

	_, err = e.reconnect(t, admin, c.ID, "wrong")
	if !errors.Is(err, cf.ErrInvalidCredential) || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("rejected token: %v", err)
	}
	unchanged("a rejected token")
	if _, err := e.reconnect(t, admin, c.ID, fakeapi.OtherAccount); !errors.Is(err, cf.ErrAccountMismatch) {
		t.Fatalf("another account: %v", err)
	}
	unchanged("another account")
	if _, err := e.reconnect(t, member, c.ID, fakeapi.Rotated); !errors.Is(err, cf.ErrDenied) {
		t.Fatalf("member reconnect: %v", err)
	}
	unchanged("a refused member")
	if _, err := e.svc.Reconnect(ctx, service.ReconnectInput{Owner: owner, Actor: admin, ID: c.ID}); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("no fields: %v", err)
	}
	if _, err := e.reconnect(t, admin, "missing", fakeapi.Rotated); !errors.Is(err, cf.ErrNotFound) {
		t.Fatalf("missing connection: %v", err)
	}
}

func TestReconnectRecoversAnUnreadableCredential(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a lost key ring: the stored ciphertext can no longer be opened.
	if err := e.store.SwapCredential(ctx, 1, cf.Credential{
		Owner: owner, ConnectionID: c.ID, Version: 2, Kind: "api_token", Ciphertext: []byte("not-sealed-by-this-key"),
	}); err != nil {
		t.Fatal(err)
	}
	use := cf.Use{Consumer: cf.Consumer{Kind: "application", ID: "dash"}, Purpose: "traffic"}
	if _, err := e.broker.HTTPClient(ctx, owner, agent, c.ID, use); !errors.Is(err, cf.ErrCredentialUnavailable) {
		t.Fatalf("unreadable credential: %v", err)
	}
	if _, err := e.reconnect(t, admin, c.ID, fakeapi.Good); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if _, err := e.broker.HTTPClient(ctx, owner, agent, c.ID, use); err != nil {
		t.Fatalf("after reconnect: %v", err)
	}
}

func TestReconnectAfterDisconnectAndRevoked(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Disconnect(ctx, owner, admin, c.ID); err != nil {
		t.Fatal(err)
	}
	r, err := e.reconnect(t, admin, c.ID, fakeapi.Good)
	if err != nil || r.State != cf.StateConnected {
		t.Fatalf("reconnect after disconnect: %+v, %v", r, err)
	}
	if cr, err := e.store.ActiveCredential(ctx, owner, c.ID); err != nil || cr.Version != 1 {
		t.Fatalf("credentials were destroyed, so the new one is version 1: %+v, %v", cr, err)
	}

	// A revoked connection is terminal.
	revoked := r
	revoked.State = cf.StateRevoked
	if _, err := e.store.UpdateConnection(ctx, revoked); err != nil {
		t.Fatal(err)
	}
	if _, err := e.reconnect(t, admin, c.ID, fakeapi.Good); !errors.Is(err, cf.ErrNotUsable) {
		t.Fatalf("reconnect a revoked connection: %v", err)
	}
}

func TestBindings(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	site := cf.Consumer{Kind: "portfolio.site", ID: "impactchiefs.com"}

	if err := e.svc.Bind(ctx, owner, member, c.ID, site, "traffic"); !errors.Is(err, cf.ErrDenied) {
		t.Fatalf("a viewer bound a connection: %v", err)
	}
	for range 2 { // idempotent
		if err := e.svc.Bind(ctx, owner, agent, c.ID, site, "traffic"); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.Bind(ctx, owner, agent, c.ID, site, "security"); err != nil {
		t.Fatal(err)
	}
	bs, err := e.svc.Bindings(ctx, owner, member, c.ID)
	if err != nil || len(bs) != 2 || bs[0].Purpose != "security" || bs[1].Purpose != "traffic" {
		t.Fatalf("bindings: %+v, %v", bs, err)
	}
	stranger := cf.Principal{Kind: cf.PrincipalUser, ID: "stranger"}
	if _, err := e.svc.Bindings(ctx, owner, stranger, c.ID); !errors.Is(err, cf.ErrDenied) {
		t.Fatalf("a stranger listed bindings: %v", err)
	}
	if err := e.svc.Unbind(ctx, owner, agent, c.ID, site, "traffic"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Unbind(ctx, owner, agent, c.ID, site, "traffic"); err != nil {
		t.Fatalf("unbinding none: %v", err)
	}
	if bs, err = e.svc.Bindings(ctx, owner, admin, c.ID); err != nil || len(bs) != 1 {
		t.Fatalf("after unbind: %+v, %v", bs, err)
	}
	for name, consumer := range map[string]cf.Consumer{"no kind": {ID: "x"}, "no id": {Kind: "x"}} {
		if err := e.svc.Bind(ctx, owner, agent, c.ID, consumer, "traffic"); !errors.Is(err, service.ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := e.svc.Bind(ctx, owner, agent, c.ID, site, ""); !errors.Is(err, service.ErrInvalidInput) {
		t.Errorf("no purpose: %v", err)
	}
	if err := e.svc.Bind(ctx, owner, agent, "missing", site, "traffic"); !errors.Is(err, cf.ErrNotFound) {
		t.Errorf("missing connection: %v", err)
	}
	got := e.audit.types()
	var changed int
	for _, ty := range got {
		if ty == cf.EventBindingChanged {
			changed++
		}
	}
	// Three binds (the repeat is recorded too) and two unbinds (so is the
	// one with nothing to remove); a refusal is access.denied, not this.
	if changed != 5 {
		t.Fatalf("binding events: %d in %v", changed, got)
	}
}

func TestExpiryIsRecorded(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	want, err := time.Parse(time.RFC3339, fakeapi.Expiry)
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.create(t, fakeapi.Good)
	if err != nil {
		t.Fatal(err)
	}
	if c.Health.CredentialExpiresAt == nil || !c.Health.CredentialExpiresAt.Equal(want) {
		t.Fatalf("created: expiry %v, want %v", c.Health.CredentialExpiresAt, want)
	}
	cr, err := e.store.ActiveCredential(ctx, owner, c.ID)
	if err != nil || cr.ExpiresAt == nil || !cr.ExpiresAt.Equal(want) {
		t.Fatalf("credential expiry: %v, %v", cr.ExpiresAt, err)
	}

	// A failed test keeps the last known expiry; a successful one refreshes it.
	e.api.Revoke(fakeapi.Good)
	if c, err = e.svc.Test(ctx, owner, agent, c.ID); err != nil || c.Health.CredentialExpiresAt == nil {
		t.Fatalf("failed test: %+v, %v", c.Health, err)
	}
	e.api.Restore(fakeapi.Good)
	if c, err = e.svc.Test(ctx, owner, agent, c.ID); err != nil || !c.Health.CredentialExpiresAt.Equal(want) {
		t.Fatalf("passed test: %+v, %v", c.Health, err)
	}

	// Replacing it with a token that does not expire clears the expiry.
	if c, err = e.reconnect(t, admin, c.ID, fakeapi.Rotated); err != nil || c.Health.CredentialExpiresAt != nil {
		t.Fatalf("reconnected: %+v, %v", c.Health, err)
	}
	if cr, err = e.store.ActiveCredential(ctx, owner, c.ID); err != nil || cr.Version != 2 || cr.ExpiresAt != nil {
		t.Fatalf("replaced credential: version %d, expiry %v, %v", cr.Version, cr.ExpiresAt, err)
	}
}
