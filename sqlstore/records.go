package sqlstore

import (
	"context"
	"fmt"
	"time"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/sqlstore/ent"
	"github.com/plexusone/connectforge/sqlstore/ent/binding"
	"github.com/plexusone/connectforge/sqlstore/ent/connection"
	"github.com/plexusone/connectforge/sqlstore/ent/credential"
	"github.com/plexusone/connectforge/sqlstore/ent/grant"
	"github.com/plexusone/connectforge/sqlstore/ent/lease"
)

func toConnection(e *ent.Connection) cf.Connection {
	return cf.Connection{
		ID:                e.ConnectionID,
		Owner:             cf.OwnerID(e.OwnerID),
		Provider:          e.Provider,
		AuthMethod:        e.AuthMethod,
		DisplayName:       e.DisplayName,
		ExternalAccountID: e.ExternalAccountID,
		ExternalLabel:     e.ExternalLabel,
		Scopes:            e.Scopes,
		State:             cf.State(e.State),
		Health:            e.Health,
		Metadata:          e.Metadata,
		CreatedBy:         cf.Principal{Kind: cf.PrincipalKind(e.CreatedByKind), ID: e.CreatedByID},
		CreatedAt:         e.CreatedAt,
		UpdatedAt:         e.UpdatedAt,
		Revision:          e.Revision,
	}
}

// getConnection reads one connection inside a transaction.
func getConnection(ctx context.Context, c *ent.Client, owner cf.OwnerID, id string) (*ent.Connection, error) {
	e, err := c.Connection.Query().
		Where(connection.OwnerID(string(owner)), connection.ConnectionID(id)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, cf.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlstore: get connection: %w", err)
	}
	return e, nil
}

// CreateConnection implements [cf.ConnectionStore].
func (s *Store) CreateConnection(ctx context.Context, in cf.Connection) (cf.Connection, error) {
	var out cf.Connection
	err := s.inTx(ctx, func(c *ent.Client) error {
		e, err := c.Connection.Create().
			SetOwnerID(string(in.Owner)).
			SetConnectionID(in.ID).
			SetProvider(in.Provider).
			SetAuthMethod(in.AuthMethod).
			SetDisplayName(in.DisplayName).
			SetExternalAccountID(in.ExternalAccountID).
			SetExternalLabel(in.ExternalLabel).
			SetScopes(in.Scopes).
			SetState(string(in.State)).
			SetHealth(in.Health).
			SetMetadata(in.Metadata).
			SetCreatedByKind(string(in.CreatedBy.Kind)).
			SetCreatedByID(in.CreatedBy.ID).
			SetCreatedAt(in.CreatedAt).
			SetUpdatedAt(in.UpdatedAt).
			SetRevision(1).
			Save(ctx)
		if ent.IsConstraintError(err) {
			return cf.ErrExists
		}
		if err != nil {
			return fmt.Errorf("sqlstore: create connection: %w", err)
		}
		out = toConnection(e)
		return nil
	})
	return out, err
}

// GetConnection implements [cf.ConnectionStore].
func (s *Store) GetConnection(ctx context.Context, owner cf.OwnerID, id string) (cf.Connection, error) {
	var out cf.Connection
	err := s.inTx(ctx, func(c *ent.Client) error {
		e, err := getConnection(ctx, c, owner, id)
		if err != nil {
			return err
		}
		out = toConnection(e)
		return nil
	})
	return out, err
}

// ListConnections implements [cf.ConnectionStore].
func (s *Store) ListConnections(ctx context.Context, owner cf.OwnerID, f cf.Filter) ([]cf.Connection, error) {
	out := []cf.Connection{}
	err := s.inTx(ctx, func(c *ent.Client) error {
		q := c.Connection.Query().Where(connection.OwnerID(string(owner)))
		if f.Provider != "" {
			q = q.Where(connection.Provider(f.Provider))
		}
		if f.State != "" {
			q = q.Where(connection.State(string(f.State)))
		}
		es, err := q.Order(ent.Asc(connection.FieldConnectionID)).All(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: list connections: %w", err)
		}
		for _, e := range es {
			out = append(out, toConnection(e))
		}
		return nil
	})
	return out, err
}

