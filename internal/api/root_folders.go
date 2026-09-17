package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

type rootFolderView struct {
	ID        int64
	Path      string
	TypeLabel string
	FreeSpace string
	Unmapped  string
	BuiltIn   bool
	Removable bool
	UsedBy    string // why a folder the user added can't be removed yet
}

var mediaTypeLabels = map[string]string{"movie": "Movies", "series": "TV Series", "music": "Music"}

var mediaTypeNouns = map[string][2]string{"movie": {"movie", "movies"}, "series": {"series", "series"}, "music": {"artist", "artists"}}

// rootFolderViews adds Radarr's Free Space and Unmapped Folders columns, and
// whether each folder can be removed. A root folder UMMarr can't read shows
// "unknown" for both rather than failing the page.
func rootFolderViews(ctx context.Context, q store.Queryer, folders []store.RootFolder) []rootFolderView {
	mapped := map[string][]string{}
	views := make([]rootFolderView, 0, len(folders))
	for _, f := range folders {
		if _, ok := mapped[f.MediaType]; !ok {
			paths, _ := store.ItemFolderPaths(ctx, q, f.MediaType)
			mapped[f.MediaType] = paths
		}
		v := rootFolderView{
			ID: f.ID, Path: f.Path, TypeLabel: f.MediaType,
			FreeSpace: "unknown", Unmapped: "unknown", BuiltIn: store.IsBuiltInRootFolder(f.Path),
		}
		if label, ok := mediaTypeLabels[f.MediaType]; ok {
			v.TypeLabel = label
		}
		if n, err := importer.FreeSpace(f.Path); err == nil {
			v.FreeSpace = importer.FormatBytes(n)
		}
		var ignore []string
		if f.MediaType == "music" {
			ignore = []string{"Various Artists"}
		}
		if n, err := importer.UnmappedFolders(f.Path, mapped[f.MediaType], ignore...); err == nil {
			v.Unmapped = strconv.Itoa(n)
		}
		if !v.BuiltIn {
			if n, err := store.RootFolderUsage(ctx, q, f.ID); err == nil {
				noun := mediaTypeNouns[f.MediaType]
				switch {
				case n == 0:
					v.Removable = true
				case n == 1:
					v.UsedBy = fmt.Sprintf("Used by 1 %s", noun[0])
				default:
					v.UsedBy = fmt.Sprintf("Used by %d %s", n, noun[1])
				}
			}
		}
		views = append(views, v)
	}
	return views
}

// DeleteRootFolder removes a root folder the user added, without touching the
// folder on disk. The Settings page only offers it for folders that can go;
// anything else is refused here as well.
func (h *handler) DeleteRootFolder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid library folder id", http.StatusBadRequest)
		return
	}
	switch err := store.DeleteRootFolder(r.Context(), h.deps.DB, id); {
	case err == nil:
		w.Header().Set("HX-Redirect", "/settings/media-management")
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, store.ErrRootFolderNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, store.ErrRootFolderBuiltIn):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, store.ErrRootFolderInUse):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
