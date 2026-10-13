# Integrating with a host

connectionforge imports no key manager, secret manager, authorization system
or application framework. Your application, the host, supplies five small
interfaces and a store. This page shows each.

| Port | You supply | connectionforge uses it to |
|---|---|---|
| `Cryptographer` | Encryption under a managed key | Seal and open credentials |
| `Authorizer` | Your authorization | Decide whether a principal may view, use, manage, share or disconnect |
| `Auditor` | Your audit trail | Record each operation and each refusal |
| `Leaser` | Short exclusive leases | Make a refresh single-flight across processes (the SQL store provides one) |
| `SecretSource` | Your secret manager | Read deployment secrets such as OAuth client secrets and app keys |
| `Store` | Persistence | Hold connections, credentials, grants and bindings: [Storage](storage.md) |

## Cryptographer

```go
type Cryptographer interface {
	Seal(ctx context.Context, purpose string, plaintext, aad []byte) ([]byte, error)
	Open(ctx context.Context, purpose string, ciphertext, aad []byte) ([]byte, error)
}
```

`purpose` is always `connectionforge.CredentialPurpose`
(`connectionforge.credential`). `aad` binds the ciphertext to its owner,
connection and credential version, so it cannot be moved to another record:
an implementation must fail to open a ciphertext with different `aad` or a
different purpose. Run `cryptotest.Run` against your implementation to check.

An adapter over [keyforge](https://github.com/plexusone/keyforge):

```go
type keyforgeCrypto struct{ c *keyforge.Client }

func (k keyforgeCrypto) Seal(ctx context.Context, purpose string, pt, aad []byte) ([]byte, error) {
	e, err := k.c.Encrypt(ctx, keyforge.Purpose(purpose), pt, aad)
	if err != nil {
		return nil, err
	}
	return e.MarshalBinary()
}

func (k keyforgeCrypto) Open(ctx context.Context, purpose string, ct, aad []byte) ([]byte, error) {
	e, err := envelope.Parse(ct)
	if err != nil {
		return nil, err
	}
	pt, _, err := k.c.Decrypt(ctx, keyforge.Purpose(purpose), e, aad)
	return pt, err
}
```

Declare `connectionforge.credential` as a purpose when you open the key
system. `cryptotest.New()` is an in-memory AES-256-GCM implementation for
tests; do not use it in production, because its key lives only in memory.

## Authorizer

```go
type Authorizer interface {
	Allowed(ctx context.Context, p Principal, a Action, c Connection) error
}
```

Return nil to allow, or an error wrapping `connectionforge.ErrDenied` to
refuse. Any other error is reported as a failure, not a refusal, so a broken
authorization backend is never mistaken for "no".

The actions are `view`, `use`, `manage`, `share` and `disconnect`. A natural
mapping onto a resource-role ladder is: `view` to view, `use` to edit, and
`manage`, `share` and `disconnect` to manage. Creating a connection is
authorized as `manage` on the connection about to exist; its `Owner` is set
and its `Revision` is zero, which lets you decide by organization role alone.

The principal's `Kind` is `user`, `service` or `agent`. For an agent acting on
someone's behalf the broker records `OnBehalfOf`; your `Authorizer` receives
only the principal, so decide there whether an agent needs its own grant.

## Auditor

```go
type Auditor interface {
	Record(ctx context.Context, e Event) error
}
```

An event has a time, a type, the owner, the actor, the connection, the
provider, success and a reason. It never carries a secret or a response body.
Event types:

`connection.created`, `connection.validated`, `connection.state_changed`,
`connection.updated`, `connection.disconnected`, `connection.deleted`,
`credential.replaced`, `credential.refreshed`, `credential.revoked`,
`grant.changed` and `access.denied`.

Return an error if you cannot record. The service reports it as
`ErrNotAudited` after the change was made; it does not roll back.

## SecretSource

```go
type SecretSource interface {
	Secret(ctx context.Context, ref string) (Secret, error)
}
```

For secrets that belong to the deployment rather than to a tenant. Per-tenant
credentials never go through it: they are sealed and stored by connectionforge.

## Tenancy

Every record carries an `Owner`, an opaque string. Use your tenant identifier,
for example an organization ID, and never a name or slug that can be released
and reassigned. Every store method takes the owner and returns
`ErrNotFound` for a record of another owner.

With PostgreSQL you can enforce this a second time in the database with
row-level security. See [Storage](storage.md#row-level-security).

## Putting it together

```go
store, err := sqlstore.New(db, sqlstore.Postgres, sqlstore.WithTxHook(setTenant))
if err != nil {
	return err
}
if err := store.Migrate(ctx); err != nil {
	return err
}

b, err := broker.New(broker.Options{
	Store: store, Crypto: crypto, Providers: providers, Authorizer: authz, Auditor: audit,
	Egress: egress.Policy{}, // HTTPS to each provider's hosts only
})
if err != nil {
	return err
}
svc, err := service.New(service.Options{
	Store: store, Broker: b, Providers: providers, Authorizer: authz, Auditor: audit,
})
```

[SystemForge](https://github.com/plexusone/systemforge) is an example of a
host: its `connections` package composes these ports from its key manager,
authorization, audit trail and row-level security, and serves the service as
an HTTP API.
