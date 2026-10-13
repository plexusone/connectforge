package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	cf "github.com/plexusone/connectionforge"
)

// Material is a credential's plaintext, as a provider adapter sees it. It
// exists only between unsealing and use; every value is a [cf.Secret].
type Material struct {
	// Kind names the structure, for example "api_token".
	Kind string
	// Fields holds the credential's values by field name.
	Fields map[string]cf.Secret
	// ExpiresAt is when an access token expires, if it does.
	ExpiresAt *time.Time
}

// Field returns a field's value, or a zero secret.
func (m Material) Field(name string) cf.Secret { return m.Fields[name] }

// Clear overwrites every value, on a best-effort basis.
func (m Material) Clear() {
	for _, s := range m.Fields {
		s.Clear()
	}
}

// wireMaterial is the sealed form, versioned so it can change.
type wireMaterial struct {
	V         int               `json:"v"`
	Kind      string            `json:"kind"`
	Fields    map[string][]byte `json:"fields"`
	ExpiresAt *time.Time        `json:"expiresAt,omitempty"`
}

const materialVersion = 1

// EncodeMaterial returns the plaintext to seal. The caller seals it and
// clears it.
func EncodeMaterial(m Material) ([]byte, error) {
	w := wireMaterial{V: materialVersion, Kind: m.Kind, Fields: map[string][]byte{}, ExpiresAt: m.ExpiresAt}
	for k, s := range m.Fields {
		w.Fields[k] = s.Reveal()
	}
	defer func() {
		for _, b := range w.Fields {
			clear(b)
		}
	}()
	b, err := json.Marshal(w)
	if err != nil {
		// The error names the type, never a value.
		return nil, fmt.Errorf("provider: encode material: %w", err)
	}
	return b, nil
}

// ErrBadMaterial is returned for plaintext that is not encoded material.
var ErrBadMaterial = errors.New("provider: not credential material")

// DecodeMaterial parses unsealed plaintext and clears it.
func DecodeMaterial(b []byte) (Material, error) {
	defer clear(b)
	var w wireMaterial
	if err := json.Unmarshal(b, &w); err != nil || w.V != materialVersion {
		// The JSON error could quote the input, so it is not wrapped.
		return Material{}, ErrBadMaterial
	}
	m := Material{Kind: w.Kind, Fields: make(map[string]cf.Secret, len(w.Fields)), ExpiresAt: w.ExpiresAt}
	for k, v := range w.Fields {
		m.Fields[k] = cf.NewSecret(v)
		clear(v)
	}
	return m, nil
}
