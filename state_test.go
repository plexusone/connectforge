package connectionforge_test

import (
	"bytes"
	"errors"
	"testing"

	cf "github.com/plexusone/connectionforge"
)

func TestTransitionTable(t *testing.T) {
	allowed := map[[2]cf.State]bool{
		{cf.StatePending, cf.StateConnected}:           true,
		{cf.StatePending, cf.StateNeedsAttention}:      true,
		{cf.StatePending, cf.StateDisconnected}:        true,
		{cf.StateConnected, cf.StateNeedsAttention}:    true,
		{cf.StateConnected, cf.StateDisconnected}:      true,
		{cf.StateConnected, cf.StateRevoked}:           true,
		{cf.StateNeedsAttention, cf.StateConnected}:    true,
		{cf.StateNeedsAttention, cf.StateDisconnected}: true,
		{cf.StateNeedsAttention, cf.StateRevoked}:      true,
		{cf.StateDisconnected, cf.StateConnected}:      true,
	}
	for _, from := range cf.States() {
		if !from.Valid() {
			t.Fatalf("%s not valid", from)
		}
		for _, to := range cf.States() {
			want := from == to || allowed[[2]cf.State{from, to}]
			if got := from.CanTransition(to); got != want {
				t.Errorf("%s -> %s = %v, want %v", from, to, got, want)
			}
			if err := from.CheckTransition(to); (err == nil) != want || (err != nil && !errors.Is(err, cf.ErrInvalidTransition)) {
				t.Errorf("CheckTransition(%s, %s) = %v", from, to, err)
			}
		}
	}
	if cf.State("bogus").Valid() || cf.State("bogus").CanTransition("bogus") {
		t.Error("unknown state accepted")
	}
	for _, s := range cf.States() {
		if s.Usable() != (s == cf.StateConnected) {
			t.Errorf("%s usable = %v", s, s.Usable())
		}
	}
}

func TestCredentialAADIsDistinct(t *testing.T) {
	a := cf.CredentialAAD("o", "c", 1)
	for _, b := range [][]byte{
		cf.CredentialAAD("o", "c", 2),
		cf.CredentialAAD("o2", "c", 1),
		cf.CredentialAAD("o", "c2", 1),
		cf.CredentialAAD("oc", "", 1),
	} {
		if bytes.Equal(a, b) {
			t.Fatalf("aad collision: %q", b)
		}
	}
}
