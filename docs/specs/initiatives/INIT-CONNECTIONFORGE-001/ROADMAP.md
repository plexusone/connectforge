# ROADMAP — connectionforge: External Connection Management

**Initiative:** `INIT-CONNECTIONFORGE-001`
**Repository:** `github.com/plexusone/connectionforge`

Specs: [PRD](PRD.md), [TRD](TRD.md), [PLAN](PLAN.md). Work items carry
stable IDs (`RMI-CONNECTIONFORGE-NNN`), are reviewed and executed by phase, and
commits implementing one carry the trailer `Refs: RMI-CONNECTIONFORGE-<NNN>`.

Later, not scheduled: the UI kit built on provider manifests, rate-limit
tracking, bring-your-own-vault through a `SecretSource`, DPoP and mutual TLS,
agent-delegated authorization, and a remote service mode.

## Phase 1 — Model, ports and secrets

**Theme:** The types, lifecycle and ports are fixed, secrets cannot leak by construction, and in-memory stores pass a conformance suite.

- [ ] `RMI-CONNECTIONFORGE-001` Core types: `Connection`, `Credential`, `Grant`, `Binding`, `Principal`, `Owner`, errors with classification
- [ ] `RMI-CONNECTIONFORGE-002` `Secret` type with redaction in string, JSON, text and `slog`, and the `Reveal` lint rule
  - Depends on: `RMI-CONNECTIONFORGE-001`
- [ ] `RMI-CONNECTIONFORGE-003` Lifecycle state machine and transition table, with state separate from observed health
  - Depends on: `RMI-CONNECTIONFORGE-001`
- [ ] `RMI-CONNECTIONFORGE-004` Port interfaces: stores, `Cryptographer`, `Authorizer`, `Auditor`, `Leaser`, and the audit event types
  - Depends on: `RMI-CONNECTIONFORGE-001`
- [ ] `RMI-CONNECTIONFORGE-005` In-memory stores, test cryptographer and leaser, and the store and cryptographer conformance suites
  - Depends on: `RMI-CONNECTIONFORGE-003`, `RMI-CONNECTIONFORGE-004`

## Phase 2 — Static credentials and the broker

**Theme:** An API-token connection is created, validated, discovered and used through a broker that never returns the secret.

- [ ] `RMI-CONNECTIONFORGE-006` `Provider` interface, optional capability interfaces, `Manifest` and provider registry
  - Depends on: `RMI-CONNECTIONFORGE-004`
- [ ] `RMI-CONNECTIONFORGE-007` Manifest-driven static-token provider: validation endpoint, status classification, account discovery by path
  - Depends on: `RMI-CONNECTIONFORGE-006`
- [ ] `RMI-CONNECTIONFORGE-008` `egress` package: manifest host allow-list, redirect restriction, private-address refusal, HTTP client factory
  - Depends on: `RMI-CONNECTIONFORGE-006`
- [ ] `RMI-CONNECTIONFORGE-009` Broker: authenticated `http.Client`, per-call authorization and state checks, use counters
  - Depends on: `RMI-CONNECTIONFORGE-005`, `RMI-CONNECTIONFORGE-007`, `RMI-CONNECTIONFORGE-008`
- [ ] `RMI-CONNECTIONFORGE-010` Service operations: create, test, rename, list, disconnect, with audit and leak tests
  - Depends on: `RMI-CONNECTIONFORGE-009`
- [ ] `RMI-CONNECTIONFORGE-026` Ent-backed `sqlstore` for SQLite and PostgreSQL: prefixed tables, `owner_id` on every table, per-transaction host hook, conditional swaps, lease rows, `Tables()`; passes the conformance suite on both
  - Depends on: `RMI-CONNECTIONFORGE-005`

## Phase 3 — GitHub App and OAuth

**Theme:** Two different credential models work end to end, and refresh is safe under concurrency.

- [ ] `RMI-CONNECTIONFORGE-011` GitHub App provider: installation tokens from the host-held app key, token cache with expiry margin, permission and repository discovery
  - Depends on: `RMI-CONNECTIONFORGE-009`
- [ ] `RMI-CONNECTIONFORGE-012` OAuth authorization code with PKCE: server-side single-use state, allow-listed return destinations, callback handling
  - Depends on: `RMI-CONNECTIONFORGE-010`
- [ ] `RMI-CONNECTIONFORGE-013` Single-flight refresh: lease, compare-and-swap, atomic persistence of a rotated refresh token, failure classification
  - Depends on: `RMI-CONNECTIONFORGE-012`, `RMI-CONNECTIONFORGE-026`
- [ ] `RMI-CONNECTIONFORGE-014` Concurrency and race tests for refresh and installation-token minting
  - Depends on: `RMI-CONNECTIONFORGE-011`, `RMI-CONNECTIONFORGE-013`
- [ ] `RMI-CONNECTIONFORGE-015` Reconnect and revoke flows, including provider-side revocation where supported
  - Depends on: `RMI-CONNECTIONFORGE-013`

## Phase 4 — Operations

**Theme:** Connections are monitored, replaced and shared safely, and a disconnect says what it would break.

- [ ] `RMI-CONNECTIONFORGE-016` Scheduled validation and health: stoppable scheduler, backoff, needs-attention transitions, overdue reporting
  - Depends on: `RMI-CONNECTIONFORGE-010`
- [ ] `RMI-CONNECTIONFORGE-017` Credential replacement and rotation with two coexisting versions and validate-before-activate
  - Depends on: `RMI-CONNECTIONFORGE-013`
- [ ] `RMI-CONNECTIONFORGE-018` Grants and bindings: enforcement through the `Authorizer`, binding-aware disconnect
  - Depends on: `RMI-CONNECTIONFORGE-010`
- [ ] `RMI-CONNECTIONFORGE-019` Status model and metrics interface: states, health, expiring credentials, failing connections
  - Depends on: `RMI-CONNECTIONFORGE-016`
- [ ] `RMI-CONNECTIONFORGE-020` Provider conformance suite and recorded-fixture harness for adapters
  - Depends on: `RMI-CONNECTIONFORGE-007`, `RMI-CONNECTIONFORGE-011`

## Phase 5 — Hardening and v0.1.0

**Theme:** An HTTP API, a CLI, documentation, review and the release.

- [ ] `RMI-CONNECTIONFORGE-021` `httpapi` handlers over the service, with no secret in any response and the host's authorizer applied
  - Depends on: `RMI-CONNECTIONFORGE-018`
- [ ] `RMI-CONNECTIONFORGE-022` CLI (`providers`, `connections`, `test`, `disconnect`) over the service
  - Depends on: `RMI-CONNECTIONFORGE-019`
- [ ] `RMI-CONNECTIONFORGE-023` Documentation: getting started, host integration guide (ports and a reference schema), provider authoring, threat model
  - Depends on: `RMI-CONNECTIONFORGE-021`
- [ ] `RMI-CONNECTIONFORGE-024` Internal review of broker, refresh and egress; fix findings; record known limitations
  - Depends on: `RMI-CONNECTIONFORGE-014`, `RMI-CONNECTIONFORGE-020`
- [ ] `RMI-CONNECTIONFORGE-025` v0.1.0 release: changelog, release notes, signed tag after CI on all platforms
  - Depends on: `RMI-CONNECTIONFORGE-022`, `RMI-CONNECTIONFORGE-023`, `RMI-CONNECTIONFORGE-024`
