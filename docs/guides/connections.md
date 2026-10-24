# Connections and their lifecycle

A **connection** is an owner's relationship with one account at a provider. It
is a record with a name, the account the provider reported, the permissions
the provider says it holds, a lifecycle **state**, and observed **health**. It
holds no secret; its credential is sealed and stored separately.

## State and health

Two things are tracked separately because they answer different questions.

| | Answers | Changes when |
|---|---|---|
| **State** | What was decided? | Someone connects, disconnects or reconnects, or a check finds a permanent failure |
| **Health** | What was last observed? | Every validation, including temporary failures |

A `connected` connection can show a failed last check, for example because the
provider was briefly unavailable. That does not make it unusable.

### States

| State | Meaning | May be used |
|---|---|---|
| `pending` | Created, not yet validated | No |
| `connected` | The provider accepted the credential at the last check | Yes |
| `needs_attention` | A permanent failure: revoked upstream, expired, or a permission lost | No, until reconnected |
| `disconnected` | A person disconnected it; credentials are destroyed | No |
| `revoked` | Revoked at the provider; terminal | No |

```text
pending ──validated──► connected ──► disconnected
                          │  ▲            ▲
                 failure  ▼  │ success    │
                     needs_attention ─────┘
                          │
                          ▼
                       revoked
```

Every store enforces the transition table; a change it forbids returns
`ErrInvalidTransition`.

### Health

| Field | Meaning |
|---|---|
| `LastValidatedAt` | When the provider was last asked |
| `LastValidation` | `ok`, `failed` or `unknown` |
| `LastError` | A classified message with the status and the provider's request ID. Never a response body |
| `ConsecutiveFails` | Failed checks in a row; reset by a success |
| `CredentialExpiresAt` | When the current credential stops working, as the provider reported at the last successful check; empty when it does not expire or the provider does not say |

The expiry is refreshed by `Create`, a successful `Test` and `Reconnect`, and
is also recorded on the sealed credential. Show it next to the connection so
a person replaces the credential before it lapses rather than after a feature
fails.

## Failures are classified

| Provider answers | Error | Effect on a check |
|---|---|---|
| 401, other 4xx | `ErrInvalidCredential` | Permanent: state becomes `needs_attention` |
| 403 | `ErrInsufficientScope` | Permanent: state becomes `needs_attention` |
| 429 | `ErrRateLimited` | Health only |
| 5xx, network error, unparseable response | `ErrTransient` | Health only |

`connectforge.Permanent(err)` reports whether an error is one of the
permanent kinds.

## The operations

All operations are on `service.Service`. Each checks authorization through
your `Authorizer` and records an audit event.

| Operation | Requires | What it does |
|---|---|---|
| `Create` | `manage` | Checks the supplied fields against the manifest, validates with the provider, then stores the connection and its sealed credential. A rejected credential stores nothing |
| `Get` | `view` | Returns one connection |
| `List` | `view` | Returns the owner's connections the actor may view, optionally filtered by provider and state |
| `Rename` | `manage` | Changes the display name |
| `Test` | `use` | Validates again and updates health and, for a permanent failure or a recovery, state |
| `Disconnect` | `disconnect` | Marks the connection disconnected and destroys its credentials. The record stays, so the audit trail and bindings keep their reference |
| `Reconnect` | `manage` | Replaces the credential, keeping the connection and everything bound to it. See below |
| `Bindings` | `view` | Lists what depends on the connection, so a person can see what a disconnect would stop |
| `Bind`, `Unbind` | `use` | Records, or removes, that a consumer depends on the connection for a purpose |

### Reconnect

A token expires, is revoked, or is rotated. `Reconnect` puts the connection
right without creating a new one, so the features bound to it keep working.
It is also how a person rotates a credential by hand: create the new token
before `CredentialExpiresAt`, then reconnect with it.

```go
conn, err := svc.Reconnect(ctx, service.ReconnectInput{
	Owner: owner, Actor: actor, ID: conn.ID,
	Fields: map[string]cf.Secret{"token": cf.SecretString(newToken)},
})
```

The new credential is validated with the provider **before** anything is
stored, so a rejected one changes nothing. It must belong to the same account
as the connection (`ErrAccountMismatch` otherwise): connecting another account
is a new connection, so a feature never silently reads someone else's data.
The check fails closed: when the provider returns no account ID, or the
connection has none recorded, the reconnect is refused with
`ErrAccountUnknown`, unless the auth method is `AccountUnverifiable`, in which
case the credential is replaced and the audit event records
`account: unverified`.
On success the connection is `connected` again with fresh health, and its
credential has a new version.

Reconnect also recovers a connection whose sealed credential can no longer be
read, for example after a lost key ring: the new credential is sealed without
opening the old one. A `disconnected` connection can be reconnected; a
`revoked` one cannot.

### Test, in detail

`Test` does not return the validation failure as an error. A failed check is a
normal outcome that is recorded; read it from the returned connection:

```go
conn, err := svc.Test(ctx, owner, actor, id)
if err != nil {
	return err // not found, denied, or the connection cannot be tested
}
switch {
case conn.State == cf.StateNeedsAttention:
	// the person must reconnect
case conn.Health.LastValidation == cf.OutcomeFailed:
	// a temporary problem; try again later
}
```

A connection that is `disconnected` or `revoked` cannot be tested and returns
`ErrNotUsable`.

## Errors a caller handles

| Error | Meaning |
|---|---|
| `ErrNotFound` | No such record for this owner. A record of another owner is also not found |
| `ErrDenied` | The `Authorizer` refused |
| `ErrNotUsable` | The connection's state does not allow it |
| `ErrAccountMismatch` | A reconnect's credential belongs to a different account |
| `ErrAccountUnknown` | The provider did not identify the credential's account, or the connection has no recorded account, so the account cannot be checked |
| `ErrConflict` | Another writer changed it first; retry |
| `ErrInvalidTransition` | The lifecycle forbids the change |
| `ErrCredentialUnavailable` | The credential could not be read or decrypted. The connection is not used; there is no fallback |
| `service.ErrInvalidInput` | A field is missing, unknown, or the auth method does not exist |
| `service.ErrNotAudited` | The change was made, but its audit event could not be recorded |

`ErrNotAudited` is special: the operation succeeded, and the returned
connection is valid. Treat the gap in the audit trail as an incident rather
than retrying the change.
