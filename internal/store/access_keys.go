package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Named access keys. The single API key in app_settings is what Prowlarr's
// app sync uses; these are extra, named, and each can be revoked on its
// own - so giving one to a tool never means regenerating the key something
// else depends on.
//
// They authenticate the web UI as well as the API, which the main key does
// not: that is the whole point of them, so a tool can fetch a page and see
// what a change actually renders.

// AccessKey is one issued key. Key is the secret itself; it is shown in
// Settings and never logged.
type AccessKey struct {
	ID         int64
	Name       string
	Key        string
	Created    time.Time
	LastUsed   *time.Time
	LastUsedBy string // not stored; filled in for display only
}

// CreateAccessKey issues a key with a name to remember it by.
func CreateAccessKey(ctx context.Context, q Queryer, name string) (AccessKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return AccessKey{}, fmt.Errorf("give the key a name, so it is clear what it is for")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return AccessKey{}, fmt.Errorf("generate access key: %w", err)
	}
	key := hex.EncodeToString(raw)
	res, err := q.ExecContext(ctx, `INSERT INTO access_keys (name, key) VALUES (?, ?)`, name, key)
	if err != nil {
		return AccessKey{}, fmt.Errorf("create access key: %w", err)
	}
	id, _ := res.LastInsertId()
	return AccessKey{ID: id, Name: name, Key: key, Created: time.Now()}, nil
}

// ListAccessKeys lists the issued keys, newest first.
func ListAccessKeys(ctx context.Context, q Queryer) ([]AccessKey, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, key, created_at, last_used_at FROM access_keys ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list access keys: %w", err)
	}
	defer rows.Close()
	var keys []AccessKey
	for rows.Next() {
		var k AccessKey
		var lastUsed *time.Time
		if err := rows.Scan(&k.ID, &k.Name, &k.Key, &k.Created, &lastUsed); err != nil {
			return nil, err
		}
		k.LastUsed = lastUsed
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// DeleteAccessKey revokes one key. Nothing else is affected.
func DeleteAccessKey(ctx context.Context, q Queryer, id int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM access_keys WHERE id = ?`, id); err != nil {
		return fmt.Errorf("revoke access key %d: %w", id, err)
	}
	return nil
}

// ValidAccessKey reports whether given matches an issued key, and records
// that it was used. The comparison runs against every key in constant
// time, so a wrong key can't be narrowed down by how long the check took.
func ValidAccessKey(ctx context.Context, q Queryer, given string) bool {
	if given == "" {
		return false
	}
	keys, err := ListAccessKeys(ctx, q)
	if err != nil {
		return false
	}
	matched := int64(0)
	for _, k := range keys {
		if subtle.ConstantTimeCompare([]byte(given), []byte(k.Key)) == 1 {
			matched = k.ID
		}
	}
	if matched == 0 {
		return false
	}
	// Best effort: a failure to record the use must not deny the request.
	_, _ = q.ExecContext(ctx, `UPDATE access_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`, matched)
	return true
}
