# AGENTS.md — connectforge

Guidance for coding agents working in this repository. The organization-wide
guidelines in `plexusone/.github` also apply.

## What this is

connectforge manages a tenant's connections to external services: provider
definitions, connections, encrypted credentials, grants and bindings, and a
broker that hands out authenticated HTTP clients. The design is the source of
truth:

- `docs/specs/initiatives/INIT-CONNECTFORGE-001/PRD.md` — what and why
- `docs/specs/initiatives/INIT-CONNECTFORGE-001/TRD.md` — model, ports,
  lifecycle, threat model
- `docs/specs/initiatives/INIT-CONNECTFORGE-001/ROADMAP.md` — phases and
  work items (`RMI-CONNECTFORGE-NNN`)

Read the TRD before changing a port, the lifecycle or a stored shape. If the
code and the TRD disagree, resolve it in the TRD in the same change.

## Rules specific to this repository

- **Own the credential lifecycle, not the API clients.** Do not import a
  provider SDK. Authenticate an `http.Client` and let the caller's SDK use it.
- **Secrets are `Secret`.** A credential value is never a `string` or `[]byte`
  outside the broker and a provider adapter. `Reveal` is called only there.
- **Never log, return or audit a secret,** and never put a request or response
  body in an error.
- **No host imports.** Do not import SystemForge, KeyForge, OmniVault or any
  application framework. Encryption, authorization, audit and secret
  retrieval are ports; adapters live in the host.
- **The schema is ours; tenancy policy is the host's.** `sqlstore` owns the
  Ent schema, its generated code and migration. Ent and the database drivers
  are imported by `sqlstore` only. Every table carries `owner_id`, every query
  filters on it, and every transaction calls the host's hook first.
- **Never hand-edit `sqlstore/ent`** outside `sqlstore/ent/schema`; run
  `go run ./sqlstore/ent/generate.go` from the repository root.
- **Run the store tests on PostgreSQL** before changing `sqlstore`: set
  `CONNECTFORGE_TEST_POSTGRES_DSN`; each case runs in its own schema.
- **Tenant scope in every port.** A store method without an owner is a bug.
- **State is not health.** A transient failure changes health, never state.
- **Fail closed.** If decryption, authorization or validation cannot
  complete, the connection is not used.
- **Egress is policy.** A request leaves only for a host named in the
  provider's manifest. Do not add a user-supplied base URL.
- **No provider-specific exceptions in the core.** Provider behavior lives in
  a provider package behind the capability interfaces.
- **Fixtures contain no real tokens.** Use obviously fake values.
- **Operating-system differences** are not expected here; if they arise, use
  `github.com/grokify/oscompat` rather than build tags.
