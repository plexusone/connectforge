package provider

import (
	"fmt"
	"net/http"

	cf "github.com/plexusone/connectionforge"
)

// requestIDHeaders are the response headers providers use for a request ID.
var requestIDHeaders = []string{"X-Request-Id", "Cf-Ray", "X-Github-Request-Id", "Request-Id"}

// StatusError is a provider's refusal, classified. It carries the status and
// the provider's request ID, never a body.
type StatusError struct {
	Provider  string
	Status    int
	RequestID string
	Class     error
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("%s: %s: HTTP %d", e.Provider, e.Class, e.Status)
	if e.RequestID != "" {
		msg += " (request " + e.RequestID + ")"
	}
	return msg
}

// Unwrap returns the class, so errors.Is works with connectionforge's errors.
func (e *StatusError) Unwrap() error { return e.Class }

// Classify returns nil for a 2xx response and a [*StatusError] otherwise: 401
// is an invalid credential, 403 an insufficient scope, 429 rate limiting,
// 5xx transient, and any other status an invalid credential.
func Classify(providerKey string, resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	e := &StatusError{Provider: providerKey, Status: resp.StatusCode}
	for _, h := range requestIDHeaders {
		if v := resp.Header.Get(h); v != "" {
			e.RequestID = v
			break
		}
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		e.Class = cf.ErrInvalidCredential
	case resp.StatusCode == http.StatusForbidden:
		e.Class = cf.ErrInsufficientScope
	case resp.StatusCode == http.StatusTooManyRequests:
		e.Class = cf.ErrRateLimited
	case resp.StatusCode >= 500:
		e.Class = cf.ErrTransient
	default:
		e.Class = cf.ErrInvalidCredential
	}
	return e
}
