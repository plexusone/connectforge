package service_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/broker"
	"github.com/plexusone/connectforge/cryptotest"
	"github.com/plexusone/connectforge/provider"
	"github.com/plexusone/connectforge/service"
	"github.com/plexusone/connectforge/store/memstore"
)

// accountProvider accepts any token and reports the account named by it:
// "acct-a" for token "a", and no account for token "none".
type accountProvider struct{}

func (accountProvider) Manifest() provider.Manifest {
	fields := []provider.Field{{Name: "token", Label: "Token", Secret: true, Required: true}}
	return provider.Manifest{
		Key: "accounts", DisplayName: "Accounts", BaseURLs: []string{"https://api.accounts.example"},
		AuthMethods: []provider.AuthMethod{
			{Key: "verified", Fields: fields},
			{Key: "unverifiable", Fields: fields, AccountUnverifiable: true},
		},
	}
}

func (accountProvider) Validate(_ context.Context, _ *http.Client, m provider.Material) (provider.Result, error) {
	b := m.Field("token").Reveal()
	defer clear(b)
	if string(b) == "none" {
		return provider.Result{}, nil
	}
	return provider.Result{AccountID: "acct-" + string(b)}, nil
}

func (accountProvider) Authenticate(*http.Request, provider.Material) error { return nil }

type accountEnv struct {
	store *memstore.Store
	svc   *service.Service
	audit *recorder
}

func setupAccounts(t *testing.T) *accountEnv {
	t.Helper()
	reg := provider.NewRegistry()
	if err := reg.Register(accountProvider{}); err != nil {
		t.Fatal(err)
	}
	e := &accountEnv{store: memstore.New(), audit: &recorder{}}
	b, err := broker.New(broker.Options{Store: e.store, Crypto: cryptotest.New(), Providers: reg, Authorizer: authz{}, Auditor: e.audit})
	if err != nil {
		t.Fatal(err)
	}
	e.svc, err = service.New(service.Options{Store: e.store, Broker: b, Providers: reg, Authorizer: authz{}, Auditor: e.audit})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *accountEnv) create(method, tok string) (cf.Connection, error) {
	return e.svc.Create(context.Background(), service.CreateInput{
		Owner: owner, Actor: admin, Provider: "accounts", AuthMethod: method,
		Fields: map[string]cf.Secret{"token": cf.SecretString(tok)},
	})
}

func (e *accountEnv) reconnect(id, tok string) (cf.Connection, error) {
	return e.svc.Reconnect(context.Background(), service.ReconnectInput{
		Owner: owner, Actor: admin, ID: id, Fields: map[string]cf.Secret{"token": cf.SecretString(tok)},
	})
}

// lastDetails returns the details of the last audit event.
func (e *accountEnv) lastDetails(t *testing.T) map[string]string {
	t.Helper()
	e.audit.mu.Lock()
	defer e.audit.mu.Unlock()
	if len(e.audit.events) == 0 {
		t.Fatal("no events")
	}
	return e.audit.events[len(e.audit.events)-1].Details
}

func TestAccountMustBeIdentified(t *testing.T) {
	ctx := context.Background()
	e := setupAccounts(t)

	if _, err := e.create("verified", "none"); !errors.Is(err, cf.ErrAccountUnknown) {
		t.Fatalf("create without an account: %v", err)
	}
	if list, err := e.store.ListConnections(ctx, owner, cf.Filter{}); err != nil || len(list) != 0 {
		t.Fatalf("a connection without an account was stored: %v, %v", list, err)
	}

	c, err := e.create("verified", "a")
	if err != nil || c.ExternalAccountID != "acct-a" {
		t.Fatalf("create: %+v, %v", c, err)
	}
	if _, err := e.reconnect(c.ID, "none"); !errors.Is(err, cf.ErrAccountUnknown) {
		t.Fatalf("reconnect without an account: %v", err)
	}
	if _, err := e.reconnect(c.ID, "b"); !errors.Is(err, cf.ErrAccountMismatch) {
		t.Fatalf("reconnect to another account: %v", err)
	}
	if cr, err := e.store.ActiveCredential(ctx, owner, c.ID); err != nil || cr.Version != 1 {
		t.Fatalf("a refused reconnect changed the credential: %+v, %v", cr, err)
	}
	if _, err := e.reconnect(c.ID, "a"); err != nil {
		t.Fatalf("reconnect to the same account: %v", err)
	}
	if d := e.lastDetails(t); d["account"] != "" {
		t.Fatalf("a verified reconnect was marked unverified: %v", d)
	}

	// A connection stored without an account, as an earlier version allowed,
	// cannot prove a replacement is for the same account.
	legacy := cf.Connection{
		ID: "legacy", Owner: owner, Provider: "accounts", AuthMethod: "verified",
		DisplayName: "Legacy", State: cf.StateConnected, CreatedBy: admin,
	}
	if _, err := e.store.CreateConnection(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := e.reconnect("legacy", "a"); !errors.Is(err, cf.ErrAccountUnknown) {
		t.Fatalf("reconnect of a connection with no recorded account: %v", err)
	}
}

func TestAccountUnverifiableMethod(t *testing.T) {
	e := setupAccounts(t)
	c, err := e.create("unverifiable", "none")
	if err != nil || c.ExternalAccountID != "" {
		t.Fatalf("create: %+v, %v", c, err)
	}
	if _, err := e.reconnect(c.ID, "none"); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if d := e.lastDetails(t); d["account"] != "unverified" {
		t.Fatalf("an unverified reconnect was not recorded: %v", d)
	}
	r, err := e.reconnect(c.ID, "b")
	if err != nil || r.ExternalAccountID != "acct-b" {
		t.Fatalf("reconnect that discovers an account: %+v, %v", r, err)
	}
	// Once both sides name an account, they must match.
	if _, err := e.reconnect(c.ID, "a"); !errors.Is(err, cf.ErrAccountMismatch) {
		t.Fatalf("reconnect to another account: %v", err)
	}
}
