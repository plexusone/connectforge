# Writing a provider

A provider is an adapter for one external service. It has three parts: a
**manifest** that describes it as data, a **validation** that proves a
credential works and names the account, and a way to **authenticate** a
request.

```go
type Provider interface {
	Manifest() Manifest
	Validate(ctx context.Context, hc *http.Client, m Material) (Result, error)
	Authenticate(r *http.Request, m Material) error
}
```

## The manifest

The manifest is declarative so a user interface can render a connection form
and an agent can read what a connection needs. It contains no code.

| Field | Meaning |
|---|---|
| `Key` | Identifies the provider, for example `cloudflare` |
| `DisplayName` | Shown to people |
| `BaseURLs` | The **only** origins a request for this provider may reach |
| `AuthMethods` | One or more ways to connect: `Key`, `Fields`, `Scopes`, `AccountSelection`, `AccountUnverifiable`, `HelpURL`, and the guidance `Setup` and `Rotation` |
| `HelpURL` | Where a person learns to create the credential |

A **field** is an input: `Name`, `Label`, `Secret`, `Required` and `Help`. A
**scope** names a permission the credential should hold, with a description
and whether it is required, so an interface can tell a person which
permissions to grant.

An auth method can guide a person through creating the credential, so an
interface can render a step-by-step setup instead of a bare form:

- `Setup` is ordered `SetupStep`s: a `Title`, a `Detail`, an optional `https`
  `URL` that opens the page at the service, and the `Rotation` modes the step
  applies to (empty means every mode).
- `Rotation` lists `RotationMode`s, the ways the credential is replaced over
  its life. `RotationManual` means a person replaces it, for example before
  it expires; `RotationAutomatic` means the host replaces it on a schedule.
  Each mode names the extra `Scopes` it needs, the `Risks` those scopes add,
  the `ExpiryDays` to give the credential when creating it, and whether it is
  `Recommended` (at most one is).

```go
AuthMethods: []provider.AuthMethod{{
	Key:    "api_token",
	Fields: []provider.Field{{Name: "token", Label: "API token", Secret: true, Required: true}},
	Rotation: []provider.RotationMode{{
		Key: provider.RotationManual, Label: "Rotate it myself", ExpiryDays: 90, Recommended: true,
	}},
	Setup: []provider.SetupStep{
		{Title: "Open API Tokens", URL: "https://dash.cloudflare.com/profile/api-tokens"},
		{Title: "Create a custom token", Detail: "Grant the permissions below, limited to your zones."},
		{Title: "Set the token to expire", Detail: "Choose an end date 90 days from today."},
	},
}},
```

`BaseURLs` are part of the security model: they are the egress allow-list.
Give a provider with regions or editions one provider per host, or a
constructor option that sets the base URL. Never let a stored connection
carry its own URL.

## A static-token service needs no code

For a service that issues an API key or token, configure the `apikey`
provider:

```go
p, err := apikey.New(apikey.Config{
	Manifest: provider.Manifest{
		Key: "cloudflare", DisplayName: "Cloudflare",
		BaseURLs: []string{"https://api.cloudflare.com"},
		HelpURL:  "https://developers.cloudflare.com/fundamentals/api/get-started/create-token/",
		AuthMethods: []provider.AuthMethod{{
			Key:    "api_token",
			Fields: []provider.Field{{Name: "token", Label: "API token", Secret: true, Required: true}},
			Scopes: []provider.Scope{{Name: "Zone:Read", Required: true}},
		}},
	},
	// Proves the token works.
	Verify: apikey.Request{Path: "/client/v4/user/tokens/verify", ExpiresAtPath: "result.expires_on"},
	// A second request discovers the account, when the first does not name it.
	Account: &apikey.Request{
		Path:             "/client/v4/accounts?per_page=50",
		AccountIDPath:    "result.0.id",
		AccountLabelPath: "result.0.name",
	},
})
```

