# ConnectionForge

Manage a tenant's connections to external services in Go: provider
definitions, connections, encrypted credentials, grants and bindings, and a
broker that hands out authenticated HTTP clients so callers never handle a
raw secret.

> **Status:** in development: the model, ports and in-memory stores are
> implemented; providers, the broker and the SQL store follow. See the
> [specifications](docs/specs/initiatives/INIT-CONNECTIONFORGE-001/PRD.md).

## What it does

- **One model for every connection:** provider, connection, credential, grant,
  binding, with a lifecycle that separates what was decided from what was
  observed.
- **Owns the credential lifecycle, not the API clients.** Provider SDKs stay in
  their own modules and receive an authenticated `http.Client`.
- **Safe refresh and rotation:** single-flight refresh that persists a rotated
  refresh token atomically.
- **Tested connections:** validation with account discovery on create, on
  demand and on a schedule.
- **Ports and adapters:** storage, encryption, authorization, audit and egress
  policy are interfaces the host supplies.
- **Declarative provider manifests** so a UI or an agent can render and repair
  a connection.

## What it is not

- Not a provider API client library.
- Not a cryptography library or a secret manager; encryption and secret
  retrieval are ports.
- Not a workflow engine.

## Design

- [PRD](docs/specs/initiatives/INIT-CONNECTIONFORGE-001/PRD.md)
- [TRD](docs/specs/initiatives/INIT-CONNECTIONFORGE-001/TRD.md)
- [Plan](docs/specs/initiatives/INIT-CONNECTIONFORGE-001/PLAN.md)
- [Roadmap](docs/specs/initiatives/INIT-CONNECTIONFORGE-001/ROADMAP.md)

## License

MIT
