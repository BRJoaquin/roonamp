package tui

import (
	"fmt"
	"math"
	"strings"

	"roonamp/internal/roon"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
)

type playerState struct {
	zones        []*roon.Zone
	idx          int
	width        int
	height       int
	contentWidth int
	prog         progress.Model
	swipeOffset  float64
	volPulse     float64
	volVisible   bool
	artRendered  string
	showArt      bool
}

func renderPlayer(ps playerState) string {
	ps.contentWidth = ps.width - 6 // border + padding
	if ps.contentWidth < 30 {
		ps.contentWidth = 30
	}
	ps.prog.Width = ps.contentWidth - 16 // leave room for time display

	var sections []string

	// -- Header bar --
	sections = append(sections, renderHeader(ps.zones, ps.idx))
	sections = append(sections, styleDim.Render(strings.Repeat("-", ps.contentWidth)))

	if len(ps.zones) == 0 {
		sections = append(sections, styleDim.Render("No zones available"))
		sections = append(sections, "")
		sections = append(sections, styleDim.Render("[q] quit"))
		return styleApp.Width(ps.width - 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, sections...),
		)
	}

	z := ps.zones[clamp(ps.idx, 0, len(ps.zones)-1)]

	// -- Album art + track info side by side --
	sections = append(sections, renderArtAndInfo(z, ps))

	// -- Progress bar --
	progLine := renderProgressBar(z, ps.prog)
	if progLine != "" {
		sections = append(sections, "")
		sections = append(sections, progLine)
	}

	// -- Volume + settings on same line --
	var statusParts []string
	if ps.volVisible {
		if volLine := renderVolume(z); volLine != "" {
			statusParts = append(statusParts, volLine)
		}
	}
	if infoLine := renderZoneInfo(z); infoLine != "" {
		statusParts = append(statusParts, infoLine)
	}
	if len(statusParts) > 0 {
		sections = append(sections, strings.Join(statusParts, styleSeparator.Render(" | ")))
	}

	// -- Zone switcher --
	if len(ps.zones) > 1 {
		sections = append(sections, "")
		sections = append(sections, renderZoneSwitcher(ps.zones, ps.idx))
	}

	// -- Help --
	sections = append(sections, "")
	sections = append(sections, renderHelpBar())

	return styleApp.Width(ps.width - 2).Render(
		lipgloss.JoinVertical(lipgloss.Left, sections...),
	)
}

// -- Album art + track info --

func renderArtAndInfo(z *roon.Zone, ps playerState) string {
	// Calculate text width accounting for art
	textWidth := ps.contentWidth
	if ps.showArt && ps.artRendered != "" {
		artWidth := lipgloss.Width(ps.artRendered) + 2 // art + gap
		textWidth = ps.contentWidth - artWidth
		if textWidth < 20 {
			textWidth = 20
		}
		info := renderNowPlaying(z, ps.swipeOffset, textWidth)
		return lipgloss.JoinHorizontal(lipgloss.Top, ps.artRendered, "  ", info)
	}

	return renderNowPlaying(z, ps.swipeOffset, textWidth)
}

// -- Header --

func renderHeader(zones []*roon.Zone, idx int) string {
	title := styleHeader.Render("roonamp")
	sep := styleSeparator.Render(" | ")

	if len(zones) == 0 {
		return title + sep + styleDim.Render("--")
	}

	z := zones[clamp(idx, 0, len(zones)-1)]
	zone := styleZoneActive.Render(z.DisplayName)
	state := stateIcon(z.State) + " " + stateLabel(z.State)

	return title + sep + zone + sep + state
}

// -- Now Playing --

func renderNowPlaying(z *roon.Zone, offset float64, maxWidth int) string {
	if z.NowPlaying == nil {
		return "\n" + styleDim.Render("-- nothing playing --") + "\n"
	}

	np := z.NowPlaying

	// Apply swipe offset as leading spaces for slide effect
	pad := ""
	off := int(math.Round(offset))
	if off > 0 {
		pad = strings.Repeat(" ", off)
	}

	track := pad + styleTrack.Render(truncate(np.ThreeLine.Line1, maxWidth))
	artist := pad + styleArtist.Render(truncate(np.ThreeLine.Line2, maxWidth))
	album := pad + styleAlbum.Render(truncate(np.ThreeLine.Line3, maxWidth))

	// Queue info
	var info string
	if z.QueueItemsRemaining > 0 {
		info = styleDim.Render(fmt.Sprintf(
			"%d tracks remaining (%s)",
			z.QueueItemsRemaining, fmtTime(z.QueueTimeRemaining),
		))
	}

	lines := []string{track, artist, album}
	if info != "" {
		lines = append(lines, info)
	}

	return "\n" + lipgloss.JoinVertical(lipgloss.Left, lines...) + "\n"
}

func truncate(s string, maxWidth int) string {
	if len(s) <= maxWidth {
		return s
	}
	if maxWidth <= 3 {
		return s[:maxWidth]
	}
	return s[:maxWidth-3] + "..."
}

// -- Progress --

func renderProgressBar(z *roon.Zone, prog progress.Model) string {
	if z.NowPlaying == nil || z.NowPlaying.Length == 0 {
		return ""
	}

	np := z.NowPlaying
	bar := prog.View()

	seekPos := 0
	if np.SeekPosition != nil {
		seekPos = *np.SeekPosition
	}
	timeStr := styleTime.Render(
		fmt.Sprintf("  %s / %s", fmtTime(seekPos), fmtTime(np.Length)),
	)

	return bar + timeStr
}

// -- Volume --

func renderVolume(z *roon.Zone) string {
	if len(z.Outputs) == 0 || z.Outputs[0].Volume == nil {
		return ""
	}
	v := z.Outputs[0].Volume
	if v.IsMuted {
		return styleVolLabel.Render("VOL MUTED")
	}
	pct := int((v.Value - v.Min) / (v.Max - v.Min) * 100)
	return styleVolLabel.Render(fmt.Sprintf("VOL %d%%", pct))
}

// -- Zone info --

func renderZoneInfo(z *roon.Zone) string {
	var parts []string

	if z.Settings != nil {
		if z.Settings.Shuffle {
			parts = append(parts, "shuffle")
		}
		if z.Settings.Loop != "" && z.Settings.Loop != "disabled" {
			parts = append(parts, "loop:"+z.Settings.Loop)
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return styleDim.Render(strings.Join(parts, " | "))
}

// -- Zone switcher --

func renderZoneSwitcher(zones []*roon.Zone, active int) string {
	var parts []string
	for i, z := range zones {
		name := z.DisplayName
		if len(name) > 14 {
			name = name[:13] + "."
		}
		icon := stateIcon(z.State)
		if i == active {
			parts = append(parts, styleZoneActive.Render(fmt.Sprintf("(*) %s %s", icon, name)))
		} else {
			parts = append(parts, styleZoneInactive.Render(fmt.Sprintf("( ) %s %s", icon, name)))
		}
	}
	return styleDim.Render("<") + " " +
		strings.Join(parts, styleSeparator.Render(" | ")) +
		" " + styleDim.Render(">")
}

// -- Help bar --

func renderHelpBar() string {
	groups := []string{
		"[space] play/pause",
		"[p/n] prev/next",
		"[-/+] vol",
		"[</>] zone",
		"[b] browse",
		"[a] art",
		"[q] quit",
	}
	return styleDim.Render(strings.Join(groups, "  "))
}

// -- Helpers --

func fmtTime(seconds int) string {
	m := seconds / 60
	s := seconds % 60
	return fmt.Sprintf("%02d:%02d", m, s)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