| `Config` field | Default | Meaning |
|---|---|---|
| `Method` | `api_token` | The auth method this provider implements |
| `Field` | `token` | The field that holds the token |
| `Header` | `Authorization` | The request header that carries it |
| `Prefix` | `Bearer ` for `Authorization`, else none | Precedes the token in the header |
| `Verify` | required | A GET that validates the token |
| `Account` | none | A second GET that discovers the account |

A `Request` has a `Path` and dot-separated paths into the JSON response, where
a number indexes an array: `AccountIDPath`, `AccountLabelPath`, `ScopesPath`
for an array of strings, and `ExpiresAtPath` for the token's expiry as an
RFC 3339 timestamp. A missing or unparseable expiry leaves it unknown rather
than failing validation. `New` requires an `AccountIDPath` in `Verify` or
`Account` unless the auth method is `AccountUnverifiable`.

When a provider reports an expiry, the service records it on the credential
and in the connection's `Health.CredentialExpiresAt`, so an interface can say
when the credential must be replaced.

## Writing your own

Implement `Provider` when a service needs more than a header token: a signed
request, a two-step handshake, or a different verification.

```go
func (p *myProvider) Validate(ctx context.Context, hc *http.Client, m provider.Material) (provider.Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/me", nil)
	if err != nil {
		return provider.Result{}, err
	}
	if err := p.Authenticate(req, m); err != nil {
		return provider.Result{}, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return provider.Result{}, fmt.Errorf("%w: %w", cf.ErrTransient, err)
	}
	defer resp.Body.Close()
	if err := provider.Classify(p.Manifest().Key, resp); err != nil {
		return provider.Result{}, err
	}
	// decode resp.Body, return the account
}
```

### Exchanging a credential for a token

When the stored credential is not sent on requests but exchanged for a
short-lived access token, implement `provider.TokenSource` as well:

```go
func (p *myProvider) Token(ctx context.Context, hc *http.Client, m provider.Material) (provider.AccessToken, error) {
	// Sign a request with the key in m, post it to the token endpoint with hc,
	// and return the token with its expiry.
}
```

The broker mints a token through the restricted client, keeps it until
`provider.TokenExpiryMargin` before it expires, and sends it on each request,
so list the token endpoint's origin in the manifest's base URLs beside the
service's. Concurrent requests share one exchange, and a failed exchange is
not kept. A token with no expiry is used once. Nothing is stored. Make
`Authenticate` return `provider.ErrTokenSourceOnly`, and in `Validate` call the
service with `provider.NewTokenClient(p, m, hc, nil)`.

Rules that keep a provider safe:

- **Use the client you are given.** It is already restricted to the
  manifest's hosts. Never build your own for a request that carries a
  credential.
- **Classify with `provider.Classify`.** It maps statuses to the typed errors
  the service understands and records the status and request ID, never a
  body.
- **Never put a body, a header or the token in an error.** A provider may
  echo a credential in an error response.
- **Call `Reveal` only here.** `Secret.Reveal` is for provider adapters and
  the broker. A test fails if it is called anywhere else in the module; clear
  the copy you take.
- **Return the account ID.** The service refuses a credential whose
  validation returns no `AccountID` (`ErrAccountUnknown`), because without it
  a reconnect cannot prove the replacement credential is for the same
  account. If the service genuinely cannot name the account, set
  `AccountUnverifiable` on the auth method: the connection is then stored
  without an account ID, and a reconnect replaces its credential without the
  proof and records `account: unverified` on the audit event. Do not make the
  account discovery depend on a human.

## Register it

```go
reg := provider.NewRegistry()
if err := reg.Register(p); err != nil {
	return err // invalid manifest, or the key is already registered
}
```

`reg.Manifests()` returns every manifest ordered by key, which is what a
connection form lists.

## Test it

Run the provider against an `httptest` server that returns the service's
documented response shape. Check the request path and header, the discovered
account, and that a revoked token yields `ErrInvalidCredential` without the
token in the error text. Use obviously fake tokens in fixtures.
