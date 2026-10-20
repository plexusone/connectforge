// Package storetest is the conformance suite every [connectforge.Store] and
// [connectforge.Leaser] must pass. A store's own tests call it with a
// constructor:
//
//	func TestConformance(t *testing.T) {
//		storetest.Run(t, func(t *testing.T) connectforge.Store { return newStore(t) })
//	}
//
// It checks owner isolation on every method, compare-and-swap on connections
// and credentials (including under concurrency), the transition table, cascade
// on delete, ordering, idempotence, and that returned records are copies.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cf "github.com/plexusone/connectforge"
)

const (
	ownerA cf.OwnerID = "org-a"
	ownerB cf.OwnerID = "org-b"
)

var alice = cf.Principal{Kind: cf.PrincipalUser, ID: "alice"}

// Run runs the store suite. newStore returns an empty store for each subtest.
func Run(t *testing.T, newStore func(t *testing.T) cf.Store) {
	t.Helper()
	tests := []struct {
		name string
		fn   func(*testing.T, cf.Store)
	}{
		{"CreateGetList", testCreateGetList},
		{"OwnerIsolation", testOwnerIsolation},
		{"UpdateCompareAndSwap", testUpdateCompareAndSwap},
		{"UpdateEnforcesTransitions", testUpdateEnforcesTransitions},
		{"ReturnsCopies", testReturnsCopies},
		{"HealthRoundTrip", testHealthRoundTrip},
		{"CredentialSwap", testCredentialSwap},
		{"ConcurrentCredentialSwap", testConcurrentCredentialSwap},
		{"GrantsAndBindings", testGrantsAndBindings},
		{"DeleteCascades", testDeleteCascades},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.fn(t, newStore(t)) })
	}
}

func conn(owner cf.OwnerID, id, provider string) cf.Connection {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return cf.Connection{
		ID: id, Owner: owner, Provider: provider, AuthMethod: "api_token",
		DisplayName: id, State: cf.StatePending, Scopes: []string{"read"},
		Metadata: map[string]string{"k": "v"}, CreatedBy: alice, CreatedAt: now, UpdatedAt: now,
	}
}

func mustCreate(t *testing.T, s cf.Store, c cf.Connection) cf.Connection {
	t.Helper()
	got, err := s.CreateConnection(context.Background(), c)
	if err != nil {
		t.Fatalf("create %s: %v", c.ID, err)
	}
	return got
}

