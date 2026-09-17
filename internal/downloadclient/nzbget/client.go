// Package nzbget talks to NZBGet's JSON-RPC API.
package nzbget

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient"
	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

// Client is one NZBGet instance.
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
		return nil, fmt.Errorf("nzbget: no URL")
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(opts.BaseURL, "/"), username: opts.Username, password: opts.Password,
		category: opts.Category, mapping: opts.Mapping, http: httpClient}, nil
}

func (c *Client) Protocol() string { return newznab.ProtocolUsenet }

// call posts one JSON-RPC request.
func (c *Client) call(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(map[string]any{"method": method, "params": params, "id": 1, "jsonrpc": "2.0"})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jsonrpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("nzbget: %w", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("nzbget: read reply: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("nzbget: the username or password was refused")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("nzbget: %s returned HTTP %d", method, resp.StatusCode)
	}
	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &rpc); err != nil {
		return fmt.Errorf("nzbget: bad %s reply: %w", method, err)
	}
	if rpc.Error != nil {
		return fmt.Errorf("nzbget: %s", rpc.Error.Message)
	}
	if out != nil && len(rpc.Result) > 0 {
		return json.Unmarshal(rpc.Result, out)
	}
	return nil
}

// Add uploads an NZB; NZBGet's own id is the download's id.
func (c *Client) Add(ctx context.Context, fetched *newznab.FetchedRelease) (string, error) {
	if fetched.Kind != newznab.KindNZB {
		return "", fmt.Errorf("nzbget only takes usenet releases")
	}
	name := fetched.FileName
	if name == "" {
		name = "ummarr.nzb"
	}
	var id int
	// append(NZBFilename, Content, Category, Priority, AddToTop, AddPaused,
	// DupeKey, DupeScore, DupeMode)
	params := []any{name, base64.StdEncoding.EncodeToString(fetched.Data), c.category, 0, false, false, "", 0, "SCORE"}
	if err := c.call(ctx, "append", params, &id); err != nil {
		return "", err
	}
	if id <= 0 {
		return "", fmt.Errorf("nzbget refused the NZB")
	}
	return strconv.Itoa(id), nil
}

type group struct {
	NZBID           int    `json:"NZBID"`
	NZBName         string `json:"NZBName"`
	Status          string `json:"Status"`
	FileSizeLo      int64  `json:"FileSizeLo"`
	FileSizeHi      int64  `json:"FileSizeHi"`
	RemainingSizeLo int64  `json:"RemainingSizeLo"`
	RemainingSizeHi int64  `json:"RemainingSizeHi"`
	DestDir         string `json:"DestDir"`
	FinalDir        string `json:"FinalDir"`
}

type historyItem struct {
	NZBID      int    `json:"NZBID"`
	Name       string `json:"Name"`
	Status     string `json:"Status"`
	FileSizeLo int64  `json:"FileSizeLo"`
	FileSizeHi int64  `json:"FileSizeHi"`
	DestDir    string `json:"DestDir"`
	FinalDir   string `json:"FinalDir"`
}

func size(lo, hi int64) int64 { return hi<<32 | lo }

// Statuses reports on the given downloads: the queue first, then history.
func (c *Client) Statuses(ctx context.Context, ids []string) (map[string]downloadclient.Status, error) {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	out := map[string]downloadclient.Status{}

	var groups []group
	if err := c.call(ctx, "listgroups", []any{0}, &groups); err != nil {
		return nil, err
	}
	for _, g := range groups {
		id := strconv.Itoa(g.NZBID)
		if !wanted[id] {
			continue
		}
		total := size(g.FileSizeLo, g.FileSizeHi)
		left := size(g.RemainingSizeLo, g.RemainingSizeHi)
		st := downloadclient.Status{ID: id, Name: g.NZBName, State: g.Status, TotalSize: total}
		if total > 0 {
			st.Progress = float64(total-left) / float64(total)
		}
		out[id] = st
	}

	var history []historyItem
	if err := c.call(ctx, "history", []any{false}, &history); err != nil {
		return out, err
	}
	for _, h := range history {
		id := strconv.Itoa(h.NZBID)
		if !wanted[id] {
			continue
		}
		if _, queued := out[id]; queued {
			continue
		}
		st := downloadclient.Status{ID: id, Name: h.Name, State: h.Status, TotalSize: size(h.FileSizeLo, h.FileSizeHi)}
		dir := h.FinalDir
		if dir == "" {
			dir = h.DestDir
		}
		switch {
		case strings.HasPrefix(h.Status, "SUCCESS"):
			st.IsFinished, st.Progress = true, 1
			c.describeFiles(&st, dir)
		case strings.HasPrefix(h.Status, "DELETED"):
			st.Failed, st.Message = true, "Deleted in NZBGet ("+h.Status+")"
		default: // FAILURE/*, WARNING/*
			st.Failed, st.Message = true, "NZBGet reports "+h.Status
		}
		out[id] = st
	}
	return out, nil
}

// describeFiles lists a finished download's files, as SABnzbd's client does,
// since NZBGet's history doesn't name them.
func (c *Client) describeFiles(st *downloadclient.Status, dir string) {
	storage := c.mapping.Map(dir)
	if storage == "" {
		st.Failed, st.Message = true, "NZBGet finished, but didn't say where the files are"
		return
	}
	st.SavePath = filepath.Dir(storage)
	info, err := os.Stat(storage)
	if err != nil {
		st.Failed, st.Message = true, "NZBGet finished, but "+storage+" isn't visible to UMMarr - check the client's path mapping"
		return
	}
	if !info.IsDir() {
		st.Files = []downloadclient.File{{Path: filepath.Base(storage), Size: info.Size()}}
		return
	}
	files, err := importer.ScanDirectory(storage)
	if err != nil {
		return
	}
	for _, f := range files {
		st.Files = append(st.Files, downloadclient.File{Path: filepath.Join(filepath.Base(storage), f.Path), Size: f.Size})
	}
}

// Test checks the connection and credentials.
func (c *Client) Test(ctx context.Context) error {
	var version string
	if err := c.call(ctx, "version", nil, &version); err != nil {
		return err
	}
	if version == "" {
		return fmt.Errorf("nzbget: no version in the reply - is that an NZBGet URL?")
	}
	return nil
}

// SetSeedRatio means nothing on usenet.
func (c *Client) SetSeedRatio(ctx context.Context, id string, ratio float64) error { return nil }

// Remove deletes the download from the queue or the history, with its files
// when deleteData is set.
func (c *Client) Remove(ctx context.Context, id string, deleteData bool) error {
	n, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("nzbget: %q isn't one of its ids", id)
	}
	// GroupDelete drops it from the queue; HistoryDelete (or
	// HistoryFinalDelete, which also deletes the files) from the history.
	if err := c.call(ctx, "editqueue", []any{"GroupDelete", 0, "", []int{n}}, nil); err == nil {
		return nil
	}
	command := "HistoryDelete"
	if deleteData {
		command = "HistoryFinalDelete"
	}
	return c.call(ctx, "editqueue", []any{command, 0, "", []int{n}}, nil)
}
