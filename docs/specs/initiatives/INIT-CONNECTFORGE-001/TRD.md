# connectforge: Technical Requirements

**Initiative:** `INIT-CONNECTFORGE-001`
**Repository:** `github.com/plexusone/connectforge`

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

connectforge is a library of ports and a core that composes them. It
imports the Go standard library, `golang.org/x/oauth2` and nothing from any
host. The host is the composition root.

```text
host application
  ├── implements Ports: stores, Cryptographer, Authorizer, Auditor, Leaser, Egress
  ├── registers Providers (adapters, each with a Manifest)
  └── mounts httpapi (optional) or calls the Service directly

connectforge
  ├── Service   create, test, reconnect, replace, revoke, list
  ├── Broker    authenticated clients for a connection
  └── Refresher single-flight token refresh
```

### Packages

| Package | Responsibility | Imports |
|---|---|---|
| `connectforge` (root) | Types, errors, `Secret`, ports, lifecycle states | stdlib |
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
| `cmd/connectforge` | Thin CLI over `service` | service |

connectforge owns its domain, including its database schema. The host
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
    Metadata          map[string]string // non-secret provider settings
    CreatedBy         Principal
    CreatedAt, UpdatedAt time.Time
    Revision          uint64            // increases on every update; drives compare-and-swap
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
| `revoked` | Revoked at the provider by connectforge; terminal |

Transient failures (network, rate limit, provider outage) change `Health`,
never `State`. The transition table lives in code, is the single source for
the documentation and is enforced by every store, which updates a connection
only by compare-and-swap on its revision.

```go
type Health struct {
    LastValidatedAt  time.Time
    LastValidation   Outcome   // ok, failed, unknown
    LastError        string    // classified; never a secret, a body, a URL or an address
    ConsecutiveFails int
    // CredentialExpiresAt is when the current credential stops working, as
    // the provider reported at the last successful validation.
    CredentialExpiresAt *time.Time
}
```

A provider reports a credential's expiry in its validation `Result`. `Create`,
a successful `Test` and `Reconnect` record it in `Health` and on the sealed
`Credential`; a failed `Test` keeps the last known value. `Health` is stored
as one JSON value, so the field needs no schema change.

Rate-limit observations join `Health` when rate-limit tracking is added.

### Credential

```go
type Credential struct {
    Owner        OwnerID
    ConnectionID string
    Version      int        // 1, 2, ... one more per replacement or refresh
    Kind         string     // "api_token", "oauth_tokens", "app_installation"
    Ciphertext   []byte     // self-describing envelope from the Cryptographer
    ExpiresAt    *time.Time // for the access token, if known
    CreatedAt    time.Time
}
```

The plaintext structure for each `Kind` is private to its provider and
sealed as a whole. The ciphertext is a self-describing envelope, so it
records which key version sealed it and needs no separate key reference.
Credentials are append-only by version, and the credential store tracks the
active version. Older versions are kept until the connection's credentials
are destroyed, so a replacement can be validated before it is activated.

### Grants and bindings

```go
type Grant struct {
    Owner        OwnerID
    ConnectionID string
    Principal    Principal
    Actions      []Action // use, manage, share, disconnect
}

type Binding struct {
    Owner        OwnerID
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
    CreateConnection(ctx context.Context, c Connection) (Connection, error) // revision 1; ErrExists
    GetConnection(ctx context.Context, owner OwnerID, id string) (Connection, error)
    ListConnections(ctx context.Context, owner OwnerID, f Filter) ([]Connection, error)
    // UpdateConnection replaces c iff the stored revision equals c.Revision,
    // else ErrConflict; a forbidden state change is ErrInvalidTransition.
    UpdateConnection(ctx context.Context, c Connection) (Connection, error)
    DeleteConnection(ctx context.Context, owner OwnerID, id string) error // cascades
}

type CredentialStore interface {
    // SwapCredential installs next as active iff the active version is
    // expectActive (0 for none) and next.Version == expectActive+1.
    SwapCredential(ctx context.Context, expectActive int, next Credential) error
    ActiveCredential(ctx context.Context, owner OwnerID, connID string) (Credential, error)
    DestroyCredentials(ctx context.Context, owner OwnerID, connID string) error
}

// GrantStore and BindingStore put, remove and list by connection.
// Store embeds all four, so one type can implement them.

type Cryptographer interface {
    Seal(ctx context.Context, purpose string, plaintext, aad []byte) ([]byte, error)
    Open(ctx context.Context, purpose string, ciphertext, aad []byte) ([]byte, error)
}

type Authorizer interface {
    Allowed(ctx context.Context, p Principal, a Action, conn Connection) error // ErrDenied
}

type Auditor interface {
    Record(ctx context.Context, e Event) error
}

type Leaser interface {
    // Acquire returns a lease or ErrLeaseHeld. The lease is released or expires.
    Acquire(ctx context.Context, key string, ttl time.Duration) (Lease, error)
}

type SecretSource interface {
    // Secret reads a deployment secret, such as an OAuth client secret or a
    // GitHub App private key, from the host's secret manager.
    Secret(ctx context.Context, ref string) (Secret, error)
}
```

