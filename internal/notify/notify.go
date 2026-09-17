// Package notify tells the outside world what happened - Sonarr's Connect:
// Discord, a plain webhook, email, Plex library refresh, Pushover, Telegram.
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Service sends every recorded event to the notifications that want it.
type Service struct {
	DB        *sql.DB
	UserAgent string
	HTTP      *http.Client
	// Sync sends in the caller's goroutine (tests); otherwise sends run in
	// the background so a slow Discord never delays a grab.
	Sync bool
}

func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// OnEvent is the sync.Listener hook.
func (s *Service) OnEvent(ctx context.Context, e store.Event) {
	if s == nil || s.DB == nil {
		return
	}
	notifications, err := store.ListNotifications(ctx, s.DB)
	if err != nil {
		log.Printf("notify: %v", err)
		return
	}
	for _, n := range notifications {
		if !n.Enabled || !n.Wants(e.Event) {
			continue
		}
		send := func(n store.Notification) {
			if err := s.Send(context.Background(), n, e); err != nil {
				log.Printf("notify %s (%s): %v", n.Name, n.Implementation, err)
			}
		}
		if s.Sync {
			send(n)
		} else {
			go send(n)
		}
	}
}

// Message is what a notification says about an event.
type Message struct {
	Subject string
	Body    string
	Color   int // for Discord embeds
}

var eventWords = map[string]string{
	store.EventGrabbed: "Grabbed", store.EventImported: "Imported", store.EventUpgraded: "Upgraded", store.EventRenamed: "Renamed",
	store.EventDeleted: "Deleted", store.EventAdded: "Added", store.EventFailed: "Download failed", store.EventImportFailed: "Import failed",
	store.EventNeedsExtraction: "Needs extraction", store.EventMatched: "Match fixed", store.EventHealth: "Health",
}

// Compose words an event.
func Compose(e store.Event) Message {
	word := eventWords[e.Event]
	if word == "" {
		word = e.Event
	}
	m := Message{Subject: fmt.Sprintf("UMMarr - %s: %s", word, e.Title), Color: 0xa855f7}
	var lines []string
	if e.Detail != "" {
		lines = append(lines, e.Detail)
	}
	if e.Quality != "" {
		lines = append(lines, "Quality: "+e.Quality)
	}
	if e.Source != "" {
		lines = append(lines, "Source: "+e.Source)
	}
	m.Body = strings.Join(lines, "\n")
	switch e.Event {
	case store.EventFailed, store.EventImportFailed, store.EventNeedsExtraction, store.EventHealth:
		m.Color = 0xd98f3e
	case store.EventImported, store.EventUpgraded:
		m.Color = 0x6fbe8e
	}
	return m
}

// TestEvent is what Test sends.
func TestEvent() store.Event {
	return store.Event{Event: store.EventGrabbed, MediaType: "movie", Title: "UMMarr test", Detail: "This is a test notification from UMMarr.", Source: "test", Added: time.Now()}
}

// Send delivers e to n.
func (s *Service) Send(ctx context.Context, n store.Notification, e store.Event) error {
	m := Compose(e)
	switch n.Implementation {
	case store.NotifyDiscord:
		return s.discord(ctx, n.Settings, m, e)
	case store.NotifyWebhook:
		return s.webhook(ctx, n.Settings, m, e)
	case store.NotifyEmail:
		return s.email(n.Settings, m)
	case store.NotifyPlex:
		return s.plex(ctx, n.Settings, e)
	case store.NotifyPushover:
		return s.pushover(ctx, n.Settings, m)
	case store.NotifyTelegram:
		return s.telegram(ctx, n.Settings, m)
	}
	return fmt.Errorf("unknown notification %q", n.Implementation)
}

// Test checks a notification's settings by sending a test message (Plex:
// by listing its libraries).
func (s *Service) Test(ctx context.Context, n store.Notification) error {
	if n.Implementation == store.NotifyPlex {
		_, err := s.plexSections(ctx, n.Settings)
		return err
	}
	return s.Send(ctx, n, TestEvent())
}

func (s *Service) post(ctx context.Context, rawURL, contentType string, body []byte, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		reply, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(reply)))
	}
	return nil
}

func (s *Service) discord(ctx context.Context, settings map[string]string, m Message, e store.Event) error {
	webhook := settings["webhook_url"]
	if !strings.HasPrefix(webhook, "https://discord.com/api/webhooks/") && !strings.HasPrefix(webhook, "https://discordapp.com/api/webhooks/") {
		return fmt.Errorf("discord: the webhook URL should start with https://discord.com/api/webhooks/")
	}
	payload := map[string]any{
		"username": firstNonEmpty(settings["username"], "UMMarr"),
		"embeds": []map[string]any{{
			"title": m.Subject, "description": m.Body, "color": m.Color, "timestamp": time.Now().UTC().Format(time.RFC3339),
			"footer": map[string]any{"text": "UMMarr · " + e.MediaType},
		}},
	}
	body, _ := json.Marshal(payload)
	return s.post(ctx, webhook, "application/json", body, nil)
}

