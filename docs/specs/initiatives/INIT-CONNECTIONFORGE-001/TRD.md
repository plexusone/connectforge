# connectionforge: Technical Requirements

**Initiative:** `INIT-CONNECTIONFORGE-001`
**Repository:** `github.com/plexusone/connectionforge`

## Concepts

| Concept | Meaning |
|---|---|
| **Provider** | A definition of an external service: its manifest and its adapter (validate, discover, refresh, revoke, authenticate a request) |
| **Connection** | A tenant-owned relationship with one account at a provider. Carries lifecycle state, observed health and metadata. Holds no secret |
| **Credential** | Encrypted authorization material for a connection, versioned. Only the credential store and the broker ever see ciphertext; only the broker sees plaintext |
| **Grant** | Who or what may use, manage, share or disconnect a connection |
| **Binding** | A consumer (application, workflow, agent) that depends on a connection |
| **Broker** | The only path from a connection to an authenticated request |
| **Owner** | The opaque tenant scope under which every record lives |

A connection is not a stored secret. Its credential is one of the ways
authorization is obtained, and the broker hides which.

## Architecture

connectionforge is a library of ports and a core that composes them. It
imports the Go standard library, `golang.org/x/oauth2` and nothing from any
host. The host is the composition root.

```text
host application
  ├── implements Ports: stores, Cryptographer, Authorizer, Auditor, Leaser, Egress
  ├── registers Providers (adapters, each with a Manifest)
  └── mounts httpapi (optional) or calls the Service directly

connectionforge
  ├── Service   create, test, reconnect, replace, revoke, list
  ├── Broker    authenticated clients for a connection
  └── Refresher single-flight token refresh
```

### Packages

| Package | Responsibility | Imports |
|---|---|---|
| `connectionforge` (root) | Types, errors, `Secret`, ports, lifecycle states | stdlib |
| `provider` | `Provider`, capability interfaces, `Manifest`, registry | root |
| `provider/apikey` | Reference static-credential provider driven by a manifest | root, provider |
| `provider/githubapp` | GitHub App installations | root, provider |
| `oauth` | Authorization code with PKCE, state, refresh | root, `x/oauth2` |
| `broker` | Authenticated `http.Client` and transports, token cache | root, provider |
| `service` | Application operations over the ports | root, provider, broker |
| `store/memstore` | In-memory stores for tests and examples | root |
| `store/storetest` | Store conformance suite | root |
| `sqlstore` | Ent-backed stores and leaser for SQLite and PostgreSQL, with migration | root, Ent, drivers |
| `sqlstore/ent` | The Ent schema and generated code | Ent |
| `egress` | Destination policy and an HTTP client factory | stdlib |
| `httpapi` | Handlers over `service`, mounted by the host | service |
| `cmd/connectionforge` | Thin CLI over `service` | service |

connectionforge owns its domain, including its database schema. The host
composes it: it opens `sqlstore` on its own database, runs its migration, and
applies its tenancy policy (for example row-level security) to the tables
`sqlstore` reports. Ent and the database drivers are imported by `sqlstore`
only, so a host that brings its own store does not pull them in.

## Data model

### Connection

```go
type Connection struct {
    ID                string
    Owner             OwnerID           // tenant scope; opaque to the library
    Provider          string            // provider key, e.g. "cloudflare"
    AuthMethod        string            // from the manifest: "api_token", "github_app", "oauth2"
    DisplayName       string            // human label, editable
    ExternalAccountID string            // discovered
    ExternalLabel     string            // discovered, shown to humans
    Scopes            []string          // granted, as reported by the provider
    State             State
    Health            Health            // observed, separate from State
    CredentialVersion int               // active credential version
    Metadata          map[string]string // non-secret provider settings
    CreatedBy         Principal
    CreatedAt, UpdatedAt time.Time
}
```

### Lifecycle state and observed health

State is what was decided; health is what was observed. A `connected`
connection can have a failed last validation.

```text
pending ──authorize/validate──► connected ──► disconnected
                                   │  ▲            ▲
                          failure  ▼  │ reconnect  │
                              needs_attention ─────┘
                                   │
                                   ▼
                                revoked
```

| State | Meaning |
|---|---|
| `pending` | Created, not yet authorized or validated |
| `connected` | Credential accepted by the provider at last check |
| `needs_attention` | A permanent failure: revoked upstream, expired, scope lost. The broker refuses use |
| `disconnected` | Disconnected by a person; credentials destroyed |
| `revoked` | Revoked at the provider by connectionforge; terminal |

