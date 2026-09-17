package downloadclient

import (
	"crypto/sha1"
	"encoding/hex"
	"testing"
)

func TestMagnetInfoHash(t *testing.T) {
	hexHash := "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	for _, magnet := range []string{
		"magnet:?xt=urn:btih:C12FE1C06BBA254A9DC9F519B335AA7C1367A88A&dn=x",
		"magnet:?dn=x&xt=urn:btih:YEX6DQDLXISUVHOJ6UM3GNNKPQJWPKEK",
	} {
		got, err := MagnetInfoHash(magnet)
		if err != nil || got != hexHash {
			t.Errorf("%s: want %s, got %s (%v)", magnet, hexHash, got, err)
		}
	}
	if _, err := MagnetInfoHash("magnet:?dn=nothing"); err == nil {
		t.Errorf("want an error without an infohash")
	}
}

func TestTorrentInfoHash(t *testing.T) {
	info := "d6:lengthi5e4:name5:a.mkv12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaae"
	torrent := "d8:announce9:http://tr7:comment3:hey4:info" + info + "e"
	sum := sha1.Sum([]byte(info))
	got, err := TorrentInfoHash([]byte(torrent))
	if err != nil || got != hex.EncodeToString(sum[:]) {
		t.Fatalf("want %s, got %s (%v)", hex.EncodeToString(sum[:]), got, err)
	}
	if _, err := TorrentInfoHash([]byte("d8:announce9:http://tre")); err == nil {
		t.Errorf("want an error without an info dictionary")
	}
}

func TestPathMapping(t *testing.T) {
	m := PathMapping{Remote: "/downloads", Local: "/data/torrents"}
	if got := m.Map("/downloads/complete/x"); got != "/data/torrents/complete/x" {
		t.Errorf("got %s", got)
	}
	if got := m.Map("/other/x"); got != "/other/x" {
		t.Errorf("got %s", got)
	}
	if got := (PathMapping{}).Map("/downloads/x"); got != "/downloads/x" {
		t.Errorf("empty mapping changed the path: %s", got)
	}
}
