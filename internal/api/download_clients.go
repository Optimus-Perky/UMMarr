package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Settings > Download Clients, as in Sonarr: a card per client, an Add
// dialog offering each program, and an edit dialog with Test.

type downloadClientView struct {
	store.DownloadClient
	Label       string // the program's display name
	Protocol    string
	FromEnv     bool // the .env Deluge, shown when nothing is saved
	PasswordSet bool
	APIKeySet   bool
}

var clientLabels = map[string]string{store.ClientDeluge: "Deluge", store.ClientQBittorrent: "qBittorrent", store.ClientSABnzbd: "SABnzbd",
	store.ClientTransmission: "Transmission", store.ClientNZBGet: "NZBGet"}

func clientView(dc store.DownloadClient, fromEnv bool) downloadClientView {
	return downloadClientView{DownloadClient: dc, Label: clientLabels[dc.Implementation], Protocol: dc.Protocol(), FromEnv: fromEnv, PasswordSet: dc.Password != "", APIKeySet: dc.APIKey != ""}
}

func (h *handler) downloadClientViews(ctx context.Context) ([]downloadClientView, error) {
	rows, err := store.ListDownloadClients(ctx, h.deps.DB)
	if err != nil {
		return nil, err
	}
	views := make([]downloadClientView, 0, len(rows))
	for _, dc := range rows {
		views = append(views, clientView(dc, false))
	}
	if len(views) == 0 && h.deps.BootstrapDelugeBaseURL != "" {
		views = append(views, clientView(store.DownloadClient{Name: "Deluge", Implementation: store.ClientDeluge, Enabled: true, Priority: 1, BaseURL: h.deps.BootstrapDelugeBaseURL}, true))
	}
	return views, nil
}

type downloadClientFormData struct {
	Client downloadClientView
	IsNew  bool
	Errors map[string]string
}

func defaultDownloadClient(implementation string) store.DownloadClient {
	dc := store.DownloadClient{Implementation: implementation, Enabled: true, Priority: 1, Name: clientLabels[implementation]}
	switch implementation {
	case store.ClientDeluge:
		dc.BaseURL = "http://media-server:8112"
	case store.ClientQBittorrent:
		dc.BaseURL = "http://media-server:8080"
	case store.ClientSABnzbd:
		dc.BaseURL = "http://media-server:8085"
	case store.ClientTransmission:
		dc.BaseURL = "http://media-server:9091"
	case store.ClientNZBGet:
		dc.BaseURL = "http://media-server:6789"
	}
	return dc
}

// parseDownloadClientForm reads the dialog over existing (which supplies
// the id and any secret left blank).
func parseDownloadClientForm(r *http.Request, existing store.DownloadClient) (store.DownloadClient, map[string]string) {
	dc := existing
	errs := map[string]string{}
	dc.Name = strings.TrimSpace(r.FormValue("name"))
	if dc.Name == "" {
		errs["name"] = "Name is required."
	}
	if impl := r.FormValue("implementation"); impl != "" {
		dc.Implementation = impl
	}
	if clientLabels[dc.Implementation] == "" {
		errs["implementation"] = "Choose Deluge, qBittorrent or SABnzbd."
	}
	dc.Enabled = r.FormValue("enabled") == "on"
	dc.BaseURL = strings.TrimRight(strings.TrimSpace(r.FormValue("base_url")), "/")
	if !strings.HasPrefix(dc.BaseURL, "http://") && !strings.HasPrefix(dc.BaseURL, "https://") {
		errs["base_url"] = "URL must start with http:// or https://."
	}
	dc.Username = strings.TrimSpace(r.FormValue("username"))
	dc.Password = r.FormValue("password")
	dc.APIKey = strings.TrimSpace(r.FormValue("api_key"))
	if dc.Implementation == store.ClientSABnzbd && dc.APIKey == "" && existing.APIKey == "" {
		errs["api_key"] = "SABnzbd needs its API key (Config → General)."
	}
	dc.Category = strings.TrimSpace(r.FormValue("category"))
	dc.RemoveFailed = r.FormValue("remove_failed") == "on"
	dc.RemotePath = strings.TrimSpace(r.FormValue("remote_path"))
	dc.LocalPath = strings.TrimSpace(r.FormValue("local_path"))
	if (dc.RemotePath == "") != (dc.LocalPath == "") {
		errs["remote_path"] = "Give both the client's path and UMMarr's path, or neither."
	}
	if p, err := strconv.Atoi(r.FormValue("priority")); err != nil || p < 1 || p > 50 {
		errs["priority"] = "Priority is 1 to 50."
	} else {
		dc.Priority = p
	}
	return dc, errs
}

