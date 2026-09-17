// Package downloadclient is what UMMarr asks of any download client - add a
// release, report on it, honour a seed ratio - with one implementation per
// client program (deluge, qbittorrent, sabnzbd).
package downloadclient

import (
	"context"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

// File is one file of a download, Path relative to Status.SavePath.
type File struct {
	Path string
	Size int64
}

// Status is a download as the client reports it.
type Status struct {
	ID         string
	Name       string
	State      string // the client's own word for it
	Message    string // why it failed, when it did
	Progress   float64
	IsFinished bool
	Failed     bool
	SavePath   string // the folder the download's files sit under, as UMMarr sees it
	Files      []File
	TotalSize  int64
}

// Client is a download client.
type Client interface {
	// Protocol is newznab.ProtocolTorrent or newznab.ProtocolUsenet.
	Protocol() string
	// Add hands a fetched release to the client and returns the id the
	// client knows it by.
	Add(ctx context.Context, fetched *newznab.FetchedRelease) (string, error)
	// Statuses reports on the given downloads; one the client no longer has
	// is simply absent.
	Statuses(ctx context.Context, ids []string) (map[string]Status, error)
	// Test checks the connection and credentials.
	Test(ctx context.Context) error
	// SetSeedRatio asks the client to stop seeding at ratio; a usenet client
	// does nothing.
	SetSeedRatio(ctx context.Context, id string, ratio float64) error
	// Remove deletes a download from the client, and its data when
	// deleteData is set. A download the client no longer has isn't an error.
	Remove(ctx context.Context, id string, deleteData bool) error
}

// PathMapping translates a path the client reports (inside its own
// container or machine) to where UMMarr sees the same files.
type PathMapping struct {
	Remote string
	Local  string
}

// Map applies the mapping to p when it applies at all.
func (m PathMapping) Map(p string) string {
	if m.Remote == "" || m.Local == "" {
		return p
	}
	remote := strings.TrimRight(m.Remote, "/\\")
	if p == remote || strings.HasPrefix(p, remote+"/") || strings.HasPrefix(p, remote+"\\") {
		return strings.TrimRight(m.Local, "/\\") + p[len(remote):]
	}
	return p
}