func (s *Service) webhook(ctx context.Context, settings map[string]string, m Message, e store.Event) error {
	target := settings["url"]
	if _, err := url.ParseRequestURI(target); err != nil {
		return fmt.Errorf("webhook: bad URL")
	}
	payload := map[string]any{
		"eventType": e.Event, "mediaType": e.MediaType, "title": e.Title, "detail": e.Detail, "source": e.Source, "quality": e.Quality,
		"subject": m.Subject, "message": m.Body, "time": time.Now().UTC().Format(time.RFC3339),
		"movieId": e.MovieID.Int64, "seriesId": e.SeriesID.Int64, "albumId": e.AlbumID.Int64,
	}
	body, _ := json.Marshal(payload)
	headers := map[string]string{}
	if u, p := settings["username"], settings["password"]; u != "" {
		req, _ := http.NewRequest(http.MethodGet, "http://x", nil)
		req.SetBasicAuth(u, p)
		headers["Authorization"] = req.Header.Get("Authorization")
	}
	method := strings.ToUpper(firstNonEmpty(settings["method"], "POST"))
	if method == "POST" {
		return s.post(ctx, target, "application/json", body, headers)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *Service) pushover(ctx context.Context, settings map[string]string, m Message) error {
	if settings["api_token"] == "" || settings["user_key"] == "" {
		return fmt.Errorf("pushover: API token and user key are required")
	}
	form := url.Values{"token": {settings["api_token"]}, "user": {settings["user_key"]}, "title": {m.Subject}, "message": {m.Body}}
	if p := settings["priority"]; p != "" {
		form.Set("priority", p)
	}
	return s.post(ctx, firstNonEmpty(settings["api_url"], "https://api.pushover.net/1/messages.json"), "application/x-www-form-urlencoded", []byte(form.Encode()), nil)
}

func (s *Service) telegram(ctx context.Context, settings map[string]string, m Message) error {
	if settings["bot_token"] == "" || settings["chat_id"] == "" {
		return fmt.Errorf("telegram: bot token and chat id are required")
	}
	payload, _ := json.Marshal(map[string]any{"chat_id": settings["chat_id"], "text": m.Subject + "\n" + m.Body})
	base := firstNonEmpty(settings["api_url"], "https://api.telegram.org")
	return s.post(ctx, base+"/bot"+settings["bot_token"]+"/sendMessage", "application/json", payload, nil)
}

// email sends over SMTP: implicit TLS on port 465, STARTTLS when offered
// otherwise, PLAIN auth when a username is set.
func (s *Service) email(settings map[string]string, m Message) error {
	host, port := settings["smtp_host"], firstNonEmpty(settings["smtp_port"], "587")
	from, to := settings["from"], settings["to"]
	if host == "" || from == "" || to == "" {
		return fmt.Errorf("email: SMTP host, from and to are required")
	}
	addr := net.JoinHostPort(host, port)
	var conn net.Conn
	var err error
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	if port == "465" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}
	defer c.Close()
	if port != "465" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return fmt.Errorf("email: starttls: %w", err)
			}
		}
	}
	if user := settings["username"]; user != "" {
		if err := c.Auth(smtp.PlainAuth("", user, settings["password"], host)); err != nil {
			return fmt.Errorf("email: login: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	recipients := strings.Split(to, ",")
	for _, rcpt := range recipients {
		if rcpt = strings.TrimSpace(rcpt); rcpt != "" {
			if err := c.Rcpt(rcpt); err != nil {
				return fmt.Errorf("email: %w", err)
			}
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", from, to, m.Subject, m.Body)
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

type plexSection struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	Name string `json:"title"`
}

func (s *Service) plexSections(ctx context.Context, settings map[string]string) ([]plexSection, error) {
	base, token := strings.TrimRight(settings["server_url"], "/"), settings["token"]
	if base == "" || token == "" {
		return nil, fmt.Errorf("plex: server URL and token are required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/library/sections", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", token)
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("plex: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("plex: the token was refused")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plex: HTTP %d", resp.StatusCode)
	}
	var out struct {
		MediaContainer struct {
			Directory []plexSection `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("plex: bad reply: %w", err)
	}
	return out.MediaContainer.Directory, nil
}

// plex refreshes the libraries of the event's media type - only for events
// that changed files.
func (s *Service) plex(ctx context.Context, settings map[string]string, e store.Event) error {
	switch e.Event {
	case store.EventImported, store.EventUpgraded, store.EventRenamed, store.EventDeleted:
	default:
		return nil
	}
	want := map[string]string{"movie": "movie", "series": "show", "music": "artist"}[e.MediaType]
	sections, err := s.plexSections(ctx, settings)
	if err != nil {
		return err
	}
	base, token := strings.TrimRight(settings["server_url"], "/"), settings["token"]
	refreshed := 0
	for _, sec := range sections {
		if want != "" && sec.Type != want {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/library/sections/"+sec.Key+"/refresh", nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Plex-Token", token)
		resp, err := s.client().Do(req)
		if err != nil {
			return fmt.Errorf("plex: %w", err)
		}
		resp.Body.Close()
		refreshed++
	}
	if refreshed == 0 && want != "" {
		return fmt.Errorf("plex: no %s library to refresh", want)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
