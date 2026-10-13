package connectionforge

import (
	"context"
	"time"
)

// ConnectionStore holds connections. Every method is scoped by owner: a record
// of another owner is [ErrNotFound]. Returned records are copies.
type ConnectionStore interface {
	// CreateConnection stores a new connection with revision 1. It returns
	// [ErrExists] if the ID is taken.
	CreateConnection(ctx context.Context, c Connection) (Connection, error)
	// GetConnection returns one connection.
	GetConnection(ctx context.Context, owner OwnerID, id string) (Connection, error)
	// ListConnections returns the owner's connections that match f, ordered
	// by ID.
	ListConnections(ctx context.Context, owner OwnerID, f Filter) ([]Connection, error)
	// UpdateConnection replaces a connection if its stored revision equals
	// c.Revision, and returns it with the next revision. Otherwise it returns
	// [ErrConflict]. A state change the transition table forbids returns
	// [ErrInvalidTransition].
	UpdateConnection(ctx context.Context, c Connection) (Connection, error)
	// DeleteConnection removes a connection with its credentials, grants and
	// bindings.
	DeleteConnection(ctx context.Context, owner OwnerID, id string) error
}

// CredentialStore holds sealed credential versions.
type CredentialStore interface {
	// SwapCredential installs next as the active version if the active
	// version is expectActive (0 for none) and next.Version is
	// expectActive+1. Otherwise it returns [ErrConflict]. The previous
	// version is kept until destroyed.
	SwapCredential(ctx context.Context, expectActive int, next Credential) error
	// ActiveCredential returns the active version.
	ActiveCredential(ctx context.Context, owner OwnerID, connectionID string) (Credential, error)
	// DestroyCredentials removes every version for a connection.
	DestroyCredentials(ctx context.Context, owner OwnerID, connectionID string) error
}

// GrantStore holds grants.
type GrantStore interface {
	// PutGrant creates or replaces the grant for a principal on a connection.
	PutGrant(ctx context.Context, g Grant) error
	// RemoveGrant removes a principal's grant. Removing none is not an error.
	RemoveGrant(ctx context.Context, owner OwnerID, connectionID string, p Principal) error
	// ListGrants returns a connection's grants.
	ListGrants(ctx context.Context, owner OwnerID, connectionID string) ([]Grant, error)
}

// BindingStore holds bindings.
type BindingStore interface {
	// PutBinding records that a consumer uses a connection for a purpose. It
	// is idempotent.
	PutBinding(ctx context.Context, b Binding) error
	// RemoveBinding removes a binding. Removing none is not an error.
	RemoveBinding(ctx context.Context, owner OwnerID, connectionID string, c Consumer, purpose string) error
	// ListBindings returns a connection's bindings.
	ListBindings(ctx context.Context, owner OwnerID, connectionID string) ([]Binding, error)
}

// Store is every store the library needs. A host may implement it with one
// type, as the packages memstore and sqlstore do.
type Store interface {
	ConnectionStore
	CredentialStore
	GrantStore
	BindingStore
}

// Cryptographer seals and opens credential material. A key-management
// library implements it; the host supplies the adapter. Open with a different
// purpose or aad must fail.
type Cryptographer interface {
	Seal(ctx context.Context, purpose string, plaintext, aad []byte) ([]byte, error)
	Open(ctx context.Context, purpose string, ciphertext, aad []byte) ([]byte, error)
}

// Authorizer decides whether a principal may act on a connection. A refusal is
// an error wrapping [ErrDenied].
type Authorizer interface {
	Allowed(ctx context.Context, p Principal, a Action, c Connection) error
}

// Leaser grants short exclusive leases, used to make refresh single-flight
// across processes.
type Leaser interface {
	// Acquire takes the lease named key for ttl, or returns [ErrLeaseHeld].
	Acquire(ctx context.Context, key string, ttl time.Duration) (Lease, error)
}

// Lease is a held lease.
type Lease interface {
	// Release gives the lease up before it expires.
	Release(ctx context.Context) error
}

// SecretSource reads secrets that belong to the deployment rather than to an
// owner, such as an OAuth client secret or a GitHub App private key. A secret
// manager implements it; the host supplies the adapter.
type SecretSource interface {
	Secret(ctx context.Context, ref string) (Secret, error)
}

// Auditor records events. An event never carries a secret or a response body.
type Auditor interface {
	Record(ctx context.Context, e Event) error
}

// EventType names what happened.
type EventType string

// Event types.
const (
	EventConnectionCreated      EventType = "connection.created"
	EventConnectionValidated    EventType = "connection.validated"
	EventConnectionStateChanged EventType = "connection.state_changed"
	EventConnectionUpdated      EventType = "connection.updated"
	EventConnectionDisconnected EventType = "connection.disconnected"
	EventConnectionDeleted      EventType = "connection.deleted"
	EventCredentialReplaced     EventType = "credential.replaced"  //nolint:gosec // G101: Enum/constant identifier matches the credential-name heuristic, but the value is a public tag, not a secret
	EventCredentialRefreshed    EventType = "credential.refreshed" //nolint:gosec // G101: Enum/constant identifier matches the credential-name heuristic, but the value is a public tag, not a secret
	EventCredentialRevoked      EventType = "credential.revoked"   //nolint:gosec // G101: Enum/constant identifier matches the credential-name heuristic, but the value is a public tag, not a secret
	EventGrantChanged           EventType = "grant.changed"
	EventBindingChanged         EventType = "binding.changed"
	EventAccessDenied           EventType = "access.denied"
)

// Event is an audit record.
type Event struct {
	Time         time.Time         `json:"time"`
	Type         EventType         `json:"type"`
	Owner        OwnerID           `json:"owner"`
	Actor        Principal         `json:"actor"`
	OnBehalfOf   *Principal        `json:"onBehalfOf,omitempty"`
	ConnectionID string            `json:"connectionId,omitempty"`
	Provider     string            `json:"provider,omitempty"`
	Success      bool              `json:"success"`
	Reason       string            `json:"reason,omitempty"`
	Details      map[string]string `json:"details,omitempty"`
}

// Clock tells the time. Tests supply a fake one.
type Clock interface{ Now() time.Time }

// SystemClock is the real clock.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }
