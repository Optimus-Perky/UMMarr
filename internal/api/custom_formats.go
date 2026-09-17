package api

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/customformat"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Settings -> Custom Formats, as in Radarr and Sonarr v4.

type customFormatView struct {
	customformat.Format
	Summary string // the conditions in one line
}

type conditionRow struct {
	Index int
	customformat.Condition
	Kinds   []struct{ Key, Label, Help string }
	Choices []string
	IsList  bool
	IsRegex bool
	IsSize  bool
}

type customFormatFormData struct {
	Format customformat.Format
	Rows   []conditionRow
	IsNew  bool
	Error  string
}

func conditionSummary(f customformat.Format) string {
	parts := make([]string, 0, len(f.Conditions))
	for _, c := range f.Conditions {
		label := strings.TrimSuffix(c.Implementation, "Specification")
		value := c.Value
		if c.Implementation == customformat.Size {
			value = fmt.Sprintf("%.1f-%.1f GB", c.Min, c.Max)
		}
		if c.Negate {
			label = "not " + label
		}
		parts = append(parts, label+" "+value)
	}
	return strings.Join(parts, " · ")
}

func (h *handler) customFormatViews(ctx context.Context) ([]customFormatView, error) {
	formats, err := store.ListCustomFormats(ctx, h.deps.DB)
	if err != nil {
		return nil, err
	}
	out := make([]customFormatView, 0, len(formats))
	for _, f := range formats {
		out = append(out, customFormatView{Format: f, Summary: conditionSummary(f)})
	}
	return out, nil
}

func conditionRows(conds []customformat.Condition) []conditionRow {
	rows := make([]conditionRow, 0, len(conds))
	for i, c := range conds {
		rows = append(rows, newConditionRow(i, c))
	}
	return rows
}

func newConditionRow(i int, c customformat.Condition) conditionRow {
	if c.Implementation == "" {
		c.Implementation = customformat.ReleaseTitle
	}
	row := conditionRow{Index: i, Condition: c, Kinds: customformat.Kinds, Choices: customformat.Choices[c.Implementation]}
	switch c.Implementation {
	case customformat.Size:
		row.IsSize = true
	case customformat.ReleaseTitle, customformat.ReleaseGroup, customformat.Edition:
		row.IsRegex = true
	default:
		row.IsList = true
	}
	return row
}

// CustomFormatConditionRow renders one blank or retyped condition row.
func (h *handler) CustomFormatConditionRow(w http.ResponseWriter, r *http.Request) {
	index, _ := strconv.Atoi(r.FormValue("index"))
	h.renderPartial(w, "custom_format_condition", newConditionRow(index, customformat.Condition{
		Name: r.FormValue("name"), Implementation: r.FormValue("implementation"),
		Negate: r.FormValue("negate") == "on", Required: r.FormValue("required") == "on",
	}))
}

func (h *handler) NewCustomFormatForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "custom_format_form", customFormatFormData{IsNew: true, Rows: conditionRows([]customformat.Condition{{}})})
}

func (h *handler) EditCustomFormatForm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "custom format")
	if !ok {
		return
	}
	f, err := store.GetCustomFormat(r.Context(), h.deps.DB, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.renderPartial(w, "custom_format_form", customFormatFormData{Format: f, Rows: conditionRows(f.Conditions)})
}

