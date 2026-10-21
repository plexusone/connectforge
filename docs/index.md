# connectforge

Manage a tenant's connections to external services in Go: provider
definitions, connections, sealed credentials, grants and bindings, and a
broker that hands out authenticated HTTP clients so callers never handle a
raw secret.

!!! warning "Pre-release"
    connectforge has not been externally audited. It supports static API-token
    connections and credentials exchanged for short-lived access tokens, such
    as service account keys. Read [Security](guides/security.md) first.

## What it does

| | |
|---|---|
| **One model for every connection** | Provider, connection, credential, grant and binding, with a lifecycle that separates what was decided from what was observed. |
| **Owns the credential lifecycle, not the API clients** | Provider SDKs stay in their own modules and receive an authenticated `*http.Client` through their `WithHTTPClient` option. |
| **Secrets stay out of sight** | `Secret` redacts itself everywhere it can be printed, errors carry a status and a request ID but never a body, and only the broker and providers can read a value. |
| **Token sources** | A provider can exchange a stored key for short-lived access tokens. The broker mints them through the restricted client, keeps them until shortly before they expire and never stores them. |
| **Tested connections** | A credential is validated with its provider before it is stored, and again on demand. A permanent failure moves the connection to `needs_attention`. |
| **Ports and adapters** | Encryption, authorization, audit, leasing and deployment secrets are interfaces your application supplies. |
| **Restricted egress** | A provider's requests reach only the hosts its manifest names, over HTTPS, never a private address. |

## What it is not

- **Not a provider API client.** It does not wrap the GitHub, Cloudflare or
  Stripe APIs.
- **Not a cryptography library or a secret manager.** Encryption and secret
  retrieval are ports; use a key-management library such as
  [keyforge](https://github.com/plexusone/keyforge) and a secret manager such
  as [omnivault](https://github.com/plexusone/omnivault).
- **Not a workflow engine.** A connection can be used by workflows; it is not
  one.

## Where to start

- [Getting started](getting-started.md) connects a service end to end in a
  few minutes.
- The [user guide](guides/index.md) explains connections, providers, how to
  integrate with a host application, storage and security.
- The [design](specs/initiatives/INIT-CONNECTFORGE-001/PRD.md) documents
  hold the requirements, the technical design and the roadmap.
