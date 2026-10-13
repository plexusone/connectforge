// Package fakeapi is a token-authenticated HTTPS API for tests: a stand-in for
// a provider such as a CDN or analytics service.
package fakeapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/plexusone/connectionforge/provider"
	"github.com/plexusone/connectionforge/provider/apikey"
)

// Tokens with fixed behaviour. Any other token is rejected with 401.
const (
	Good      = "fake-good-token"
	NoScope   = "fake-noscope-token"
	Limited   = "fake-limited-token"
	Broken    = "fake-broken-token"
	NotJSON   = "fake-notjson-token"
	AccountID = "acct-1"
	Label     = "Acme Production"
)

// API is a running fake API.
type API struct {
	*httptest.Server
	mu      sync.Mutex
	revoked map[string]bool
	seen    []string
}

// Start starts the API and stops it when the test ends.
func Start(t testing.TB) *API {
	t.Helper()
	a := &API{revoked: map[string]bool{}}
	a.Server = httptest.NewTLSServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.Close)
	return a
}

// Revoke makes a token invalid from now on; Restore undoes it.
func (a *API) Revoke(tok string) { a.mu.Lock(); a.revoked[tok] = true; a.mu.Unlock() }

// Restore makes a revoked token valid again.
func (a *API) Restore(tok string) { a.mu.Lock(); delete(a.revoked, tok); a.mu.Unlock() }

// Seen returns the Authorization headers received on /data.
func (a *API) Seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seen...)
}

func (a *API) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Request-Id", "req-123")
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	a.mu.Lock()
	revoked := a.revoked[tok]
	if r.URL.Path == "/data" {
		a.seen = append(a.seen, r.Header.Get("Authorization"))
	}
	a.mu.Unlock()
	switch r.URL.Path {
	case "/redirect-away":
		http.Redirect(w, r, "https://elsewhere.example/data", http.StatusFound)
		return
	case "/redirect-same":
		http.Redirect(w, r, "/data", http.StatusFound)
		return
	}
	switch {
	case revoked:
		// A provider might echo the token in an error body; callers must not.
		http.Error(w, "token "+tok+" revoked", http.StatusUnauthorized)
	case tok == NoScope:
		http.Error(w, "forbidden", http.StatusForbidden)
	case tok == Limited:
		http.Error(w, "slow down", http.StatusTooManyRequests)
	case tok == Broken:
		http.Error(w, "oops", http.StatusServiceUnavailable)
	case tok == NotJSON:
		_, _ = w.Write([]byte("hello")) //nolint:errcheck // test server: a failed write fails the client side
	case tok != Good:
		http.Error(w, "invalid token "+tok, http.StatusUnauthorized)
	case r.URL.Path == "/verify":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"result":{"id":"tok-1","status":"active","scopes":["zone:read","analytics:read"]}}`) //nolint:errcheck // as above
	case r.URL.Path == "/accounts":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"result":[{"id":%q,"name":%q}]}`, AccountID, Label) //nolint:errcheck // as above
	case r.URL.Path == "/data":
		_, _ = w.Write([]byte("ok")) //nolint:errcheck // as above
	default:
		http.NotFound(w, r)
	}
}

// Manifest returns a manifest whose base URL is the API.
func (a *API) Manifest() provider.Manifest {
	return provider.Manifest{
		Key: "fakecdn", DisplayName: "Fake CDN", BaseURLs: []string{a.URL},
		AuthMethods: []provider.AuthMethod{{
			Key:              "api_token",
			Fields:           []provider.Field{{Name: "token", Label: "API token", Secret: true, Required: true}},
			Scopes:           []provider.Scope{{Name: "zone:read", Required: true}},
			AccountSelection: "discovered",
		}},
	}
}

// Provider returns an apikey provider for the API.
func (a *API) Provider(t testing.TB) *apikey.Provider {
	t.Helper()
	p, err := apikey.New(apikey.Config{
		Manifest: a.Manifest(),
		Verify:   apikey.Request{Path: "/verify", ScopesPath: "result.scopes"},
		Account:  &apikey.Request{Path: "/accounts", AccountIDPath: "result.0.id", AccountLabelPath: "result.0.name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
