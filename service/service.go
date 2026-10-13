// Package service holds connectionforge's operations: create, get, list,
// rename, test and disconnect. Each checks authorization through the host's
// [cf.Authorizer] and records an audit event.
package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"time"

	cf "github.com/plexusone/connectionforge"
	"github.com/plexusone/connectionforge/broker"
	"github.com/plexusone/connectionforge/provider"
)

// ErrNotAudited wraps an audit failure after an operation succeeded: the
// change was made, and the gap in the trail must be treated as an incident.
var ErrNotAudited = errors.New("service: the change was made but its event was not recorded")

// ErrInvalidInput is returned for input that does not match the provider's
// manifest.
var ErrInvalidInput = errors.New("service: invalid input")

// Options configures a [Service].
type Options struct {
	Store      cf.Store
	Broker     *broker.Broker
	Providers  *provider.Registry
	Authorizer cf.Authorizer
	Auditor    cf.Auditor
	Clock      cf.Clock
	// NewID returns a new connection ID. The default is a random UUID.
	NewID func() string
}

// Service runs connection operations.
type Service struct {
	o Options
}

// New returns a service.
func New(o Options) (*Service, error) {
	switch {
	case o.Store == nil, o.Broker == nil, o.Providers == nil, o.Authorizer == nil, o.Auditor == nil:
		return nil, errors.New("service: Store, Broker, Providers, Authorizer and Auditor are required")
	}
	if o.Clock == nil {
		o.Clock = cf.SystemClock{}
	}
	if o.NewID == nil {
		o.NewID = NewUUID
	}
	return &Service{o: o}, nil
}

// NewUUID returns a random (version 4) UUID.
func NewUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// CreateInput is a new connection.
type CreateInput struct {
	Owner       cf.OwnerID
	Actor       cf.Principal
	Provider    string
	AuthMethod  string
	DisplayName string
	// Fields are the method's inputs, by field name.
	Fields   map[string]cf.Secret
	Metadata map[string]string
}

// Create validates the credential with the provider and, if it is accepted,
// stores the connection and its sealed credential. A rejected credential
// stores nothing.
func (s *Service) Create(ctx context.Context, in CreateInput) (cf.Connection, error) {
	prov, err := s.o.Providers.Get(in.Provider)
	if err != nil {
		return cf.Connection{}, err
	}
	man := prov.Manifest()
	method, ok := man.Method(in.AuthMethod)
	if !ok {
		return cf.Connection{}, fmt.Errorf("%w: %s has no auth method %q", ErrInvalidInput, in.Provider, in.AuthMethod)
	}
	if err := checkFields(method, in.Fields); err != nil {
		return cf.Connection{}, err
	}
	now := s.o.Clock.Now()
	c := cf.Connection{
		ID: s.o.NewID(), Owner: in.Owner, Provider: in.Provider, AuthMethod: in.AuthMethod,
		DisplayName: in.DisplayName, State: cf.StatePending, Metadata: maps.Clone(in.Metadata),
		CreatedBy: in.Actor, CreatedAt: now, UpdatedAt: now,
	}
	if c.DisplayName == "" {
		c.DisplayName = man.DisplayName
	}
	if err := s.allow(ctx, in.Actor, cf.ActionManage, c); err != nil {
		return cf.Connection{}, err
	}

	m := provider.Material{Kind: in.AuthMethod, Fields: in.Fields}
	hc, err := s.o.Broker.ProviderClient(man)
	if err != nil {
		return cf.Connection{}, err
	}
	res, err := prov.Validate(ctx, hc, m)
	if err != nil {
		return cf.Connection{}, s.audited(ctx, event(c, in.Actor, cf.EventConnectionCreated, now, err), err)
	}
	c.ExternalAccountID, c.ExternalLabel, c.Scopes = res.AccountID, res.AccountLabel, res.Scopes
	c.State = cf.StateConnected
	c.Health = cf.Health{LastValidatedAt: now, LastValidation: cf.OutcomeOK}

	created, err := s.o.Store.CreateConnection(ctx, c)
	if err != nil {
		return cf.Connection{}, err
	}
	cred, err := s.o.Broker.Seal(ctx, created, 1, m)
	if err == nil {
		err = s.o.Store.SwapCredential(ctx, 0, cred)
	}
	if err != nil {
		if derr := s.o.Store.DeleteConnection(ctx, created.Owner, created.ID); derr != nil {
			err = errors.Join(err, fmt.Errorf("service: removing the incomplete connection: %w", derr))
		}
		return cf.Connection{}, err
	}
	return created, s.audited(ctx, event(created, in.Actor, cf.EventConnectionCreated, now, nil), nil)
}

