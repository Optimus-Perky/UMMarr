package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/notify"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Settings > Connect, as in Sonarr: a card per notification, an Add dialog
// offering each service, and an edit dialog with Test.

var notifyLabels = map[string]string{
	store.NotifyDiscord: "Discord", store.NotifyWebhook: "Webhook", store.NotifyEmail: "Email",
	store.NotifyPlex: "Plex Media Server", store.NotifyPushover: "Pushover", store.NotifyTelegram: "Telegram",
}

// notifySecrets are settings the form never shows back.
var notifySecrets = map[string]bool{"webhook_url": true, "password": true, "token": true, "api_token": true, "user_key": true, "bot_token": true}

type notificationView struct {
	store.Notification
	Label  string
	Events string // the ticked events, for the card
	Set    map[string]bool
}

func notificationViewOf(n store.Notification) notificationView {
	v := notificationView{Notification: n, Label: notifyLabels[n.Implementation], Set: map[string]bool{}}
	var on []string
	for _, f := range []struct {
		ok   bool
		name string
	}{{n.OnGrab, "Grab"}, {n.OnImport, "Import"}, {n.OnUpgrade, "Upgrade"}, {n.OnRename, "Rename"}, {n.OnDelete, "Delete"}, {n.OnAdded, "Added"}, {n.OnFailed, "Failure"}, {n.OnHealth, "Health"}} {
		if f.ok {
			on = append(on, f.name)
		}
	}
	v.Events = strings.Join(on, ", ")
	for k, val := range n.Settings {
		v.Set[k] = val != ""
	}
	return v
}

type notificationFormData struct {
	Notification notificationView
	IsNew        bool
	Errors       map[string]string
}

func defaultNotification(implementation string) store.Notification {
	n := store.Notification{Implementation: implementation, Enabled: true, Name: notifyLabels[implementation], Settings: map[string]string{},
		OnGrab: true, OnImport: true, OnUpgrade: true, OnFailed: true, OnHealth: true}
	switch implementation {
	case store.NotifyEmail:
		n.Settings["smtp_port"] = "587"
	case store.NotifyWebhook:
		n.Settings["method"] = "POST"
	case store.NotifyPlex:
		n.Settings["server_url"] = "http://media-server:32400"
		n.OnGrab, n.OnFailed, n.OnHealth, n.OnRename, n.OnDelete = false, false, false, true, true
	}
	return n
}

// notifyFields lists each implementation's settings fields, in form order.
var notifyFields = map[string][]string{
	store.NotifyDiscord:  {"webhook_url", "username"},
	store.NotifyWebhook:  {"url", "method", "username", "password"},
	store.NotifyEmail:    {"smtp_host", "smtp_port", "username", "password", "from", "to"},
	store.NotifyPlex:     {"server_url", "token"},
	store.NotifyPushover: {"api_token", "user_key", "priority"},
	store.NotifyTelegram: {"bot_token", "chat_id"},
}

var notifyRequired = map[string][]string{
	store.NotifyDiscord:  {"webhook_url"},
	store.NotifyWebhook:  {"url"},
	store.NotifyEmail:    {"smtp_host", "from", "to"},
	store.NotifyPlex:     {"server_url", "token"},
	store.NotifyPushover: {"api_token", "user_key"},
	store.NotifyTelegram: {"bot_token", "chat_id"},
}

func parseNotificationForm(r *http.Request, existing store.Notification) (store.Notification, map[string]string) {
	n := existing
	errs := map[string]string{}
	n.Name = strings.TrimSpace(r.FormValue("name"))
	if n.Name == "" {
		errs["name"] = "Name is required."
	}
	if impl := r.FormValue("implementation"); impl != "" {
		n.Implementation = impl
	}
	if notifyLabels[n.Implementation] == "" {
		errs["implementation"] = "Unknown notification type."
	}
	n.Enabled = r.FormValue("enabled") == "on"
	n.OnGrab, n.OnImport, n.OnUpgrade, n.OnRename = r.FormValue("on_grab") == "on", r.FormValue("on_import") == "on", r.FormValue("on_upgrade") == "on", r.FormValue("on_rename") == "on"
	n.OnDelete, n.OnAdded, n.OnFailed, n.OnHealth = r.FormValue("on_delete") == "on", r.FormValue("on_added") == "on", r.FormValue("on_failed") == "on", r.FormValue("on_health") == "on"
	settings := map[string]string{}
	for k, v := range existing.Settings {
		settings[k] = v
	}
	for _, field := range notifyFields[n.Implementation] {
		v := strings.TrimSpace(r.FormValue(field))
		if v == "" && notifySecrets[field] {
			continue // blank keeps the saved secret
		}
		settings[field] = v
	}
	n.Settings = settings
	for _, field := range notifyRequired[n.Implementation] {
		if settings[field] == "" {
			errs[field] = "Required."
		}
	}
	return n, errs
}

