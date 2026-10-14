// Package memstore is an in-memory [connectforge.Store] and
// [connectforge.Leaser]. It is the reference for behaviour that other
// stores are compared against, and it is meant for tests and examples.
package memstore

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	cf "github.com/plexusone/connectforge"
)

type connKey struct {
	owner cf.OwnerID
	id    string
}

type credSet struct {
	active   int
	versions map[int]cf.Credential
}

// Store is an in-memory store. It is safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	conns    map[connKey]cf.Connection
	creds    map[connKey]*credSet
	grants   map[connKey][]cf.Grant
	bindings map[connKey][]cf.Binding
	leases   map[string]leaseRec
	leaseSeq uint64
	clock    cf.Clock
}

// New returns an empty store using the system clock for leases.
func New() *Store { return NewWithClock(cf.SystemClock{}) }

// NewWithClock returns an empty store using clock for leases.
func NewWithClock(clock cf.Clock) *Store {
	return &Store{
		conns:    map[connKey]cf.Connection{},
		creds:    map[connKey]*credSet{},
		grants:   map[connKey][]cf.Grant{},
		bindings: map[connKey][]cf.Binding{},
		leases:   map[string]leaseRec{},
		clock:    clock,
	}
}

// CreateConnection implements [cf.ConnectionStore].
func (s *Store) CreateConnection(ctx context.Context, c cf.Connection) (cf.Connection, error) {
	if err := ctx.Err(); err != nil {
		return cf.Connection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := connKey{c.Owner, c.ID}
	if _, ok := s.conns[k]; ok {
		return cf.Connection{}, cf.ErrExists
	}
	c = c.Clone()
	c.Revision = 1
	s.conns[k] = c
	return c.Clone(), nil
}

// GetConnection implements [cf.ConnectionStore].
func (s *Store) GetConnection(ctx context.Context, owner cf.OwnerID, id string) (cf.Connection, error) {
	if err := ctx.Err(); err != nil {
		return cf.Connection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[connKey{owner, id}]
	if !ok {
		return cf.Connection{}, cf.ErrNotFound
	}
	return c.Clone(), nil
}

// ListConnections implements [cf.ConnectionStore].
func (s *Store) ListConnections(ctx context.Context, owner cf.OwnerID, f cf.Filter) ([]cf.Connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []cf.Connection{}
	for k, c := range s.conns {
		if k.owner == owner && f.Match(c) {
			out = append(out, c.Clone())
		}
	}
	slices.SortFunc(out, func(a, b cf.Connection) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// UpdateConnection implements [cf.ConnectionStore].
func (s *Store) UpdateConnection(ctx context.Context, c cf.Connection) (cf.Connection, error) {
	if err := ctx.Err(); err != nil {
		return cf.Connection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := connKey{c.Owner, c.ID}
	cur, ok := s.conns[k]
	if !ok {
		return cf.Connection{}, cf.ErrNotFound
	}
	if cur.Revision != c.Revision {
		return cf.Connection{}, cf.ErrConflict
	}
	if err := cur.State.CheckTransition(c.State); err != nil {
		return cf.Connection{}, err
	}
	c = c.Clone()
	c.Revision = cur.Revision + 1
	c.CreatedAt, c.CreatedBy, c.Provider = cur.CreatedAt, cur.CreatedBy, cur.Provider
	s.conns[k] = c
	return c.Clone(), nil
}

// DeleteConnection implements [cf.ConnectionStore].
func (s *Store) DeleteConnection(ctx context.Context, owner cf.OwnerID, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := connKey{owner, id}
	if _, ok := s.conns[k]; !ok {
		return cf.ErrNotFound
	}
	delete(s.conns, k)
	delete(s.creds, k)
	delete(s.grants, k)
	delete(s.bindings, k)
	return nil
}

// SwapCredential implements [cf.CredentialStore].
func (s *Store) SwapCredential(ctx context.Context, expectActive int, next cf.Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := connKey{next.Owner, next.ConnectionID}
	if _, ok := s.conns[k]; !ok {
		return cf.ErrNotFound
	}
	set := s.creds[k]
	if set == nil {
		set = &credSet{versions: map[int]cf.Credential{}}
		s.creds[k] = set
	}
	if set.active != expectActive || next.Version != expectActive+1 {
		return cf.ErrConflict
	}
	set.versions[next.Version] = next.Clone()
	set.active = next.Version
	return nil
}

// ActiveCredential implements [cf.CredentialStore].
func (s *Store) ActiveCredential(ctx context.Context, owner cf.OwnerID, connectionID string) (cf.Credential, error) {
	if err := ctx.Err(); err != nil {
		return cf.Credential{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.creds[connKey{owner, connectionID}]
	if set == nil || set.active == 0 {
		return cf.Credential{}, cf.ErrNotFound
	}
	return set.versions[set.active].Clone(), nil
}

// DestroyCredentials implements [cf.CredentialStore].
func (s *Store) DestroyCredentials(ctx context.Context, owner cf.OwnerID, connectionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := connKey{owner, connectionID}
	if set := s.creds[k]; set != nil {
		for _, v := range set.versions {
			clear(v.Ciphertext)
		}
	}
	delete(s.creds, k)
	return nil
}

// PutGrant implements [cf.GrantStore].
func (s *Store) PutGrant(ctx context.Context, g cf.Grant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := connKey{g.Owner, g.ConnectionID}
	if _, ok := s.conns[k]; !ok {
		return cf.ErrNotFound
	}
	gs := slices.DeleteFunc(s.grants[k], func(x cf.Grant) bool { return x.Principal == g.Principal })
	s.grants[k] = append(gs, g.Clone())
	return nil
}

// RemoveGrant implements [cf.GrantStore].
func (s *Store) RemoveGrant(ctx context.Context, owner cf.OwnerID, connectionID string, p cf.Principal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := connKey{owner, connectionID}
	s.grants[k] = slices.DeleteFunc(s.grants[k], func(x cf.Grant) bool { return x.Principal == p })
	return nil
}

// ListGrants implements [cf.GrantStore].
func (s *Store) ListGrants(ctx context.Context, owner cf.OwnerID, connectionID string) ([]cf.Grant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []cf.Grant{}
	for _, g := range s.grants[connKey{owner, connectionID}] {
		out = append(out, g.Clone())
	}
	slices.SortFunc(out, func(a, b cf.Grant) int {
		return cmp.Or(cmp.Compare(a.Principal.Kind, b.Principal.Kind), cmp.Compare(a.Principal.ID, b.Principal.ID))
	})
	return out, nil
}

func sameBinding(c cf.Consumer, purpose string) func(cf.Binding) bool {
	return func(b cf.Binding) bool { return b.Consumer == c && b.Purpose == purpose }
}

// PutBinding implements [cf.BindingStore].
func (s *Store) PutBinding(ctx context.Context, b cf.Binding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := connKey{b.Owner, b.ConnectionID}
	if _, ok := s.conns[k]; !ok {
		return cf.ErrNotFound
	}
	if slices.ContainsFunc(s.bindings[k], sameBinding(b.Consumer, b.Purpose)) {
		return nil
	}
	s.bindings[k] = append(s.bindings[k], b)
	return nil
}

// RemoveBinding implements [cf.BindingStore].
func (s *Store) RemoveBinding(ctx context.Context, owner cf.OwnerID, connectionID string, c cf.Consumer, purpose string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := connKey{owner, connectionID}
	s.bindings[k] = slices.DeleteFunc(s.bindings[k], sameBinding(c, purpose))
	return nil
}

// ListBindings implements [cf.BindingStore].
func (s *Store) ListBindings(ctx context.Context, owner cf.OwnerID, connectionID string) ([]cf.Binding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.bindings[connKey{owner, connectionID}])
	if out == nil {
		out = []cf.Binding{}
	}
	slices.SortFunc(out, func(a, b cf.Binding) int {
		return cmp.Or(cmp.Compare(a.Consumer.Kind, b.Consumer.Kind), cmp.Compare(a.Consumer.ID, b.Consumer.ID), cmp.Compare(a.Purpose, b.Purpose))
	})
	return out, nil
}

// Acquire implements [cf.Leaser].
func (s *Store) Acquire(ctx context.Context, key string, ttl time.Duration) (cf.Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	if cur, ok := s.leases[key]; ok && now.Before(cur.exp) {
		return nil, cf.ErrLeaseHeld
	}
	s.leaseSeq++
	s.leases[key] = leaseRec{exp: now.Add(ttl), id: s.leaseSeq}
	return &lease{s: s, key: key, id: s.leaseSeq}, nil
}

type leaseRec struct {
	exp time.Time
	id  uint64
}

type lease struct {
	s   *Store
	key string
	id  uint64
}

func (l *lease) Release(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	// Only release our own lease: after expiry someone else may hold the key.
	if l.s.leases[l.key].id == l.id {
		delete(l.s.leases, l.key)
	}
	return nil
}