func checkFields(m provider.AuthMethod, fields map[string]cf.Secret) error {
	known := map[string]bool{}
	for _, f := range m.Fields {
		known[f.Name] = true
		if f.Required && fields[f.Name].IsZero() {
			return fmt.Errorf("%w: %s is required", ErrInvalidInput, f.Name)
		}
	}
	for name := range fields {
		if !known[name] {
			return fmt.Errorf("%w: unknown field %s", ErrInvalidInput, name)
		}
	}
	return nil
}

// Get returns a connection the actor may view.
func (s *Service) Get(ctx context.Context, owner cf.OwnerID, actor cf.Principal, id string) (cf.Connection, error) {
	c, err := s.o.Store.GetConnection(ctx, owner, id)
	if err != nil {
		return cf.Connection{}, err
	}
	if err := s.allow(ctx, actor, cf.ActionView, c); err != nil {
		return cf.Connection{}, err
	}
	return c, nil
}

// List returns the owner's connections that the actor may view.
func (s *Service) List(ctx context.Context, owner cf.OwnerID, actor cf.Principal, f cf.Filter) ([]cf.Connection, error) {
	all, err := s.o.Store.ListConnections(ctx, owner, f)
	if err != nil {
		return nil, err
	}
	out := make([]cf.Connection, 0, len(all))
	for _, c := range all {
		err := s.o.Authorizer.Allowed(ctx, actor, cf.ActionView, c)
		switch {
		case err == nil:
			out = append(out, c)
		case !errors.Is(err, cf.ErrDenied):
			return nil, err
		}
	}
	return out, nil
}

// Rename changes a connection's display name.
func (s *Service) Rename(ctx context.Context, owner cf.OwnerID, actor cf.Principal, id, name string) (cf.Connection, error) {
	if name == "" {
		return cf.Connection{}, fmt.Errorf("%w: a name is required", ErrInvalidInput)
	}
	c, err := s.update(ctx, owner, actor, id, cf.ActionManage, func(c *cf.Connection) error {
		c.DisplayName = name
		return nil
	})
	if err != nil {
		return cf.Connection{}, err
	}
	return c, s.audited(ctx, event(c, actor, cf.EventConnectionUpdated, s.o.Clock.Now(), nil), nil)
}

// Test validates a connection's credential with its provider and records
// the result as health. A permanent failure moves the connection to
// needs_attention and a success moves it back to connected; a transient
// failure changes only health. The validation outcome is in the returned
// connection's health, not in the error.
func (s *Service) Test(ctx context.Context, owner cf.OwnerID, actor cf.Principal, id string) (cf.Connection, error) {
	c, err := s.o.Store.GetConnection(ctx, owner, id)
	if err != nil {
		return cf.Connection{}, err
	}
	if err := s.allow(ctx, actor, cf.ActionUse, c); err != nil {
		return cf.Connection{}, err
	}
	if c.State != cf.StateConnected && c.State != cf.StateNeedsAttention {
		return cf.Connection{}, fmt.Errorf("%w: %s is %s", cf.ErrNotUsable, c.ID, c.State)
	}
	prov, err := s.o.Providers.Get(c.Provider)
	if err != nil {
		return cf.Connection{}, err
	}
	m, err := s.o.Broker.Unseal(ctx, c)
	if err != nil {
		return cf.Connection{}, err
	}
	defer m.Clear()
	hc, err := s.o.Broker.ProviderClient(prov.Manifest())
	if err != nil {
		return cf.Connection{}, err
	}
	res, verr := prov.Validate(ctx, hc, m)
	now := s.o.Clock.Now()
	c, err = s.update(ctx, owner, actor, id, cf.ActionUse, func(c *cf.Connection) error {
		if verr != nil {
			c.Health.LastValidatedAt = now
			c.Health.LastValidation = cf.OutcomeFailed
			c.Health.LastError = verr.Error()
			c.Health.ConsecutiveFails++
			if cf.Permanent(verr) {
				c.State = cf.StateNeedsAttention
			}
			return nil
		}
		c.Health = cf.Health{LastValidatedAt: now, LastValidation: cf.OutcomeOK}
		c.State = cf.StateConnected
		if res.AccountLabel != "" {
			c.ExternalLabel = res.AccountLabel
		}
		if res.Scopes != nil {
			c.Scopes = res.Scopes
		}
		return nil
	})
	if err != nil {
		return cf.Connection{}, err
	}
	return c, s.audited(ctx, event(c, actor, cf.EventConnectionValidated, now, verr), nil)
}

