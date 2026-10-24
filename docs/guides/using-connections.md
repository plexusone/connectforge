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
  and a name that resolves to an address that is not public: private,
  loopback, link-local, shared, reserved or documentation ranges, or an IPv6
  form (NAT64, 6to4, Teredo, IPv4-compatible) that embeds one.
- **Mints its own tokens.** For a provider whose credential is exchanged for
  short-lived access tokens, the client mints the first when the first
  request is made, keeps it until a minute before it expires, and then mints
  another, so a client that lives a while keeps working. The exchange reaches
  only the provider's hosts, and the token is never stored.
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

When `Health.CredentialExpiresAt` is set, a feature can warn ahead of time:
a credential that expires in a few days still works, but the person who
manages the connection should replace it now.

## Dependencies are visible

Record a **binding** when a feature starts depending on a connection, so a
disconnect can show what would stop working:

```go
err := svc.Bind(ctx, owner, actor, id,
	cf.Consumer{Kind: "application", ID: "dashboards"}, "traffic")
```

Bindings are idempotent, and deleting a connection removes them. Through
the service, `Bind` and `Unbind` require **use**, so whoever may use a
connection may say that a feature depends on it, and `Bindings` lists them for
anyone who may view it. Show that list before a disconnect:

```go
bs, err := svc.Bindings(ctx, owner, actor, id)
// "Disconnecting stops: portfolio.site impactchiefs.com (traffic)"
```

A [reconnect](connections.md#reconnect) leaves bindings untouched.
