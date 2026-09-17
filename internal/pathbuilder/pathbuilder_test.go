package pathbuilder

import (
	"reflect"
	"testing"
)

func TestResolveTemplate(t *testing.T) {
	cases := []struct {
		name, template string
		tokens         map[string]string
		want           string
	}{
		{"basic", "{Movie Title} ({Release Year})", map[string]string{"Movie Title": "Inception", "Release Year": "2010"}, "Inception (2010)"},
		{"missing token resolves empty", "{Artist Name}", map[string]string{}, ""},
		{"zero-padded numeric", "Season {season:00}", map[string]string{"season": "3"}, "Season 03"},
		{"zero-padded track", "{track:00} - {Track Title}", map[string]string{"track": "7", "Track Title": "Angels"}, "07 - Angels"},
		{"padding on non-numeric passes through", "{season:00}", map[string]string{"season": "N/A"}, "N/A"},
		{"no tokens at all", "Season {season}", map[string]string{"season": "3"}, "Season 3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveTemplate(c.template, c.tokens); got != c.want {
				t.Errorf("ResolveTemplate(%q) = %q, want %q", c.template, got, c.want)
			}
		})
	}
}

func TestSanitizeSegment(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Se7en", "Se7en"},
		{"Se7en: The Movie", "Se7en The Movie"},
		{`Weird<>:"/\|?*Name`, "WeirdName"},
		{"Trailing dots...", "Trailing dots"},
		{"Trailing space ", "Trailing space"},
		{"", ""},
	}
	for _, c := range cases {
		if got := SanitizeSegment(c.in, Options{}); got != c.want {
			t.Errorf("SanitizeSegment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeSegment_ColonModeIgnoredWhenRemoving(t *testing.T) {
	if got := SanitizeSegment("Se7en: The Movie", Options{Colon: ColonDash}); got != "Se7en The Movie" {
		t.Errorf("want the colon removed when not replacing, got %q", got)
	}
}

func TestSanitizeSegment_ReplaceIllegal(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		colon ColonReplacement
		want  string
	}{
		{"colon delete", "Se7en: The Movie", ColonDelete, "Se7en The Movie"},
		{"colon dash", "Se7en: The Movie", ColonDash, "Se7en- The Movie"},
		{"colon space dash", "Se7en: The Movie", ColonSpaceDash, "Se7en - The Movie"},
		{"colon space dash space", "Se7en: The Movie", ColonSpaceDashSpace, "Se7en - The Movie"},
		{"colon space dash space, no space after", "10:30", ColonSpaceDashSpace, "10 - 30"},
		{"smart, space after", "Se7en: The Movie", ColonSmart, "Se7en - The Movie"},
		{"smart, no space after", "10:30", ColonSmart, "10-30"},
		{"unset colon mode deletes", "Se7en: The Movie", "", "Se7en The Movie"},
		{"other characters", `AC/DC\Live <Now> "Quoted" a|b What? Star*`, ColonDelete, "AC+DC+Live Now Quoted ab What! Star-"},
		{"control characters still removed", "Tab\there", ColonDelete, "Tabhere"},
		{"trailing dots and spaces still trimmed", "Why?. ", ColonDelete, "Why!"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeSegment(c.in, Options{ReplaceIllegal: true, Colon: c.colon}); got != c.want {
				t.Errorf("SanitizeSegment(%q, %s) = %q, want %q", c.in, c.colon, got, c.want)
			}
		})
	}
}

func TestJoinSegments(t *testing.T) {
	if got := JoinSegments("root", "Inception (2010)"); got != "root/Inception (2010)" {
		t.Errorf("JoinSegments = %q", got)
	}
	if got := JoinSegments("root", "", "Album"); got != "root/Album" {
		t.Errorf("JoinSegments with empty middle segment = %q, want empty segments skipped", got)
	}
}

func TestResolveTemplatePath_MultiSegment(t *testing.T) {
	got := ResolveTemplatePath("Various Artists/{Series Name}", map[string]string{"Series Name": "Now That's What I Call Music"}, Options{})
	want := []string{"Various Artists", "Now That's What I Call Music"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveTemplatePath = %v, want %v", got, want)
	}
}

func TestResolveTemplatePath_SlashInTokenValueDoesNotCreateExtraLevel(t *testing.T) {
	// A movie title containing "/" must be sanitized away within its own
	// segment, not treated as an extra directory level - the split on "/"
	// happens on the raw template, before substitution.
	got := ResolveTemplatePath("{Movie Title} ({Release Year})", map[string]string{"Movie Title": "AKA/Alias", "Release Year": "1999"}, Options{})
	if len(got) != 1 {
		t.Fatalf("want 1 segment even though the title contains '/', got %v", got)
	}
	if got[0] != "AKAAlias (1999)" {
		t.Errorf("want slash stripped from within the segment, got %q", got[0])
	}
}

func TestResolveTemplatePath_ReplacedSlashStaysInItsSegment(t *testing.T) {
	got := ResolveTemplatePath("{Artist Name}", map[string]string{"Artist Name": "AC/DC"}, Options{ReplaceIllegal: true})
	if !reflect.DeepEqual(got, []string{"AC+DC"}) {
		t.Errorf("want one segment with the slash replaced, got %v", got)
	}
}