// Disconnect destroys a connection's credentials and marks it disconnected.
// The record stays, so the audit trail and bindings keep their reference.
func (s *Service) Disconnect(ctx context.Context, owner cf.OwnerID, actor cf.Principal, id string) (cf.Connection, error) {
	c, err := s.update(ctx, owner, actor, id, cf.ActionDisconnect, func(c *cf.Connection) error {
		c.State = cf.StateDisconnected
		return nil
	})
	if err != nil {
		return cf.Connection{}, err
	}
	if err := s.o.Store.DestroyCredentials(ctx, owner, id); err != nil {
		return c, fmt.Errorf("service: connection is disconnected but its credentials were not destroyed: %w", err)
	}
	return c, s.audited(ctx, event(c, actor, cf.EventConnectionDisconnected, s.o.Clock.Now(), nil), nil)
}

// ReconnectInput is a replacement credential for an existing connection.
type ReconnectInput struct {
	Owner cf.OwnerID
	Actor cf.Principal
	ID    string
	// Fields are the auth method's inputs, by field name, as for Create.
	Fields map[string]cf.Secret
}

// Reconnect replaces a connection's credential with a new one, keeping the
// connection, so what is bound to it keeps working. The new credential is
// validated with the provider first; if it is rejected, or belongs to a
// different account, nothing changes. On success the previous versions stay
// until the connection's credentials are destroyed, the connection is
// `connected` again with fresh health, and a connection that needed
// attention, or was disconnected, is usable.
//
// It also recovers a connection whose sealed credential can no longer be
// read, since the new one is sealed without opening the old.
func (s *Service) Reconnect(ctx context.Context, in ReconnectInput) (cf.Connection, error) {
	c, err := s.o.Store.GetConnection(ctx, in.Owner, in.ID)
	if err != nil {
		return cf.Connection{}, err
	}
	if err := s.allow(ctx, in.Actor, cf.ActionManage, c); err != nil {
		return cf.Connection{}, err
	}
	if c.State == cf.StateRevoked {
		return cf.Connection{}, fmt.Errorf("%w: %s is revoked", cf.ErrNotUsable, c.ID)
	}
	prov, err := s.o.Providers.Get(c.Provider)
	if err != nil {
		return cf.Connection{}, err
	}
	man := prov.Manifest()
	method, ok := man.Method(c.AuthMethod)
	if !ok {
		return cf.Connection{}, fmt.Errorf("%w: %s has no auth method %q", ErrInvalidInput, c.Provider, c.AuthMethod)
	}
	if err := checkFields(method, in.Fields); err != nil {
		return cf.Connection{}, err
	}

	m := provider.Material{Kind: c.AuthMethod, Fields: in.Fields}
	hc, err := s.o.Broker.ProviderClient(man)
	if err != nil {
		return cf.Connection{}, err
	}
	res, verr := prov.Validate(ctx, hc, m)
	now := s.o.Clock.Now()
	if verr != nil {
		return cf.Connection{}, s.audited(ctx, event(c, in.Actor, cf.EventCredentialReplaced, now, verr), verr)
	}
	if c.ExternalAccountID != "" && res.AccountID != "" && c.ExternalAccountID != res.AccountID {
		return cf.Connection{}, s.audited(ctx, event(c, in.Actor, cf.EventCredentialReplaced, now, cf.ErrAccountMismatch), cf.ErrAccountMismatch)
	}

	active := 0
	if cur, err := s.o.Store.ActiveCredential(ctx, in.Owner, in.ID); err == nil {
		active = cur.Version
	} else if !errors.Is(err, cf.ErrNotFound) {
		return cf.Connection{}, err
	}
	cred, err := s.o.Broker.Seal(ctx, c, active+1, m)
	if err != nil {
		return cf.Connection{}, err
	}
	if err := s.o.Store.SwapCredential(ctx, active, cred); err != nil {
		return cf.Connection{}, err
	}

	out, err := s.update(ctx, in.Owner, in.Actor, in.ID, cf.ActionManage, func(c *cf.Connection) error {
		c.State = cf.StateConnected
		c.Health = cf.Health{LastValidatedAt: now, LastValidation: cf.OutcomeOK}
		if res.AccountID != "" {
			c.ExternalAccountID = res.AccountID
		}
		if res.AccountLabel != "" {
			c.ExternalLabel = res.AccountLabel
		}
		if res.Scopes != nil {
			c.Scopes = res.Scopes
		}
		return nil
	})
	if err != nil {
		return cf.Connection{}, fmt.Errorf("service: the credential was replaced but the connection was not updated: %w", err)
	}
	return out, s.audited(ctx, event(out, in.Actor, cf.EventCredentialReplaced, now, nil), nil)
}

