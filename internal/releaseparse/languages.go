package releaseparse

import (
	"regexp"
	"strings"
)

// languageTokens are the release-name words Radarr's LanguageParser looks
// for, mapped to the language they mean.
var languageTokens = []struct {
	pattern  *regexp.Regexp
	language string
}{
	{regexp.MustCompile(`(?i)\b(?:german|deutsch|videomann)\b`), "German"},
	{regexp.MustCompile(`(?i)\b(?:french|truefrench|vff|vfq|vfi|vf2|vostfr)\b`), "French"},
	{regexp.MustCompile(`(?i)\b(?:spanish|castellano|latino|esp)\b`), "Spanish"},
	{regexp.MustCompile(`(?i)\b(?:italian|ita)\b`), "Italian"},
	{regexp.MustCompile(`(?i)\b(?:dutch|nl|flemish)\b`), "Dutch"},
	{regexp.MustCompile(`(?i)\b(?:japanese|jap|jpn)\b`), "Japanese"},
	{regexp.MustCompile(`(?i)\b(?:korean|kor)\b`), "Korean"},
	{regexp.MustCompile(`(?i)\b(?:chinese|mandarin|cantonese|chi)\b`), "Chinese"},
	{regexp.MustCompile(`(?i)\b(?:russian|rus)\b`), "Russian"},
	{regexp.MustCompile(`(?i)\b(?:portuguese|pt-br|brazilian)\b`), "Portuguese"},
	{regexp.MustCompile(`(?i)\b(?:swedish|swe)\b`), "Swedish"},
	{regexp.MustCompile(`(?i)\b(?:norwegian|nor)\b`), "Norwegian"},
	{regexp.MustCompile(`(?i)\b(?:danish|dan)\b`), "Danish"},
	{regexp.MustCompile(`(?i)\b(?:finnish|fin)\b`), "Finnish"},
	{regexp.MustCompile(`(?i)\bnordic\b`), "Nordic"},
	{regexp.MustCompile(`(?i)\b(?:polish|pl|pldub)\b`), "Polish"},
	{regexp.MustCompile(`(?i)\b(?:hindi|hin|tamil|telugu)\b`), "Hindi"},
	{regexp.MustCompile(`(?i)\b(?:turkish|tur)\b`), "Turkish"},
	{regexp.MustCompile(`(?i)\b(?:hungarian|hun)\b`), "Hungarian"},
	{regexp.MustCompile(`(?i)\b(?:czech|cz)\b`), "Czech"},
	{regexp.MustCompile(`(?i)\b(?:arabic)\b`), "Arabic"},
	{regexp.MustCompile(`(?i)\b(?:english|eng)\b`), "English"},
}

var multiPattern = regexp.MustCompile(`(?i)\b(?:multi|dual[-. ]?audio|dl)\b`)

// Languages guesses a release's audio languages from its name, the way
// Radarr does: named languages win, "MULTi" adds English alongside, and a
// name that says nothing is taken as English.
func Languages(title string) []string {
	// Strip the release group so a group like "-ITA" isn't read as a language.
	name := title
	if m := releaseGroupPattern.FindStringIndex(strings.TrimSpace(title)); m != nil {
		name = title[:m[0]]
	}
	var out []string
	seen := map[string]bool{}
	for _, t := range languageTokens {
		if t.pattern.MatchString(name) && !seen[t.language] {
			seen[t.language] = true
			out = append(out, t.language)
		}
	}
	if multiPattern.MatchString(name) && !seen["English"] {
		out = append(out, "English")
	}
	if len(out) == 0 {
		return []string{"English"}
	}
	return out
}

// KnownLanguages lists every language Languages can find, English first.
func KnownLanguages() []string {
	out := []string{"English"}
	for _, t := range languageTokens {
		if t.language != "English" {
			out = append(out, t.language)
		}
	}
	return out
}
