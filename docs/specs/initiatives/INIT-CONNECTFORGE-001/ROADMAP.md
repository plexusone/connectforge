# ROADMAP — connectforge: External Connection Management

**Initiative:** `INIT-CONNECTFORGE-001`
**Repository:** `github.com/plexusone/connectforge`

Specs: [PRD](PRD.md), [TRD](TRD.md), [PLAN](PLAN.md). Work items carry
stable IDs (`RMI-CONNECTFORGE-NNN`), are reviewed and executed by phase, and
commits implementing one carry the trailer `Refs: RMI-CONNECTFORGE-<NNN>`.

Later, not scheduled: the UI kit built on provider manifests, rate-limit
tracking, DPoP and mutual TLS,
agent-delegated authorization, and a remote service mode.

## Phase 1 — Model, ports and secrets

**Theme:** The types, lifecycle and ports are fixed, secrets cannot leak by construction, and in-memory stores pass a conformance suite.

- [x] `RMI-CONNECTFORGE-001` Core types: `Connection`, `Credential`, `Grant`, `Binding`, `Principal`, `Owner`, errors with classification
- [x] `RMI-CONNECTFORGE-002` `Secret` type with redaction in string, JSON, text and `slog`, and a test that confines `Reveal` to the broker and providers
  - Depends on: `RMI-CONNECTFORGE-001`
- [x] `RMI-CONNECTFORGE-003` Lifecycle state machine and transition table, with state separate from observed health
  - Depends on: `RMI-CONNECTFORGE-001`
- [x] `RMI-CONNECTFORGE-004` Port interfaces: stores, `Cryptographer`, `Authorizer`, `Auditor`, `Leaser`, and the audit event types
  - Depends on: `RMI-CONNECTFORGE-001`
- [x] `RMI-CONNECTFORGE-005` In-memory stores, test cryptographer and leaser, and the store and cryptographer conformance suites
  - Depends on: `RMI-CONNECTFORGE-003`, `RMI-CONNECTFORGE-004`
- [x] `RMI-CONNECTFORGE-027` `SecretSource` port for deployment secrets (OAuth client secrets, GitHub App keys), implemented by the host's secret manager
  - Depends on: `RMI-CONNECTFORGE-002`

## Phase 2 — Static credentials and the broker

**Theme:** An API-token connection is created, validated, discovered and used through a broker that never returns the secret.

- [x] `RMI-CONNECTFORGE-006` `Provider` interface, optional capability interfaces, `Manifest` and provider registry
  - Depends on: `RMI-CONNECTFORGE-004`
- [x] `RMI-CONNECTFORGE-007` Manifest-driven static-token provider: validation endpoint, status classification, account discovery by path
  - Depends on: `RMI-CONNECTFORGE-006`
- [x] `RMI-CONNECTFORGE-008` `egress` package: manifest host allow-list, redirect restriction, private-address refusal, HTTP client factory
  - Depends on: `RMI-CONNECTFORGE-006`
- [x] `RMI-CONNECTFORGE-009` Broker: authenticated `http.Client`, per-call authorization and state checks, use counters
  - Depends on: `RMI-CONNECTFORGE-005`, `RMI-CONNECTFORGE-007`, `RMI-CONNECTFORGE-008`
- [x] `RMI-CONNECTFORGE-010` Service operations: create, test, rename, list, disconnect, with audit and leak tests
  - Depends on: `RMI-CONNECTFORGE-009`
- [x] `RMI-CONNECTFORGE-026` Ent-backed `sqlstore` for SQLite and PostgreSQL: prefixed tables, `owner_id` on every table, per-transaction host hook, conditional swaps, lease rows, `Tables()`; passes the conformance suite on both
  - Depends on: `RMI-CONNECTFORGE-005`

## Phase 3 — GitHub App and OAuth

**Theme:** Two different credential models work end to end, and refresh is safe under concurrency.

- [ ] `RMI-CONNECTFORGE-011` GitHub App provider: installation tokens from the app key read through `SecretSource`, token cache with expiry margin, permission and repository discovery
  - Depends on: `RMI-CONNECTFORGE-009`, `RMI-CONNECTFORGE-027`
- [ ] `RMI-CONNECTFORGE-012` OAuth authorization code with PKCE: server-side single-use state, allow-listed return destinations, callback handling
  - Depends on: `RMI-CONNECTFORGE-010`
- [ ] `RMI-CONNECTFORGE-013` Single-flight refresh: lease, compare-and-swap, atomic persistence of a rotated refresh token, failure classification
  - Depends on: `RMI-CONNECTFORGE-012`, `RMI-CONNECTFORGE-026`
- [ ] `RMI-CONNECTFORGE-014` Concurrency and race tests for refresh and installation-token minting
  - Depends on: `RMI-CONNECTFORGE-011`, `RMI-CONNECTFORGE-013`
- [ ] `RMI-CONNECTFORGE-015` Reconnect and revoke flows, including provider-side revocation where supported
  - Depends on: `RMI-CONNECTFORGE-013`

## Phase 4 — Operations

**Theme:** Connections are monitored, replaced and shared safely, and a disconnect says what it would break.

- [ ] `RMI-CONNECTFORGE-016` Scheduled validation and health: stoppable scheduler, backoff, needs-attention transitions, overdue reporting
  - Depends on: `RMI-CONNECTFORGE-010`
- [x] `RMI-CONNECTFORGE-017` Credential replacement and rotation with two coexisting versions and validate-before-activate
  - Depends on: `RMI-CONNECTFORGE-013`
- [ ] `RMI-CONNECTFORGE-018` Grants and bindings: enforcement through the `Authorizer`, binding-aware disconnect. Bindings are listed, bound and unbound through the service; grants are held by the host's authorization (SystemForge uses its resource grants), so what remains is a disconnect that requires acknowledging its bindings
  - Depends on: `RMI-CONNECTFORGE-010`
- [ ] `RMI-CONNECTFORGE-019` Status model and metrics interface: states, health, expiring credentials, failing connections
  - Depends on: `RMI-CONNECTFORGE-016`
- [ ] `RMI-CONNECTFORGE-020` Provider conformance suite and recorded-fixture harness for adapters
  - Depends on: `RMI-CONNECTFORGE-007`, `RMI-CONNECTFORGE-011`

## Phase 5 — Hardening and v0.1.0

**Theme:** An HTTP API, a CLI, documentation, review and the release.

- [ ] `RMI-CONNECTFORGE-021` `httpapi` handlers over the service, with no secret in any response and the host's authorizer applied
  - Depends on: `RMI-CONNECTFORGE-018`
- [ ] `RMI-CONNECTFORGE-022` CLI (`providers`, `connections`, `test`, `disconnect`) over the service
  - Depends on: `RMI-CONNECTFORGE-019`
- [ ] `RMI-CONNECTFORGE-023` Documentation: getting started, host integration guide (ports and a reference schema), provider authoring, threat model
  - Depends on: `RMI-CONNECTFORGE-021`
- [ ] `RMI-CONNECTFORGE-024` Internal review of broker, refresh and egress; fix findings; record known limitations
  - Depends on: `RMI-CONNECTFORGE-014`, `RMI-CONNECTFORGE-020`
- [ ] `RMI-CONNECTFORGE-025` v0.1.0 release: changelog, release notes, signed tag after CI on all platforms
  - Depends on: `RMI-CONNECTFORGE-022`, `RMI-CONNECTFORGE-023`, `RMI-CONNECTFORGE-024`
