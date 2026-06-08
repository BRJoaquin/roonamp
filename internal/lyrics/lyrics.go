// Package lyrics fetches and parses synced lyrics from LRCLIB.
//
// LRCLIB (https://lrclib.net) is a free, key-less synced-lyrics database.
// Roon's extension API does not expose lyrics, so we look them up out-of-band
// using the now_playing metadata (title/artist/album/duration) as the signature.
package lyrics

import (
	"errors"
	"strconv"
	"time"
)

// Line is one entry from a parsed LRC file.
type Line struct {
	At   time.Duration
	Text string
}

// Lyrics is the result of a lookup. Synced lines drive the timed view; if
// only plain-text lyrics are available, PlainLines is populated instead.
type Lyrics struct {
	Synced     []Line
	PlainLines []string
}

// Signature uniquely identifies a track for LRCLIB lookup.
type Signature struct {
	Title    string
	Artist   string
	Album    string
	Duration int // seconds
}

func (s Signature) Empty() bool {
	return s.Title == "" || s.Artist == ""
}

// Key returns a stable cache key for the signature.
func (s Signature) Key() string {
	return s.Title + "\x1f" + s.Artist + "\x1f" + s.Album + "\x1f" + strconv.Itoa(s.Duration)
}

// ErrNotFound is returned when LRCLIB has no entry for the signature.
var ErrNotFound = errors.New("lyrics not found")
