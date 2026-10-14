package memstore_test

import (
	"context"
	"errors"
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
