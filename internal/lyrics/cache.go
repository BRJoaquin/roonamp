package lyrics

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// CacheDir returns the on-disk cache directory. Returns "" if no usable home
// is available, in which case the caller should skip caching.
func CacheDir() string {
	dir := os.Getenv("XDG_CACHE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		dir = filepath.Join(home, ".cache")
	}
	return filepath.Join(dir, "roonamp", "lyrics")
}

type cacheEntry struct {
	Synced     []cacheLine `json:"synced,omitempty"`
	PlainLines []string    `json:"plain,omitempty"`
	NotFound   bool        `json:"not_found,omitempty"`
}

type cacheLine struct {
	Ms   int64  `json:"ms"`
	Text string `json:"t"`
}

func cachePath(sig Signature) string {
	dir := CacheDir()
	if dir == "" {
		return ""
	}
	sum := sha1.Sum([]byte(sig.Key()))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
}

// LoadCache returns a cached Lyrics result for sig. It returns ErrNotFound if
// the cache previously recorded a miss, (nil, nil) if there is no cache entry,
// or the cached lyrics otherwise.
func LoadCache(sig Signature) (*Lyrics, error) {
	path := cachePath(sig)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	if e.NotFound {
		return nil, ErrNotFound
	}
	out := &Lyrics{PlainLines: e.PlainLines}
	for _, l := range e.Synced {
		out.Synced = append(out.Synced, Line{
			At:   time.Duration(l.Ms) * time.Millisecond,
			Text: l.Text,
		})
	}
	return out, nil
}

// SaveCache writes lyrics (or a not-found marker) to disk. Best-effort.
func SaveCache(sig Signature, lyr *Lyrics, notFound bool) {
	path := cachePath(sig)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	var e cacheEntry
	if notFound {
		e.NotFound = true
	} else if lyr != nil {
		e.PlainLines = lyr.PlainLines
		for _, l := range lyr.Synced {
			e.Synced = append(e.Synced, cacheLine{
				Ms:   l.At.Milliseconds(),
				Text: l.Text,
			})
		}
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
}
