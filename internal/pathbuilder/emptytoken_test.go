package pathbuilder_test

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/pathbuilder"
)

// A token nothing filled in used to leave its brackets behind, so an album
// with no release date became "Superfly ()" on disk. The brackets belong to
// the token, so they go with it.
func TestResolveTemplate_DropsBracketsAroundAnEmptyToken(t *testing.T) {
	cases := []struct {
		name     string
		template string
		tokens   map[string]string
		want     string
	}{
		{"album with no year", "{Album Title} ({Release Year})", map[string]string{"Album Title": "Superfly"}, "Superfly"},
		{"album with a year", "{Album Title} ({Release Year})", map[string]string{"Album Title": "Power Up", "Release Year": "2020"}, "Power Up (2020)"},
		{"trailing quality dropped", "{Movie Title} ({Release Year}) [{Quality Title}]",
			map[string]string{"Movie Title": "Heat", "Release Year": "1995"}, "Heat (1995)"},
		{"padded token still pads", "{track:00} - {Track Title}", map[string]string{"track": "3", "Track Title": "SOS"}, "03 - SOS"},
		{"brackets in a value are kept", "{Album Title}", map[string]string{"Album Title": "Live (1975)"}, "Live (1975)"},
		{"empty token without brackets still empties", "{Album Title} {Release Year}", map[string]string{"Album Title": "Superfly"}, "Superfly "},
	}
	for _, c := range cases {
		if got := pathbuilder.ResolveTemplate(c.template, c.tokens); got != c.want {
			t.Errorf("%s: ResolveTemplate(%q) = %q, want %q", c.name, c.template, got, c.want)
		}
	}
}