// Bindings lists what depends on a connection, so a person can see what a
// disconnect would stop. It requires view.
func (s *Service) Bindings(ctx context.Context, owner cf.OwnerID, actor cf.Principal, id string) ([]cf.Binding, error) {
	c, err := s.o.Store.GetConnection(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if err := s.allow(ctx, actor, cf.ActionView, c); err != nil {
		return nil, err
	}
	return s.o.Store.ListBindings(ctx, owner, id)
}

// Bind records that a consumer uses a connection for a purpose. It is
// idempotent and requires use: whoever may use a connection may say that a
// feature depends on it.
func (s *Service) Bind(ctx context.Context, owner cf.OwnerID, actor cf.Principal, id string, consumer cf.Consumer, purpose string) error {
	return s.changeBinding(ctx, owner, actor, id, consumer, purpose, true)
}

// Unbind removes a binding. It requires use and is not an error when none
// exists.
func (s *Service) Unbind(ctx context.Context, owner cf.OwnerID, actor cf.Principal, id string, consumer cf.Consumer, purpose string) error {
	return s.changeBinding(ctx, owner, actor, id, consumer, purpose, false)
}

func (s *Service) changeBinding(ctx context.Context, owner cf.OwnerID, actor cf.Principal, id string, consumer cf.Consumer, purpose string, add bool) error {
	if consumer.Kind == "" || consumer.ID == "" || purpose == "" {
		return fmt.Errorf("%w: a binding needs a consumer kind, a consumer ID and a purpose", ErrInvalidInput)
	}
	c, err := s.o.Store.GetConnection(ctx, owner, id)
	if err != nil {
		return err
	}
	if err := s.allow(ctx, actor, cf.ActionUse, c); err != nil {
		return err
	}
	now := s.o.Clock.Now()
	if add {
		err = s.o.Store.PutBinding(ctx, cf.Binding{Owner: owner, ConnectionID: id, Consumer: consumer, Purpose: purpose, CreatedAt: now})
	} else {
		err = s.o.Store.RemoveBinding(ctx, owner, id, consumer, purpose)
	}
	if err != nil {
		return err
	}
	e := event(c, actor, cf.EventBindingChanged, now, nil)
	e.Details = map[string]string{"consumer": consumer.Kind + ":" + consumer.ID, "purpose": purpose, "bound": fmt.Sprint(add)}
	return s.audited(ctx, e, nil)
}

// maxAttempts bounds retries of a compare-and-swap that lost a race.
const maxAttempts = 3

// update reads, authorizes, changes and writes a connection, retrying when
// another writer got there first.
func (s *Service) update(ctx context.Context, owner cf.OwnerID, actor cf.Principal, id string, a cf.Action, change func(*cf.Connection) error) (cf.Connection, error) {
	for range maxAttempts {
		c, err := s.o.Store.GetConnection(ctx, owner, id)
		if err != nil {
			return cf.Connection{}, err
		}
		if err := s.allow(ctx, actor, a, c); err != nil {
			return cf.Connection{}, err
		}
		next := c.Clone()
		if err := change(&next); err != nil {
			return cf.Connection{}, err
		}
		next.UpdatedAt = s.o.Clock.Now()
		out, err := s.o.Store.UpdateConnection(ctx, next)
		if errors.Is(err, cf.ErrConflict) {
			continue
		}
		return out, err
	}
	return cf.Connection{}, cf.ErrConflict
}

// allow checks authorization and records a refusal.
func (s *Service) allow(ctx context.Context, actor cf.Principal, a cf.Action, c cf.Connection) error {
	err := s.o.Authorizer.Allowed(ctx, actor, a, c)
	if err == nil || !errors.Is(err, cf.ErrDenied) {
		return err
	}
	e := event(c, actor, cf.EventAccessDenied, s.o.Clock.Now(), err)
	e.Reason = string(a)
	return s.audited(ctx, e, err)
}

func event(c cf.Connection, actor cf.Principal, t cf.EventType, now time.Time, err error) cf.Event {
	e := cf.Event{Time: now, Type: t, Owner: c.Owner, Actor: actor, ConnectionID: c.ID, Provider: c.Provider, Success: err == nil}
	if err != nil {
		e.Reason = err.Error()
	}
	return e
}

// audited records e. With cause set it returns cause, joined with any audit
// failure; otherwise it returns nil or [ErrNotAudited].
func (s *Service) audited(ctx context.Context, e cf.Event, cause error) error {
	if err := s.o.Auditor.Record(ctx, e); err != nil {
		if cause != nil {
			return errors.Join(cause, fmt.Errorf("service: recording %s: %w", e.Type, err))
		}
		return fmt.Errorf("%w: %s: %w", ErrNotAudited, e.Type, err)
	}
	return cause
}