Transient failures (network, rate limit, provider outage) change `Health`,
never `State`. The transition table lives in code, is the single source for
the documentation and is enforced by the store through compare-and-swap on a
version number.

```go
type Health struct {
    LastValidatedAt  time.Time
    LastValidation   Outcome   // ok, failed, unknown
    LastError        string    // classified, never containing a secret
    ConsecutiveFails int
    RateLimit        *RateLimit
}
```

### Credential

```go
type Credential struct {
    ConnectionID string
    Version      int
    Kind         string     // "api_token", "oauth_tokens", "app_installation"
    Ciphertext   []byte     // opaque envelope produced by the Cryptographer
    KeyRef       string     // purpose and key version recorded by the Cryptographer
    ExpiresAt    *time.Time // for the access token, if known
    RefreshAfter *time.Time
    CreatedAt    time.Time
    Superseded   *time.Time // set when a newer version is active
}
```

The plaintext structure for each `Kind` is private to its provider and
sealed as a whole. Credentials are append-only by version; the active
version is on the connection. During replacement two versions coexist until
the new one validates.

### Grants and bindings

```go
type Grant struct {
    ConnectionID string
    Principal    Principal
    Actions      []Action // use, manage, share, disconnect
}

type Binding struct {
    ConnectionID string
    Consumer     Consumer // kind and id: application, workflow, agent
    Purpose      string   // e.g. "code-health"
}
```

A grant is authority; a binding is dependency. Deleting a connection lists
bindings first.

## Ports

Every port method takes an `Owner` or a record that carries it, and an
implementation must scope by it.

```go
type ConnectionStore interface {
    Create(ctx context.Context, c Connection) error
    Get(ctx context.Context, owner OwnerID, id string) (Connection, error)
    List(ctx context.Context, owner OwnerID, f Filter) ([]Connection, error)
    // CompareAndSwap updates iff stored UpdatedVersion == expect.
    CompareAndSwap(ctx context.Context, c Connection, expect uint64) error
    Delete(ctx context.Context, owner OwnerID, id string) error
}

type CredentialStore interface {
    Put(ctx context.Context, owner OwnerID, cr Credential) error
    // Active returns the active version's ciphertext only.
    Active(ctx context.Context, owner OwnerID, connID string) (Credential, error)
    // Swap installs next as active iff the active version is expectVersion.
    Swap(ctx context.Context, owner OwnerID, connID string, expectVersion int, next Credential) error
    Destroy(ctx context.Context, owner OwnerID, connID string) error
}

type Cryptographer interface {
    Seal(ctx context.Context, purpose string, plaintext, aad []byte) (ciphertext []byte, keyRef string, err error)
    Open(ctx context.Context, purpose string, ciphertext, aad []byte) (plaintext []byte, err error)
}

type Authorizer interface {
    Allowed(ctx context.Context, p Principal, a Action, conn Connection) error
}

type Auditor interface { Record(ctx context.Context, e Event) }

type Leaser interface {
    // Acquire returns a lease or ErrHeld. The lease is released or expires.
    Acquire(ctx context.Context, key string, ttl time.Duration) (Lease, error)
}
```

- **Cryptographer** binds `aad` to the owner, connection ID and credential
  version, so a ciphertext cannot be moved between connections or tenants.
  The purpose is `connectionforge.credential`. The port is deliberately the
  same shape as an envelope-encryption client's encrypt and decrypt, so a
  key-management library satisfies it with a small adapter in the host.
- **Leaser** is how refresh is made single-flight across processes. A host
  may implement it with an advisory lock or a lease row; the in-memory
  implementation is for tests.
- **Authorizer** is called by `service` and by the broker. A failed
  authorization is an error, never a silent downgrade.

## The SQL store

`sqlstore` implements `ConnectionStore`, `CredentialStore`, the grant and
binding stores and `Leaser` over Ent, for SQLite and PostgreSQL. The memory
store remains the reference for behaviour, and both run the same conformance
suite.

```go
store, err := sqlstore.New(db, sqlstore.Postgres,
    sqlstore.WithTxHook(func(ctx context.Context, tx *sql.Tx) error {
        return setTenant(ctx, tx) // host-supplied, e.g. SET LOCAL for RLS
    }))
err = store.Migrate(ctx)
tables := store.Tables() // for the host's tenancy policy
```

- **Own generated client.** The Ent schema and generated code live in
  `sqlstore/ent`. The host does not regenerate or merge them into its own
  client; it shares the database, not the code generation.