func (h *handler) notificationViews(ctx context.Context) ([]notificationView, error) {
	rows, err := store.ListNotifications(ctx, h.deps.DB)
	if err != nil {
		return nil, err
	}
	views := make([]notificationView, 0, len(rows))
	for _, n := range rows {
		views = append(views, notificationViewOf(n))
	}
	return views, nil
}

func (h *handler) NewNotificationForm(w http.ResponseWriter, r *http.Request) {
	implementation := r.URL.Query().Get("implementation")
	if notifyLabels[implementation] == "" {
		http.Error(w, "unknown notification type", http.StatusBadRequest)
		return
	}
	h.renderPartial(w, "notification_form", notificationFormData{Notification: notificationViewOf(defaultNotification(implementation)), IsNew: true})
}

func (h *handler) notificationFromPath(w http.ResponseWriter, r *http.Request) (store.Notification, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid notification id", http.StatusBadRequest)
		return store.Notification{}, false
	}
	n, err := store.GetNotification(r.Context(), h.deps.DB, id)
	if errors.Is(err, store.ErrNotificationNotFound) {
		http.NotFound(w, r)
		return store.Notification{}, false
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return store.Notification{}, false
	}
	return n, true
}

func (h *handler) EditNotificationForm(w http.ResponseWriter, r *http.Request) {
	if n, ok := h.notificationFromPath(w, r); ok {
		h.renderPartial(w, "notification_form", notificationFormData{Notification: notificationViewOf(n)})
	}
}

func (h *handler) saveNotification(w http.ResponseWriter, r *http.Request, existing store.Notification, isNew bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	n, errs := parseNotificationForm(r, existing)
	if taken, err := store.NotificationNameTaken(r.Context(), h.deps.DB, n.Name, n.ID); err == nil && taken {
		errs["name"] = "Another notification already has that name."
	}
	if len(errs) > 0 {
		h.renderPartial(w, "notification_form", notificationFormData{Notification: notificationViewOf(n), IsNew: isNew, Errors: errs})
		return
	}
	var err error
	if isNew {
		_, err = store.CreateNotification(r.Context(), h.deps.DB, n)
	} else {
		err = store.UpdateNotification(r.Context(), h.deps.DB, n)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/connect")
	w.WriteHeader(http.StatusOK)
}

func (h *handler) CreateNotification(w http.ResponseWriter, r *http.Request) {
	h.saveNotification(w, r, defaultNotification(r.FormValue("implementation")), true)
}

func (h *handler) UpdateNotification(w http.ResponseWriter, r *http.Request) {
	if existing, ok := h.notificationFromPath(w, r); ok {
		h.saveNotification(w, r, existing, false)
	}
}

func (h *handler) DeleteNotification(w http.ResponseWriter, r *http.Request) {
	n, ok := h.notificationFromPath(w, r)
	if !ok {
		return
	}
	if err := store.DeleteNotification(r.Context(), h.deps.DB, n.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/connect")
	w.WriteHeader(http.StatusOK)
}

// TestNotification sends a test message with the dialog's current values.
func (h *handler) TestNotification(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	base := defaultNotification(r.FormValue("implementation"))
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		if saved, err := store.GetNotification(r.Context(), h.deps.DB, id); err == nil {
			base = saved
		}
	}
	n, errs := parseNotificationForm(r, base)
	if len(errs) > 0 {
		var messages []string
		for _, field := range append([]string{"name"}, notifyFields[n.Implementation]...) {
			if msg, ok := errs[field]; ok {
				messages = append(messages, field+": "+msg)
			}
		}
		h.renderPartial(w, "indexer_test_result", indexerTestResult{Message: strings.Join(messages, " ")})
		return
	}
	if h.deps.Notifier == nil {
		h.renderPartial(w, "indexer_test_result", indexerTestResult{Message: "notifications aren't available"})
		return
	}
	if err := h.deps.Notifier.Test(r.Context(), n); err != nil {
		h.renderPartial(w, "indexer_test_result", indexerTestResult{Message: err.Error()})
		return
	}
	h.renderPartial(w, "indexer_test_result", indexerTestResult{OK: true, Message: "Test sent."})
}

var _ = notify.Compose
