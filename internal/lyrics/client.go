package lyrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	lrclibBase = "https://lrclib.net/api/get"
	userAgent  = "roonamp (https://github.com/brokenrubik/roonamp)"
	httpTimeo  = 8 * time.Second
)

// httpClient is package-level so tests / future flags can override it.
var httpClient = &http.Client{Timeout: httpTimeo}

type lrclibResponse struct {
	PlainLyrics  string `json:"plainLyrics"`
	SyncedLyrics string `json:"syncedLyrics"`
}

// Fetch queries LRCLIB for the given signature. Returns ErrNotFound (404) if
// no match exists. Network/parse errors are returned as-is.
//
// Roon joins collaborators as "A / B / C" in the artist line, which rarely
// matches LRCLIB's single-artist field, so a not-found result is retried once
// with just the primary artist.
func Fetch(sig Signature) (*Lyrics, error) {
	lyr, err := fetchOnce(sig)
	if !errors.Is(err, ErrNotFound) {
		return lyr, err
	}
	if primary, _, found := strings.Cut(sig.Artist, " / "); found {
		retry := sig
		retry.Artist = strings.TrimSpace(primary)
		return fetchOnce(retry)
	}
	return nil, ErrNotFound
}

func fetchOnce(sig Signature) (*Lyrics, error) {
	if sig.Empty() {
		return nil, ErrNotFound
	}

	q := url.Values{}
	q.Set("track_name", sig.Title)
	q.Set("artist_name", sig.Artist)
	if sig.Album != "" {
		q.Set("album_name", sig.Album)
	}
	if sig.Duration > 0 {
		q.Set("duration", fmt.Sprintf("%d", sig.Duration))
	}

	req, err := http.NewRequest("GET", lrclibBase+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("lrclib http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var r lrclibResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decode lrclib: %w", err)
	}

	out := &Lyrics{}
	if r.SyncedLyrics != "" {
		out.Synced = ParseLRC(r.SyncedLyrics)
	}
	if r.PlainLyrics != "" {
		for _, ln := range strings.Split(r.PlainLyrics, "\n") {
			out.PlainLines = append(out.PlainLines, strings.TrimRight(ln, "\r"))
		}
	}
	if len(out.Synced) == 0 && len(out.PlainLines) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}
