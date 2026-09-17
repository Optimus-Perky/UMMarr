// Package qbittorrent talks to qBittorrent's Web API (v2).
package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

// Client is one qBittorrent instance.
type Client struct {
	baseURL  string
	username string
	password string
	category string
	mapping  downloadclient.PathMapping
	http     *http.Client
}

// Options configures a Client.
type Options struct {
	BaseURL  string
	Username string
	Password string
	Category string
	Mapping  downloadclient.PathMapping
	HTTP     *http.Client
}

// New builds a client; nothing is contacted until it's used.
func New(opts Options) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, fmt.Errorf("qbittorrent: no URL")
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		httpClient = &http.Client{Jar: jar, Timeout: 60 * time.Second}
	} else if httpClient.Jar == nil {
		httpClient.Jar, _ = cookiejar.New(nil)
	}
	return &Client{baseURL: strings.TrimRight(opts.BaseURL, "/"), username: opts.Username, password: opts.Password, category: opts.Category, mapping: opts.Mapping, http: httpClient}, nil
}

func (c *Client) Protocol() string { return newznab.ProtocolTorrent }

func (c *Client) login(ctx context.Context) error {
	form := url.Values{"username": {c.username}, "password": {c.password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.baseURL)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("qbittorrent: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("qbittorrent: too many failed logins, the Web UI has banned this address for a while")
	}
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "Ok." {
		return fmt.Errorf("qbittorrent: login failed (HTTP %d %s) - check the username and password", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// do sends a request, logging in first when the session has lapsed.
func (c *Client) do(ctx context.Context, method, path string, body func() (io.Reader, string)) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		reader, contentType := body()
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
		if err != nil {
			return nil, err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("Referer", c.baseURL)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("qbittorrent: %w", err)
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden && attempt == 0 {
			if err := c.login(ctx); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("qbittorrent: %s returned HTTP %d %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
		}
		return data, nil
	}
	return nil, fmt.Errorf("qbittorrent: not logged in")
}

func formBody(values url.Values) func() (io.Reader, string) {
	return func() (io.Reader, string) {
		return strings.NewReader(values.Encode()), "application/x-www-form-urlencoded"
	}
}

// Add sends a magnet link or .torrent file; the torrent's infohash is its id.
func (c *Client) Add(ctx context.Context, fetched *newznab.FetchedRelease) (string, error) {
	var id string
	var err error
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	switch fetched.Kind {
	case newznab.KindMagnet:
		if id, err = downloadclient.MagnetInfoHash(fetched.MagnetURI); err != nil {
			return "", err
		}
		w.WriteField("urls", fetched.MagnetURI)
	case newznab.KindTorrentFile:
		if id, err = downloadclient.TorrentInfoHash(fetched.Data); err != nil {
			return "", err
		}
		part, err := w.CreateFormFile("torrents", fetched.FileName)
		if err != nil {
			return "", err
		}
		part.Write(fetched.Data)
	default:
		return "", fmt.Errorf("qbittorrent can't take a usenet NZB")
	}
	if c.category != "" {
		w.WriteField("category", c.category)
	}
	w.Close()
	data := buf.Bytes()
	contentType := w.FormDataContentType()
	out, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/add", func() (io.Reader, string) { return bytes.NewReader(data), contentType })
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(out)) == "Fails." {
		return "", fmt.Errorf("qbittorrent refused the torrent")
	}
	return id, nil
}

type torrentInfo struct {
	Hash       string  `json:"hash"`
	Name       string  `json:"name"`
	State      string  `json:"state"`
	Progress   float64 `json:"progress"`
	SavePath   string  `json:"save_path"`
	Size       int64   `json:"size"`
	AmountLeft int64   `json:"amount_left"`
}

type torrentFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// finishedStates are qBittorrent's states once every byte is present.
var finishedStates = map[string]bool{"uploading": true, "pausedUP": true, "stoppedUP": true, "queuedUP": true, "stalledUP": true, "checkingUP": true, "forcedUP": true}

// Statuses reports on torrents by infohash.
func (c *Client) Statuses(ctx context.Context, ids []string) (map[string]downloadclient.Status, error) {
	out := map[string]downloadclient.Status{}
	if len(ids) == 0 {
		return out, nil
	}
	data, err := c.do(ctx, http.MethodGet, "/api/v2/torrents/info?hashes="+url.QueryEscape(strings.Join(ids, "|")), func() (io.Reader, string) { return nil, "" })
	if err != nil {
		return nil, err
	}
	var infos []torrentInfo
	if err := json.Unmarshal(data, &infos); err != nil {
		return nil, fmt.Errorf("qbittorrent: bad torrent list: %w", err)
	}
	for _, t := range infos {
		st := downloadclient.Status{
			ID: strings.ToLower(t.Hash), Name: t.Name, State: t.State, Progress: t.Progress,
			IsFinished: finishedStates[t.State] || (t.Progress >= 1 && t.AmountLeft == 0),
			Failed:     t.State == "error" || t.State == "missingFiles", SavePath: c.mapping.Map(t.SavePath), TotalSize: t.Size,
		}
		if st.Failed {
			st.Message = "qBittorrent reports " + t.State
		}
		if st.IsFinished {
			fdata, err := c.do(ctx, http.MethodGet, "/api/v2/torrents/files?hash="+url.QueryEscape(t.Hash), func() (io.Reader, string) { return nil, "" })
			if err != nil {
				return nil, err
			}
			var files []torrentFile
			if err := json.Unmarshal(fdata, &files); err != nil {
				return nil, fmt.Errorf("qbittorrent: bad file list: %w", err)
			}
			for _, f := range files {
				st.Files = append(st.Files, downloadclient.File{Path: f.Name, Size: f.Size})
			}
		}
		out[st.ID] = st
	}
	return out, nil
}

// Test logs in and reads the version.
func (c *Client) Test(ctx context.Context) error {
	if err := c.login(ctx); err != nil {
		return err
	}
	_, err := c.do(ctx, http.MethodGet, "/api/v2/app/version", func() (io.Reader, string) { return nil, "" })
	return err
}

// SetSeedRatio stops the torrent at ratio.
func (c *Client) SetSeedRatio(ctx context.Context, id string, ratio float64) error {
	_, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/setShareLimits", formBody(url.Values{
		"hashes": {id}, "ratioLimit": {fmt.Sprintf("%.2f", ratio)}, "seedingTimeLimit": {"-2"}, "inactiveSeedingTimeLimit": {"-2"},
	}))
	return err
}

// Remove deletes the torrent, and its files when deleteData is set.
func (c *Client) Remove(ctx context.Context, id string, deleteData bool) error {
	_, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/delete", formBody(url.Values{
		"hashes": {id}, "deleteFiles": {strconv.FormatBool(deleteData)},
	}))
	return err
}
