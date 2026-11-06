package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/plexusone/connectforge/provider"
)

func TestDetailsAreShortened(t *testing.T) {
	var many []string
	for i := range 30 {
		many = append(many, fmt.Sprintf("site-%d.example.com", i))
	}
	in := []provider.Detail{
		{Label: "  Sites\n", Values: many},
		{Label: "", Values: []string{"dropped: no label"}},
		{Label: "Long", Values: []string{strings.Repeat("é", 300), "  ", "tab\there"}},
	}
	for i := range 10 {
		in = append(in, provider.Detail{Label: fmt.Sprintf("extra %d", i)})
	}
	got := details(in)
	if len(got) != maxDetails {
		t.Fatalf("details = %d, want %d", len(got), maxDetails)
	}
	if got[0].Label != "Sites" || len(got[0].Values) != maxDetailValues || got[0].More != 5 {
		t.Errorf("sites = %q, %d values, more %d", got[0].Label, len(got[0].Values), got[0].More)
	}
	long := got[1]
	if long.Label != "Long" || len(long.Values) != 2 {
		t.Fatalf("long = %+v", long)
	}
	if v := long.Values[0]; len(v) > maxValueLen || !strings.HasSuffix(v, "…") || !strings.HasPrefix(v, "é") {
		t.Errorf("a long value was not cut on a rune boundary: %d bytes", len(v))
	}
	if long.Values[1] != "tabhere" {
		t.Errorf("control characters kept: %q", long.Values[1])
	}
	// Directional overrides and isolates are removed, so a value cannot
	// display reordered.
	bidi := details([]provider.Detail{{Label: "Sites\u202e", Values: []string{"evil\u202egnp.exe", "\u2066a\u2069\u200fb\u061c"}}})
	if bidi[0].Label != "Sites" || bidi[0].Values[0] != "evilgnp.exe" || bidi[0].Values[1] != "ab" {
		t.Errorf("directional characters kept: %+v", bidi)
	}
	if details(nil) != nil {
		t.Error("no details gave a non-nil slice")
	}
}
