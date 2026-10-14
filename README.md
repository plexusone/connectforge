# ConnectForge

[![Go CI][go-ci-svg]][go-ci-url]
[![Go Lint][go-lint-svg]][go-lint-url]
[![Go SAST][go-sast-svg]][go-sast-url]
[![Docs][docs-godoc-svg]][docs-godoc-url]
[![Docs][docs-mkdoc-svg]][docs-mkdoc-url]
[![Visualization][viz-svg]][viz-url]
[![License][license-svg]][license-url]

 [go-ci-svg]: https://github.com/plexusone/connectforge/actions/workflows/go-ci.yaml/badge.svg?branch=main
 [go-ci-url]: https://github.com/plexusone/connectforge/actions/workflows/go-ci.yaml
 [go-lint-svg]: https://github.com/plexusone/connectforge/actions/workflows/go-lint.yaml/badge.svg?branch=main
 [go-lint-url]: https://github.com/plexusone/connectforge/actions/workflows/go-lint.yaml
 [go-sast-svg]: https://github.com/plexusone/connectforge/actions/workflows/go-sast-codeql.yaml/badge.svg?branch=main
 [go-sast-url]: https://github.com/plexusone/connectforge/actions/workflows/go-sast-codeql.yaml
 [docs-godoc-svg]: https://pkg.go.dev/badge/github.com/plexusone/connectforge
 [docs-godoc-url]: https://pkg.go.dev/github.com/plexusone/connectforge
 [docs-mkdoc-svg]: https://img.shields.io/badge/Go-dev%20guide-blue.svg
 [docs-mkdoc-url]: https://plexusone.github.io/connectforge
 [viz-svg]: https://img.shields.io/badge/Go-visualizaton-blue.svg
 [viz-url]: https://mango-dune-07a8b7110.1.azurestaticapps.net/?repo=plexusone%2Fconnectforge
 [loc-svg]: https://tokei.rs/b1/github/plexusone/connectforge
 [repo-url]: https://github.com/plexusone/connectforge
 [license-svg]: https://img.shields.io/badge/license-MIT-blue.svg
 [license-url]: https://github.com/plexusone/connectforge/blob/main/LICENSE

Manage a tenant's connections to external services in Go: provider
definitions, connections, encrypted credentials, grants and bindings, and a
broker that hands out authenticated HTTP clients so callers never handle a
raw secret.

> **Status:** in development. Static-token connections work end to end:
> the model, the in-memory and SQL stores, the egress policy, the broker and
> the service. GitHub App and OAuth follow. See the
> [specifications](docs/specs/initiatives/INIT-CONNECTFORGE-001/PRD.md).

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

- [PRD](docs/specs/initiatives/INIT-CONNECTFORGE-001/PRD.md)
- [TRD](docs/specs/initiatives/INIT-CONNECTFORGE-001/TRD.md)
- [Plan](docs/specs/initiatives/INIT-CONNECTFORGE-001/PLAN.md)
- [Roadmap](docs/specs/initiatives/INIT-CONNECTFORGE-001/ROADMAP.md)

## License

MIT
