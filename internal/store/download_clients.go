package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Download client implementations.
const (
	ClientDeluge       = "deluge"
	ClientQBittorrent  = "qbittorrent"
	ClientSABnzbd      = "sabnzbd"
	ClientTransmission = "transmission"
	ClientNZBGet       = "nzbget"
)

// DownloadClient mirrors one download_clients row (migration 00022).
type DownloadClient struct {
	ID             int64
	Name           string
	Implementation string
	Enabled        bool
	Priority       int // lower is tried first, as in Sonarr
	BaseURL        string
	Username       string
	Password       string
	APIKey         string
	Category       string
	RemotePath     string // the client's own path to its downloads...
	LocalPath      string // ...and where UMMarr sees the same files
	// RemoveFailed deletes a failed download, and its data, from the client
	// once it has been blocklisted (Sonarr's Remove Failed).
	RemoveFailed bool
}

// Protocol is "usenet" for SABnzbd, "torrent" for the rest.
func (c DownloadClient) Protocol() string {
	if c.Implementation == ClientSABnzbd || c.Implementation == ClientNZBGet {
		return "usenet"
	}
	return "torrent"
}

// ErrDownloadClientNotFound means no row has that id.
var ErrDownloadClientNotFound = errors.New("download client not found")

const downloadClientColumns = `id, name, implementation, enabled, priority, base_url, username, password, api_key, category, remote_path, local_path, remove_failed`

func scanDownloadClient(row interface{ Scan(...any) error }) (DownloadClient, error) {
	var c DownloadClient
	err := row.Scan(&c.ID, &c.Name, &c.Implementation, &c.Enabled, &c.Priority, &c.BaseURL, &c.Username, &c.Password, &c.APIKey, &c.Category, &c.RemotePath, &c.LocalPath, &c.RemoveFailed)
	return c, err
}

// ListDownloadClients lists every client, by priority then name.
func ListDownloadClients(ctx context.Context, q Queryer) ([]DownloadClient, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+downloadClientColumns+` FROM download_clients ORDER BY priority, name`)
	if err != nil {
		return nil, fmt.Errorf("list download clients: %w", err)
	}
	defer rows.Close()
	var out []DownloadClient
	for rows.Next() {
		c, err := scanDownloadClient(rows)
		if err != nil {
			return nil, fmt.Errorf("scan download client: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetDownloadClient reads one client.
func GetDownloadClient(ctx context.Context, q Queryer, id int64) (DownloadClient, error) {
	c, err := scanDownloadClient(q.QueryRowContext(ctx, `SELECT `+downloadClientColumns+` FROM download_clients WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return DownloadClient{}, ErrDownloadClientNotFound
	}
	if err != nil {
		return DownloadClient{}, fmt.Errorf("get download client %d: %w", id, err)
	}
	return c, nil
}

// CreateDownloadClient adds a client.
func CreateDownloadClient(ctx context.Context, q Queryer, c DownloadClient) (int64, error) {
	res, err := q.ExecContext(ctx, `
		INSERT INTO download_clients (name, implementation, enabled, priority, base_url, username, password, api_key, category, remote_path, local_path, remove_failed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Implementation, c.Enabled, c.Priority, c.BaseURL, c.Username, c.Password, c.APIKey, c.Category, c.RemotePath, c.LocalPath, c.RemoveFailed)
	if err != nil {
		return 0, fmt.Errorf("create download client: %w", err)
	}
	return res.LastInsertId()
}

// UpdateDownloadClient saves a client. An empty Password or APIKey keeps
// the stored one, so the form never has to show a secret.
func UpdateDownloadClient(ctx context.Context, q Queryer, c DownloadClient) error {
	_, err := q.ExecContext(ctx, `
		UPDATE download_clients SET name = ?, implementation = ?, enabled = ?, priority = ?, base_url = ?, username = ?,
			password = CASE WHEN ? = '' THEN password ELSE ? END, api_key = CASE WHEN ? = '' THEN api_key ELSE ? END,
			category = ?, remote_path = ?, local_path = ?, remove_failed = ?
		WHERE id = ?`,
		c.Name, c.Implementation, c.Enabled, c.Priority, c.BaseURL, c.Username, c.Password, c.Password, c.APIKey, c.APIKey, c.Category, c.RemotePath, c.LocalPath, c.RemoveFailed, c.ID)
	if err != nil {
		return fmt.Errorf("update download client %d: %w", c.ID, err)
	}
	return nil
}

// DeleteDownloadClient removes a client; its grabs keep their history.
func DeleteDownloadClient(ctx context.Context, q Queryer, id int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM download_clients WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete download client %d: %w", id, err)
	}
	return nil
}

// DownloadClientNameTaken reports whether another client already has name.
func DownloadClientNameTaken(ctx context.Context, q Queryer, name string, exceptID int64) (bool, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM download_clients WHERE name = ? AND id <> ?`, name, exceptID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}
