package memstore_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/store/memstore"
	"github.com/plexusone/connectforge/store/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(*testing.T) cf.Store { return memstore.New() })
}

func TestLeaserConformance(t *testing.T) {
	storetest.RunLeaser(t, func(*testing.T) cf.Leaser { return memstore.New() })
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func TestLeaseExpires(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := memstore.NewWithClock(clock)
	if _, err := s.Acquire(ctx, "k", time.Minute); err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(59 * time.Second)
	if _, err := s.Acquire(ctx, "k", time.Minute); !errors.Is(err, cf.ErrLeaseHeld) {
		t.Fatalf("before expiry: %v", err)
	}
	clock.t = clock.t.Add(time.Second)
	if _, err := s.Acquire(ctx, "k", time.Minute); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
}

func TestSwapRemovesSupersededCredentials(t *testing.T) {
	ctx := context.Background()
	s := memstore.New()
	if _, err := s.CreateConnection(ctx, cf.Connection{ID: "c1", Owner: "org-a", Provider: "p", State: cf.StateConnected}); err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 3; v++ {
		ct := []byte(fmt.Sprintf("sealed-%d", v))
		if err := s.SwapCredential(ctx, v-1, cf.Credential{Owner: "org-a", ConnectionID: "c1", Version: v, Kind: "k", Ciphertext: ct}); err != nil {
			t.Fatal(err)
		}
	}
	if got := memstore.CredentialVersions(s, "org-a", "c1"); !slices.Equal(got, []int{3}) {
		t.Fatalf("stored versions: %v", got)
	}
}
