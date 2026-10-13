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

## Failures are classified

| Provider answers | Error | Effect on a check |
|---|---|---|
| 401, other 4xx | `ErrInvalidCredential` | Permanent: state becomes `needs_attention` |
| 403 | `ErrInsufficientScope` | Permanent: state becomes `needs_attention` |
| 429 | `ErrRateLimited` | Health only |
| 5xx, network error, unparseable response | `ErrTransient` | Health only |

`connectionforge.Permanent(err)` reports whether an error is one of the
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
| `ErrConflict` | Another writer changed it first; retry |
| `ErrInvalidTransition` | The lifecycle forbids the change |
| `ErrCredentialUnavailable` | The credential could not be read or decrypted. The connection is not used; there is no fallback |
| `service.ErrInvalidInput` | A field is missing, unknown, or the auth method does not exist |
| `service.ErrNotAudited` | The change was made, but its audit event could not be recorded |

`ErrNotAudited` is special: the operation succeeded, and the returned
connection is valid. Treat the gap in the audit trail as an incident rather
than retrying the change.