- **Cryptographer** binds `aad` to the owner, connection ID and credential
  version, so a ciphertext cannot be moved between connections or tenants.
  The purpose is `connectforge.credential`. The port is deliberately the
  same shape as an envelope-encryption client's encrypt and decrypt, so a
  key-management library satisfies it with a small adapter in the host.
- **Leaser** is how refresh is made single-flight across processes. A host
  may implement it with an advisory lock or a lease row; the in-memory
  implementation is for tests.
- **Authorizer** is called by `service` and by the broker. A failed
  authorization is an error, never a silent downgrade.
- **Auditor** returns an error. An operation whose event cannot be recorded
  reports it; it is never dropped silently.
- **SecretSource** is how deployment secrets reach the library. A secret
  manager (for example omnivault) implements it in the host. Per-owner
  credentials never go through it: they are sealed and stored as above.

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
- **Table names are prefixed** `connectforge_`, so they cannot collide with
  the host's.
- **Tenant column.** Every table carries `owner_id` (text). Every query
  filters on it in code; a host that enables row-level security keys its
  policy on the same column, so isolation holds twice.
- **Every operation runs in a transaction** and calls the host's `TxHook`
  first, so session variables such as a tenant setting apply to it. A hook
  error aborts the operation.
- **Uniqueness and swaps are in the database.** Credential versions are
  unique per connection and the active version is the highest, so two
  concurrent `SwapCredential` calls for the same version are decided by the
  unique index. `UpdateConnection` is a conditional update on the revision,
  and its affected-row count decides the outcome.
- **The lease table has no owner.** Lease names (for example
  `refresh:<connection>`) are not owned records, so `Tables()` leaves the
  lease table out of the host's tenancy policy.
- **No driver is imported.** The host opens the database with the driver it
  already uses. A SQLite database needs foreign keys on (Ent requires it to
  migrate) and one connection or immediate transactions, so writers queue.
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

- `Reveal` is called only inside provider adapters and the broker. A test
  (`boundary_test.go`) fails on a call to `Reveal` anywhere else.
- Errors from adapters are classified (`ErrInvalidCredential`,
  `ErrInsufficientScope`, `ErrTransient`, `ErrRateLimited`, ...) and carry
  the provider's status and request ID, never a request or response body that
  could echo a credential.
- Plaintext buffers are zeroed after use on a best-effort basis.

## Providers

```go
type Provider interface {
    Manifest() Manifest
    // Validate checks the credential with the provider, using an HTTP client
    // the caller restricted to the manifest's hosts, and returns the account.
    Validate(ctx context.Context, hc *http.Client, m Material) (Result, error)
    // Authenticate adds the credential to the caller's copy of a request.
    Authenticate(r *http.Request, m Material) error
}

// Optional capabilities, discovered by type assertion (later phases).
type Refresher  interface { Refresh(ctx context.Context, hc *http.Client, m Material) (Material, error) }
type Revoker    interface { Revoke(ctx context.Context, hc *http.Client, m Material) error }
type Discoverer interface { Accounts(ctx context.Context, hc *http.Client, m Material) ([]Account, error) }
```

A provider whose credential is exchanged for a short-lived access token, such
as a service account key signed into a token request, also implements
`TokenSource`:

```go
type TokenSource interface {
    Token(ctx context.Context, hc *http.Client, m Material) (AccessToken, error)
}
```

The broker detects it by type assertion and, in place of `Authenticate`,
authenticates each request with a token it mints through the same egress-
restricted client, so the manifest lists the token endpoint's origin beside
the service's. The token is kept in the client until a minute before it
expires and concurrent requests share one exchange. It is derived from the
stored credential and never stored, so a refresh needs no lease and nothing
is persisted. A failed exchange is returned and not kept. A provider
validates with `provider.NewTokenClient`, which applies the same rule to its
own calls, and its `Authenticate` returns `provider.ErrTokenSourceOnly`.

