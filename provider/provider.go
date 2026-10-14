// Package provider defines what a provider adapter implements: a declarative
// [Manifest], validation with account discovery, and authentication of
// outgoing requests. It also holds the [Registry] a host fills at startup.
package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
)

// Provider is an adapter for one external service.
type Provider interface {
	// Manifest describes the provider. It is data and never changes.
	Manifest() Manifest
	// Validate checks the credential with the provider, using hc, which the
	// caller has restricted to the manifest's hosts, and returns the account
	// it belongs to. A rejection wraps a connectforge provider error.
	Validate(ctx context.Context, hc *http.Client, m Material) (Result, error)
	// Authenticate adds the credential to an outgoing request. The request
	// is the caller's copy.
	Authenticate(r *http.Request, m Material) error
}

// Result is what validation discovered.
type Result struct {
	AccountID    string
	AccountLabel string
	// Scopes are the permissions the provider reports for the credential,
	// if it reports them.
	Scopes []string
}

// Manifest describes a provider so a user interface can render a connection
// form and an agent can read what a connection needs. It contains no code.
type Manifest struct {
	// Key identifies the provider, for example "cloudflare".
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	// BaseURLs are the only origins a request for this provider may reach.
	BaseURLs    []string     `json:"baseUrls"`
	AuthMethods []AuthMethod `json:"authMethods"`
	HelpURL     string       `json:"helpUrl,omitempty"`
}

// AuthMethod is one way to connect to a provider.
type AuthMethod struct {
	// Key identifies the method, for example "api_token".
	Key    string  `json:"key"`
	Fields []Field `json:"fields"`
	Scopes []Scope `json:"scopes,omitempty"`
	// AccountSelection says how the account is chosen: "discovered" from the
	// credential, or "chosen" by the person from a list.
	AccountSelection string `json:"accountSelection,omitempty"`
	HelpURL          string `json:"helpUrl,omitempty"`
}

// Field is an input a method needs.
type Field struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
	Help     string `json:"help,omitempty"`
}

// Scope is a permission a method asks for or expects.
type Scope struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Method returns the auth method with key.
func (m Manifest) Method(key string) (AuthMethod, bool) {
	i := slices.IndexFunc(m.AuthMethods, func(a AuthMethod) bool { return a.Key == key })
	if i < 0 {
		return AuthMethod{}, false
	}
	return m.AuthMethods[i], true
}

// Hosts returns the host (with port, if any) of each base URL.
func (m Manifest) Hosts() ([]string, error) {
	hosts := make([]string, 0, len(m.BaseURLs))
	for _, b := range m.BaseURLs {
		u, err := url.Parse(b)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("provider %s: bad base URL %q", m.Key, b)
		}
		hosts = append(hosts, u.Host)
	}
	return hosts, nil
}

// Validate checks that the manifest is complete.
func (m Manifest) Validate() error {
	switch {
	case m.Key == "":
		return errors.New("provider: manifest has no key")
	case len(m.BaseURLs) == 0:
		return fmt.Errorf("provider %s: manifest has no base URLs", m.Key)
	case len(m.AuthMethods) == 0:
		return fmt.Errorf("provider %s: manifest has no auth methods", m.Key)
	}
	if _, err := m.Hosts(); err != nil {
		return err
	}
	for _, a := range m.AuthMethods {
		if a.Key == "" || len(a.Fields) == 0 {
			return fmt.Errorf("provider %s: auth method %q needs a key and fields", m.Key, a.Key)
		}
	}
	return nil
}

// ErrUnknownProvider is returned for a provider key that is not registered.
var ErrUnknownProvider = errors.New("provider: unknown provider")

// Registry holds the providers a host supports. It is safe for concurrent
// use.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{providers: map[string]Provider{}} }

// Register adds a provider. Its manifest must be valid and its key unused.
func (r *Registry) Register(p Provider) error {
	m := p.Manifest()
	if err := m.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[m.Key]; ok {
		return fmt.Errorf("provider: %s is already registered", m.Key)
	}
	r.providers[m.Key] = p
	return nil
}

// Get returns the provider with key.
func (r *Registry) Get(key string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[key]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, key)
	}
	return p, nil
}

// Manifests returns every registered manifest, ordered by key.
func (r *Registry) Manifests() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Manifest, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p.Manifest())
	}
	slices.SortFunc(out, func(a, b Manifest) int {
		switch {
		case a.Key < b.Key:
			return -1
		case a.Key > b.Key:
			return 1
		}
		return 0
	})
	return out
}
