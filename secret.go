package connectforge

import (
	"fmt"
	"log/slog"
)

const redacted = "[redacted]"

// Secret holds credential material. It redacts itself wherever a value is
// printed, logged or encoded, so a secret placed in a struct, an error or a log
// record does not leak. Only [Secret.Reveal] returns the value, and it is called
// only by the broker and provider adapters.
type Secret struct {
	v []byte
}

// NewSecret returns a secret holding a copy of b.
func NewSecret(b []byte) Secret { return Secret{v: append([]byte(nil), b...)} }

// SecretString returns a secret holding s.
func SecretString(s string) Secret { return Secret{v: []byte(s)} }

// Reveal returns a copy of the secret's value. The caller clears it when done.
func (s Secret) Reveal() []byte { return append([]byte(nil), s.v...) }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return len(s.v) == 0 }

// Clear overwrites the secret's value in place, on a best-effort basis.
func (s Secret) Clear() { clear(s.v) }

// String returns a fixed placeholder.
func (Secret) String() string { return redacted }

// GoString returns a fixed placeholder.
func (Secret) GoString() string { return redacted }

// Format prints a fixed placeholder for every verb. Format has no way to
// report a failed write to fmt, which records write errors itself, so the
// result of Write is deliberately not used.
func (Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redacted)) }

// MarshalJSON encodes a fixed placeholder.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// MarshalText encodes a fixed placeholder.
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// LogValue logs a fixed placeholder.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }
