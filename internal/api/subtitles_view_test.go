package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// A file with 30 subtitle languages reads as "English +29" on the episode
// row, with the full list only when expanded.
func TestSubtitles_CollapseToFirstAndCount(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()
	seriesID := seedTestSeries(t, db)
	var episodeID int64
	db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&episodeID)
	fileID, _ := store.AttachEpisodeFile(ctx, db, episodeID, "Season 01/ep.mkv", 1)
	info := mediainfo.Info{
		Schema: mediainfo.Schema, Source: mediainfo.SourceFFprobe, AnalyzedAt: time.Now(), VideoCodec: "h265", AudioCodec: "HE-AAC", AudioChannels: 5.1,
		AudioLanguages: []string{"eng"},
		Subtitles:      []string{"ara", "bul", "chi", "cze", "dan", "eng", "fre", "ger", "gre", "heb"},
	}
	if err := store.SaveMediaInfo(ctx, db, "episode", fileID, info); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, srv, "/tv/"+itoa(seriesID))
	cell := subtitleCell(t, body)
	for _, want := range []string{"<details class=\"lang-list\">", "<summary>English <span class=\"lang-more\">+9</span></summary>", "Arabic, Bulgarian"} {
		if !strings.Contains(cell, want) {
			t.Errorf("want %q in the subtitles cell, got:\n%s", want, cell)
		}
	}
	if strings.Contains(cell[:strings.Index(cell, "</summary>")], "Arabic") {
		t.Error("want only the first language before the count, the rest hidden until expanded")
	}

	// One or two languages read fine as they are.
	info.Subtitles = []string{"eng", "fre"}
	store.SaveMediaInfo(ctx, db, "episode", fileID, info)
	_, body = get(t, srv, "/tv/"+itoa(seriesID))
	if cell := subtitleCell(t, body); strings.Contains(cell, "lang-list") || !strings.Contains(cell, "English, French") {
		t.Errorf("want one or two languages shown plainly, got:\n%s", cell)
	}
}

// subtitleCell is the episode row's Subtitles cell, not the column heading.
func subtitleCell(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<td data-col="subtitles">`)
	if start < 0 {
		t.Fatal("no subtitles cell on the page")
	}
	cell := body[start:]
	return cell[:strings.Index(cell, "</td>")]
}
