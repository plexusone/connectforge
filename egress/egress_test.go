package egress_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"

	"github.com/plexusone/connectforge/egress"
	"github.com/plexusone/connectforge/internal/fakeapi"
)

// loopback lets the tests reach their local servers.
var loopback = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}

func host(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func get(c *http.Client, u string) error {
	resp, err := c.Get(u)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func TestPolicy(t *testing.T) {
	api := fakeapi.Start(t)
	base := api.Client().Transport.(*http.Transport) //nolint:forcetypeassert // httptest's client always uses *http.Transport
	h := host(t, api.URL)

	allowed := egress.Client(egress.Policy{Hosts: []string{h}, AllowPrefixes: loopback, Transport: base})
	if err := get(allowed, api.URL+"/data"); err != nil {
		t.Fatalf("allowed host: %v", err)
	}
	if err := get(allowed, api.URL+"/redirect-same"); err != nil {
		t.Fatalf("same-host redirect: %v", err)
	}
	if err := get(allowed, api.URL+"/redirect-away"); !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("redirect to another host: %v", err)
	}

	other := egress.Client(egress.Policy{Hosts: []string{"api.example.com"}, AllowPrefixes: loopback, Transport: base})
	if err := get(other, api.URL+"/data"); !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("host outside the manifest: %v", err)
	}

	// The host is allowed, but it resolves to a loopback address.
	strict := egress.Client(egress.Policy{Hosts: []string{h}, Transport: base})
	if err := get(strict, api.URL+"/data"); !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("loopback address: %v", err)
	}

	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)
	ph := host(t, plain.URL)
	if err := get(egress.Client(egress.Policy{Hosts: []string{ph}, AllowPrefixes: loopback}), plain.URL); !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("plain HTTP: %v", err)
	}
	if err := get(egress.Client(egress.Policy{Hosts: []string{ph}, AllowPrefixes: loopback, AllowHTTP: true}), plain.URL); err != nil {
		t.Fatalf("plain HTTP when allowed: %v", err)
	}
}
