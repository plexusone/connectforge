package connectforge

import "errors"

// Store errors.
var (
	// ErrNotFound is returned when a record does not exist for the owner. A
	// record that belongs to another owner is also not found.
	ErrNotFound = errors.New("connectforge: not found")
	// ErrExists is returned when creating a record that already exists.
	ErrExists = errors.New("connectforge: already exists")
	// ErrConflict is returned when a compare-and-swap finds a different version
	// than expected: another writer got there first.
	ErrConflict = errors.New("connectforge: version conflict")
	// ErrInvalidTransition is returned for a lifecycle change the transition
	// table does not allow.
	ErrInvalidTransition = errors.New("connectforge: invalid state transition")
	// ErrAccountMismatch is returned when a new credential belongs to a
	// different account than the connection was made for. Connecting another
	// account is a new connection, so the features bound to this one never
	// silently read someone else's data.
	ErrAccountMismatch = errors.New("connectforge: credential is for a different account")
	// ErrLeaseHeld is returned when a lease is held by someone else.
	ErrLeaseHeld = errors.New("connectforge: lease held")
)

// Use errors.
var (
	// ErrDenied is returned when the host's authorizer refuses an action.
	ErrDenied = errors.New("connectforge: denied")
	// ErrNotUsable is returned when a connection's state does not allow use.
	ErrNotUsable = errors.New("connectforge: connection not usable")
	// ErrCredentialUnavailable is returned when a credential cannot be read or
	// decrypted. The connection is not used: there is no fallback.
	ErrCredentialUnavailable = errors.New("connectforge: credential unavailable")
)

// Provider errors, as classified by a provider adapter. An adapter wraps one
// of these; it never puts a request or response body in an error.
var (
	// ErrInvalidCredential means the provider rejected the credential: it is
	// wrong, expired or revoked. It is a permanent failure.
	ErrInvalidCredential = errors.New("connectforge: invalid credential")
	// ErrInsufficientScope means the credential is valid but lacks a
	// permission the connection needs. It is a permanent failure.
	ErrInsufficientScope = errors.New("connectforge: insufficient scope")
	// ErrRateLimited means the provider is limiting requests. It is transient.
	ErrRateLimited = errors.New("connectforge: rate limited")
	// ErrTransient means a network failure or provider error that may succeed
	// on retry.
	ErrTransient = errors.New("connectforge: transient failure")
)

// Permanent reports whether err is a provider failure that retrying will not
// fix, so the connection needs attention.
func Permanent(err error) bool {
	return errors.Is(err, ErrInvalidCredential) || errors.Is(err, ErrInsufficientScope)
}
