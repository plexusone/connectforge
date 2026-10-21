package provider_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/provider"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// source mints "tok-N" tokens that last life, and counts the exchanges.
type source struct {
	clock *fakeClock
	life  time.Duration
	typ   string
	n     atomic.Int32
	fail  atomic.Bool
}

func (s *source) Token(_ context.Context, _ *http.Client, _ provider.Material) (provider.AccessToken, error) {
	if s.fail.Load() {
		return provider.AccessToken{}, errors.New("exchange refused")
	}
	n := s.n.Add(1)
	tok := provider.AccessToken{Type: s.typ, Value: cf.SecretString(fmt.Sprintf("tok-%d", n))}
	if s.life > 0 {
		tok.ExpiresAt = s.clock.Now().Add(s.life)
	}
	return tok, nil
}

// echo returns the Authorization header it was sent.
func echo(t *testing.T) *http.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Auth", r.Header.Get("Authorization"))
	}))
	t.Cleanup(srv.Close)
	return srv.Client()
}

func get(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.Header.Get("X-Auth")
}

func newClient(t *testing.T, s *source, clock *fakeClock) (*http.Client, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Auth", r.Header.Get("Authorization"))
	}))
	t.Cleanup(srv.Close)
	hc := srv.Client()
	return provider.NewTokenClient(s, provider.Material{}, hc, clock), srv.URL
}

func TestTokenIsKeptUntilTheMarginThenReplaced(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := &source{clock: clock, life: time.Hour}
	c, url := newClient(t, s, clock)

	if got := get(t, c, url); got != "Bearer tok-1" {
		t.Fatalf("first request sent %q", got)
	}
	clock.advance(30 * time.Minute)
	if got := get(t, c, url); got != "Bearer tok-1" || s.n.Load() != 1 {
		t.Fatalf("kept token: sent %q after %d exchanges", got, s.n.Load())
	}
	clock.advance(30*time.Minute - provider.TokenExpiryMargin + time.Second)
	if got := get(t, c, url); got != "Bearer tok-2" {
		t.Fatalf("inside the margin, sent %q", got)
	}
}

func TestTokenWithoutExpiryIsNotKept(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	s := &source{clock: clock}
	c, url := newClient(t, s, clock)
	get(t, c, url)
	get(t, c, url)
	if s.n.Load() != 2 {
		t.Fatalf("%d exchanges for two requests, want 2", s.n.Load())
	}
}

func TestTokenType(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	c, url := newClient(t, &source{clock: clock, life: time.Hour, typ: "DPoP"}, clock)
	if got := get(t, c, url); got != "DPoP tok-1" {
		t.Fatalf("sent %q", got)
	}
}

func TestFailedExchangeIsReturnedAndNotKept(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	s := &source{clock: clock, life: time.Hour}
	c, url := newClient(t, s, clock)
	s.fail.Store(true)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := c.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("request succeeded without a token")
	}
	s.fail.Store(false)
	if got := get(t, c, url); got != "Bearer tok-1" {
		t.Fatalf("after recovery, sent %q", got)
	}
}

func TestEmptyTokenIsRefused(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	empty := emptySource{}
	hc := echo(t)
	c := provider.NewTokenClient(empty, provider.Material{}, hc, clock)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(req) //nolint:bodyclose // the request fails before a response
	if !errors.Is(err, cf.ErrCredentialUnavailable) {
		t.Fatalf("err = %v, want ErrCredentialUnavailable", err)
	}
}

type emptySource struct{}

func (emptySource) Token(context.Context, *http.Client, provider.Material) (provider.AccessToken, error) {
	return provider.AccessToken{}, nil
}

func TestConcurrentRequestsShareOneExchange(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	s := &source{clock: clock, life: time.Hour}
	c, url := newClient(t, s, clock)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { get(t, c, url) })
	}
	wg.Wait()
	if s.n.Load() != 1 {
		t.Fatalf("%d exchanges for concurrent requests, want 1", s.n.Load())
	}
}
