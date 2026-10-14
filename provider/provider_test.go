package provider_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/provider"
)

func manifest(key string) provider.Manifest {
	return provider.Manifest{
		Key: key, DisplayName: key, BaseURLs: []string{"https://api." + key + ".example"},
		AuthMethods: []provider.AuthMethod{{Key: "api_token", Fields: []provider.Field{{Name: "token", Secret: true, Required: true}}}},
	}
}

type stub struct{ m provider.Manifest }

func (s stub) Manifest() provider.Manifest { return s.m }

func (stub) Validate(context.Context, *http.Client, provider.Material) (provider.Result, error) {
	return provider.Result{}, nil
}

func (stub) Authenticate(*http.Request, provider.Material) error { return nil }

func TestRegistry(t *testing.T) {
	r := provider.NewRegistry()
	for _, k := range []string{"posthog", "cloudflare"} {
		if err := r.Register(stub{manifest(k)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Register(stub{manifest("posthog")}); err == nil {
		t.Fatal("duplicate registered")
	}
	if err := r.Register(stub{provider.Manifest{Key: "bad"}}); err == nil {
		t.Fatal("invalid manifest registered")
	}
	if _, err := r.Get("github"); !errors.Is(err, provider.ErrUnknownProvider) {
		t.Fatalf("unknown: %v", err)
	}
	ms := r.Manifests()
	if len(ms) != 2 || ms[0].Key != "cloudflare" || ms[1].Key != "posthog" {
		t.Fatalf("manifests: %+v", ms)
	}
}

func TestManifest(t *testing.T) {
	m := manifest("cloudflare")
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	hosts, err := m.Hosts()
	if err != nil || len(hosts) != 1 || hosts[0] != "api.cloudflare.example" {
		t.Fatalf("hosts: %v, %v", hosts, err)
	}
	if _, ok := m.Method("api_token"); !ok {
		t.Fatal("method not found")
	}
	if _, ok := m.Method("oauth2"); ok {
		t.Fatal("unknown method found")
	}
	for name, bad := range map[string]provider.Manifest{
		"key":     {BaseURLs: m.BaseURLs, AuthMethods: m.AuthMethods},
		"urls":    {Key: "x", AuthMethods: m.AuthMethods},
		"methods": {Key: "x", BaseURLs: m.BaseURLs},
		"url":     {Key: "x", BaseURLs: []string{"not a url"}, AuthMethods: m.AuthMethods},
		"fields":  {Key: "x", BaseURLs: m.BaseURLs, AuthMethods: []provider.AuthMethod{{Key: "k"}}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: invalid manifest accepted", name)
		}
	}
}

func TestMaterialRoundTrip(t *testing.T) {
	exp := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	in := provider.Material{Kind: "oauth_tokens", Fields: map[string]cf.Secret{
		"access_token": cf.SecretString("at"), "refresh_token": cf.SecretString("rt"),
	}, ExpiresAt: &exp}
	b, err := provider.EncodeMaterial(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := provider.DecodeMaterial(b)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != in.Kind || string(out.Field("access_token").Reveal()) != "at" ||
		string(out.Field("refresh_token").Reveal()) != "rt" || !out.ExpiresAt.Equal(exp) {
		t.Fatalf("round trip: %+v", out)
	}
	for _, c := range b {
		if c != 0 {
			t.Fatal("DecodeMaterial did not clear its input")
		}
	}
	if _, err := provider.DecodeMaterial([]byte(`{"v":9}`)); !errors.Is(err, provider.ErrBadMaterial) {
		t.Fatalf("unknown version: %v", err)
	}
	if _, err := provider.DecodeMaterial([]byte(`token-in-the-clear`)); !errors.Is(err, provider.ErrBadMaterial) || strings.Contains(err.Error(), "token") {
		t.Fatalf("bad input: %v", err)
	}
}

func TestClassify(t *testing.T) {
	for status, want := range map[int]error{
		200: nil, 204: nil,
		401: cf.ErrInvalidCredential, 400: cf.ErrInvalidCredential,
		403: cf.ErrInsufficientScope, 429: cf.ErrRateLimited, 503: cf.ErrTransient,
	} {
		resp := &http.Response{StatusCode: status, Header: http.Header{"Cf-Ray": {"ray-1"}}}
		err := provider.Classify("cloudflare", resp)
		if want == nil {
			if err != nil {
				t.Errorf("%d: %v", status, err)
			}
			continue
		}
		var se *provider.StatusError
		if !errors.Is(err, want) || !errors.As(err, &se) || se.RequestID != "ray-1" || se.Status != status {
			t.Errorf("%d: %v", status, err)
		}
	}
}
