package apikey_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/internal/fakeapi"
	"github.com/plexusone/connectforge/provider"
	"github.com/plexusone/connectforge/provider/apikey"
)

func material(tok string) provider.Material {
	return provider.Material{Kind: "api_token", Fields: map[string]cf.Secret{"token": cf.SecretString(tok)}}
}

func TestValidateDiscoversTheAccount(t *testing.T) {
	api := fakeapi.Start(t)
	res, err := api.Provider(t).Validate(context.Background(), api.Client(), material(fakeapi.Good))
	if err != nil {
		t.Fatal(err)
	}
	if res.AccountID != fakeapi.AccountID || res.AccountLabel != fakeapi.Label ||
		len(res.Scopes) != 2 || res.Scopes[0] != "zone:read" {
		t.Fatalf("result: %+v", res)
	}
}

func TestValidateClassifiesRefusals(t *testing.T) {
	api := fakeapi.Start(t)
	p := api.Provider(t)
	for tok, want := range map[string]error{
		"wrong":         cf.ErrInvalidCredential,
		fakeapi.NoScope: cf.ErrInsufficientScope,
		fakeapi.Limited: cf.ErrRateLimited,
		fakeapi.Broken:  cf.ErrTransient,
		fakeapi.NotJSON: apikey.ErrNotJSON,
	} {
		_, err := p.Validate(context.Background(), api.Client(), material(tok))
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", tok, err, want)
		}
		if err != nil && strings.Contains(err.Error(), tok) {
			t.Errorf("%s: error contains the token: %v", tok, err)
		}
	}
}

func TestAuthenticate(t *testing.T) {
	api := fakeapi.Start(t)
	p := api.Provider(t)
	r, err := http.NewRequest(http.MethodGet, api.URL+"/data", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Authenticate(r, material("tok")); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("header: %q", got)
	}
	if err := p.Authenticate(r, provider.Material{}); !errors.Is(err, cf.ErrCredentialUnavailable) {
		t.Fatalf("missing token: %v", err)
	}

	custom, err := apikey.New(apikey.Config{Manifest: api.Manifest(), Header: "X-Api-Key", Verify: apikey.Request{Path: "/verify"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := custom.Authenticate(r, material("tok")); err != nil || r.Header.Get("X-Api-Key") != "tok" {
		t.Fatalf("custom header: %q, %v", r.Header.Get("X-Api-Key"), err)
	}
}

func TestNewChecksConfig(t *testing.T) {
	api := fakeapi.Start(t)
	m := api.Manifest()
	for name, cfg := range map[string]apikey.Config{
		"verify":   {Manifest: m},
		"method":   {Manifest: m, Method: "oauth2", Verify: apikey.Request{Path: "/verify"}},
		"field":    {Manifest: m, Field: "key", Verify: apikey.Request{Path: "/verify"}},
		"manifest": {Manifest: provider.Manifest{Key: "x"}, Verify: apikey.Request{Path: "/verify"}},
	} {
		if _, err := apikey.New(cfg); err == nil {
			t.Errorf("%s: bad config accepted", name)
		}
	}
}
