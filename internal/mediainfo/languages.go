package mediainfo

import "strings"

// languages maps ISO 639-2 codes (both B and T forms) to the two-letter
// code Sonarr's tokens use and the name the pages show.
var languages = map[string][2]string{
	"eng": {"EN", "English"}, "fre": {"FR", "French"}, "fra": {"FR", "French"}, "ger": {"DE", "German"}, "deu": {"DE", "German"},
	"spa": {"ES", "Spanish"}, "ita": {"IT", "Italian"}, "jpn": {"JA", "Japanese"}, "kor": {"KO", "Korean"}, "chi": {"ZH", "Chinese"},
	"zho": {"ZH", "Chinese"}, "rus": {"RU", "Russian"}, "por": {"PT", "Portuguese"}, "dut": {"NL", "Dutch"}, "nld": {"NL", "Dutch"},
	"swe": {"SV", "Swedish"}, "nor": {"NO", "Norwegian"}, "nob": {"NB", "Norwegian Bokmål"}, "nno": {"NN", "Norwegian Nynorsk"},
	"dan": {"DA", "Danish"}, "fin": {"FI", "Finnish"}, "pol": {"PL", "Polish"}, "hun": {"HU", "Hungarian"}, "cze": {"CS", "Czech"},
	"ces": {"CS", "Czech"}, "gre": {"EL", "Greek"}, "ell": {"EL", "Greek"}, "tur": {"TR", "Turkish"}, "heb": {"HE", "Hebrew"},
	"ara": {"AR", "Arabic"}, "hin": {"HI", "Hindi"}, "tha": {"TH", "Thai"}, "vie": {"VI", "Vietnamese"}, "ind": {"ID", "Indonesian"},
	"ukr": {"UK", "Ukrainian"}, "rum": {"RO", "Romanian"}, "ron": {"RO", "Romanian"}, "bul": {"BG", "Bulgarian"}, "hrv": {"HR", "Croatian"},
	"srp": {"SR", "Serbian"}, "slv": {"SL", "Slovenian"}, "slo": {"SK", "Slovak"}, "slk": {"SK", "Slovak"}, "ice": {"IS", "Icelandic"},
	"isl": {"IS", "Icelandic"}, "est": {"ET", "Estonian"}, "lav": {"LV", "Latvian"}, "lit": {"LT", "Lithuanian"}, "tam": {"TA", "Tamil"},
	"tel": {"TE", "Telugu"}, "mal": {"ML", "Malayalam"}, "kan": {"KN", "Kannada"}, "mar": {"MR", "Marathi"}, "ben": {"BN", "Bengali"},
	"urd": {"UR", "Urdu"}, "pan": {"PA", "Punjabi"}, "guj": {"GU", "Gujarati"}, "may": {"MS", "Malay"}, "msa": {"MS", "Malay"},
	"fil": {"TL", "Filipino"}, "tgl": {"TL", "Tagalog"}, "per": {"FA", "Persian"}, "fas": {"FA", "Persian"}, "cat": {"CA", "Catalan"},
	"baq": {"EU", "Basque"}, "eus": {"EU", "Basque"}, "glg": {"GL", "Galician"}, "wel": {"CY", "Welsh"}, "cym": {"CY", "Welsh"},
	"gle": {"GA", "Irish"}, "afr": {"AF", "Afrikaans"}, "alb": {"SQ", "Albanian"}, "sqi": {"SQ", "Albanian"}, "mac": {"MK", "Macedonian"},
	"mkd": {"MK", "Macedonian"}, "bos": {"BS", "Bosnian"}, "lat": {"LA", "Latin"},
}

// normalizeLanguage lower-cases a stream's language tag, turns a two-letter
// code into its three-letter one, and drops "undetermined" markers.
func normalizeLanguage(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	switch c {
	case "", "und", "unk", "zxx", "mis", "mul", "qaa":
		return ""
	}
	if len(c) == 2 {
		for long, l := range languages {
			if strings.EqualFold(l[0], c) {
				return long
			}
		}
	}
	return c
}

// LanguageShort is the two-letter code: eng gives EN.
func LanguageShort(code string) string {
	if l, ok := languages[strings.ToLower(code)]; ok {
		return l[0]
	}
	return strings.ToUpper(code)
}

// LanguageName is the name: eng gives English.
func LanguageName(code string) string {
	if l, ok := languages[strings.ToLower(code)]; ok {
		return l[1]
	}
	return strings.ToUpper(code)
}

// LanguageNames joins codes as names: "English, French".
func LanguageNames(codes []string) string {
	names := make([]string, 0, len(codes))
	for _, c := range codes {
		names = appendUnique(names, LanguageName(c))
	}
	return strings.Join(names, ", ")
}