- **Table names are prefixed** `connectionforge_`, so they cannot collide with
  the host's.
- **Tenant column.** Every table carries `owner_id` (text). Every query
  filters on it in code; a host that enables row-level security keys its
  policy on the same column, so isolation holds twice.
- **Every operation runs in a transaction** and calls the host's `TxHook`
  first, so session variables such as a tenant setting apply to it. A hook
  error aborts the operation.
- **Uniqueness and swaps are in the database.** One active credential version
  per connection is a unique index; `Swap` and `CompareAndSwap` are
  conditional updates whose affected-row count decides the outcome.
- **Leases** are rows with an expiry, acquired by a conditional insert or
  update, so refresh is single-flight across processes with no other
  infrastructure.
- **The schema is versioned** with the module. A change that alters stored
  data needs a migration note in the changelog.
- Ciphertext is stored as bytes; no column ever holds plaintext.

## Secrets

```go
type Secret struct{ v []byte }

func (Secret) String() string               { return "[redacted]" }
func (Secret) GoString() string             { return "[redacted]" }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (Secret) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }
func (Secret) LogValue() slog.Value         { return slog.StringValue("[redacted]") }
func (s Secret) Reveal() []byte             { return s.v } // narrow, grep-able
```

- `Reveal` is called only inside provider adapters and the broker. A lint
  rule in CI fails a call to `Reveal` anywhere else.
- Errors from adapters are classified (`ErrInvalidCredential`,
  `ErrInsufficientScope`, `ErrTransient`, `ErrRateLimited`, ...) and carry
  the provider's status and request ID, never a request or response body that
  could echo a credential.
- Plaintext buffers are zeroed after use on a best-effort basis.

## Providers

```go
type Provider interface {
    Manifest() Manifest
    // Validate checks the credential and returns discovered account details.
    Validate(ctx context.Context, c Material) (Validation, error)
}

// Optional capabilities, discovered by type assertion.
type Refresher     interface { Refresh(ctx context.Context, c Material) (Material, error) }
type Revoker       interface { Revoke(ctx context.Context, c Material) error }
type Discoverer    interface { Accounts(ctx context.Context, c Material) ([]Account, error) }
type Authenticator interface { Authenticate(ctx context.Context, c Material, r *http.Request) error }
```

`Material` is the unsealed credential handed to an adapter inside the
broker. Refresh and revoke are optional because static keys do not support
them; the broker and the service check the capability and degrade
explicitly (a non-refreshing connection that expires moves to
`needs_attention`).

### Manifest

A manifest is data, so a UI can render a connection form and an agent can
read what a connection needs:

```json
{
  "key": "cloudflare",
  "displayName": "Cloudflare",
  "baseUrls": ["https://api.cloudflare.com"],
  "authMethods": [{
    "key": "api_token",
    "fields": [
      {"name": "token", "label": "API token", "secret": true, "required": true}
    ],
    "scopes": [{"name": "zone:read"}, {"name": "analytics:read"}],
    "accountSelection": "discovered",
    "validation": {"operation": "verify_token"},
    "helpUrl": "https://developers.cloudflare.com/fundamentals/api/get-started/create-token/"
  }]
}
```

Property names are camelCase (a document format owned here). A manifest
never contains executable content. Secret fields are never echoed back by
any API.

### Reference providers

| Provider | Method | Validation | Discovery |
|---|---|---|---|
| Static token (driven by manifest) | API key or token in a header | A GET to a manifest-named verification endpoint; status classification | Account and label from the response through a manifest path |
| GitHub App | App ID and private key held as host configuration; per-connection installation ID | Mint an installation token, then read the installation | Account login, repository selection, granted permissions |

For GitHub App, the long-lived material is the **app key**, held by the host
(through a `SecretSource`), not per-connection. The connection holds only the
installation ID, so there is no stored long-lived credential per tenant. The
broker mints installation tokens, caches them until a margin before expiry
and records their permissions.

## OAuth 2.0

- **Authorization code with PKCE** (S256) always, even for confidential
  clients. State is bound to owner, principal and a validated return
  destination, stored server-side with a short expiry and single use.
- **Return destinations are allow-listed** by the host. A callback never
  redirects to a value taken from the request.
- **Scopes** are the minimum the enabled features need; a reconnect that adds
  a scope is a distinct consent.
- **Callbacks** are handled by the library (or `httpapi`), never by an
  embedded application.
- Token exchange uses `golang.org/x/oauth2`; the library does not implement
  the protocol.