func cred(owner cf.OwnerID, id string, version int) cf.Credential {
	return cf.Credential{
		Owner: owner, ConnectionID: id, Version: version, Kind: "api_token",
		Ciphertext: []byte(fmt.Sprintf("sealed-%d", version)),
		CreatedAt:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func testCreateGetList(t *testing.T, s cf.Store) {
	ctx := context.Background()
	got := mustCreate(t, s, conn(ownerA, "c2", "github"))
	if got.Revision != 1 {
		t.Fatalf("revision = %d, want 1", got.Revision)
	}
	mustCreate(t, s, conn(ownerA, "c1", "cloudflare"))
	mustCreate(t, s, conn(ownerB, "c3", "cloudflare"))
	if _, err := s.CreateConnection(ctx, conn(ownerA, "c1", "cloudflare")); !errors.Is(err, cf.ErrExists) {
		t.Fatalf("duplicate create: %v", err)
	}
	c, err := s.GetConnection(ctx, ownerA, "c2")
	if err != nil || c.Provider != "github" || c.DisplayName != "c2" || c.Scopes[0] != "read" || c.Metadata["k"] != "v" {
		t.Fatalf("get: %+v, %v", c, err)
	}
	list, err := s.ListConnections(ctx, ownerA, cf.Filter{})
	if err != nil || len(list) != 2 || list[0].ID != "c1" || list[1].ID != "c2" {
		t.Fatalf("list: %+v, %v", list, err)
	}
	list, err = s.ListConnections(ctx, ownerA, cf.Filter{Provider: "github"})
	if err != nil || len(list) != 1 || list[0].ID != "c2" {
		t.Fatalf("list by provider: %+v, %v", list, err)
	}
	list, err = s.ListConnections(ctx, ownerA, cf.Filter{State: cf.StateConnected})
	if err != nil || len(list) != 0 {
		t.Fatalf("list by state: %+v, %v", list, err)
	}
}

func testOwnerIsolation(t *testing.T, s cf.Store) {
	ctx := context.Background()
	c := mustCreate(t, s, conn(ownerA, "c1", "github"))
	if err := s.SwapCredential(ctx, 0, cred(ownerA, "c1", 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutGrant(ctx, cf.Grant{Owner: ownerA, ConnectionID: "c1", Principal: alice, Actions: []cf.Action{cf.ActionUse}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetConnection(ctx, ownerB, "c1"); !errors.Is(err, cf.ErrNotFound) {
		t.Errorf("get as other owner: %v", err)
	}
	other := c
	other.Owner = ownerB
	other.DisplayName = "stolen"
	if _, err := s.UpdateConnection(ctx, other); !errors.Is(err, cf.ErrNotFound) {
		t.Errorf("update as other owner: %v", err)
	}
	if err := s.DeleteConnection(ctx, ownerB, "c1"); !errors.Is(err, cf.ErrNotFound) {
		t.Errorf("delete as other owner: %v", err)
	}
	if list, err := s.ListConnections(ctx, ownerB, cf.Filter{}); err != nil || len(list) != 0 {
		t.Errorf("list as other owner: %v, %v", list, err)
	}
	if _, err := s.ActiveCredential(ctx, ownerB, "c1"); !errors.Is(err, cf.ErrNotFound) {
		t.Errorf("credential as other owner: %v", err)
	}
	if err := s.SwapCredential(ctx, 1, cred(ownerB, "c1", 2)); !errors.Is(err, cf.ErrNotFound) {
		t.Errorf("swap as other owner: %v", err)
	}
	if err := s.DestroyCredentials(ctx, ownerB, "c1"); err != nil {
		t.Errorf("destroy as other owner: %v", err)
	}
	if _, err := s.ActiveCredential(ctx, ownerA, "c1"); err != nil {
		t.Errorf("credential destroyed by other owner: %v", err)
	}
	if err := s.PutGrant(ctx, cf.Grant{Owner: ownerB, ConnectionID: "c1", Principal: alice}); !errors.Is(err, cf.ErrNotFound) {
		t.Errorf("grant as other owner: %v", err)
	}
	if gs, err := s.ListGrants(ctx, ownerB, "c1"); err != nil || len(gs) != 0 {
		t.Errorf("grants as other owner: %v, %v", gs, err)
	}
	if err := s.PutBinding(ctx, cf.Binding{Owner: ownerB, ConnectionID: "c1", Consumer: cf.Consumer{Kind: "app", ID: "x"}}); !errors.Is(err, cf.ErrNotFound) {
		t.Errorf("binding as other owner: %v", err)
	}
	if got, err := s.GetConnection(ctx, ownerA, "c1"); err != nil || got.DisplayName != "c1" {
		t.Errorf("connection changed by other owner: %+v, %v", got, err)
	}
}

func testUpdateCompareAndSwap(t *testing.T, s cf.Store) {
	ctx := context.Background()
	c := mustCreate(t, s, conn(ownerA, "c1", "github"))
	c.DisplayName = "renamed"
	c.State = cf.StateConnected
	c.Provider = "changed"
	u, err := s.UpdateConnection(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if u.Revision != 2 || u.DisplayName != "renamed" || u.State != cf.StateConnected {
		t.Fatalf("updated: %+v", u)
	}
	if u.Provider != "github" {
		t.Errorf("provider changed on update: %q", u.Provider)
	}
	if _, err := s.UpdateConnection(ctx, c); !errors.Is(err, cf.ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if _, err := s.UpdateConnection(ctx, conn(ownerA, "missing", "x")); !errors.Is(err, cf.ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}

	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			x := u
			x.DisplayName = fmt.Sprintf("w%d", i)
			if _, err := s.UpdateConnection(ctx, x); err == nil {
				wins.Add(1)
			} else if !errors.Is(err, cf.ErrConflict) {
				t.Errorf("concurrent update: %v", err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d concurrent updates won, want 1", wins.Load())
	}
}

func testUpdateEnforcesTransitions(t *testing.T, s cf.Store) {
	ctx := context.Background()
	c := mustCreate(t, s, conn(ownerA, "c1", "github"))
	c.State = cf.StateRevoked
	if _, err := s.UpdateConnection(ctx, c); !errors.Is(err, cf.ErrInvalidTransition) {
		t.Fatalf("pending to revoked: %v", err)
	}
	c.State = cf.StateConnected
	c, err := s.UpdateConnection(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	c.State = cf.StateRevoked
	if c, err = s.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.State = cf.StateConnected
	if _, err := s.UpdateConnection(ctx, c); !errors.Is(err, cf.ErrInvalidTransition) {
		t.Fatalf("revoked is terminal: %v", err)
	}
}

func testReturnsCopies(t *testing.T, s cf.Store) {
	ctx := context.Background()
	in := conn(ownerA, "c1", "github")
	mustCreate(t, s, in)
	in.Scopes[0] = "mutated"
	got, err := s.GetConnection(ctx, ownerA, "c1")
	if err != nil {
		t.Fatal(err)
	}
	got.Scopes[0] = "mutated"
	got.Metadata["k"] = "mutated"
	again, err := s.GetConnection(ctx, ownerA, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if again.Scopes[0] != "read" || again.Metadata["k"] != "v" {
		t.Fatalf("store shares memory with callers: %+v", again)
	}
	cr := cred(ownerA, "c1", 1)
	if err := s.SwapCredential(ctx, 0, cr); err != nil {
		t.Fatal(err)
	}
	cr.Ciphertext[0] = 'X'
	a, err := s.ActiveCredential(ctx, ownerA, "c1")
	if err != nil || string(a.Ciphertext) != "sealed-1" {
		t.Fatalf("credential shares memory: %q, %v", a.Ciphertext, err)
	}
}

// testHealthRoundTrip checks that a connection's health, including the
// credential's expiry, is stored and read back, is not shared with callers,
// and is cleared when a replaced credential reports none.
func testHealthRoundTrip(t *testing.T, s cf.Store) {
	ctx := context.Background()
	c := mustCreate(t, s, conn(ownerA, "c1", "cloudflare"))
	checked := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	exp := time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC)
	c.State = cf.StateConnected
	stored := exp // the store gets its own copy, so exp stays the expected value
	c.Health = cf.Health{LastValidatedAt: checked, LastValidation: cf.OutcomeOK, CredentialExpiresAt: &stored}
	c, err := s.UpdateConnection(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetConnection(ctx, ownerA, "c1")
	if err != nil {
		t.Fatal(err)
	}
	h := got.Health
	if !h.LastValidatedAt.Equal(checked) || h.LastValidation != cf.OutcomeOK ||
		h.CredentialExpiresAt == nil || !h.CredentialExpiresAt.Equal(exp) {
		t.Fatalf("health: %+v", h)
	}
	*got.Health.CredentialExpiresAt = checked
	again, err := s.GetConnection(ctx, ownerA, "c1")
	if err != nil || again.Health.CredentialExpiresAt == nil || !again.Health.CredentialExpiresAt.Equal(exp) {
		t.Fatalf("expiry shares memory with callers: %+v, %v", again.Health, err)
	}

	c.Health.CredentialExpiresAt = nil
	if _, err := s.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListConnections(ctx, ownerA, cf.Filter{})
	if err != nil || len(list) != 1 || list[0].Health.CredentialExpiresAt != nil {
		t.Fatalf("cleared expiry: %+v, %v", list, err)
	}
}

func testCredentialSwap(t *testing.T, s cf.Store) {
	ctx := context.Background()
	mustCreate(t, s, conn(ownerA, "c1", "github"))
	if _, err := s.ActiveCredential(ctx, ownerA, "c1"); !errors.Is(err, cf.ErrNotFound) {
		t.Fatalf("no credential yet: %v", err)
	}
	if err := s.SwapCredential(ctx, 0, cred(ownerA, "missing", 1)); !errors.Is(err, cf.ErrNotFound) {
		t.Fatalf("swap for missing connection: %v", err)
	}
	if err := s.SwapCredential(ctx, 0, cred(ownerA, "c1", 2)); !errors.Is(err, cf.ErrConflict) {
		t.Fatalf("skipping a version: %v", err)
	}
	if err := s.SwapCredential(ctx, 0, cred(ownerA, "c1", 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.SwapCredential(ctx, 0, cred(ownerA, "c1", 1)); !errors.Is(err, cf.ErrConflict) {
		t.Fatalf("stale swap: %v", err)
	}
	exp := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	c2 := cred(ownerA, "c1", 2)
	c2.ExpiresAt = &exp
	if err := s.SwapCredential(ctx, 1, c2); err != nil {
		t.Fatal(err)
	}
	a, err := s.ActiveCredential(ctx, ownerA, "c1")
	if err != nil || a.Version != 2 || string(a.Ciphertext) != "sealed-2" || a.Kind != "api_token" ||
		a.ExpiresAt == nil || !a.ExpiresAt.Equal(exp) {
		t.Fatalf("active: %+v, %v", a, err)
	}
	if err := s.DestroyCredentials(ctx, ownerA, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActiveCredential(ctx, ownerA, "c1"); !errors.Is(err, cf.ErrNotFound) {
		t.Fatalf("after destroy: %v", err)
	}
	if err := s.SwapCredential(ctx, 0, cred(ownerA, "c1", 1)); err != nil {
		t.Fatalf("reconnect after destroy: %v", err)
	}
}

func testConcurrentCredentialSwap(t *testing.T, s cf.Store) {
	ctx := context.Background()
	mustCreate(t, s, conn(ownerA, "c1", "github"))
	if err := s.SwapCredential(ctx, 0, cred(ownerA, "c1", 1)); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := s.SwapCredential(ctx, 1, cred(ownerA, "c1", 2)); err == nil {
				wins.Add(1)
			} else if !errors.Is(err, cf.ErrConflict) {
				t.Errorf("concurrent swap: %v", err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d concurrent swaps won, want 1", wins.Load())
	}
}

func testGrantsAndBindings(t *testing.T, s cf.Store) {
	ctx := context.Background()
	mustCreate(t, s, conn(ownerA, "c1", "github"))
	bob := cf.Principal{Kind: cf.PrincipalAgent, ID: "bob"}
	for _, g := range []cf.Grant{
		{Owner: ownerA, ConnectionID: "c1", Principal: bob, Actions: []cf.Action{cf.ActionUse}},
		{Owner: ownerA, ConnectionID: "c1", Principal: alice, Actions: []cf.Action{cf.ActionUse}},
		{Owner: ownerA, ConnectionID: "c1", Principal: alice, Actions: []cf.Action{cf.ActionManage, cf.ActionUse}},
	} {
		if err := s.PutGrant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	gs, err := s.ListGrants(ctx, ownerA, "c1")
	if err != nil || len(gs) != 2 || gs[0].Principal != bob || gs[1].Principal != alice || len(gs[1].Actions) != 2 {
		t.Fatalf("grants: %+v, %v", gs, err)
	}
	if err := s.RemoveGrant(ctx, ownerA, "c1", bob); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveGrant(ctx, ownerA, "c1", bob); err != nil {
		t.Fatalf("removing none: %v", err)
	}
	if gs, err = s.ListGrants(ctx, ownerA, "c1"); err != nil || len(gs) != 1 {
		t.Fatalf("after remove: %+v, %v", gs, err)
	}

	app := cf.Consumer{Kind: "application", ID: "dash"}
	b := cf.Binding{Owner: ownerA, ConnectionID: "c1", Consumer: app, Purpose: "traffic",
		CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	for range 2 {
		if err := s.PutBinding(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	b2 := b
	b2.Purpose = "security"
	if err := s.PutBinding(ctx, b2); err != nil {
		t.Fatal(err)
	}
	bs, err := s.ListBindings(ctx, ownerA, "c1")
	if err != nil || len(bs) != 2 || bs[0].Purpose != "security" || bs[1].Purpose != "traffic" {
		t.Fatalf("bindings: %+v, %v", bs, err)
	}
	if err := s.RemoveBinding(ctx, ownerA, "c1", app, "traffic"); err != nil {
		t.Fatal(err)
	}
	if bs, err = s.ListBindings(ctx, ownerA, "c1"); err != nil || len(bs) != 1 {
		t.Fatalf("after remove: %+v, %v", bs, err)
	}
}

func testDeleteCascades(t *testing.T, s cf.Store) {
	ctx := context.Background()
	mustCreate(t, s, conn(ownerA, "c1", "github"))
	if err := s.SwapCredential(ctx, 0, cred(ownerA, "c1", 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutGrant(ctx, cf.Grant{Owner: ownerA, ConnectionID: "c1", Principal: alice, Actions: []cf.Action{cf.ActionUse}}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutBinding(ctx, cf.Binding{Owner: ownerA, ConnectionID: "c1", Consumer: cf.Consumer{Kind: "app", ID: "x"}, Purpose: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConnection(ctx, ownerA, "c1"); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, conn(ownerA, "c1", "github"))
	if _, err := s.ActiveCredential(ctx, ownerA, "c1"); !errors.Is(err, cf.ErrNotFound) {
		t.Errorf("credential survived delete: %v", err)
	}
	if gs, err := s.ListGrants(ctx, ownerA, "c1"); err != nil || len(gs) != 0 {
		t.Errorf("grants survived delete: %+v, %v", gs, err)
	}
	if bs, err := s.ListBindings(ctx, ownerA, "c1"); err != nil || len(bs) != 0 {
		t.Errorf("bindings survived delete: %+v, %v", bs, err)
	}
}

// RunLeaser runs the leaser suite. newLeaser returns a fresh leaser.
func RunLeaser(t *testing.T, newLeaser func(t *testing.T) cf.Leaser) {
	t.Helper()
	t.Run("ExclusiveUntilReleased", func(t *testing.T) {
		ctx := context.Background()
		l := newLeaser(t)
		a, err := l.Acquire(ctx, "refresh:c1", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.Acquire(ctx, "refresh:c1", time.Minute); !errors.Is(err, cf.ErrLeaseHeld) {
			t.Fatalf("second acquire: %v", err)
		}
		other, err := l.Acquire(ctx, "refresh:c2", time.Minute)
		if err != nil {
			t.Fatalf("other key: %v", err)
		}
		if err := a.Release(ctx); err != nil {
			t.Fatal(err)
		}
		b, err := l.Acquire(ctx, "refresh:c1", time.Minute)
		if err != nil {
			t.Fatalf("after release: %v", err)
		}
		// Releasing an old lease must not release the current holder's.
		if err := a.Release(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := l.Acquire(ctx, "refresh:c1", time.Minute); !errors.Is(err, cf.ErrLeaseHeld) {
			t.Fatalf("stale release freed the lease: %v", err)
		}
		for _, x := range []cf.Lease{b, other} {
			if err := x.Release(ctx); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("OneWinnerUnderConcurrency", func(t *testing.T) {
		ctx := context.Background()
		l := newLeaser(t)
		var wins atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if _, err := l.Acquire(ctx, "k", time.Minute); err == nil {
					wins.Add(1)
				} else if !errors.Is(err, cf.ErrLeaseHeld) {
					t.Errorf("acquire: %v", err)
				}
			})
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("%d acquires won, want 1", wins.Load())
		}
	})
}