// UpdateConnection implements [cf.ConnectionStore].
func (s *Store) UpdateConnection(ctx context.Context, in cf.Connection) (cf.Connection, error) {
	var out cf.Connection
	err := s.inTx(ctx, func(c *ent.Client) error {
		cur, err := getConnection(ctx, c, in.Owner, in.ID)
		if err != nil {
			return err
		}
		if cur.Revision != in.Revision {
			return cf.ErrConflict
		}
		if err := cf.State(cur.State).CheckTransition(in.State); err != nil {
			return err
		}
		upd := c.Connection.Update().
			Where(
				connection.OwnerID(string(in.Owner)),
				connection.ConnectionID(in.ID),
				connection.Revision(in.Revision),
			).
			SetAuthMethod(in.AuthMethod).
			SetDisplayName(in.DisplayName).
			SetExternalAccountID(in.ExternalAccountID).
			SetExternalLabel(in.ExternalLabel).
			SetState(string(in.State)).
			SetHealth(in.Health).
			SetUpdatedAt(in.UpdatedAt).
			SetRevision(in.Revision + 1)
		if in.Scopes == nil {
			upd = upd.ClearScopes()
		} else {
			upd = upd.SetScopes(in.Scopes)
		}
		if in.Metadata == nil {
			upd = upd.ClearMetadata()
		} else {
			upd = upd.SetMetadata(in.Metadata)
		}
		n, err := upd.Save(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: update connection: %w", err)
		}
		if n != 1 {
			// Another writer changed it between the read and the update.
			return cf.ErrConflict
		}
		e, err := getConnection(ctx, c, in.Owner, in.ID)
		if err != nil {
			return err
		}
		out = toConnection(e)
		return nil
	})
	return out, err
}

// DeleteConnection implements [cf.ConnectionStore].
func (s *Store) DeleteConnection(ctx context.Context, owner cf.OwnerID, id string) error {
	return s.inTx(ctx, func(c *ent.Client) error {
		if _, err := getConnection(ctx, c, owner, id); err != nil {
			return err
		}
		o := string(owner)
		if _, err := c.Credential.Delete().Where(credential.OwnerID(o), credential.ConnectionID(id)).Exec(ctx); err != nil {
			return fmt.Errorf("sqlstore: delete credentials: %w", err)
		}
		if _, err := c.Grant.Delete().Where(grant.OwnerID(o), grant.ConnectionID(id)).Exec(ctx); err != nil {
			return fmt.Errorf("sqlstore: delete grants: %w", err)
		}
		if _, err := c.Binding.Delete().Where(binding.OwnerID(o), binding.ConnectionID(id)).Exec(ctx); err != nil {
			return fmt.Errorf("sqlstore: delete bindings: %w", err)
		}
		if _, err := c.Connection.Delete().Where(connection.OwnerID(o), connection.ConnectionID(id)).Exec(ctx); err != nil {
			return fmt.Errorf("sqlstore: delete connection: %w", err)
		}
		return nil
	})
}

// activeVersion returns the highest credential version, or 0 for none.
func activeVersion(ctx context.Context, c *ent.Client, owner cf.OwnerID, id string) (*ent.Credential, error) {
	e, err := c.Credential.Query().
		Where(credential.OwnerID(string(owner)), credential.ConnectionID(id)).
		Order(ent.Desc(credential.FieldVersion)).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlstore: read credential: %w", err)
	}
	return e, nil
}

// SwapCredential implements [cf.CredentialStore]. The unique index on
// (owner, connection, version) decides between concurrent swaps. Older
// versions are deleted in the same transaction.
func (s *Store) SwapCredential(ctx context.Context, expectActive int, next cf.Credential) error {
	return s.inTx(ctx, func(c *ent.Client) error {
		if _, err := getConnection(ctx, c, next.Owner, next.ConnectionID); err != nil {
			return err
		}
		cur, err := activeVersion(ctx, c, next.Owner, next.ConnectionID)
		if err != nil {
			return err
		}
		active := 0
		if cur != nil {
			active = cur.Version
		}
		if active != expectActive || next.Version != expectActive+1 {
			return cf.ErrConflict
		}
		err = c.Credential.Create().
			SetOwnerID(string(next.Owner)).
			SetConnectionID(next.ConnectionID).
			SetVersion(next.Version).
			SetKind(next.Kind).
			SetCiphertext(next.Ciphertext).
			SetNillableExpiresAt(next.ExpiresAt).
			SetCreatedAt(next.CreatedAt).
			Exec(ctx)
		if ent.IsConstraintError(err) {
			return cf.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("sqlstore: swap credential: %w", err)
		}
		_, err = c.Credential.Delete().Where(
			credential.OwnerID(string(next.Owner)), credential.ConnectionID(next.ConnectionID),
			credential.VersionLT(next.Version),
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: remove superseded credentials: %w", err)
		}
		return nil
	})
}

