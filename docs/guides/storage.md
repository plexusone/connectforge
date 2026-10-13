# Storage

connectionforge has two stores that pass the same conformance suite.

| Store | Package | Use |
|---|---|---|
| In memory | `store/memstore` | Tests and examples; the reference for behaviour |
| SQL | `sqlstore` | SQLite and PostgreSQL through Ent, on your own database |

A store implements `connectionforge.Store`, which is `ConnectionStore`,
`CredentialStore`, `GrantStore` and `BindingStore`. The SQL store and
`memstore` also implement `Leaser`.

## The SQL store

```go
import "github.com/plexusone/connectionforge/sqlstore"

store, err := sqlstore.New(db, sqlstore.Postgres,
	sqlstore.WithTxHook(func(ctx context.Context, tx *sql.Tx) error {
		return setTenant(ctx, tx) // for example, SET LOCAL for row-level security
	}),
)
if err != nil {
	return err
}
if err := store.Migrate(ctx); err != nil { // idempotent
	return err
}
tables := store.Tables() // for your tenancy policy
```

- **You own the database.** `New` takes an open `*sql.DB`, and you close it.
  The package imports no database driver; use the one you already use.
- **The schema is the module's.** Ent code lives in `sqlstore/ent`. You do not
  regenerate or merge it into your own Ent client; you share the database,
  not the code generation.
- **Tables are prefixed `connectionforge_`:** `connections`, `credentials`,
  `grants`, `bindings` and `leases`.
- **Every owned table has `owner_id`** (text), and every query filters on it.
- **Ciphertext is stored as bytes.** No column holds plaintext.

### Migrate

`Migrate` creates or updates the schema to match this version of the module.
Run it with a role allowed to change the schema, and not through the
transaction hook. A change that alters stored data is called out in the
changelog.

### SQLite

SQLite needs foreign keys on (Ent requires it to migrate) and either one open
connection or immediate transactions, so that concurrent writers queue instead
of failing:

```go
db, err := sql.Open("sqlite", "file:cf.db?_txlock=immediate&_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)")
db.SetMaxOpenConns(1)
```

### Concurrency

Races are decided by the database, not by locks in the process:

- `UpdateConnection` is a conditional update on the connection's revision. The
  loser gets `ErrConflict` and retries (the service retries a few times).
- `SwapCredential` installs version *n+1* only if the active version is *n*; a
  unique index on (owner, connection, version) decides concurrent swaps.
- A lease is a row with an expiry, taken by a conditional insert or update, so
  refresh can be single-flight across processes. Expiry uses the store's
  clock, so processes sharing a database should have synchronized clocks.

## Row-level security

On PostgreSQL, enable row-level security on the owned tables so the database
refuses another tenant's rows even if a query forgot its filter. Key the
policy on `owner_id`, and set the tenant for each transaction in the hook:

```go
func setTenant(ctx context.Context, tx *sql.Tx) error {
	tenant, ok := tenantFrom(ctx)
	if !ok {
		return errors.New("no tenant in context") // fail closed
	}
	// set_config(..., true) is SET LOCAL, scoped to this transaction.
	_, err := tx.ExecContext(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant)
	return err
}
```

```sql
ALTER TABLE connectionforge_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectionforge_connections FORCE ROW LEVEL SECURITY;
CREATE POLICY connectionforge_connections_tenant ON connectionforge_connections
  USING (owner_id = current_setting('app.current_tenant', true))
  WITH CHECK (owner_id = current_setting('app.current_tenant', true));
```

Repeat for each table `Tables()` returns. The lease table has no owner and is
not part of the policy.

!!! note "Test as an ordinary role"
    Superusers and the table owner bypass row-level security. Test your policy
    with an application role that owns nothing, or it will appear to work.

## The in-memory store

`memstore.New()` returns a store and leaser that live as long as the process.
`memstore.NewWithClock` takes a clock so a test can advance lease expiry.
Nothing is persisted.

## Testing your own store

`store/storetest` is the conformance suite. Call it with a constructor and it
checks owner isolation on every method, compare-and-swap under concurrency,
the transition table, cascade on delete, ordering and that returned records are
copies:

```go
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) cf.Store { return newMyStore(t) })
	storetest.RunLeaser(t, func(t *testing.T) cf.Leaser { return newMyStore(t) })
}
```
