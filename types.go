package connectionforge

import (
	"maps"
	"slices"
	"strconv"
	"time"
)

// OwnerID is the tenant scope that owns a record. It is opaque to the library;
// a host typically uses an organization ID. Every store method is scoped by it.
type OwnerID string

// PrincipalKind distinguishes who or what acts.
type PrincipalKind string

// Principal kinds.
const (
	PrincipalUser    PrincipalKind = "user"
	PrincipalService PrincipalKind = "service"
	PrincipalAgent   PrincipalKind = "agent"
)

// Principal is who or what performs an action, as the host identifies it.
type Principal struct {
	Kind PrincipalKind `json:"kind"`
	ID   string        `json:"id"`
}

// Action is something a principal may do with a connection.
type Action string

// Actions on a connection.
const (
	ActionView       Action = "view"
	ActionUse        Action = "use"
	ActionManage     Action = "manage"
	ActionShare      Action = "share"
	ActionDisconnect Action = "disconnect"
)

// Consumer is something that depends on a connection: an application, a
// workflow or an agent.
type Consumer struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Use describes why a connection is being used, for authorization and for use
// counters.
type Use struct {
	Consumer Consumer `json:"consumer"`
	// Purpose is the feature using the connection, for example "code-health".
	Purpose string `json:"purpose"`
	// OnBehalfOf is the principal an agent or service acts for, if any.
	OnBehalfOf *Principal `json:"onBehalfOf,omitempty"`
}

// Outcome is the result of the last validation.
type Outcome string

// Validation outcomes.
const (
	OutcomeUnknown Outcome = "unknown"
	OutcomeOK      Outcome = "ok"
	OutcomeFailed  Outcome = "failed"
)

// Health is what was last observed about a connection, kept apart from its
// lifecycle [State].
type Health struct {
	LastValidatedAt  time.Time `json:"lastValidatedAt,omitzero"`
	LastValidation   Outcome   `json:"lastValidation,omitempty"`
	LastError        string    `json:"lastError,omitempty"`
	ConsecutiveFails int       `json:"consecutiveFails,omitempty"`
}

// Connection is an owner's relationship with one account at a provider. It
// holds no secret.
type Connection struct {
	ID                string            `json:"id"`
	Owner             OwnerID           `json:"owner"`
	Provider          string            `json:"provider"`
	AuthMethod        string            `json:"authMethod"`
	DisplayName       string            `json:"displayName"`
	ExternalAccountID string            `json:"externalAccountId,omitempty"`
	ExternalLabel     string            `json:"externalLabel,omitempty"`
	Scopes            []string          `json:"scopes,omitempty"`
	State             State             `json:"state"`
	Health            Health            `json:"health"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	CreatedBy         Principal         `json:"createdBy"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
	// Revision increases on every update and drives compare-and-swap.
	Revision uint64 `json:"revision"`
}

// Clone returns a deep copy of c.
func (c Connection) Clone() Connection {
	c.Scopes = slices.Clone(c.Scopes)
	c.Metadata = maps.Clone(c.Metadata)
	return c
}

// Credential is the encrypted authorization material for a connection, one
// version of it. Plaintext never appears here.
type Credential struct {
	Owner        OwnerID `json:"owner"`
	ConnectionID string  `json:"connectionId"`
	// Version starts at 1 and increases by one with each replacement or
	// refresh.
	Version int `json:"version"`
	// Kind names the plaintext's structure, which its provider defines, for
	// example "api_token" or "oauth_tokens".
	Kind string `json:"kind"`
	// Ciphertext is the sealed material, from the [Cryptographer].
	Ciphertext []byte `json:"-"`
	// ExpiresAt is when the access token expires, if known.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// Clone returns a deep copy of c.
func (c Credential) Clone() Credential {
	c.Ciphertext = slices.Clone(c.Ciphertext)
	if c.ExpiresAt != nil {
		t := *c.ExpiresAt
		c.ExpiresAt = &t
	}
	return c
}

// CredentialPurpose is the purpose under which credentials are sealed.
const CredentialPurpose = "connectionforge.credential"

// CredentialAAD returns the additional data a credential is sealed with. It
// binds the ciphertext to its owner, connection and version, so it cannot be
// moved to another record.
func CredentialAAD(owner OwnerID, connectionID string, version int) []byte {
	b := make([]byte, 0, len(owner)+len(connectionID)+24)
	b = append(b, "connectionforge/v1\x00"...)
	b = append(b, owner...)
	b = append(b, 0)
	b = append(b, connectionID...)
	b = append(b, 0)
	return strconv.AppendInt(b, int64(version), 10)
}

// Grant is authority: what a principal may do with a connection.
type Grant struct {
	Owner        OwnerID   `json:"owner"`
	ConnectionID string    `json:"connectionId"`
	Principal    Principal `json:"principal"`
	Actions      []Action  `json:"actions"`
}

// Clone returns a deep copy of g.
func (g Grant) Clone() Grant {
	g.Actions = slices.Clone(g.Actions)
	return g
}

// Binding is dependency: a consumer that uses a connection for a purpose.
type Binding struct {
	Owner        OwnerID   `json:"owner"`
	ConnectionID string    `json:"connectionId"`
	Consumer     Consumer  `json:"consumer"`
	Purpose      string    `json:"purpose"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Filter narrows a connection listing. Zero fields match everything.
type Filter struct {
	Provider string
	State    State
}

// Match reports whether c passes the filter.
func (f Filter) Match(c Connection) bool {
	return (f.Provider == "" || c.Provider == f.Provider) && (f.State == "" || c.State == f.State)
}
