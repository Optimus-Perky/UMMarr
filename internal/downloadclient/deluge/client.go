// Package deluge is a client for Deluge's Web UI JSON-RPC API
// (deluge-web's /json endpoint), UMMarr's download client. Deluge has no
// REST API even in its current 2.x line - everything goes through one
// POST endpoint with a cookie-session login, distinct enough from the
// GET-only metadata provider clients that it gets its own minimal HTTP
// handling rather than reusing internal/metadata/providers/httpclient.
package deluge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"
)

// defaultHTTPTimeout bounds every call this package makes when the caller
// hasn't supplied its own *http.Client (see New) - long enough for a real
// but slow response (a big core.get_torrents_status batch, Deluge under
// load), short enough that a genuinely stuck connection can't wedge a
// caller forever.
const defaultHTTPTimeout = 30 * time.Second

// ErrMissingCredential is returned by any call made without a base URL set.
var ErrMissingCredential = errors.New("deluge: no base URL configured (set UMMARR_DELUGE_BASE_URL)")

// ErrNotAuthenticated is returned when Deluge rejects a request as
// unauthenticated even after a fresh login attempt.
var ErrNotAuthenticated = errors.New("deluge: session rejected by server")

// Client talks to a self-hosted Deluge Web UI. Construct with New; the
// zero value is not usable.
type Client struct {
	baseURL  string // POST {baseURL}/json
	password string
	http     *http.Client // carries a cookiejar for the _session_id cookie

	mu       sync.Mutex
	rpcID    int
	loggedIn bool
}

// Options configures a Client. HTTP is only ever overridden in tests, to
// point at an httptest.Server instead of a real Deluge instance - it must
// still be given a cookiejar (http.Client{Jar: cookiejar.New(nil)}) since
// deluge-web's session auth is entirely cookie-based.
type Options struct {
	BaseURL  string
	Password string
	HTTP     *http.Client
}

// New builds a Deluge client.
func New(opts Options) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, ErrMissingCredential
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, fmt.Errorf("deluge: build cookie jar: %w", err)
		}
		// No Timeout on a bare http.Client means "wait forever" - a single
		// slow/stuck Deluge response would hang whichever caller is
		// blocked on it. DownloadService.RefreshQueue runs on a 1-minute
		// ticker in a single goroutine (for range ticker.C), so one hung
		// call there doesn't just stall that request - since Go tickers
		// drop ticks the receiver isn't ready for rather than queuing
		// them, it permanently wedges the whole fallback poller for every
		// grab, forever, with no error to even notice by (found live
		// 2026-09-15: 32 grabs stuck at status=downloading despite Deluge
		// showing them finished, zero log output for 6+ minutes/ticks).
		httpClient = &http.Client{Jar: jar, Timeout: defaultHTTPTimeout}
	}
	return &Client{baseURL: opts.BaseURL, password: opts.Password, http: httpClient}, nil
}

// AddOptions configures where Deluge saves a newly added torrent's data -
// this is how movies/tv/music each land in their own root folder.
type AddOptions struct {
	DownloadLocation string
}

func (o AddOptions) toParams() map[string]any {
	if o.DownloadLocation == "" {
		return map[string]any{}
	}
	return map[string]any{"download_location": o.DownloadLocation}
}

// AddMagnet adds a torrent by magnet URI, returning its torrent id (infohash).
func (c *Client) AddMagnet(ctx context.Context, magnetURI string, opts AddOptions) (string, error) {
	var torrentID string
	if err := c.call(ctx, "core.add_torrent_magnet", []any{magnetURI, opts.toParams()}, &torrentID); err != nil {
		return "", err
	}
	return torrentID, nil
}

// AddTorrentFile adds a torrent from raw .torrent file bytes, returning its
// torrent id.
func (c *Client) AddTorrentFile(ctx context.Context, fileName string, data []byte, opts AddOptions) (string, error) {
	filedump := base64.StdEncoding.EncodeToString(data)
	var torrentID *string
	if err := c.call(ctx, "core.add_torrent_file", []any{fileName, filedump, opts.toParams()}, &torrentID); err != nil {
		return "", err
	}
	if torrentID == nil {
		return "", fmt.Errorf("deluge: add_torrent_file returned no torrent id for %q", fileName)
	}
	return *torrentID, nil
}

// TorrentFile is one file within a torrent, per core.get_torrents_status'
// "files" key - Path is relative to the torrent's SavePath. The
// underlying RPC also returns an "index" key per file; not decoded here,
// no caller needs it.
type TorrentFile struct {
	Path string
	Size int64
}

// TorrentStatus is one torrent's status, per core.get_torrents_status.
type TorrentStatus struct {
	Hash       string
	Name       string
	State      string
	Message    string
	Progress   float64
	IsFinished bool
	SavePath   string
	TotalSize  int64
	TotalDone  int64
	ETA        int64
	Ratio      float64
	Files      []TorrentFile
}

type torrentFileWire struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type torrentStatusWire struct {
	Name       string            `json:"name"`
	State      string            `json:"state"`
	Message    string            `json:"message"`
	Progress   float64           `json:"progress"`
	IsFinished bool              `json:"is_finished"`
	SavePath   string            `json:"save_path"`
	TotalSize  int64             `json:"total_size"`
	TotalDone  int64             `json:"total_done"`
	ETA        int64             `json:"eta"`
	Ratio      float64           `json:"ratio"`
	Files      []torrentFileWire `json:"files"`
}

