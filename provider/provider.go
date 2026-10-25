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
	"time"
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
	// ExpiresAt is when the credential stops working, if the provider
	// reports it; nil when it does not expire or the provider does not say.
	ExpiresAt *time.Time
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
	// AccountUnverifiable says the provider cannot tell which account a
	// credential belongs to. Without it, a credential whose validation does
	// not return an account ID is refused, so a reconnect can always prove
	// the replacement is for the same account. With it, a connection may
	// have no account ID and a reconnect replaces its credential without
	// that proof, which is recorded in the audit event.
	AccountUnverifiable bool   `json:"accountUnverifiable,omitempty"`
	HelpURL             string `json:"helpUrl,omitempty"`
	// Setup is what a person does, in order, to create the credential at
	// the service before entering it.
	Setup []SetupStep `json:"setup,omitempty"`
	// Rotation lists the ways the credential can be replaced over its life,
	// with what each needs. Empty means the method does not describe
	// rotation; a person replaces the credential by reconnecting.
	Rotation []RotationMode `json:"rotation,omitempty"`
}

// SetupStep is one step of creating a credential at the service.
type SetupStep struct {
	Title string `json:"title"`
	// Detail explains the step in plain text.
	Detail string `json:"detail,omitempty"`
	// URL opens the page at the service where the step is done.
	URL string `json:"url,omitempty"`
	// Rotation limits the step to these rotation mode keys; empty means
	// every mode.
	Rotation []string `json:"rotation,omitempty"`
}

// Rotation mode keys a host may act on. A provider may describe others; a
// host that does not know a mode does not offer it.
const (
	// RotationManual means a person replaces the credential, for example
	// before it expires.
	RotationManual = "manual"
	// RotationAutomatic means the host replaces the credential on a
	// schedule, which usually needs more permission at the service.
	RotationAutomatic = "automatic"
)

// RotationMode is one way a credential is replaced over its life.
type RotationMode struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	// Scopes are the permissions the credential needs in this mode in
	// addition to the method's.
	Scopes []Scope `json:"scopes,omitempty"`
	// Risks say what those extra permissions also allow, so a person can
	// weigh the mode before choosing it.
	Risks []string `json:"risks,omitempty"`
	// ExpiryDays is the lifetime to give the credential when creating it,
	// for services that support one; 0 means no expiry.
	ExpiryDays int `json:"expiryDays,omitempty"`
	// Recommended marks the mode to offer first. At most one mode is.
	Recommended bool `json:"recommended,omitempty"`
}

// RotationMode returns the method's rotation mode with key.
func (a AuthMethod) RotationMode(key string) (RotationMode, bool) {
	i := slices.IndexFunc(a.Rotation, func(r RotationMode) bool { return r.Key == key })
	if i < 0 {
		return RotationMode{}, false
	}
	return a.Rotation[i], true
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
		if err := a.validateGuidance(); err != nil {
			return fmt.Errorf("provider %s: auth method %q: %w", m.Key, a.Key, err)
		}
	}
	return nil
}

// validateGuidance checks the setup steps and rotation modes.
func (a AuthMethod) validateGuidance() error {
	seen := map[string]bool{}
	recommended := 0
	for _, r := range a.Rotation {
		switch {
		case r.Key == "" || r.Label == "":
			return errors.New("a rotation mode needs a key and a label")
		case seen[r.Key]:
			return fmt.Errorf("rotation mode %q is listed twice", r.Key)
		case r.ExpiryDays < 0:
			return fmt.Errorf("rotation mode %q has a negative expiry", r.Key)
		}
		seen[r.Key] = true
		if r.Recommended {
			recommended++
		}
	}
	if recommended > 1 {
		return errors.New("more than one rotation mode is recommended")
	}
	for _, st := range a.Setup {
		if st.Title == "" {
			return errors.New("a setup step needs a title")
		}
		if st.URL != "" {
			if u, err := url.Parse(st.URL); err != nil || u.Scheme != "https" || u.Host == "" {
				return fmt.Errorf("setup step %q: the link must be an https URL", st.Title)
			}
		}
		for _, k := range st.Rotation {
			if !seen[k] {
				return fmt.Errorf("setup step %q names rotation mode %q, which is not listed", st.Title, k)
			}
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
