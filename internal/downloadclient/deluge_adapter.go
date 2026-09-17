package downloadclient

import (
	"context"
	"fmt"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient/deluge"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

// Deluge adapts the Deluge RPC client to Client.
type Deluge struct {
	Client  *deluge.Client
	Mapping PathMapping
}

func (d *Deluge) Protocol() string { return newznab.ProtocolTorrent }

func (d *Deluge) Add(ctx context.Context, fetched *newznab.FetchedRelease) (string, error) {
	switch fetched.Kind {
	case newznab.KindMagnet:
		return d.Client.AddMagnet(ctx, fetched.MagnetURI, deluge.AddOptions{})
	case newznab.KindTorrentFile:
		return d.Client.AddTorrentFile(ctx, fetched.FileName, fetched.Data, deluge.AddOptions{})
	}
	return "", fmt.Errorf("deluge can't take a %s", kindName(fetched.Kind))
}

func (d *Deluge) Statuses(ctx context.Context, ids []string) (map[string]Status, error) {
	statuses, err := d.Client.GetTorrentsStatus(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Status, len(statuses))
	for id, ts := range statuses {
		st := Status{
			ID: id, Name: ts.Name, State: ts.State, Message: ts.Message, Progress: ts.Progress / 100,
			IsFinished: ts.IsFinished, Failed: ts.State == "Error" && ts.Message != "",
			SavePath: d.Mapping.Map(ts.SavePath), TotalSize: ts.TotalSize,
		}
		for _, f := range ts.Files {
			st.Files = append(st.Files, File{Path: f.Path, Size: f.Size})
		}
		out[id] = st
	}
	return out, nil
}

func (d *Deluge) Test(ctx context.Context) error {
	_, err := d.Client.GetTorrentsStatus(ctx, []string{})
	return err
}

func (d *Deluge) SetSeedRatio(ctx context.Context, id string, ratio float64) error {
	return d.Client.SetTorrentOptions(ctx, []string{id}, map[string]any{"stop_at_ratio": true, "stop_ratio": ratio})
}

func kindName(kind int) string {
	switch kind {
	case newznab.KindMagnet:
		return "magnet link"
	case newznab.KindTorrentFile:
		return "torrent file"
	case newznab.KindNZB:
		return "usenet NZB"
	}
	return "release of unknown kind"
}

func (d *Deluge) Remove(ctx context.Context, id string, deleteData bool) error {
	return d.Client.RemoveTorrent(ctx, id, deleteData)
}
