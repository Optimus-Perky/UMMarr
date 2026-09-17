package api_test

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// Every Settings panel that opens a form does it the same way: a button
// htmx-swaps the form into <div id="X-dialog-body"> inside
// <dialog id="X-dialog">, and one shared htmx:afterSwap listener calls
// showModal() on whichever dialog the swapped body belongs to.
//
// Metadata Sources and Custom Formats had the markup but no opener - the
// listener named the four dialogs that existed when it was written - so
// the GET succeeded, the form landed in a dialog nobody opened, and every
// button on those panels looked dead. These tests pin both halves of the
// convention: the dialog a button targets must exist on the same page, and
// the opener must be the generic one rather than a list of ids.

var (
	dialogTargetRe = regexp.MustCompile(`hx-target="#([a-z0-9-]+-dialog-body)"`)
	dialogIDRe     = regexp.MustCompile(`<dialog[^>]*\bid="([a-z0-9-]+-dialog)"`)
	bodyIDRe       = regexp.MustCompile(`\bid="([a-z0-9-]+-dialog-body)"`)
	settingsTabRe  = regexp.MustCompile(`href="/settings/([a-z-]+)"`)
)

// settingsTabSlugs reads the tabs off the Settings page itself, so a tab
// added later is covered without touching these tests.
func settingsTabSlugs(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	status, body := get(t, srv, "/settings")
	if status != 200 {
		t.Fatalf("GET /settings = %d", status)
	}
	seen := map[string]bool{}
	var slugs []string
	for _, m := range settingsTabRe.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			slugs = append(slugs, m[1])
		}
	}
	if len(slugs) < 5 {
		t.Fatalf("want the Settings tabs linked from /settings, found %v", slugs)
	}
	return slugs
}

func TestSettingsTabs_EveryDialogTargetHasItsDialog(t *testing.T) {
	srv := newTestServer(t)
	for _, tab := range settingsTabSlugs(t, srv) {
		status, body := get(t, srv, "/settings/"+tab)
		if status != 200 {
			t.Fatalf("GET /settings/%s = %d", tab, status)
		}
		dialogs := map[string]bool{}
		for _, m := range dialogIDRe.FindAllStringSubmatch(body, -1) {
			dialogs[m[1]] = true
		}
		bodies := map[string]bool{}
		for _, m := range bodyIDRe.FindAllStringSubmatch(body, -1) {
			bodies[m[1]] = true
		}
		for _, m := range dialogTargetRe.FindAllStringSubmatch(body, -1) {
			target := m[1]
			if !bodies[target] {
				t.Errorf("/settings/%s: a button swaps into #%s, which the page never renders", tab, target)
				continue
			}
			// The shared opener derives the dialog's id from the body's:
			// "X-dialog-body" must sit inside <dialog id="X-dialog">.
			if want := strings.TrimSuffix(target, "-body"); !dialogs[want] {
				t.Errorf("/settings/%s: #%s has no <dialog id=%q> to open, so the form swaps in invisibly", tab, target, want)
			}
		}
	}
}

func TestSettingsTabs_DialogOpenerIsGeneric(t *testing.T) {
	srv := newTestServer(t)
	for _, tab := range settingsTabSlugs(t, srv) {
		_, body := get(t, srv, "/settings/"+tab)
		if !strings.Contains(body, "ummarrSettingsDialogs") || !strings.Contains(body, "showModal()") {
			t.Errorf("/settings/%s: the shared dialog opener is missing, so its dialogs never show", tab)
		}
		// A per-dialog id inside the opener means the next panel added
		// will be dead on arrival again.
		if strings.Contains(body, "getElementById('indexer-dialog')") {
			t.Errorf("/settings/%s: the opener still names dialogs one by one", tab)
		}
	}
}
