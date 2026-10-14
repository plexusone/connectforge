package connectforge_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	cf "github.com/plexusone/connectforge"
)

const sentinel = "sk-sentinel-0000"

func TestSecretNeverPrints(t *testing.T) {
	s := cf.SecretString(sentinel)
	holder := struct {
		Token cf.Secret
		Ptr   *cf.Secret
	}{s, &s}

	var outs []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
		outs = append(outs, fmt.Sprintf(verb, s), fmt.Sprintf(verb, holder))
	}
	outs = append(outs, fmt.Sprint(s), fmt.Errorf("wrap: %v", s).Error())
	j, err := json.Marshal(holder)
	if err != nil {
		t.Fatal(err)
	}
	outs = append(outs, string(j))
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("m", "token", s, "holder", holder)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("m", "token", s)
	outs = append(outs, buf.String())

	for _, o := range outs {
		if strings.Contains(o, sentinel) || strings.Contains(o, fmt.Sprintf("%x", sentinel)) {
			t.Fatalf("secret leaked: %s", o)
		}
	}
}

func TestSecretReveal(t *testing.T) {
	in := []byte(sentinel)
	s := cf.NewSecret(in)
	in[0] = 'X'
	got := s.Reveal()
	if string(got) != sentinel {
		t.Fatalf("NewSecret did not copy: %q", got)
	}
	got[0] = 'Y'
	if string(s.Reveal()) != sentinel {
		t.Fatal("Reveal returned the secret's own buffer")
	}
	if s.IsZero() || !(cf.Secret{}).IsZero() {
		t.Fatal("IsZero")
	}
	s.Clear()
	if bytes.Contains(s.Reveal(), []byte("sentinel")) {
		t.Fatal("Clear left the value")
	}
}

func TestPermanent(t *testing.T) {
	for err, want := range map[error]bool{
		fmt.Errorf("x: %w", cf.ErrInvalidCredential): true,
		cf.ErrInsufficientScope:                      true,
		cf.ErrRateLimited:                            false,
		fmt.Errorf("x: %w", cf.ErrTransient):         false,
		errors.New("other"):                          false,
	} {
		if got := cf.Permanent(err); got != want {
			t.Errorf("Permanent(%v) = %v", err, got)
		}
	}
}
