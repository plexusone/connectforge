package service

import (
	"strings"
	"unicode"
	"unicode/utf8"

	cf "github.com/plexusone/connectforge"
	"github.com/plexusone/connectforge/provider"
)

// Limits on what a validation's details keep, so a provider that lists
// thousands of sites still stores and shows a short summary.
const (
	maxDetails      = 8
	maxDetailValues = 25
	maxLabelLen     = 64
	maxValueLen     = 200
)

// details returns a provider's details shortened for storage and display:
// at most maxDetails, each with at most maxDetailValues values, labels and
// values cut to length, and control characters removed. A detail with no
// label is dropped. More counts the values left out.
func details(in []provider.Detail) []cf.Detail {
	var out []cf.Detail
	for _, d := range in {
		label := clip(d.Label, maxLabelLen)
		if label == "" {
			continue
		}
		if len(out) == maxDetails {
			break
		}
		cd := cf.Detail{Label: label}
		for _, v := range d.Values {
			v = clip(v, maxValueLen)
			if v == "" {
				continue
			}
			if len(cd.Values) == maxDetailValues {
				cd.More++
				continue
			}
			cd.Values = append(cd.Values, v)
		}
		out = append(out, cd)
	}
	return out
}

// clip removes control characters and surrounding space from s and cuts it
// to at most n bytes on a rune boundary, marking a cut with an ellipsis.
func clip(s string, n int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
