# User guide

connectforge is a library. Your application, the **host**, supplies where
data is stored, how credentials are encrypted, who is allowed to do what, and
where audit events go. connectforge supplies the model, the lifecycle, the
provider contract, the broker and the operations.

```text
your application
  ├── implements   Cryptographer, Authorizer, Auditor, Leaser, SecretSource
  ├── registers    providers (a manifest and a verification each)
  └── calls        service (create, test, list, ...) and broker (HTTP clients)

connectforge
  ├── service   operations, authorized and audited
  ├── broker    the only path from a stored credential to a request
  └── stores    in memory, or SQLite and PostgreSQL through Ent
```

## Who reads what

| You are | Read |
|---|---|
| Building an application that lets people connect services | [Connections and their lifecycle](connections.md), then [Using a connection](using-connections.md) |
| Adding a service | [Writing a provider](providers.md) |
| Wiring connectforge into a platform | [Integrating with a host](host-integration.md) and [Storage](storage.md) |
| Reviewing the design | [Security](security.md) and the [design documents](../specs/initiatives/INIT-CONNECTFORGE-001/TRD.md) |

## Vocabulary

| Term | Meaning |
|---|---|
| **Owner** | The tenant a record belongs to, an opaque string such as an organization ID. Every store method is scoped by it |
| **Provider** | An adapter for one external service: a manifest, validation and request authentication |
| **Connection** | An owner's relationship with one account at a provider. It holds no secret |
| **Credential** | One sealed version of a connection's authorization material |
| **Principal** | Who or what acts: a user, a service or an agent |
| **Use** | Why a connection is being used: a consumer and a purpose |
| **Grant** | What a principal may do with a connection |
| **Binding** | A consumer that depends on a connection |
