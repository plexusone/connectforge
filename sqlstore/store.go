// Package sqlstore is connectforge's [connectforge.Store] and
// [connectforge.Leaser] on SQLite or PostgreSQL, through Ent.
//
// The host owns the database: it opens it, passes it to [New], calls
// [Store.Migrate], and applies its tenancy policy to [Store.Tables]. Every
// operation runs in its own transaction and calls the host's [TxHook] first,
// so a session setting such as the tenant for row-level security applies to
// it. Queries also filter by owner in code, so isolation does not depend on
// the hook alone.
//
// This package imports no database driver; the host chooses one. For SQLite,
// give the store a database with one open connection or immediate
// transactions, so that concurrent writers queue instead of failing, and with
// foreign keys enabled, which Ent requires to migrate a SQLite database.
package sqlstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/sqlstore/ent"
)

// Dialect is a supported database.
type Dialect string

// Supported dialects.
const (
	SQLite   Dialect = dialect.SQLite
	Postgres Dialect = dialect.Postgres
)

// TxHook runs at the start of every transaction, before any query. A host uses
// it to set session variables, for example the tenant for row-level security.
// An error aborts the operation.
type TxHook func(ctx context.Context, tx *sql.Tx) error

// Option configures a [Store].
type Option func(*Store)

// WithTxHook sets the transaction hook.
func WithTxHook(h TxHook) Option { return func(s *Store) { s.hook = h } }

// WithClock sets the clock used for leases. The default is the system clock.
func WithClock(c cf.Clock) Option { return func(s *Store) { s.clock = c } }

// Table names. Every table except [LeaseTable] has an owner_id column.
const (
	ConnectionTable = "connectforge_connections"
	CredentialTable = "connectforge_credentials"
	GrantTable      = "connectforge_grants"
	BindingTable    = "connectforge_bindings"
	LeaseTable      = "connectforge_leases"
)

// Store is a connectforge store in a SQL database. It is safe for
// concurrent use.
type Store struct {
	db      *sql.DB
	dialect Dialect
	hook    TxHook
	clock   cf.Clock
}

var (
	_ cf.Store  = (*Store)(nil)
	_ cf.Leaser = (*Store)(nil)
)

// New returns a store over db. The caller owns db and closes it. It does not
// create the schema: call [Store.Migrate].
func New(db *sql.DB, d Dialect, opts ...Option) (*Store, error) {
	switch d {
	case SQLite, Postgres:
	default:
		return nil, fmt.Errorf("sqlstore: unsupported dialect %q", d)
	}
	if db == nil {
		return nil, errors.New("sqlstore: a database is required")
	}
	s := &Store{db: db, dialect: d, clock: cf.SystemClock{}}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Migrate creates or updates the schema to match this version of the store.
// It is idempotent and runs outside the transaction hook, so the host runs it
// with a role allowed to change the schema.
func (s *Store) Migrate(ctx context.Context) error {
	client := ent.NewClient(ent.Driver(entsql.OpenDB(string(s.dialect), s.db)))
	if err := client.Schema.Create(ctx); err != nil {
		return fmt.Errorf("sqlstore: migrate: %w", err)
	}
	return nil
}

// Tables returns the tables that hold owned records, each with an owner_id
// column, for the host's tenancy policy. The lease table is not among them.
func (s *Store) Tables() []string {
	return []string{ConnectionTable, CredentialTable, GrantTable, BindingTable}
}

// inTx runs fn in a transaction with an Ent client bound to it, after the
// host's hook. It commits if fn returns nil and rolls back otherwise.
func (s *Store) inTx(ctx context.Context, fn func(*ent.Client) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlstore: begin: %w", err)
	}
	defer func() {
		if err != nil {
			if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("sqlstore: rollback: %w", rerr))
			}
		}
	}()
	if s.hook != nil {
		if err := s.hook(ctx, tx); err != nil {
			return fmt.Errorf("sqlstore: transaction hook: %w", err)
		}
	}
	drv := entsql.NewDriver(string(s.dialect), entsql.Conn{ExecQuerier: tx})
	if err := fn(ent.NewClient(ent.Driver(drv))); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlstore: commit: %w", err)
	}
	return nil
}

// newToken returns a random lease token.
func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return hex.EncodeToString(b)
}