func (h *handler) NewDownloadClientForm(w http.ResponseWriter, r *http.Request) {
	implementation := r.URL.Query().Get("implementation")
	if clientLabels[implementation] == "" {
		http.Error(w, "implementation must be deluge, qbittorrent, transmission, sabnzbd or nzbget", http.StatusBadRequest)
		return
	}
	h.renderPartial(w, "download_client_form", downloadClientFormData{Client: clientView(defaultDownloadClient(implementation), false), IsNew: true})
}

func (h *handler) downloadClientFromPath(w http.ResponseWriter, r *http.Request) (store.DownloadClient, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid download client id", http.StatusBadRequest)
		return store.DownloadClient{}, false
	}
	dc, err := store.GetDownloadClient(r.Context(), h.deps.DB, id)
	if errors.Is(err, store.ErrDownloadClientNotFound) {
		http.NotFound(w, r)
		return store.DownloadClient{}, false
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return store.DownloadClient{}, false
	}
	return dc, true
}

func (h *handler) EditDownloadClientForm(w http.ResponseWriter, r *http.Request) {
	if dc, ok := h.downloadClientFromPath(w, r); ok {
		h.renderPartial(w, "download_client_form", downloadClientFormData{Client: clientView(dc, false)})
	}
}

func (h *handler) saveDownloadClient(w http.ResponseWriter, r *http.Request, existing store.DownloadClient, isNew bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dc, errs := parseDownloadClientForm(r, existing)
	if taken, err := store.DownloadClientNameTaken(r.Context(), h.deps.DB, dc.Name, dc.ID); err == nil && taken {
		errs["name"] = "Another download client already has that name."
	}
	if len(errs) > 0 {
		view := clientView(dc, false)
		view.PasswordSet, view.APIKeySet = existing.Password != "", existing.APIKey != ""
		h.renderPartial(w, "download_client_form", downloadClientFormData{Client: view, IsNew: isNew, Errors: errs})
		return
	}
	var err error
	if isNew {
		_, err = store.CreateDownloadClient(r.Context(), h.deps.DB, dc)
	} else {
		err = store.UpdateDownloadClient(r.Context(), h.deps.DB, dc)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/download-clients")
	w.WriteHeader(http.StatusOK)
}

func (h *handler) CreateDownloadClient(w http.ResponseWriter, r *http.Request) {
	h.saveDownloadClient(w, r, defaultDownloadClient(r.FormValue("implementation")), true)
}

func (h *handler) UpdateDownloadClient(w http.ResponseWriter, r *http.Request) {
	if existing, ok := h.downloadClientFromPath(w, r); ok {
		h.saveDownloadClient(w, r, existing, false)
	}
}

func (h *handler) DeleteDownloadClient(w http.ResponseWriter, r *http.Request) {
	dc, ok := h.downloadClientFromPath(w, r)
	if !ok {
		return
	}
	if err := store.DeleteDownloadClient(r.Context(), h.deps.DB, dc.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/download-clients")
	w.WriteHeader(http.StatusOK)
}

// TestDownloadClient tests the dialog's current values without saving them;
// a blank secret tests with the saved one.
func (h *handler) TestDownloadClient(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	base := defaultDownloadClient(r.FormValue("implementation"))
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		if saved, err := store.GetDownloadClient(r.Context(), h.deps.DB, id); err == nil {
			base = saved
		}
	}
	dc, errs := parseDownloadClientForm(r, base)
	if dc.Password == "" {
		dc.Password = base.Password
	}
	if dc.APIKey == "" {
		dc.APIKey = base.APIKey
	}
	if len(errs) > 0 {
		var messages []string
		for _, name := range []string{"name", "implementation", "base_url", "api_key", "remote_path", "priority"} {
			if msg, ok := errs[name]; ok {
				messages = append(messages, msg)
			}
		}
		h.renderPartial(w, "indexer_test_result", indexerTestResult{Message: strings.Join(messages, " ")})
		return
	}
	if h.deps.Download == nil {
		h.renderPartial(w, "indexer_test_result", indexerTestResult{Message: "download service isn't available"})
		return
	}
	if err := h.deps.Download.TestClient(r.Context(), dc); err != nil {
		h.renderPartial(w, "indexer_test_result", indexerTestResult{Message: err.Error()})
		return
	}
	h.renderPartial(w, "indexer_test_result", indexerTestResult{OK: true})
}

// UpdateDownloadHandling saves Settings -> Download Clients -> Failed
// Download Handling.
func (h *handler) UpdateDownloadHandling(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	d := store.DownloadHandling{RedownloadFailed: r.FormValue("redownload_failed") == "on"}
	if err := store.UpdateDownloadHandling(r.Context(), h.deps.DB, d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/download-clients")
	w.WriteHeader(http.StatusOK)
}