### Refresh

1. The broker finds the access token expired or within the margin.
2. It acquires a lease on `refresh:<connection>` from the `Leaser`. If the
   lease is held, it waits and re-reads the credential; one exchange is
   enough for all callers.
3. It reads the active credential, calls the adapter's `Refresh`, seals the
   result and installs it with `Swap(expectVersion)`. A rotated refresh token
   is part of the new sealed material, so access and refresh token change
   atomically.
4. Failure classification: `invalid_grant` or revocation moves the
   connection to `needs_attention`; network, 5xx and rate-limit responses set
   health and retry with backoff, leaving state unchanged.

If the exchange succeeds but `Swap` fails (a concurrent writer), the new
tokens are discarded and the winner's credential is used; with providers
that rotate refresh tokens on use, the loser re-reads before ever calling
the provider.

## Broker

```go
type Broker interface {
    // HTTPClient returns a client whose requests are authenticated for the
    // connection. It never exposes the credential.
    HTTPClient(ctx context.Context, owner OwnerID, p Principal, connID string, use Use) (*http.Client, error)
}
```

- Authorization (`use`) and state are checked on every call, and the
  `Use` (consumer and purpose) is recorded as a counter, not an event per
  request.
- The returned transport authenticates each request lazily, so a refresh
  happens in the transport, not at client creation.
- The client is wrapped by `egress`: a request whose host is not in the
  provider's manifest `baseUrls` is refused, redirects to another host are
  refused, and resolved private, loopback and link-local addresses are
  refused unless the host explicitly allows a development destination.
- The result is an `*http.Client` a provider SDK can accept
  (`WithHTTPClient`), which is how SDKs consume a connection without
  connectionforge importing them.

## Audit

Events: `connection.created`, `connection.validated`, `connection.state_changed`,
`credential.replaced`, `credential.refreshed`, `credential.revoked`,
`connection.used` (counters), `connection.shared`, `connection.disconnected`,
`grant.changed`, `access.denied`, `admin.read`. Each carries owner, actor
(principal and, where applicable, on-behalf-of principal), connection ID,
provider and outcome. No event carries a secret or a response body.

## Delegated authorization (design only)

The model leaves room without implementing it:

- `Principal` distinguishes a user, a service and an agent, and a use may
  carry an `OnBehalfOf` principal and an evidence reference.
- `AuthMethod` is an open string. A future method (workload identity, token
  exchange, DPoP-bound tokens, an agent-authorization flow) is a provider
  whose `Authenticate` obtains authorization per request and stores no
  long-lived secret; the connection record is unchanged.
- The `Authorizer` receives the principal, the action, the connection and the
  use, so policy can bind access to an operation, not only to a connection.

## Threat model (summary)

| Threat | Mitigation |
|---|---|
| Stolen database contents | Credentials are ciphertext under a managed key; AAD binds to owner and connection |
| Cross-tenant access | Owner is in every port signature; conformance suite proves refusal |
| Secret in logs, errors, responses | `Secret` type, classified errors, no bodies in errors, `Reveal` lint |
| SSRF through a configurable endpoint | Endpoints come from the manifest; egress policy; no user-supplied base URL except by host allow-list |
| Open redirect or callback hijack | Server-side single-use state, PKCE, allow-listed return destinations |
| Lost refresh token under concurrency | Lease plus compare-and-swap; rotated token persisted atomically |
| Compromised app private key (GitHub App) | Held by the host through a secret source; short-lived installation tokens; documented rotation |
| Confused deputy through a shared connection | Grants checked per use; bindings record purpose; audit of each use class |

Known limitations are recorded in this section as they are found.

## Testing

- **Store conformance suite** (`storetest`): tenancy isolation, compare-and-
  swap, active-version swap, destroy, listing filters. Every store, including
  the host's, runs it.
- **Cryptographer conformance**: round trip, AAD mismatch fails, wrong
  purpose fails, no plaintext in the stored value.
- **Provider fixtures**: recorded HTTP responses with `httptest`, covering
  valid, revoked, insufficient scope, rate-limited and provider-error cases.
  Fixtures contain no real tokens.
- **Concurrency**: N goroutines refreshing one connection produce one
  exchange and a consistent final credential; race detector clean.
- **Leak tests**: every error and event type is asserted not to contain a
  sentinel secret placed in the credential.
- **Egress tests**: redirects, DNS rebinding to a private address and
  off-manifest hosts are refused.
- Integration tests against live providers are opt-in by environment
  variable and excluded from the default run.
