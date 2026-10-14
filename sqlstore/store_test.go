package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	_ "modernc.org/sqlite"             // registers the "sqlite" driver

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/sqlstore"
	"github.com/plexusone/connectforge/store/storetest"
)

// postgresEnv names a PostgreSQL connection string for the opt-in Postgres
// tests. Each test case runs in its own schema, which is dropped afterwards.
const postgresEnv = "CONNECTFORGE_TEST_POSTGRES_DSN"

func openSQLite(t *testing.T, opts ...sqlstore.Option) *sqlstore.Store {
	t.Helper()
	q := url.Values{}
	q.Set("_txlock", "immediate")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "cf.db")+"?"+q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return migrated(t, db, sqlstore.SQLite, opts...)
}

func migrated(t *testing.T, db *sql.DB, d sqlstore.Dialect, opts ...sqlstore.Option) *sqlstore.Store {
	t.Helper()
	s, err := sqlstore.New(db, d, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate is not idempotent: %v", err)
	}
	return s
}

// openPostgres returns a store in a fresh schema, or skips without a DSN.
func openPostgres(t *testing.T, opts ...sqlstore.Option) *sqlstore.Store {
	t.Helper()
	dsn := os.Getenv(postgresEnv)
	if dsn == "" {
		t.Skipf("%s is not set", postgresEnv)
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("cf_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return migrated(t, db, sqlstore.Postgres, opts...)
}

func TestSQLiteConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) cf.Store { return openSQLite(t) })
	storetest.RunLeaser(t, func(t *testing.T) cf.Leaser { return openSQLite(t) })
}

func TestPostgresConformance(t *testing.T) {
	if os.Getenv(postgresEnv) == "" {
		t.Skipf("%s is not set", postgresEnv)
	}
	storetest.Run(t, func(t *testing.T) cf.Store { return openPostgres(t) })
	storetest.RunLeaser(t, func(t *testing.T) cf.Leaser { return openPostgres(t) })
}

func TestTxHookRunsAndCanAbort(t *testing.T) {
	ctx := context.Background()
	var calls int
	refuse := false
	s := openSQLite(t, sqlstore.WithTxHook(func(context.Context, *sql.Tx) error {
		calls++
		if refuse {
			return errors.New("no tenant")
		}
		return nil
	}))
	c := cf.Connection{ID: "c1", Owner: "o", Provider: "p", State: cf.StatePending}
	if _, err := s.CreateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("hook called %d times", calls)
	}
	refuse = true
	if _, err := s.GetConnection(ctx, "o", "c1"); err == nil {
		t.Fatal("a failing hook did not abort the read")
	}
	if _, err := s.CreateConnection(ctx, cf.Connection{ID: "c2", Owner: "o", Provider: "p"}); err == nil {
		t.Fatal("a failing hook did not abort the write")
	}
	refuse = false
	if list, err := s.ListConnections(ctx, "o", cf.Filter{}); err != nil || len(list) != 1 {
		t.Fatalf("aborted write was kept: %v, %v", list, err)
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func TestLeaseExpires(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := openSQLite(t, sqlstore.WithClock(clock))
	if _, err := s.Acquire(ctx, "k", time.Minute); err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(30 * time.Second)
	if _, err := s.Acquire(ctx, "k", time.Minute); !errors.Is(err, cf.ErrLeaseHeld) {
		t.Fatalf("before expiry: %v", err)
	}
	clock.t = clock.t.Add(31 * time.Second)
	if _, err := s.Acquire(ctx, "k", time.Minute); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
}

func TestTablesAreOwned(t *testing.T) {
	s := openSQLite(t)
	got := s.Tables()
	want := []string{sqlstore.ConnectionTable, sqlstore.CredentialTable, sqlstore.GrantTable, sqlstore.BindingTable}
	if !slices.Equal(got, want) || slices.Contains(got, sqlstore.LeaseTable) {
		t.Fatalf("tables: %v", got)
	}
}

func TestUnsupportedDialect(t *testing.T) {
	if _, err := sqlstore.New(&sql.DB{}, "mysql"); err == nil {
		t.Fatal("mysql accepted")
	}
	if _, err := sqlstore.New(nil, sqlstore.SQLite); err == nil {
		t.Fatal("nil database accepted")
	}
}
