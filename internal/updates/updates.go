// Package updates compares the running build with the newest commit on
// GitHub. UMMarr is built from source into its container, so applying an
// update is a rebuild - this only says whether one is waiting.
package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Result is the latest check.
type Result struct {
	CheckedAt time.Time
	Current   string
	Latest    string
	LatestAt  time.Time
	Message   string
	Behind    bool
	Error     string
}

// Checker asks GitHub about Repo (owner/name).
type Checker struct {
	Repo      string
	Branch    string
	Current   string // the running build's short revision, "dev" when unknown
	APIBase   string // GitHub's API, overridable for tests
	UserAgent string
	HTTP      *http.Client

	mu     sync.Mutex
	last   Result
	branch string // the repository's default branch, asked once
}

// Last is the latest result.
func (c *Checker) Last() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Check fetches the branch head and compares.
func (c *Checker) Check(ctx context.Context) Result {
	res := Result{CheckedAt: time.Now(), Current: c.Current}
	base := c.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	branch := c.Branch
	if branch == "" {
		branch = c.defaultBranch(ctx, base, client)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/commits/%s", base, c.Repo, branch), nil)
	if err != nil {
		res.Error = err.Error()
		return c.remember(res)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		res.Error = err.Error()
		return c.remember(res)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		res.Error = fmt.Sprintf("GitHub returned HTTP %d for %s@%s - a private repository needs a token, and a branch that does not exist answers 422", resp.StatusCode, c.Repo, branch)
		return c.remember(res)
	}
	var commit struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&commit); err != nil {
		res.Error = "bad reply from GitHub: " + err.Error()
		return c.remember(res)
	}
	res.Latest = commit.SHA
	if len(res.Latest) > 7 {
		res.Latest = res.Latest[:7]
	}
	res.LatestAt = commit.Commit.Author.Date
	res.Message = strings.SplitN(commit.Commit.Message, "\n", 2)[0]
	current := strings.TrimSuffix(c.Current, "-dirty")
	res.Behind = current != "dev" && current != "" && !strings.HasPrefix(commit.SHA, current)
	return c.remember(res)
}

func (c *Checker) remember(r Result) Result {
	c.mu.Lock()
	c.last = r
	c.mu.Unlock()
	return r
}

// defaultBranch asks GitHub which branch the repository actually uses, so a
// repo on master isn't checked against main (which answers 422 and reads as
// "private repository"). Falls back to master, this project's own branch.
func (c *Checker) defaultBranch(ctx context.Context, base string, client *http.Client) string {
	c.mu.Lock()
	cached := c.branch
	c.mu.Unlock()
	if cached != "" {
		return cached
	}
	branch := "master"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s", base, c.Repo), nil)
	if err == nil {
		req.Header.Set("Accept", "application/vnd.github+json")
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		if resp, err := client.Do(req); err == nil {
			defer resp.Body.Close()
			var repo struct {
				DefaultBranch string `json:"default_branch"`
			}
			if json.NewDecoder(resp.Body).Decode(&repo) == nil && repo.DefaultBranch != "" {
				branch = repo.DefaultBranch
			}
		}
	}
	c.mu.Lock()
	c.branch = branch
	c.mu.Unlock()
	return branch
}