`Material` is the unsealed credential handed to an adapter inside the
broker: a kind, its fields as `Secret` values, and an expiry. It is encoded
for sealing by the `provider` package, versioned, with field values as bytes
so they can be cleared. A provider never makes its own HTTP client; it uses
the one it is given, so egress policy applies to validation as well as use.
Adapters classify a refusal with `provider.Classify`: 401 and other 4xx are
an invalid credential, 403 an insufficient scope, 429 rate limiting and 5xx
transient; the error carries the status and the provider's request ID, never
a body. Refresh and revoke are optional because static keys do not support
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

Validation must return the account ID, so `Reconnect` can prove a
replacement credential is for the connection's account: `Create` refuses a
credential with no account ID, and `Reconnect` refuses one whose account
differs (`ErrAccountMismatch`) or cannot be compared because either side has
none (`ErrAccountUnknown`). An auth method whose provider cannot name the
account sets `"accountUnverifiable": true`; its connections may have no
account ID, and a reconnect that cannot compare accounts proceeds and
records `account: unverified` in its audit event.

An auth method may also guide a person through creating the credential:

```json
"setup": [
  {"title": "Open API Tokens", "url": "https://dash.cloudflare.com/profile/api-tokens"},
  {"title": "Allow the token to roll itself", "rotation": ["automatic"]}
],
"rotation": [
  {"key": "manual", "label": "Rotate it myself", "expiryDays": 90, "recommended": true},
  {"key": "automatic", "label": "Rotate it for me",
   "scopes": [{"name": "API Tokens: Edit", "required": true}],
   "risks": ["The token can also create other tokens"]}
]
```

`setup` is ordered steps, each with an optional `https` link and the rotation
modes it applies to. `rotation` lists the ways the credential is replaced:
each mode's extra `scopes`, the `risks` those scopes add, the lifetime to set
when creating the credential (`expiryDays`), and at most one `recommended`
mode. `manual` and `automatic` are the keys a host acts on; a host does not
offer a mode it does not know. Validation rejects a step without a title, a
non-`https` link, a step naming an unlisted mode, duplicate or unlabeled
modes, and more than one recommended mode.

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

- Authorization (`use`) and state are checked each time a client is made,
  and the `Use` (consumer and purpose) is reported to an optional `OnUse`
  hook for counters, not recorded as an event per request. A refusal is
  audited.
- The credential is unsealed once, when the client is made, and the
  transport adds it to a copy of each request. A caller makes a client per
  unit of work, so a disconnect or a replaced credential applies to the next
  client. Refresh (phase 3) happens when a client is made, under the lease.
- The client is wrapped by `egress`: a request whose host is not in the
  provider's manifest `baseUrls` is refused, redirects to another host are
  refused, and a resolved address that is not public is refused: private,
  loopback, link-local, shared, reserved and documentation ranges, and IPv6
  transition forms that embed an IPv4 address (a NAT64 address is judged by
  the IPv4 address it embeds; 6to4, Teredo and IPv4-compatible addresses are
  refused outright), unless the host explicitly allows a development
  destination by listing its range in `AllowPrefixes`. Link-local and cloud
  metadata addresses, and IPv6 forms that embed one, are refused whatever
  the policy allows.
- The result is an `*http.Client` a provider SDK can accept
  (`WithHTTPClient`), which is how SDKs consume a connection without
  connectforge importing them.

## Service

`service` runs the operations a host exposes. Each authorizes through the
host's `Authorizer`, audits, and retries a lost compare-and-swap a bounded
number of times.

| Operation | Action | Behaviour |
|---|---|---|
| `Create` | `manage` | Checks fields against the manifest, validates with the provider, and stores the connection and its sealed credential only if the provider accepts it |
| `Get`, `List` | `view` | `List` returns only the connections the actor may view |
| `Rename` | `manage` | Changes the display name |
| `Test` | `use` | Validates again; the outcome goes to health. A permanent failure moves the connection to `needs_attention`, success back to `connected`, a transient failure changes only health |
| `Disconnect` | `disconnect` | Marks it disconnected and destroys its credentials; the record stays |

An audit failure after a successful change returns `ErrNotAudited`: the change
was made and the gap in the trail is reported, not hidden.

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
