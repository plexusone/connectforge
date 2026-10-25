package broker_test

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"runtime"
	"testing"
	"time"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/broker"
	"github.com/plexusone/connectforge/cryptotest"
	"github.com/plexusone/connectforge/egress"
	"github.com/plexusone/connectforge/internal/fakeapi"
	"github.com/plexusone/connectforge/provider"
	"github.com/plexusone/connectforge/store/memstore"
)

type allowAll struct{}

func (allowAll) Allowed(context.Context, cf.Principal, cf.Action, cf.Connection) error { return nil }

// TestProviderClientSharesTransport proves validation clients reuse one
// transport per provider, so repeated validations do not each leave a
// connection and its goroutines open.
func TestProviderClientSharesTransport(t *testing.T) {
	api := fakeapi.Start(t)
	reg := provider.NewRegistry()
	if err := reg.Register(api.Provider(t)); err != nil {
		t.Fatal(err)
	}
	b, err := broker.New(broker.Options{
		Store: memstore.New(), Crypto: cryptotest.New(), Providers: reg, Authorizer: allowAll{},
		Egress: egress.Policy{
			AllowPrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
			Transport:     api.Client().Transport.(*http.Transport), //nolint:forcetypeassert // httptest's client uses *http.Transport
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := api.Manifest()
	first, err := b.ProviderClient(m)
	if err != nil {
		t.Fatal(err)
	}
	first.Timeout = time.Nanosecond // a caller's change stays in its copy

	get := func() {
		hc, err := b.ProviderClient(m)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := hc.Get(api.URL + "/data")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	get() // the first connection's goroutines are expected
	before := runtime.NumGoroutine()
	for range 50 {
		get()
	}
	time.Sleep(50 * time.Millisecond) // let finished handlers exit
	if grown := runtime.NumGoroutine() - before; grown > 10 {
		t.Fatalf("goroutines grew by %d over 50 validations", grown)
	}
}
