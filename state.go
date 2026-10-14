package connectforge

import (
	"fmt"
	"slices"
)

// State is a connection's lifecycle state: what was decided. What was last
// observed is [Health]; a transient failure changes health, never state.
type State string

// Lifecycle states.
const (
	// StatePending is a connection created but not yet authorized or validated.
	StatePending State = "pending"
	// StateConnected is a connection whose credential the provider accepted at
	// the last check.
	StateConnected State = "connected"
	// StateNeedsAttention is a connection with a permanent failure: revoked
	// upstream, expired, or a scope lost. It is not used until reconnected.
	StateNeedsAttention State = "needs_attention"
	// StateDisconnected is a connection a person disconnected. Its credentials
	// are destroyed.
	StateDisconnected State = "disconnected"
	// StateRevoked is a connection whose credential was revoked at the
	// provider. It is terminal.
	StateRevoked State = "revoked"
)

// transitions is the lifecycle transition table. It is the single source for
// the documentation and for every store.
var transitions = map[State][]State{
	StatePending:        {StateConnected, StateNeedsAttention, StateDisconnected},
	StateConnected:      {StateNeedsAttention, StateDisconnected, StateRevoked},
	StateNeedsAttention: {StateConnected, StateDisconnected, StateRevoked},
	StateDisconnected:   {StateConnected},
	StateRevoked:        nil,
}

// States returns every lifecycle state.
func States() []State {
	return []State{StatePending, StateConnected, StateNeedsAttention, StateDisconnected, StateRevoked}
}

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	_, ok := transitions[s]
	return ok
}

// Next returns the states s may move to.
func (s State) Next() []State { return slices.Clone(transitions[s]) }

// CanTransition reports whether s may move to next. Staying in the same state
// is always allowed.
func (s State) CanTransition(next State) bool {
	return s == next && s.Valid() || slices.Contains(transitions[s], next)
}

// Usable reports whether a connection in state s may be used.
func (s State) Usable() bool { return s == StateConnected }

// CheckTransition returns an error wrapping [ErrInvalidTransition] if s may not
// move to next.
func (s State) CheckTransition(next State) error {
	if !s.CanTransition(next) {
		return fmt.Errorf("%w: %s to %s", ErrInvalidTransition, s, next)
	}
	return nil
}
