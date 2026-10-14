# connectforge: Product Requirements

**Initiative:** `INIT-CONNECTFORGE-001`
**Repository:** `github.com/plexusone/connectforge`

## Problem

Applications that integrate with third-party services (a code host, a CDN, an
analytics product, a payment processor) each end up with a private, ad hoc way
to hold the customer's credentials for that service. The same gaps recur:

- **Credentials are scattered.** An API token in one table, an OAuth refresh
  token in another, a GitHub App key in an environment variable. There is no
  single inventory of which tenant has connected what.
- **Nobody knows whether a connection still works.** A token is revoked or
  expires, and the first sign is a failed sync inside a feature.
- **OAuth refresh is easy to get subtly wrong.** Two workers refresh the same
  token at once, the rotated refresh token is lost, and the customer has to
  reconnect.
- **Secrets leak through convenience.** Raw tokens reach logs, API responses
  and browser code because the simplest way to use a credential is to hand it
  to the caller.
- **Every provider is a special case.** GitHub Apps mint short-lived
  installation tokens from a private key; Cloudflare uses scoped API tokens;
  Stripe uses restricted keys or Connect. Each new provider re-solves
  validation, account discovery and revocation.

Hosted automation products solve this with a connection framework: a
connection is an authorized account that is reusable, tested and labelled.
There is no equivalent library for Go applications that is multi-tenant,
encrypts credentials under a managed key, and is ready for agent-delegated
authorization.

## Goals

1. **One model for every external connection**: provider, connection,
   credential, grant, binding, with a single lifecycle.
2. **Own the credential lifecycle, not the API clients.** Provider SDKs stay
   in their own modules; connectforge supplies an authenticated transport,
   validation and status.
3. **Secrets stay server-side and out of normal paths.** Callers receive
   metadata and an authenticated HTTP client, not raw tokens; secrets are
   redacted in logs, errors and JSON by construction.
4. **Correct refresh and rotation.** Refresh is single-flight and persists a
   rotated refresh token atomically; replacing a static credential is a
   separate, audited operation.
5. **Tested connections.** Every connection can be validated on demand and on
   a schedule, with account discovery and a human-readable account label.
6. **Ports and adapters.** Storage, encryption, authorization, audit and
   egress policy are interfaces supplied by the host; the library imports no
   host framework.
7. **Declarative provider manifests.** A provider states its authentication
   methods, fields, scopes and validation operation as data, so a UI or an
   agent can render and repair a connection without provider-specific code.
8. **Room for delegated authorization.** A connection is not synonymous with
   a stored secret, so workload identity and agent-delegated grants can be
   added without changing the model.

## Non-goals

- Provider API clients. connectforge does not wrap the GitHub, Cloudflare
  or Stripe APIs.
- Cryptography. Encryption is a port; a key-management library implements it.
- A secret manager. Retrieving bootstrap secrets (client secrets, an app
  private key) is the host's job, through a port.
- A workflow or automation engine. A connection can be used by workflows; it
  is not one.
- Authenticating or authorizing the host's users. The host supplies an
  authorizer and a tenant scope.
- Shipping a UI in the core module. A UI kit built on the provider manifests
  is a separate, later module.

## Users

| User | Needs |
|---|---|
| Application developer | Ask for an authenticated HTTP client for a tenant's connection; never handle a raw token |
| Tenant administrator | Connect, test, rename, reconnect, replace and revoke connections; see what depends on one |
| Operator | See which connections are failing or expiring; know that refresh and rotation are safe under concurrency |
| Provider author | Describe a provider and its validation once, with conformance tests, and have it work for every host |
| Security reviewer | Read a stated threat model and an audit trail that never contains a secret |
| Platform integrator | Plug in storage, encryption, authorization, audit and egress policy |

## Requirements

### Functional

