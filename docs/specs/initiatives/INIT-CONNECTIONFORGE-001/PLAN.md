# connectionforge: Plan

**Initiative:** `INIT-CONNECTIONFORGE-001`
**Repository:** `github.com/plexusone/connectionforge`

Implements the [PRD](PRD.md) and [TRD](TRD.md). The [roadmap](ROADMAP.md)
holds the work items and their status.

## Approach

- **Model and ports first.** The data model, the lifecycle and the port
  interfaces are the parts that stored data and hosts depend on, so they are
  specified and given in-memory implementations and conformance suites before
  any provider or HTTP surface exists.
- **Secrets are a type, not a convention.** The redacting secret type and the
  rule that the broker never returns one are built before the first provider.
- **Two reference providers that disagree.** A static API-token provider and
  GitHub App exercise very different credential models and expose abstraction
  problems early. OAuth refresh follows once those are stable.
- **Library before service.** The broker, validation and refresh are complete
  and tested with in-memory ports before handlers or a CLI exist.
- **Public-ready from the first commit.** No host-specific facts, no
  customer references and no secrets in fixtures.

## Phases

| Phase | Theme | Outcome |
|---|---|---|
| 1 | Model, ports and secrets | The types, lifecycle, ports, redacting secret type and in-memory stores with conformance suites |
| 2 | Static credentials and the broker | An API-token provider validated end to end, with the authenticated-client broker and audit |
| 3 | GitHub App and OAuth | Installation tokens, authorization code with PKCE, single-flight refresh |
| 4 | Operations | Scheduled validation, health, rotation and replacement, bindings, grants and egress policy |
| 5 | Hardening and v0.1.0 | HTTP API, CLI, documentation, threat model, review and release |

## Dependencies and sequencing

- Phase 2's broker depends on the `Cryptographer` and `CredentialStore` ports
  fixed in phase 1.
- Phase 3's refresh depends on the store's compare-and-swap credential update
  and the lease port from phase 1.
- Phase 4's egress policy is applied by the broker, so it follows phase 2.
- The release waits for a security review of the broker and refresh paths.

## Integration with a host

connectionforge owns its domain, including its schema. A host application (for
example a platform that already owns tenancy, authorization and storage) opens
`sqlstore` on its own database, runs its migration, applies its tenancy policy
to the tables it reports, supplies the remaining ports and mounts the optional
HTTP package. The host's key-management library implements `Cryptographer` and
its authorization implements `Authorizer`; those adapters live in the host.
