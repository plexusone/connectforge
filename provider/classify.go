package provider

import (
	"fmt"
	"net/http"

	cf "github.com/plexusone/connectforge"
)

// requestIDHeaders are the response headers providers use for a request ID.
var requestIDHeaders = []string{"X-Request-Id", "Cf-Ray", "X-Github-Request-Id", "Request-Id"}

// maxRequestID bounds the request ID kept from a response header.
const maxRequestID = 128

// StatusError is a provider's refusal, classified. It carries the status and
// the provider's request ID, never a body. The request ID is at most 128
// printable ASCII characters; anything else in the header is dropped.
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

// Unwrap returns the class, so errors.Is works with connectforge's errors.
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
		if v := requestID(resp.Header.Get(h)); v != "" {
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

// requestID returns v's printable ASCII characters, at most maxRequestID of
// them: a header is the provider's data and ends up in stored health.
func requestID(v string) string {
	b := make([]byte, 0, min(len(v), maxRequestID))
	for i := 0; i < len(v) && len(b) < maxRequestID; i++ {
		if c := v[i]; c >= 0x21 && c <= 0x7e {
			b = append(b, c)
		}
	}
	return string(b)
}
