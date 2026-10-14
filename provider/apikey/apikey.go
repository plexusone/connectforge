// Package apikey is a provider for services that issue a static API key or
// token, driven by configuration: where the token goes in a request, which
// endpoint verifies it, and where the account is in the response.
package apikey

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/provider"
)

// Config describes a static-token provider.
type Config struct {
	// Manifest is the provider's manifest. It needs an auth method with key
	// Method whose fields include Field.
	Manifest provider.Manifest
	// Method is the auth method's key. Default "api_token".
	Method string
	// Field is the credential field holding the token. Default "token".
	Field string
	// Header is the request header that carries the token. Default
	// "Authorization".
	Header string
	// Prefix precedes the token in the header. Default "Bearer " when Header
	// is Authorization, otherwise none.
	Prefix string
	// Verify is the request that validates the token. Required.
	Verify Request
	// Account, if set, is a second request that discovers the account, for
	// providers whose verification response does not name it.
	Account *Request
}

// Request is a GET to a path under the manifest's first base URL, and where
// to find the account in its JSON response. Paths are dot-separated keys and
// array indexes, for example "result.0.id".
type Request struct {
	Path             string
	AccountIDPath    string
	AccountLabelPath string
	// ScopesPath, if set, names an array of strings.
	ScopesPath string
}

// Provider is a static-token provider.
type Provider struct {
	cfg  Config
	base string
}

// New returns a provider for cfg.
func New(cfg Config) (*Provider, error) {
	if cfg.Method == "" {
		cfg.Method = "api_token"
	}
	if cfg.Field == "" {
		cfg.Field = "token"
	}
	if cfg.Header == "" {
		cfg.Header = "Authorization"
		if cfg.Prefix == "" {
			cfg.Prefix = "Bearer "
		}
	}
	if err := cfg.Manifest.Validate(); err != nil {
		return nil, err
	}
	m, ok := cfg.Manifest.Method(cfg.Method)
	if !ok {
		return nil, fmt.Errorf("apikey %s: no auth method %q", cfg.Manifest.Key, cfg.Method)
	}
	hasField := false
	for _, f := range m.Fields {
		hasField = hasField || f.Name == cfg.Field
	}
	if !hasField {
		return nil, fmt.Errorf("apikey %s: method %q has no field %q", cfg.Manifest.Key, cfg.Method, cfg.Field)
	}
	if cfg.Verify.Path == "" {
		return nil, fmt.Errorf("apikey %s: a verify path is required", cfg.Manifest.Key)
	}
	return &Provider{cfg: cfg, base: strings.TrimSuffix(cfg.Manifest.BaseURLs[0], "/")}, nil
}

// Manifest implements [provider.Provider].
func (p *Provider) Manifest() provider.Manifest { return p.cfg.Manifest }

// Authenticate implements [provider.Provider].
func (p *Provider) Authenticate(r *http.Request, m provider.Material) error {
	tok := m.Field(p.cfg.Field)
	if tok.IsZero() {
		return fmt.Errorf("%w: no %s", cf.ErrCredentialUnavailable, p.cfg.Field)
	}
	b := tok.Reveal()
	defer clear(b)
	r.Header.Set(p.cfg.Header, p.cfg.Prefix+string(b))
	return nil
}

// Validate implements [provider.Provider].
func (p *Provider) Validate(ctx context.Context, hc *http.Client, m provider.Material) (provider.Result, error) {
	doc, err := p.get(ctx, hc, m, p.cfg.Verify.Path)
	if err != nil {
		return provider.Result{}, err
	}
	res := extract(doc, p.cfg.Verify)
	if p.cfg.Account != nil {
		acct, err := p.get(ctx, hc, m, p.cfg.Account.Path)
		if err != nil {
			return provider.Result{}, err
		}
		a := extract(acct, *p.cfg.Account)
		res.AccountID, res.AccountLabel = a.AccountID, a.AccountLabel
		if a.Scopes != nil {
			res.Scopes = a.Scopes
		}
	}
	return res, nil
}

// maxBody bounds a response read during validation.
const maxBody = 1 << 20

func (p *Provider) get(ctx context.Context, hc *http.Client, m provider.Material, path string) (doc any, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("apikey %s: %w", p.cfg.Manifest.Key, err)
	}
	req.Header.Set("Accept", "application/json")
	if err := p.Authenticate(req, m); err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		// Transport errors name the URL and the cause, never a header.
		return nil, fmt.Errorf("%w: %s: %w", cf.ErrTransient, p.cfg.Manifest.Key, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("apikey %s: closing response: %w", p.cfg.Manifest.Key, cerr)
		}
	}()
	if err := provider.Classify(p.cfg.Manifest.Key, resp); err != nil {
		return nil, err
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: %w: %s", cf.ErrTransient, ErrNotJSON, p.cfg.Manifest.Key)
	}
	return doc, nil
}

func extract(doc any, r Request) provider.Result {
	res := provider.Result{
		AccountID:    asString(lookup(doc, r.AccountIDPath)),
		AccountLabel: asString(lookup(doc, r.AccountLabelPath)),
	}
	if r.ScopesPath != "" {
		if arr, ok := lookup(doc, r.ScopesPath).([]any); ok {
			for _, v := range arr {
				if s := asString(v); s != "" {
					res.Scopes = append(res.Scopes, s)
				}
			}
		}
	}
	return res
}

// lookup follows a dot-separated path of object keys and array indexes.
func lookup(doc any, path string) any {
	if path == "" {
		return nil
	}
	cur := doc
	for _, part := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			cur = v[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil
			}
			cur = v[i]
		default:
			return nil
		}
	}
	return cur
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

// ErrNotJSON is returned when a validation response is not JSON.
var ErrNotJSON = errors.New("apikey: response is not JSON")
