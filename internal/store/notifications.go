package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Notification implementations.
const (
	NotifyDiscord  = "discord"
	NotifyWebhook  = "webhook"
	NotifyEmail    = "email"
	NotifyPlex     = "plex"
	NotifyPushover = "pushover"
	NotifyTelegram = "telegram"
)

// Notification mirrors one notifications row (migration 00024).
type Notification struct {
	ID             int64
	Name           string
	Implementation string
	Enabled        bool
	Settings       map[string]string // the implementation's own fields
	OnGrab         bool
	OnImport       bool
	OnUpgrade      bool
	OnRename       bool
	OnDelete       bool
	OnAdded        bool
	OnFailed       bool
	OnHealth       bool
}

// Wants reports whether the notification is for events of kind event.
func (n Notification) Wants(event string) bool {
	switch event {
	case EventGrabbed:
		return n.OnGrab
	case EventImported:
		return n.OnImport
	case EventUpgraded:
		return n.OnUpgrade
	case EventRenamed:
		return n.OnRename
	case EventDeleted:
		return n.OnDelete
	case EventAdded, EventMatched:
		return n.OnAdded
	case EventFailed, EventImportFailed, EventNeedsExtraction:
		return n.OnFailed
	case EventHealth:
		return n.OnHealth
	}
	return false
}

// ErrNotificationNotFound means no row has that id.
var ErrNotificationNotFound = errors.New("notification not found")

const notificationColumns = `id, name, implementation, enabled, settings, on_grab, on_import, on_upgrade, on_rename, on_delete, on_added, on_failed, on_health`

func scanNotification(row interface{ Scan(...any) error }) (Notification, error) {
	var n Notification
	var settings string
	err := row.Scan(&n.ID, &n.Name, &n.Implementation, &n.Enabled, &settings, &n.OnGrab, &n.OnImport, &n.OnUpgrade, &n.OnRename, &n.OnDelete, &n.OnAdded, &n.OnFailed, &n.OnHealth)
	if err != nil {
		return n, err
	}
	n.Settings = map[string]string{}
	_ = json.Unmarshal([]byte(settings), &n.Settings)
	return n, nil
}

// ListNotifications lists every notification by name.
func ListNotifications(ctx context.Context, q Queryer) ([]Notification, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+notificationColumns+` FROM notifications ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetNotification reads one notification.
func GetNotification(ctx context.Context, q Queryer, id int64) (Notification, error) {
	n, err := scanNotification(q.QueryRowContext(ctx, `SELECT `+notificationColumns+` FROM notifications WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Notification{}, ErrNotificationNotFound
	}
	if err != nil {
		return Notification{}, fmt.Errorf("get notification %d: %w", id, err)
	}
	return n, nil
}

func notificationSettings(n Notification) (string, error) {
	if n.Settings == nil {
		return "{}", nil
	}
	b, err := json.Marshal(n.Settings)
	if err != nil {
		return "", fmt.Errorf("marshal notification settings: %w", err)
	}
	return string(b), nil
}

// CreateNotification adds a notification.
func CreateNotification(ctx context.Context, q Queryer, n Notification) (int64, error) {
	settings, err := notificationSettings(n)
	if err != nil {
		return 0, err
	}
	res, err := q.ExecContext(ctx, `
		INSERT INTO notifications (name, implementation, enabled, settings, on_grab, on_import, on_upgrade, on_rename, on_delete, on_added, on_failed, on_health)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Name, n.Implementation, n.Enabled, settings, n.OnGrab, n.OnImport, n.OnUpgrade, n.OnRename, n.OnDelete, n.OnAdded, n.OnFailed, n.OnHealth)
	if err != nil {
		return 0, fmt.Errorf("create notification: %w", err)
	}
	return res.LastInsertId()
}

// UpdateNotification saves a notification (Settings replaces the stored
// map wholesale; the caller keeps secrets the form left blank).
func UpdateNotification(ctx context.Context, q Queryer, n Notification) error {
	settings, err := notificationSettings(n)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `
		UPDATE notifications SET name = ?, implementation = ?, enabled = ?, settings = ?, on_grab = ?, on_import = ?, on_upgrade = ?,
			on_rename = ?, on_delete = ?, on_added = ?, on_failed = ?, on_health = ?
		WHERE id = ?`,
		n.Name, n.Implementation, n.Enabled, settings, n.OnGrab, n.OnImport, n.OnUpgrade, n.OnRename, n.OnDelete, n.OnAdded, n.OnFailed, n.OnHealth, n.ID)
	if err != nil {
		return fmt.Errorf("update notification %d: %w", n.ID, err)
	}
	return nil
}

// DeleteNotification removes a notification.
func DeleteNotification(ctx context.Context, q Queryer, id int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM notifications WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete notification %d: %w", id, err)
	}
	return nil
}

// NotificationNameTaken reports whether another notification has name.
func NotificationNameTaken(ctx context.Context, q Queryer, name string, exceptID int64) (bool, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE name = ? AND id <> ?`, name, exceptID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}
