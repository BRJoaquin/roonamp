package lyrics

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// ParseLRC parses LRC-formatted text into time-sorted lines.
//
// Supports multiple timestamps per line ([01:23.45][02:34.56]text) and the
// common [mm:ss.xx] / [mm:ss.xxx] / [mm:ss] forms. Tag lines like [ar:...],
// [ti:...], [length:...] are skipped. Empty payloads are kept as gap markers.
func ParseLRC(text string) []Line {
	var out []Line
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}

		stamps, rest := extractTimestamps(line)
		if len(stamps) == 0 {
			continue
		}
		text := strings.TrimSpace(rest)
		for _, t := range stamps {
			out = append(out, Line{At: t, Text: text})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// extractTimestamps pulls leading [mm:ss(.xx)] groups from a line. Metadata
// tags like [ar:Artist] or [length:03:21] return no timestamps.
func extractTimestamps(line string) ([]time.Duration, string) {
	var stamps []time.Duration
	for {
		if !strings.HasPrefix(line, "[") {
			break
		}
		end := strings.IndexByte(line, ']')
		if end < 0 {
			break
		}
		inner := line[1:end]
		t, ok := parseTimestamp(inner)
		if !ok {
			// Not a timestamp -- if we already collected some, return what we have.
			// Otherwise this is a metadata tag, skip the line entirely.
			if len(stamps) == 0 {
				return nil, ""
			}
			break
		}
		stamps = append(stamps, t)
		line = line[end+1:]
	}
	return stamps, line
}

func parseTimestamp(s string) (time.Duration, bool) {
	// Format: mm:ss(.xx|.xxx)?
	colon := strings.IndexByte(s, ':')
	if colon < 1 {
		return 0, false
	}
	mm, err := strconv.Atoi(s[:colon])
	if err != nil {
		return 0, false
	}
	rest := s[colon+1:]
	var secStr, fracStr string
	if dot := strings.IndexByte(rest, '.'); dot >= 0 {
		secStr = rest[:dot]
		fracStr = rest[dot+1:]
	} else {
		secStr = rest
	}
	ss, err := strconv.Atoi(secStr)
	if err != nil {
		return 0, false
	}
	d := time.Duration(mm)*time.Minute + time.Duration(ss)*time.Second
	if fracStr != "" {
		f, err := strconv.Atoi(fracStr)
		if err != nil {
			return 0, false
		}
		// Normalize: ".5" -> 500ms, ".50" -> 500ms, ".500" -> 500ms.
		switch len(fracStr) {
		case 1:
			d += time.Duration(f) * 100 * time.Millisecond
		case 2:
			d += time.Duration(f) * 10 * time.Millisecond
		default:
			// Treat >=3 digits as milliseconds, truncating extras.
			if len(fracStr) > 3 {
				fracStr = fracStr[:3]
				f, _ = strconv.Atoi(fracStr)
			}
			d += time.Duration(f) * time.Millisecond
		}
	}
	return d, true
}

// CurrentIndex returns the index of the line currently active at position pos,
// or -1 if pos is before the first line. Lines must be time-sorted.
func CurrentIndex(lines []Line, pos time.Duration) int {
	if len(lines) == 0 {
		return -1
	}
	// Binary search for the largest index where At <= pos.
	idx := sort.Search(len(lines), func(i int) bool { return lines[i].At > pos })
	return idx - 1
}
