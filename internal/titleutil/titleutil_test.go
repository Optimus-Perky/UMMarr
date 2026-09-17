package titleutil

import "testing"

func TestCleanTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"The Matrix", "thematrix"},
		{"Marvel's Agents of S.H.I.E.L.D.", "marvelsagentsofshield"},
		{"Amélie", "amelie"},
		{"", ""},
		{"Se7en", "se7en"},
		{"Now That's What I Call Music! 50", "nowthatswhaticallmusic50"},
	}
	for _, c := range cases {
		if got := CleanTitle(c.in); got != c.want {
			t.Errorf("CleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSortTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"The Matrix", "Matrix"},
		{"A Beautiful Mind", "Beautiful Mind"},
		{"An American in Paris", "American in Paris"},
		{"The Wire", "Wire"},
		{"Fargo", "Fargo"},
		{"A", "A"},
		{"", ""},
		{"Anaconda", "Anaconda"}, // must not strip "an" as a substring, only "an " as a leading word
	}
	for _, c := range cases {
		if got := SortTitle(c.in); got != c.want {
			t.Errorf("SortTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"The Fast and the Furious":                "the-fast-and-the-furious",
		"Terminator: The Sarah Connor Chronicles": "terminator-the-sarah-connor-chronicles",
		"Amélie":                       "amelie",
		"  Spaces -- and (brackets)! ": "spaces-and-brackets",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SlugWithYear("The Fast and the Furious", 2001); got != "the-fast-and-the-furious-2001" {
		t.Errorf("SlugWithYear: got %q", got)
	}
	if got := SlugWithYear("Unknown Year", 0); got != "unknown-year" {
		t.Errorf("SlugWithYear without a year: got %q", got)
	}
}