var torrentStatusKeys = []string{
	"name", "state", "message", "progress", "is_finished",
	"save_path", "total_size", "total_done", "eta", "ratio", "files",
}

// GetTorrentsStatus fetches status for the given torrent ids (empty = all
// torrents Deluge knows about).
func (c *Client) GetTorrentsStatus(ctx context.Context, ids []string) (map[string]TorrentStatus, error) {
	filter := map[string]any{}
	if len(ids) > 0 {
		filter["id"] = ids
	}

	var wire map[string]torrentStatusWire
	if err := c.call(ctx, "core.get_torrents_status", []any{filter, torrentStatusKeys}, &wire); err != nil {
		return nil, err
	}

	statuses := make(map[string]TorrentStatus, len(wire))
	for hash, w := range wire {
		files := make([]TorrentFile, len(w.Files))
		for i, wf := range w.Files {
			files[i] = TorrentFile{Path: wf.Path, Size: wf.Size}
		}
		statuses[hash] = TorrentStatus{
			Hash: hash, Name: w.Name, State: w.State, Message: w.Message,
			Progress: w.Progress, IsFinished: w.IsFinished, SavePath: w.SavePath,
			TotalSize: w.TotalSize, TotalDone: w.TotalDone, ETA: w.ETA, Ratio: w.Ratio,
			Files: files,
		}
	}
	return statuses, nil
}

type rpcRequest struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
	ID     int    `json:"id"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	ID     int             `json:"id"`
}

type rpcError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("deluge: rpc error %d: %s", e.Code, e.Message)
}

// call issues one JSON-RPC request, logging in first if no session exists
// yet, and retrying exactly once after a fresh login if the response
// indicates the session was rejected - Deluge documents no fixed session
// TTL, so "the server just said no" is the only correct re-login trigger.
func (c *Client) call(ctx context.Context, method string, params []any, out any) error {
	if c.baseURL == "" {
		return ErrMissingCredential
	}

	c.mu.Lock()
	loggedIn := c.loggedIn
	c.mu.Unlock()
	if !loggedIn {
		if err := c.login(ctx); err != nil {
			return err
		}
	}

	resp, status, err := c.rawCall(ctx, method, params)
	if err != nil {
		return err
	}
	if isAuthFailure(status, resp) {
		c.mu.Lock()
		c.loggedIn = false
		c.mu.Unlock()
		if err := c.login(ctx); err != nil {
			return err
		}
		resp, status, err = c.rawCall(ctx, method, params)
		if err != nil {
			return err
		}
		if isAuthFailure(status, resp) {
			return ErrNotAuthenticated
		}
	}
	if resp.Error != nil {
		return resp.Error
	}

	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("deluge: decode %s result: %w", method, err)
	}
	return nil
}

// isAuthFailure recognizes a rejected session so call() knows to re-login
// and retry once. Deluge's web API surfaces this either as a plain HTTP 401
// or as a JSON-RPC error whose message names the NotAuthenticatedException
// - checking both since the exact shape isn't guaranteed across versions.
func isAuthFailure(httpStatus int, resp *rpcResponse) bool {
	if httpStatus == http.StatusUnauthorized {
		return true
	}
	if resp != nil && resp.Error != nil && strings.Contains(strings.ToLower(resp.Error.Message), "not authenticated") {
		return true
	}
	return false
}

func (c *Client) login(ctx context.Context) error {
	resp, _, err := c.rawCall(ctx, "auth.login", []any{c.password})
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	var ok bool
	if err := json.Unmarshal(resp.Result, &ok); err != nil {
		return fmt.Errorf("deluge: decode auth.login result: %w", err)
	}
	if !ok {
		return ErrNotAuthenticated
	}
	c.mu.Lock()
	c.loggedIn = true
	c.mu.Unlock()
	return nil
}

func (c *Client) rawCall(ctx context.Context, method string, params []any) (*rpcResponse, int, error) {
	c.mu.Lock()
	c.rpcID++
	id := c.rpcID
	c.mu.Unlock()

	body, err := json.Marshal(rpcRequest{Method: method, Params: params, ID: id})
	if err != nil {
		return nil, 0, fmt.Errorf("deluge: encode %s request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/json", bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("deluge: build %s request: %w", method, err)
	}
	// deluge-web checks Content-Type exactly and rejects anything else.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("deluge: do %s request: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return &rpcResponse{}, resp.StatusCode, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("deluge: %s: http %d", method, resp.StatusCode)
	}

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("deluge: decode %s response: %w", method, err)
	}
	return &rpcResp, resp.StatusCode, nil
}

// SetTorrentOptions sets per-torrent options through core.set_torrent_options,
// e.g. {"stop_at_ratio": true, "stop_ratio": 1.5}.
func (c *Client) SetTorrentOptions(ctx context.Context, ids []string, options map[string]any) error {
	var ignored any
	return c.call(ctx, "core.set_torrent_options", []any{ids, options}, &ignored)
}

// RemoveTorrent deletes a torrent through core.remove_torrent, with its
// downloaded data when removeData is set.
func (c *Client) RemoveTorrent(ctx context.Context, id string, removeData bool) error {
	var ignored any
	return c.call(ctx, "core.remove_torrent", []any{id, removeData}, &ignored)
}
