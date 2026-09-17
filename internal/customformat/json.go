package customformat

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Import and export in Radarr's and Sonarr's JSON shape - what TRaSH Guides
// publishes and what their "Export" buttons produce:
//
//	{"name": "...", "includeCustomFormatWhenRenaming": false,
//	 "specifications": [{"name": "...", "implementation": "ReleaseTitleSpecification",
//	   "negate": false, "required": true, "fields": {"value": "\\bREMUX\\b"}}]}
//
// Radarr and Sonarr number sources, flags and languages differently, so a
// number is read as the app the JSON came from; UMMarr's own export writes
// names, which read the same either way.

// Apps a JSON import can come from.
const (
	FromRadarr = "radarr"
	FromSonarr = "sonarr"
)

type jsonFormat struct {
	Name                string     `json:"name"`
	IncludeWhenRenaming bool       `json:"includeCustomFormatWhenRenaming"`
	Specifications      []jsonSpec `json:"specifications"`
}

type jsonSpec struct {
	Name           string          `json:"name"`
	Implementation string          `json:"implementation"`
	Negate         bool            `json:"negate"`
	Required       bool            `json:"required"`
	Fields         json.RawMessage `json:"fields"`
}

var radarrSources = map[int]string{5: "DVD", 6: "HDTV", 7: "WEBDL", 8: "WEBRip", 9: "Bluray"}
var sonarrSources = map[int]string{1: "HDTV", 2: "HDTV", 3: "WEBDL", 4: "WEBRip", 5: "DVD", 6: "Bluray"}
var radarrModifiers = map[int]string{5: "REMUX"}
var radarrFlags = map[int]string{1: "Freeleech", 2: "Halfleech", 4: "Double Upload", 32: "Internal", 64: "Internal", 128: "Scene", 256: "Freeleech 75%", 512: "Freeleech 25%", 2048: "Nuked"}
var sonarrFlags = map[int]string{1: "Freeleech", 2: "Halfleech", 4: "Double Upload", 8: "Internal", 16: "Scene", 32: "Freeleech 75%", 64: "Freeleech 25%", 128: "Nuked"}
var releaseTypes = map[int]string{1: "SingleEpisode", 2: "MultiEpisode", 3: "SeasonPack"}

// languageIDs are the ids Radarr and Sonarr share.
var languageIDs = map[int]string{1: "English", 2: "French", 3: "Spanish", 4: "German", 5: "Italian", 6: "Danish", 7: "Dutch", 8: "Japanese",
	10: "Chinese", 11: "Russian", 12: "Polish", 14: "Swedish", 15: "Norwegian", 16: "Finnish", 17: "Turkish", 18: "Portuguese", 19: "Flemish",
	20: "Greek", 21: "Korean", 22: "Hungarian", 23: "Hebrew", 25: "Czech"}

// fieldsOf reads a specification's fields, which Radarr's API sends as a list
// of {name, value} and TRaSH as an object.
func fieldsOf(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	if json.Unmarshal(raw, &out) == nil {
		return out
	}
	var list []struct {
		Name  string `json:"name"`
		Value any    `json:"value"`
	}
	if json.Unmarshal(raw, &list) == nil {
		for _, f := range list {
			out[f.Name] = f.Value
		}
	}
	return out
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// ImportResult is how importing a batch of formats went.
type ImportResult struct {
	Formats []Format
	Skipped []string // "Name: why"
}

// ParseJSON reads one format or a list of them. from is FromRadarr or
// FromSonarr, for numbered values.
func ParseJSON(data []byte, from string) (ImportResult, error) {
	var list []jsonFormat
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(data, &list); err != nil {
			return ImportResult{}, fmt.Errorf("not custom format JSON: %w", err)
		}
	} else {
		var one jsonFormat
		if err := json.Unmarshal(data, &one); err != nil {
			return ImportResult{}, fmt.Errorf("not custom format JSON: %w", err)
		}
		list = []jsonFormat{one}
	}
	var res ImportResult
	for _, jf := range list {
		if strings.TrimSpace(jf.Name) == "" {
			res.Skipped = append(res.Skipped, "a format with no name")
			continue
		}
		f := Format{Name: strings.TrimSpace(jf.Name), IncludeWhenRenaming: jf.IncludeWhenRenaming}
		var problem string
		for _, s := range jf.Specifications {
			c, err := conditionFromJSON(s, from)
			if err != nil {
				problem = err.Error()
				break
			}
			f.Conditions = append(f.Conditions, c)
		}
		if problem == "" && len(f.Conditions) == 0 {
			problem = "it has no conditions"
		}
		if problem != "" {
			res.Skipped = append(res.Skipped, f.Name+": "+problem)
			continue
		}
		res.Formats = append(res.Formats, f)
	}
	return res, nil
}

func conditionFromJSON(s jsonSpec, from string) (Condition, error) {
	c := Condition{Name: s.Name, Implementation: s.Implementation, Negate: s.Negate, Required: s.Required}
	fields := fieldsOf(s.Fields)
	value := fields["value"]
	lookup := func(named map[int]string) (string, error) {
		if str, ok := value.(string); ok {
			if _, err := strconv.Atoi(str); err != nil {
				return str, nil // UMMarr's own export writes names
			}
		}
		n, ok := number(value)
		if !ok {
			return "", fmt.Errorf("%q has no value", s.Name)
		}
		name, ok := named[int(n)]
		if !ok {
			return "", fmt.Errorf("%q uses a value UMMarr can't detect (%v)", s.Name, value)
		}
		return name, nil
	}
	var err error
	switch s.Implementation {
	case ReleaseTitle, ReleaseGroup, Edition:
		c.Value, _ = value.(string)
	case Source:
		if from == FromSonarr {
			c.Value, err = lookup(sonarrSources)
		} else {
			c.Value, err = lookup(radarrSources)
		}
	case Resolution:
		if str, ok := value.(string); ok && strings.HasSuffix(str, "p") {
			c.Value = str
		} else if n, ok := number(value); ok {
			c.Value = fmt.Sprintf("%dp", int(n))
		}
	case QualityModifier:
		c.Value, err = lookup(radarrModifiers)
	case IndexerFlag:
		if from == FromSonarr {
			c.Value, err = lookup(sonarrFlags)
		} else {
			c.Value, err = lookup(radarrFlags)
		}
	case Language:
		c.Value, err = lookup(languageIDs)
	case ReleaseType:
		c.Value, err = lookup(releaseTypes)
	case Size:
		c.Min, _ = number(fields["min"])
		c.Max, _ = number(fields["max"])
	default:
		return c, fmt.Errorf("%q is a %s, which UMMarr doesn't support", s.Name, strings.TrimSuffix(s.Implementation, "Specification"))
	}
	if err != nil {
		return c, err
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// ExportJSON writes formats in the shape ParseJSON reads, with names for
// values.
func ExportJSON(formats []Format) ([]byte, error) {
	out := make([]jsonFormat, 0, len(formats))
	for _, f := range formats {
		jf := jsonFormat{Name: f.Name, IncludeWhenRenaming: f.IncludeWhenRenaming, Specifications: []jsonSpec{}}
		for _, c := range f.Conditions {
			fields := map[string]any{"value": c.Value}
			if c.Implementation == Size {
				fields = map[string]any{"min": c.Min, "max": c.Max}
			}
			raw, _ := json.Marshal(fields)
			jf.Specifications = append(jf.Specifications, jsonSpec{Name: c.Name, Implementation: c.Implementation, Negate: c.Negate, Required: c.Required, Fields: raw})
		}
		out = append(out, jf)
	}
	return json.MarshalIndent(out, "", "  ")
}