| ID | Requirement |
|---|---|
| FR1 | A registry of provider definitions, each with a manifest: authentication methods, credential fields (marked secret or not), scopes, account-selection needs, validation operation |
| FR2 | Tenant-owned connections with a lifecycle state, a display name, an external account identifier and label, granted scopes and observed health, kept distinct |
| FR3 | Versioned, encrypted credentials separate from the connection record; the credential store never holds plaintext |
| FR4 | Static credentials: API keys and tokens, with validation and account discovery on create |
| FR5 | OAuth 2.0 authorization code with PKCE, state and a validated return destination, following the current OAuth security best practice |
| FR6 | Automatic access-token refresh, single-flight per credential, persisting a rotated refresh token atomically, with classification of permanent versus transient failure |
| FR7 | GitHub App installations as a first-class method: installation tokens minted from the app key and cached until near expiry |
| FR8 | Connection testing on demand and on a schedule, producing a validation result and updating observed health without changing lifecycle state |
| FR9 | Reconnect, replace credential, rotate, disconnect and revoke, each audited; revocation calls the provider where it supports it |
| FR10 | A credential broker: `HTTPClient` and `Token`-less access that injects authorization per request, so a caller never receives the secret |
| FR11 | Grants: which principals (users, services, agents) may use, manage, share or disconnect a connection, enforced through a host `Authorizer` |
| FR12 | Bindings: which consumers (application, workflow, agent) use a connection, so disconnecting can report what would stop working |
| FR13 | Multiple connections per provider per tenant, and multiple credentials per connection during replacement |
| FR14 | Audit events for create, validate, use (sampled or counted), refresh, replace, revoke and administrative access, with no secret values |
| FR15 | Egress policy: provider endpoints come from the manifest, redirects are restricted, and private and link-local destinations are refused |
| FR16 | Conformance suites for the stores, the cryptographer port and providers, with recorded fixtures for provider adapters |
| FR17 | Rate-limit observation from provider responses, surfaced as health (later) |
| FR18 | Deployment secrets (OAuth client secrets, app keys) read through a host `SecretSource` port, never stored with per-owner credentials |

### Non-functional

| ID | Requirement |
|---|---|
| NFR1 | A secret value has a type that redacts itself in `String`, `GoString`, `MarshalJSON`, `MarshalText` and `slog`; it is never an error message |
| NFR2 | Fail closed: if decryption, authorization or validation cannot complete, the connection is not used |
| NFR3 | Tenant isolation is part of every store method signature; a store conformance test proves cross-tenant reads and writes fail |
| NFR4 | Library-first: HTTP handlers and a CLI are thin adapters over the library |
| NFR5 | No import of any host framework, and no import of a key-management or secret-manager module in the core |
| NFR6 | Public-ready repository and history from the first commit |
| NFR7 | Ships an Ent-backed store for SQLite and PostgreSQL that a host composes on its own database, applying its own tenancy policy; a host may still supply its own store |
| NFR8 | Context-aware, cancellable operations; no goroutines that outlive the caller's context except a documented, stoppable scheduler |

## Success criteria

1. A host can store a Cloudflare-style API token, see it validated with a
   discovered account label, and obtain an authenticated `*http.Client` for it
   without the token appearing in any API response, log or error.
2. Two processes refreshing the same OAuth connection at once result in one
   token exchange, and the rotated refresh token is not lost.
3. A GitHub App connection produces installation tokens, caches them, and
   renews them before expiry without a stored long-lived token.
4. Disconnecting a connection reports the bindings that depend on it before it
   acts, and the audit trail records who did it.
5. A cross-tenant read, write or use is refused by every store implementation.
6. A new provider can be added by describing a manifest and implementing the
   validation, without changing the core.

## Out of scope for v0.1.0

The UI kit, rate-limit tracking, DPoP and mutual TLS,
agent-delegated authorization and any provider beyond the two reference
implementations (an API-token provider and GitHub App). The model is designed
so each fits without changing stored data.

## Open questions

- Whether the HTTP handlers live in this module or in the host. The default
  is a small `connectforge/httpapi` package that a host may mount or
  ignore.
- Whether usage events are recorded individually or as counters per
  connection per period.
