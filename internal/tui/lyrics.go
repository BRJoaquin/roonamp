package tui

import (
	"strings"
	"time"

	"roonamp/internal/lyrics"
	"roonamp/internal/roon"

	"github.com/charmbracelet/lipgloss"
)

type lyricsState struct {
	sig     lyrics.Signature // signature of the currently-loaded lyrics
	pending lyrics.Signature // signature we are currently fetching, if any
	lyr     *lyrics.Lyrics
	loading bool
	errMsg  string
}

func (ls *lyricsState) reset() {
	*ls = lyricsState{}
}

func trackSignature(z *roon.Zone) lyrics.Signature {
	if z == nil || z.NowPlaying == nil {
		return lyrics.Signature{}
	}
	np := z.NowPlaying
	return lyrics.Signature{
		Title:    np.ThreeLine.Line1,
		Artist:   np.ThreeLine.Line2,
		Album:    np.ThreeLine.Line3,
		Duration: np.Length,
	}
}

// -- View rendering --

func renderLyrics(m *Model) string {
	w, h := m.width, m.height
	if w == 0 {
		w = 60
	}
	if h == 0 {
		h = 24
	}

	z := m.currentZone()
	header := renderLyricsHeader(z, m.connected)

	var body string
	switch {
	case z == nil || z.NowPlaying == nil:
		body = styleDim.Render("-- nothing playing --")
	case m.lyrics.loading && m.lyrics.lyr == nil:
		body = styleDim.Render("Fetching lyrics...")
	case m.lyrics.errMsg != "" && m.lyrics.lyr == nil:
		body = styleStatusStopped.Render("Lyrics error: ") + m.lyrics.errMsg
	case m.lyrics.lyr == nil:
		body = styleDim.Render("No lyrics found for this track.")
	case len(m.lyrics.lyr.Synced) > 0:
		body = renderSynced(m.lyrics.lyr.Synced, m.effectiveSeekPos()+lyricLead, h-8, w-6)
	case len(m.lyrics.lyr.PlainLines) > 0:
		body = renderPlain(m.lyrics.lyr.PlainLines, w-6)
	default:
		body = styleDim.Render("No lyrics found for this track.")
	}

	help := styleDim.Render("[L/esc/q] back  [space] play/pause  [n/p] next/prev")

	sections := []string{
		header,
		styleDim.Render(strings.Repeat("-", w-6)),
		"",
		body,
		"",
		help,
	}

	return styleApp.Width(w - 2).Render(
		lipgloss.JoinVertical(lipgloss.Left, sections...),
	)
}

func renderLyricsHeader(z *roon.Zone, connected bool) string {
	title := styleHeader.Render("roonamp / lyrics")
	sep := styleSeparator.Render(" | ")
	suffix := ""
	if !connected {
		suffix = sep + styleStatusStopped.Render("[reconnecting...]")
	}
	if z == nil || z.NowPlaying == nil {
		return title + suffix
	}
	np := z.NowPlaying
	return title + sep +
		styleTrack.Render(np.ThreeLine.Line1) + sep +
		styleArtist.Render(np.ThreeLine.Line2) + suffix
}

// lyricLead is added to the playback position for lyric selection so a line
// lights up just before it is sung -- LRC timestamps mark the start of the
// line, which otherwise reads as late once the eye catches up.
const lyricLead = 250 * time.Millisecond

// effectiveSeekPos returns the wall-clock-interpolated playback position for
// the current zone. The anchor is maintained by updateSeekAnchor; while
// playing, this advances at real time; while paused or stopped, it holds at
// the last known position.
func (m *Model) effectiveSeekPos() time.Duration {
	z := m.currentZone()
	if z == nil || z.NowPlaying == nil {
		return 0
	}
	if m.seekAnchorPlaying && !m.seekAnchorAt.IsZero() {
		return m.seekAnchorPos + time.Since(m.seekAnchorAt)
	}
	if z.NowPlaying.SeekPosition == nil {
		return 0
	}
	return time.Duration(*z.NowPlaying.SeekPosition) * time.Second
}

// renderSynced shows a window of lines centered on the active one, with the
// active line drawn bold.
func renderSynced(lines []lyrics.Line, pos time.Duration, maxRows, maxWidth int) string {
	if maxRows < 3 {
		maxRows = 3
	}
	cur := lyrics.CurrentIndex(lines, pos)

	half := maxRows / 2
	start := cur - half
	if start < 0 {
		start = 0
	}
	end := start + maxRows
	if end > len(lines) {
		end = len(lines)
		start = end - maxRows
		if start < 0 {
			start = 0
		}
	}

	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		text := lines[i].Text
		switch {
		case text == "":
			text = styleDim.Render("...")
		case i == cur:
			text = styleTrack.Bold(true).Render(truncate(text, maxWidth))
		default:
			text = styleAlbum.Render(truncate(text, maxWidth))
		}
		out = append(out, text)
	}
	return lipgloss.JoinVertical(lipgloss.Left, out...)
}

func renderPlain(lines []string, maxWidth int) string {
	rendered := make([]string, 0, len(lines)+2)
	rendered = append(rendered, styleDim.Italic(true).Render("(unsynced lyrics)"), "")
	for _, ln := range lines {
		rendered = append(rendered, styleAlbum.Render(truncate(ln, maxWidth)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rendered...)
}
