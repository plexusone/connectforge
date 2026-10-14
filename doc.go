// Package connectforge manages a tenant's connections to external services:
// the provider, the connection, its encrypted credential, who may use it and
// what depends on it.
//
// It owns the credential lifecycle, not the provider API clients. A caller asks
// the broker for an authenticated [net/http.Client] and never handles a raw
// secret. Storage, encryption, authorization, audit and egress policy are
// ports that the host application supplies.
//
// The design is in docs/specs/initiatives/INIT-CONNECTFORGE-001.
package connectforge