// ActiveCredential implements [cf.CredentialStore].
func (s *Store) ActiveCredential(ctx context.Context, owner cf.OwnerID, id string) (cf.Credential, error) {
	var out cf.Credential
	err := s.inTx(ctx, func(c *ent.Client) error {
		e, err := activeVersion(ctx, c, owner, id)
		if err != nil {
			return err
		}
		if e == nil {
			return cf.ErrNotFound
		}
		out = cf.Credential{
			Owner: owner, ConnectionID: id, Version: e.Version, Kind: e.Kind,
			Ciphertext: e.Ciphertext, ExpiresAt: e.ExpiresAt, CreatedAt: e.CreatedAt,
		}
		return nil
	})
	return out, err
}

// DestroyCredentials implements [cf.CredentialStore].
func (s *Store) DestroyCredentials(ctx context.Context, owner cf.OwnerID, id string) error {
	return s.inTx(ctx, func(c *ent.Client) error {
		_, err := c.Credential.Delete().Where(credential.OwnerID(string(owner)), credential.ConnectionID(id)).Exec(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: destroy credentials: %w", err)
		}
		return nil
	})
}

func actionsToStrings(as []cf.Action) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = string(a)
	}
	return out
}

// PutGrant implements [cf.GrantStore].
func (s *Store) PutGrant(ctx context.Context, g cf.Grant) error {
	return s.inTx(ctx, func(c *ent.Client) error {
		if _, err := getConnection(ctx, c, g.Owner, g.ConnectionID); err != nil {
			return err
		}
		o := string(g.Owner)
		_, err := c.Grant.Delete().Where(
			grant.OwnerID(o), grant.ConnectionID(g.ConnectionID),
			grant.PrincipalKind(string(g.Principal.Kind)), grant.PrincipalID(g.Principal.ID),
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: replace grant: %w", err)
		}
		err = c.Grant.Create().
			SetOwnerID(o).
			SetConnectionID(g.ConnectionID).
			SetPrincipalKind(string(g.Principal.Kind)).
			SetPrincipalID(g.Principal.ID).
			SetActions(actionsToStrings(g.Actions)).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: put grant: %w", err)
		}
		return nil
	})
}

// RemoveGrant implements [cf.GrantStore].
func (s *Store) RemoveGrant(ctx context.Context, owner cf.OwnerID, id string, p cf.Principal) error {
	return s.inTx(ctx, func(c *ent.Client) error {
		_, err := c.Grant.Delete().Where(
			grant.OwnerID(string(owner)), grant.ConnectionID(id),
			grant.PrincipalKind(string(p.Kind)), grant.PrincipalID(p.ID),
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: remove grant: %w", err)
		}
		return nil
	})
}

// ListGrants implements [cf.GrantStore].
func (s *Store) ListGrants(ctx context.Context, owner cf.OwnerID, id string) ([]cf.Grant, error) {
	out := []cf.Grant{}
	err := s.inTx(ctx, func(c *ent.Client) error {
		es, err := c.Grant.Query().
			Where(grant.OwnerID(string(owner)), grant.ConnectionID(id)).
			Order(ent.Asc(grant.FieldPrincipalKind), ent.Asc(grant.FieldPrincipalID)).
			All(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: list grants: %w", err)
		}
		for _, e := range es {
			g := cf.Grant{
				Owner: owner, ConnectionID: id,
				Principal: cf.Principal{Kind: cf.PrincipalKind(e.PrincipalKind), ID: e.PrincipalID},
			}
			for _, a := range e.Actions {
				g.Actions = append(g.Actions, cf.Action(a))
			}
			out = append(out, g)
		}
		return nil
	})
	return out, err
}

