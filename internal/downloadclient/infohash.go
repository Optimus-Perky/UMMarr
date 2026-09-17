package downloadclient

import (
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// MagnetInfoHash is the lower-case hex infohash a magnet link names.
func MagnetInfoHash(magnet string) (string, error) {
	u, err := url.Parse(magnet)
	if err != nil {
		return "", err
	}
	for _, xt := range u.Query()["xt"] {
		if !strings.HasPrefix(strings.ToLower(xt), "urn:btih:") {
			continue
		}
		h := xt[len("urn:btih:"):]
		switch len(h) {
		case 40:
			return strings.ToLower(h), nil
		case 32:
			raw, err := base32.StdEncoding.DecodeString(strings.ToUpper(h))
			if err != nil {
				return "", fmt.Errorf("magnet: bad base32 infohash: %w", err)
			}
			return hex.EncodeToString(raw), nil
		}
	}
	return "", fmt.Errorf("magnet link has no infohash")
}

// TorrentInfoHash is the lower-case hex infohash of a .torrent file: the
// SHA-1 of its bencoded "info" dictionary.
func TorrentInfoHash(data []byte) (string, error) {
	end, err := bencodeEnd(data, 0)
	if err != nil {
		return "", fmt.Errorf("torrent: %w", err)
	}
	if end > len(data) || data[0] != 'd' {
		return "", fmt.Errorf("torrent: not a bencoded dictionary")
	}
	pos := 1
	for pos < end-1 {
		keyEnd, err := bencodeEnd(data, pos)
		if err != nil {
			return "", fmt.Errorf("torrent: %w", err)
		}
		key, err := bencodeString(data, pos)
		if err != nil {
			return "", fmt.Errorf("torrent: %w", err)
		}
		valueEnd, err := bencodeEnd(data, keyEnd)
		if err != nil {
			return "", fmt.Errorf("torrent: %w", err)
		}
		if key == "info" {
			sum := sha1.Sum(data[keyEnd:valueEnd])
			return hex.EncodeToString(sum[:]), nil
		}
		pos = valueEnd
	}
	return "", fmt.Errorf("torrent: no info dictionary")
}

func bencodeString(data []byte, pos int) (string, error) {
	colon := strings.IndexByte(string(data[pos:]), ':')
	if colon < 0 {
		return "", fmt.Errorf("bad string at %d", pos)
	}
	n, err := strconv.Atoi(string(data[pos : pos+colon]))
	if err != nil || pos+colon+1+n > len(data) {
		return "", fmt.Errorf("bad string length at %d", pos)
	}
	return string(data[pos+colon+1 : pos+colon+1+n]), nil
}

// bencodeEnd is the index just past the bencoded value starting at pos.
func bencodeEnd(data []byte, pos int) (int, error) {
	if pos >= len(data) {
		return 0, fmt.Errorf("unexpected end")
	}
	switch c := data[pos]; {
	case c == 'i':
		e := strings.IndexByte(string(data[pos:]), 'e')
		if e < 0 {
			return 0, fmt.Errorf("bad integer at %d", pos)
		}
		return pos + e + 1, nil
	case c == 'l' || c == 'd':
		p := pos + 1
		for p < len(data) && data[p] != 'e' {
			next, err := bencodeEnd(data, p)
			if err != nil {
				return 0, err
			}
			p = next
		}
		if p >= len(data) {
			return 0, fmt.Errorf("unterminated %c at %d", c, pos)
		}
		return p + 1, nil
	case c >= '0' && c <= '9':
		colon := strings.IndexByte(string(data[pos:]), ':')
		if colon < 0 {
			return 0, fmt.Errorf("bad string at %d", pos)
		}
		n, err := strconv.Atoi(string(data[pos : pos+colon]))
		if err != nil {
			return 0, fmt.Errorf("bad string length at %d", pos)
		}
		return pos + colon + 1 + n, nil
	}
	return 0, fmt.Errorf("bad value at %d", pos)
}
