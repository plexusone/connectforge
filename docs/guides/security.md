# Security

!!! warning "Not externally audited"
    connectforge has had internal review only. It handles credentials that
    can expose a customer's data, so read this page and the
    [technical design](../specs/initiatives/INIT-CONNECTFORGE-001/TRD.md)
    before relying on it.

## What it protects

A connection is a credential for a customer's account at another service. The
module is built so that:

- the credential is **encrypted at rest** under a key your key manager holds,
  bound to its owner, connection and version;
- a **caller never receives** the credential: the broker returns an
  authenticated HTTP client;
- the credential **cannot appear** in a record, an event, an error or a log
  line through the module's own code;
- a request carrying a credential can **reach only** the provider's hosts;
- a record of **another tenant is never visible**, in code or, with
  row-level security, in the database.

## Defences

| Threat | Mitigation |
|---|---|
| Stolen database contents | Credentials are ciphertext; additional data binds owner, connection and version, so a ciphertext cannot be moved |
| Cross-tenant access | The owner is in every store method; the conformance suite proves refusal; optional row-level security repeats it in the database |
| A secret in a log, error or response | `Secret` redacts in every format; provider errors carry a status and a request ID, never a body; a test confines `Reveal` to the broker and providers |
| Server-side request forgery through a configurable endpoint | Hosts come from the manifest; HTTPS only; no redirect to another host; private, loopback, link-local, shared and other special-purpose addresses, and IPv6 forms (NAT64, 6to4, Teredo, IPv4-compatible) that embed one, are refused after DNS resolution |
| A lost update under concurrency | Compare-and-swap on the connection revision and on the credential version |
| A credential that cannot be read | The connection is not used; there is no fallback |
| An authorization backend outage mistaken for "no" | An `Authorizer` error that is not `ErrDenied` is reported as a failure |

## What you must do

- **Supply a real `Cryptographer`.** `cryptotest.New()` keeps its key in
  memory and exists for tests.
- **Use a stable, non-reusable owner.** An organization ID, not a slug or a
  name that can be released and given to someone else.
- **Fail closed in the transaction hook** when there is no tenant.
- **Keep the egress policy strict in production.** `AllowPrefixes`,
  `AllowPrivate` and `AllowHTTP` exist for tests against local fakes; leave
  them off. When a development or staging setup needs a non-public
  destination, list only its range in `AllowPrefixes` (for example
  `127.0.0.0/8`) rather than setting the deprecated `AllowPrivate`.
  Link-local and cloud metadata addresses (`169.254.0.0/16`, `fe80::/10`,
  `fd00:ec2::254`, `100.100.100.200`) are refused whatever the policy says.
- **Back up your key material** with the database. Without the key, stored
  credentials cannot be read.
- **Ask providers for the narrowest read permissions** their features need,
  in the manifest's scopes.
- **Treat `ErrNotAudited` as an incident.** The change was made.

## Secrets in your own code

- Hold credentials as `connectforge.Secret` from the moment you receive
  them, and never convert to `string` except at the last moment a provider
  needs it.
- Do not log a request body from a provider call.
- Do not put a credential in a URL, a query string or an error message.

## Access tokens

A provider that implements `TokenSource` has its stored key exchanged for
access tokens. The exchange goes through the same egress-restricted client as
every other request, so the manifest must name the token endpoint's origin and
a key cannot send the exchange elsewhere. A token is held as a `Secret` in the
client that uses it, lives about an hour, and is dropped with the client. It is
never sealed, logged or audited.

## Known limitations

- **Static keys, and keys exchanged for tokens.** A provider can exchange a
  stored key for short-lived access tokens, which the broker keeps in memory
  in the client that uses them and never stores. OAuth with refresh and GitHub
  App installations are planned. A credential is replaced in place with
  `Reconnect`.
- **Rotation is described, not performed.** A manifest can describe automatic
  rotation, but connectforge does not replace credentials on a schedule; a
  host that offers it implements it. The credential's expiry is recorded when
  the provider reports it, and nothing yet warns before it passes.
- **A client holds the credential it was made with.** A disconnect applies to
  the next client.
- **Grants are the host's.** Your `Authorizer` decides who may view, use or
  manage a connection. Bindings are managed by the service, but a disconnect
  does not yet require acknowledging them.
- **Rate-limit observation, DPoP and agent-delegated authorization** are not
  implemented. The model leaves room for them: a connection is not
  synonymous with a stored secret.

## Reporting a problem

Report a suspected vulnerability privately to the maintainers through the
repository's security advisories rather than in a public issue.