// PutBinding implements [cf.BindingStore].
func (s *Store) PutBinding(ctx context.Context, b cf.Binding) error {
	return s.inTx(ctx, func(c *ent.Client) error {
		if _, err := getConnection(ctx, c, b.Owner, b.ConnectionID); err != nil {
			return err
		}
		o := string(b.Owner)
		exists, err := c.Binding.Query().Where(
			binding.OwnerID(o), binding.ConnectionID(b.ConnectionID),
			binding.ConsumerKind(b.Consumer.Kind), binding.ConsumerID(b.Consumer.ID),
			binding.Purpose(b.Purpose),
		).Exist(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: read binding: %w", err)
		}
		if exists {
			return nil
		}
		err = c.Binding.Create().
			SetOwnerID(o).
			SetConnectionID(b.ConnectionID).
			SetConsumerKind(b.Consumer.Kind).
			SetConsumerID(b.Consumer.ID).
			SetPurpose(b.Purpose).
			SetCreatedAt(b.CreatedAt).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: put binding: %w", err)
		}
		return nil
	})
}

// RemoveBinding implements [cf.BindingStore].
func (s *Store) RemoveBinding(ctx context.Context, owner cf.OwnerID, id string, cons cf.Consumer, purpose string) error {
	return s.inTx(ctx, func(c *ent.Client) error {
		_, err := c.Binding.Delete().Where(
			binding.OwnerID(string(owner)), binding.ConnectionID(id),
			binding.ConsumerKind(cons.Kind), binding.ConsumerID(cons.ID), binding.Purpose(purpose),
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: remove binding: %w", err)
		}
		return nil
	})
}

// ListBindings implements [cf.BindingStore].
func (s *Store) ListBindings(ctx context.Context, owner cf.OwnerID, id string) ([]cf.Binding, error) {
	out := []cf.Binding{}
	err := s.inTx(ctx, func(c *ent.Client) error {
		es, err := c.Binding.Query().
			Where(binding.OwnerID(string(owner)), binding.ConnectionID(id)).
			Order(ent.Asc(binding.FieldConsumerKind), ent.Asc(binding.FieldConsumerID), ent.Asc(binding.FieldPurpose)).
			All(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: list bindings: %w", err)
		}
		for _, e := range es {
			out = append(out, cf.Binding{
				Owner: owner, ConnectionID: id,
				Consumer:  cf.Consumer{Kind: e.ConsumerKind, ID: e.ConsumerID},
				Purpose:   e.Purpose,
				CreatedAt: e.CreatedAt,
			})
		}
		return nil
	})
	return out, err
}

// Acquire implements [cf.Leaser]. An expired lease is taken over by a
// conditional update, and a new one is created; a unique index on the name
// decides between concurrent creators. Expiry uses the store's clock, so
// processes sharing a database should have synchronized clocks.
func (s *Store) Acquire(ctx context.Context, key string, ttl time.Duration) (cf.Lease, error) {
	token := newToken()
	err := s.inTx(ctx, func(c *ent.Client) error {
		now := s.clock.Now()
		n, err := c.Lease.Update().
			Where(lease.Name(key), lease.ExpiresAtLTE(now)).
			SetToken(token).
			SetExpiresAt(now.Add(ttl)).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: take over lease: %w", err)
		}
		if n == 1 {
			return nil
		}
		held, err := c.Lease.Query().Where(lease.Name(key)).Exist(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: read lease: %w", err)
		}
		if held {
			return cf.ErrLeaseHeld
		}
		err = c.Lease.Create().SetName(key).SetToken(token).SetExpiresAt(now.Add(ttl)).Exec(ctx)
		if ent.IsConstraintError(err) {
			return cf.ErrLeaseHeld
		}
		if err != nil {
			return fmt.Errorf("sqlstore: create lease: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &sqlLease{s: s, key: key, token: token}, nil
}

type sqlLease struct {
	s     *Store
	key   string
	token string
}

// Release gives up the lease if it is still ours.
func (l *sqlLease) Release(ctx context.Context) error {
	return l.s.inTx(ctx, func(c *ent.Client) error {
		_, err := c.Lease.Delete().Where(lease.Name(l.key), lease.Token(l.token)).Exec(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: release lease: %w", err)
		}
		return nil
	})
}
