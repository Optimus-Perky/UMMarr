// Package sabnzbd talks to SABnzbd's API.
package sabnzbd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient"
	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

// Client is one SABnzbd instance.
type Client struct {
	baseURL  string
	apiKey   string
	category string
	mapping  downloadclient.PathMapping
	http     *http.Client
}

// Options configures a Client.
type Options struct {
	BaseURL  string
	APIKey   string
	Category string
	Mapping  downloadclient.PathMapping
	HTTP     *http.Client
}

// New builds a client; nothing is contacted until it's used.
func New(opts Options) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, fmt.Errorf("sabnzbd: no URL")
	}
	if opts.APIKey == "" {
		return nil, fmt.Errorf("sabnzbd: no API key")
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(opts.BaseURL, "/"), apiKey: opts.APIKey, category: opts.Category, mapping: opts.Mapping, http: httpClient}, nil
}

func (c *Client) Protocol() string { return newznab.ProtocolUsenet }

func (c *Client) call(ctx context.Context, mode string, params url.Values, body io.Reader, contentType string, out any) error {
	q := url.Values{"mode": {mode}, "output": {"json"}, "apikey": {c.apiKey}}
	for k, vs := range params {
		q[k] = vs
	}
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api?"+q.Encode(), body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sabnzbd: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sabnzbd: %s returned HTTP %d", mode, resp.StatusCode)
	}
	var apiErr struct {
		Status *bool  `json:"status"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(data, &apiErr) == nil && apiErr.Error != "" {
		return fmt.Errorf("sabnzbd: %s", apiErr.Error)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("sabnzbd: bad %s response: %w", mode, err)
		}
	}
	return nil
}

// Add uploads an NZB; SABnzbd's nzo id is the download's id.
func (c *Client) Add(ctx context.Context, fetched *newznab.FetchedRelease) (string, error) {
	if fetched.Kind != newznab.KindNZB {
		return "", fmt.Errorf("sabnzbd only takes usenet NZBs")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("name", fetched.FileName)
	if err != nil {
		return "", err
	}
	part.Write(fetched.Data)
	w.Close()
	params := url.Values{"nzbname": {strings.TrimSuffix(fetched.FileName, ".nzb")}}
	if c.category != "" {
		params.Set("cat", c.category)
	}
	var resp struct {
		Status bool     `json:"status"`
		NzoIDs []string `json:"nzo_ids"`
	}
	if err := c.call(ctx, "addfile", params, &buf, w.FormDataContentType(), &resp); err != nil {
		return "", err
	}
	if !resp.Status || len(resp.NzoIDs) == 0 {
		return "", fmt.Errorf("sabnzbd didn't accept the NZB")
	}
	return resp.NzoIDs[0], nil
}

type queueSlot struct {
	NzoID    string `json:"nzo_id"`
	Filename string `json:"filename"`
	Status   string `json:"status"`
	Percent  string `json:"percentage"`
	MB       string `json:"mb"`
}

type historySlot struct {
	NzoID       string `json:"nzo_id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Storage     string `json:"storage"`
	FailMessage string `json:"fail_message"`
	Bytes       int64  `json:"bytes"`
}

// Statuses reports on downloads by nzo id, from the queue and then the
// history. A completed download's files come from its storage folder, which
// SABnzbd names but doesn't list.
func (c *Client) Statuses(ctx context.Context, ids []string) (map[string]downloadclient.Status, error) {
	out := map[string]downloadclient.Status{}
	if len(ids) == 0 {
		return out, nil
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	var queue struct {
		Queue struct {
			Slots []queueSlot `json:"slots"`
		} `json:"queue"`
	}
	if err := c.call(ctx, "queue", url.Values{"limit": {"500"}}, nil, "", &queue); err != nil {
		return nil, err
	}
	for _, s := range queue.Queue.Slots {
		if !wanted[s.NzoID] {
			continue
		}
		var pct float64
		fmt.Sscanf(s.Percent, "%f", &pct)
		out[s.NzoID] = downloadclient.Status{ID: s.NzoID, Name: s.Filename, State: s.Status, Progress: pct / 100}
	}
	var history struct {
		History struct {
			Slots []historySlot `json:"slots"`
		} `json:"history"`
	}
	if err := c.call(ctx, "history", url.Values{"limit": {"500"}}, nil, "", &history); err != nil {
		return nil, err
	}
	for _, s := range history.History.Slots {
		if !wanted[s.NzoID] {
			continue
		}
		if _, queued := out[s.NzoID]; queued {
			continue
		}
		st := downloadclient.Status{ID: s.NzoID, Name: s.Name, State: s.Status, TotalSize: s.Bytes}
		switch s.Status {
		case "Completed":
			st.IsFinished, st.Progress = true, 1
			storage := c.mapping.Map(s.Storage)
			st.SavePath = filepath.Dir(storage)
			if info, err := os.Stat(storage); err == nil && info.IsDir() {
				files, err := importer.ScanDirectory(storage)
				if err == nil {
					for _, f := range files {
						st.Files = append(st.Files, downloadclient.File{Path: filepath.Join(filepath.Base(storage), f.Path), Size: f.Size})
					}
				}
			} else if err == nil {
				st.Files = []downloadclient.File{{Path: filepath.Base(storage), Size: info.Size()}}
			} else {
				st.Failed, st.Message = true, "SABnzbd finished, but "+storage+" isn't visible to UMMarr - check the client's path mapping"
			}
		case "Failed":
			st.Failed, st.Message = true, s.FailMessage
		default:
			st.Progress = 1 // post-processing: verifying, repairing, extracting
		}
		out[s.NzoID] = st
	}
	return out, nil
}

// Test reads the version with the API key.
func (c *Client) Test(ctx context.Context) error {
	var resp struct {
		Version string `json:"version"`
	}
	if err := c.call(ctx, "version", nil, nil, "", &resp); err != nil {
		return err
	}
	if resp.Version == "" {
		return fmt.Errorf("sabnzbd: no version in the reply - is that a SABnzbd URL?")
	}
	return nil
}

// SetSeedRatio means nothing on usenet.
func (c *Client) SetSeedRatio(ctx context.Context, id string, ratio float64) error { return nil }

// Remove deletes the download from the queue or, once finished, from the
// history, along with its files when deleteData is set.
func (c *Client) Remove(ctx context.Context, id string, deleteData bool) error {
	del := "0"
	if deleteData {
		del = "1"
	}
	params := url.Values{"name": {"delete"}, "value": {id}, "del_files": {del}}
	if err := c.call(ctx, "queue", params, nil, "", nil); err != nil {
		return err
	}
	return c.call(ctx, "history", params, nil, "", nil)
}
