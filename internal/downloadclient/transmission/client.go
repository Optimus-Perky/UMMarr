// Package transmission talks to Transmission's RPC API.
package transmission

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

// Client is one Transmission instance.
type Client struct {
	baseURL   string
	username  string
	password  string
	label     string
	mapping   downloadclient.PathMapping
	http      *http.Client
	sessionID string
}

// Options configures a Client.
type Options struct {
	BaseURL  string
	Username string
	Password string
	// Label is added to every torrent, as Transmission's labels; optional.
	Label   string
	Mapping downloadclient.PathMapping
	HTTP    *http.Client
}

// New builds a client; nothing is contacted until it's used.
func New(opts Options) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, fmt.Errorf("transmission: no URL")
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(opts.BaseURL, "/"), username: opts.Username, password: opts.Password,
		label: opts.Label, mapping: opts.Mapping, http: httpClient}, nil
}

func (c *Client) Protocol() string { return newznab.ProtocolTorrent }

type rpcRequest struct {
	Method    string `json:"method"`
	Arguments any    `json:"arguments,omitempty"`
}

type rpcResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
}

// call posts an RPC request, doing Transmission's 409 session-id handshake
// once: the first call without the header is answered with the id to use.
func (c *Client) call(ctx context.Context, method string, args any, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		body, err := json.Marshal(rpcRequest{Method: method, Arguments: args})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/transmission/rpc", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.sessionID != "" {
			req.Header.Set("X-Transmission-Session-Id", c.sessionID)
		}
		if c.username != "" || c.password != "" {
			req.SetBasicAuth(c.username, c.password)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("transmission: %w", err)
		}
		data, readErr := readBody(resp)
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		switch resp.StatusCode {
		case http.StatusConflict:
			c.sessionID = resp.Header.Get("X-Transmission-Session-Id")
			if c.sessionID == "" {
				return fmt.Errorf("transmission: no session id in its 409 reply")
			}
			continue
		case http.StatusUnauthorized:
			return fmt.Errorf("transmission: the username or password was refused")
		case http.StatusOK:
		default:
			return fmt.Errorf("transmission: %s returned HTTP %d", method, resp.StatusCode)
		}
		var rpc rpcResponse
		if err := json.Unmarshal(data, &rpc); err != nil {
			return fmt.Errorf("transmission: bad %s reply: %w", method, err)
		}
		if rpc.Result != "success" {
			return fmt.Errorf("transmission: %s", rpc.Result)
		}
		if out != nil && len(rpc.Arguments) > 0 {
			return json.Unmarshal(rpc.Arguments, out)
		}
		return nil
	}
	return fmt.Errorf("transmission: the session id was refused twice")
}

func readBody(resp *http.Response) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, fmt.Errorf("transmission: read reply: %w", err)
	}
	return buf.Bytes(), nil
}

type addArgs struct {
	Filename    string   `json:"filename,omitempty"`
	MetaInfo    string   `json:"metainfo,omitempty"`
	Paused      bool     `json:"paused"`
	Labels      []string `json:"labels,omitempty"`
	DownloadDir string   `json:"download-dir,omitempty"`
}

type addResult struct {
	Added struct {
		HashString string `json:"hashString"`
		Name       string `json:"name"`
	} `json:"torrent-added"`
	Duplicate struct {
		HashString string `json:"hashString"`
		Name       string `json:"name"`
	} `json:"torrent-duplicate"`
}

// Add sends a release to Transmission; the torrent's infohash is its id.
func (c *Client) Add(ctx context.Context, fetched *newznab.FetchedRelease) (string, error) {
	var args addArgs
	switch fetched.Kind {
	case newznab.KindMagnet:
		args.Filename = fetched.MagnetURI
	case newznab.KindTorrentFile:
		args.MetaInfo = base64.StdEncoding.EncodeToString(fetched.Data)
	default:
		return "", fmt.Errorf("transmission can't take a usenet release")
	}
	if c.label != "" {
		args.Labels = []string{c.label}
	}
	var res addResult
	if err := c.call(ctx, "torrent-add", args, &res); err != nil {
		return "", err
	}
	if res.Added.HashString != "" {
		return strings.ToLower(res.Added.HashString), nil
	}
	if res.Duplicate.HashString != "" {
		// Already there: treat it as ours, as the other clients do.
		return strings.ToLower(res.Duplicate.HashString), nil
	}
	return "", fmt.Errorf("transmission: it accepted the torrent but gave no hash")
}

type torrent struct {
	HashString    string  `json:"hashString"`
	Name          string  `json:"name"`
	Status        int     `json:"status"`
	PercentDone   float64 `json:"percentDone"`
	LeftUntilDone int64   `json:"leftUntilDone"`
	TotalSize     int64   `json:"totalSize"`
	DownloadDir   string  `json:"downloadDir"`
	Error         int     `json:"error"`
	ErrorString   string  `json:"errorString"`
	Files         []struct {
		Name   string `json:"name"`
		Length int64  `json:"length"`
	} `json:"files"`
}

// Statuses reports on the given torrents.
func (c *Client) Statuses(ctx context.Context, ids []string) (map[string]downloadclient.Status, error) {
	args := map[string]any{"fields": []string{"hashString", "name", "status", "percentDone", "leftUntilDone", "totalSize", "downloadDir", "error", "errorString", "files"}}
	if len(ids) > 0 {
		args["ids"] = ids
	}
	var res struct {
		Torrents []torrent `json:"torrents"`
	}
	if err := c.call(ctx, "torrent-get", args, &res); err != nil {
		return nil, err
	}
	out := make(map[string]downloadclient.Status, len(res.Torrents))
	for _, t := range res.Torrents {
		id := strings.ToLower(t.HashString)
		st := downloadclient.Status{
			ID: id, Name: t.Name, State: stateName(t.Status), Progress: t.PercentDone,
			IsFinished: t.LeftUntilDone == 0 && t.PercentDone >= 1,
			SavePath:   c.mapping.Map(t.DownloadDir), TotalSize: t.TotalSize,
		}
		if t.Error != 0 {
			st.Failed, st.Message = true, t.ErrorString
			if st.Message == "" {
				st.Message = "Transmission reported an error"
			}
		}
		for _, f := range t.Files {
			st.Files = append(st.Files, downloadclient.File{Path: f.Name, Size: f.Length})
		}
		out[id] = st
	}
	return out, nil
}

func stateName(status int) string {
	switch status {
	case 0:
		return "stopped"
	case 1, 2:
		return "checking"
	case 3, 4:
		return "downloading"
	case 5, 6:
		return "seeding"
	}
	return "unknown"
}

// Test checks the connection and credentials.
func (c *Client) Test(ctx context.Context) error {
	var res struct {
		Version string `json:"version"`
	}
	if err := c.call(ctx, "session-get", nil, &res); err != nil {
		return err
	}
	return nil
}

// SetSeedRatio stops the torrent at ratio.
func (c *Client) SetSeedRatio(ctx context.Context, id string, ratio float64) error {
	return c.call(ctx, "torrent-set", map[string]any{"ids": []string{id}, "seedRatioLimit": ratio, "seedRatioMode": 1}, nil)
}

// Remove deletes the torrent, and its files when deleteData is set.
func (c *Client) Remove(ctx context.Context, id string, deleteData bool) error {
	return c.call(ctx, "torrent-remove", map[string]any{"ids": []string{id}, "delete-local-data": deleteData}, nil)
}