// parseCustomFormatForm reads the editor's rows.
func parseCustomFormatForm(r *http.Request) (customformat.Format, error) {
	f := customformat.Format{Name: strings.TrimSpace(r.FormValue("name")), IncludeWhenRenaming: r.FormValue("include_when_renaming") == "on"}
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		f.ID = id
	}
	if f.Name == "" {
		return f, fmt.Errorf("Name is required.")
	}
	for i := 0; i < 100; i++ {
		implementation := r.FormValue(fmt.Sprintf("c%d_implementation", i))
		if implementation == "" {
			continue
		}
		c := customformat.Condition{
			Name:           strings.TrimSpace(r.FormValue(fmt.Sprintf("c%d_name", i))),
			Implementation: implementation,
			Negate:         r.FormValue(fmt.Sprintf("c%d_negate", i)) == "on",
			Required:       r.FormValue(fmt.Sprintf("c%d_required", i)) == "on",
			Value:          strings.TrimSpace(r.FormValue(fmt.Sprintf("c%d_value", i))),
		}
		c.Min, _ = strconv.ParseFloat(r.FormValue(fmt.Sprintf("c%d_min", i)), 64)
		c.Max, _ = strconv.ParseFloat(r.FormValue(fmt.Sprintf("c%d_max", i)), 64)
		if c.Name == "" {
			c.Name = strings.TrimSuffix(implementation, "Specification")
		}
		if err := c.Validate(); err != nil {
			return f, err
		}
		f.Conditions = append(f.Conditions, c)
	}
	if len(f.Conditions) == 0 {
		return f, fmt.Errorf("Add at least one condition.")
	}
	return f, nil
}

func (h *handler) SaveCustomFormat(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, err := parseCustomFormatForm(r)
	if err == nil {
		var taken bool
		if taken, err = store.CustomFormatNameTaken(r.Context(), h.deps.DB, f.Name, f.ID); taken {
			err = fmt.Errorf("Another custom format is already called %q.", f.Name)
		}
	}
	if err != nil {
		h.renderPartial(w, "custom_format_form", customFormatFormData{Format: f, Rows: conditionRows(f.Conditions), IsNew: f.ID == 0, Error: err.Error()})
		return
	}
	if _, err := store.SaveCustomFormat(r.Context(), h.deps.DB, f); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/custom-formats")
	w.WriteHeader(http.StatusOK)
}

func (h *handler) DeleteCustomFormat(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "custom format")
	if !ok {
		return
	}
	if err := store.DeleteCustomFormat(r.Context(), h.deps.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/custom-formats")
	w.WriteHeader(http.StatusOK)
}

// ImportCustomFormats takes JSON from Radarr, Sonarr or TRaSH Guides.
func (h *handler) ImportCustomFormats(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	from := customformat.FromRadarr
	if r.FormValue("from") == customformat.FromSonarr {
		from = customformat.FromSonarr
	}
	res, err := customformat.ParseJSON([]byte(r.FormValue("json")), from)
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	var added, renamed int
	var problems []string
	for _, f := range res.Formats {
		name := f.Name
		for i := 2; ; i++ {
			taken, err := store.CustomFormatNameTaken(r.Context(), h.deps.DB, f.Name, 0)
			if err != nil || !taken {
				break
			}
			f.Name = fmt.Sprintf("%s (%d)", name, i)
			renamed++
		}
		if _, err := store.SaveCustomFormat(r.Context(), h.deps.DB, f); err != nil {
			problems = append(problems, f.Name+": "+err.Error())
			continue
		}
		added++
	}
	problems = append(problems, res.Skipped...)
	msg := fmt.Sprintf("Imported %d custom format(s).", added)
	if renamed > 0 {
		msg += fmt.Sprintf(" %d were renamed to avoid a clash.", renamed)
	}
	if len(problems) > 0 {
		msg += " Skipped: " + strings.Join(problems, "; ")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	class := "indexer-test"
	reload := ""
	if added == 0 {
		class = "indexer-test failed"
	} else {
		reload = ` <a href="/settings/custom-formats">Reload to see them.</a>`
	}
	fmt.Fprintf(w, `<p class="%s">%s%s</p>`, class, html.EscapeString(msg), reload)
}

// ExportCustomFormats downloads every format as JSON.
func (h *handler) ExportCustomFormats(w http.ResponseWriter, r *http.Request) {
	formats, err := store.ListCustomFormats(r.Context(), h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := customformat.ExportJSON(formats)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="ummarr-custom-formats.json"`)
	w.Write(data)
}
