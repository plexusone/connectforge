# Using a connection

The broker is the only path from a stored credential to an outgoing request.
You ask it for an HTTP client for a connection; it checks that you may, unseals
the credential, and returns a client that authenticates each request.

## Get a client

```go
hc, err := b.HTTPClient(ctx, owner, principal, connectionID, cf.Use{
	Consumer: cf.Consumer{Kind: "application", ID: "dashboards"},
	Purpose:  "traffic",
})
```

The broker, in order:

1. Loads the connection for the owner. Another owner's connection is not found.
2. Asks the `Authorizer` whether the principal may **use** it, and records a
   refusal as an audit event.
3. Requires the state to be `connected`; otherwise `ErrNotUsable`.
4. Reads and decrypts the active credential. Any failure is
   `ErrCredentialUnavailable`.
5. Returns a client restricted to the provider's hosts.

`Use` says why: a consumer (an application, workflow or agent) and a purpose.
It is passed to your `OnUse` hook, if you set one, for counters. An agent
acting for someone sets `OnBehalfOf`, which is recorded with any refusal.

## What the client does

- **Adds the credential to a copy of each request.** The request you built is
  not modified.
- **Reaches only the provider's hosts.** A request for any other host fails
  with `egress.ErrRefused`, as does a redirect to another host, plain HTTP,
  and a name that resolves to a private, loopback or link-local address.
- **Holds the credential it was made with.** A disconnect or a replaced
  credential applies to the next client, so make a client per unit of work
  rather than keeping one for the life of the process.

## Hand it to an SDK

Most provider SDKs accept an `*http.Client`. Give them the broker's, and they
authenticate and stay inside the allowed hosts without ever seeing the token:

```go
hc, err := b.HTTPClient(ctx, owner, principal, id, use)
if err != nil {
	return err
}
client, err := someprovider.NewClient(someprovider.WithHTTPClient(hc))
```

If an SDK insists on a token argument, pass any non-empty placeholder: the
broker's transport replaces the `Authorization` header, or sets the header the
provider's manifest names.

## Check before you rely on it

A connection can stop working between requests. Call `Test` when a feature
starts and on a schedule, and show the person `Health.LastError` when a check
fails. For a feature that cannot proceed, return a message that tells the
person to reconnect, not a raw error.

## Dependencies are visible

Record a **binding** when a feature starts depending on a connection, so a
disconnect can show what would stop working:

```go
err := store.PutBinding(ctx, cf.Binding{
	Owner: owner, ConnectionID: id,
	Consumer: cf.Consumer{Kind: "application", ID: "dashboards"},
	Purpose:  "traffic",
})
```

Bindings are idempotent, and deleting a connection removes them.
