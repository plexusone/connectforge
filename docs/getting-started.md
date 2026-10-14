# Getting started

This page connects a fictional service, verifies its token, and makes an
authenticated request, using the in-memory store and a test cryptographer.
A real deployment replaces those two with a SQL store and a key-management
adapter; see [Integrating with a host](guides/host-integration.md).

## Install

```bash
go get github.com/plexusone/connectforge@v0.1.0
```

Requires Go 1.26.

## 1. Describe the provider

A provider is a manifest (what a person must supply, which hosts a request may
reach) and a verification request. For a service that issues a static API
token, the `apikey` provider is configuration only:

```go
import (
	"github.com/plexusone/connectforge/provider"
	"github.com/plexusone/connectforge/provider/apikey"
)

p, err := apikey.New(apikey.Config{
	Manifest: provider.Manifest{
		Key:         "example",
		DisplayName: "Example",
		BaseURLs:    []string{"https://api.example.com"},
		AuthMethods: []provider.AuthMethod{{
			Key:    "api_token",
			Fields: []provider.Field{{Name: "token", Label: "API token", Secret: true, Required: true}},
		}},
	},
	// GET /v1/me proves the token works and names the account.
	Verify: apikey.Request{Path: "/v1/me", AccountIDPath: "id", AccountLabelPath: "name"},
})
if err != nil {
	return err
}
providers := provider.NewRegistry()
if err := providers.Register(p); err != nil {
	return err
}
```

## 2. Compose the pieces

```go
import (
	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/broker"
	"github.com/plexusone/connectforge/cryptotest"
	"github.com/plexusone/connectforge/service"
	"github.com/plexusone/connectforge/store/memstore"
)

store := memstore.New()
b, err := broker.New(broker.Options{
	Store:      store,
	Crypto:     cryptotest.New(), // tests only: use a key-management adapter
	Providers:  providers,
	Authorizer: authorizer,  // yours: may this principal do this?
	Auditor:    auditor,     // yours: where events go
})
if err != nil {
	return err
}
svc, err := service.New(service.Options{
	Store: store, Broker: b, Providers: providers, Authorizer: authorizer, Auditor: auditor,
})
if err != nil {
	return err
}
```

`Authorizer` and `Auditor` are small interfaces; the
[host integration guide](guides/host-integration.md) shows each.

## 3. Connect

```go
owner := cf.OwnerID("org-1") // the tenant: opaque to the library
actor := cf.Principal{Kind: cf.PrincipalUser, ID: "alice"}

conn, err := svc.Create(ctx, service.CreateInput{
	Owner: owner, Actor: actor,
	Provider: "example", AuthMethod: "api_token",
	DisplayName: "Production",
	Fields:      map[string]cf.Secret{"token": cf.SecretString(tokenFromTheUser)},
})
```

`Create` calls the provider's verification endpoint first. If the provider
rejects the token, nothing is stored and the error wraps
`connectforge.ErrInvalidCredential`. On success `conn` holds the discovered
account (`ExternalAccountID`, `ExternalLabel`) and state `connected`. The token
itself is sealed and stored separately and is never part of `conn`.

## 4. Use

```go
hc, err := b.HTTPClient(ctx, owner, actor, conn.ID, cf.Use{
	Consumer: cf.Consumer{Kind: "application", ID: "dashboards"},
	Purpose:  "traffic",
})
if err != nil {
	return err
}
resp, err := hc.Get("https://api.example.com/v1/zones")
```

`hc` adds the token to each request and can reach only `api.example.com`. Pass
it to a provider SDK's `WithHTTPClient` option and the SDK never sees the
token either.

## 5. Keep it healthy

```go
conn, err = svc.Test(ctx, owner, actor, conn.ID)
// conn.State and conn.Health now reflect the provider's answer.

conn, err = svc.Disconnect(ctx, owner, actor, conn.ID)
```

## Next

- [Connections and their lifecycle](guides/connections.md)
- [Using a connection](guides/using-connections.md)
- [Writing a provider](guides/providers.md)
