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
	zones       []*roon.Zone
	idx         int
	width       int
	height      int
	prog        progress.Model
	swipeOffset float64
	volPulse    float64
	artRendered string
	showArt     bool
}

func renderPlayer(ps playerState) string {
	contentWidth := ps.width - 6 // border + padding
	if contentWidth < 30 {
		contentWidth = 30
	}
	ps.prog.Width = contentWidth - 16 // leave room for time display

	var sections []string

	// -- Header bar --
	sections = append(sections, renderHeader(ps.zones, ps.idx))
	sections = append(sections, styleDim.Render(strings.Repeat("-", contentWidth)))

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
		sections = append(sections, progLine)
	}

	// -- Volume --
	volLine := renderVolumeBar(z, contentWidth, ps.volPulse)
	if volLine != "" {
		sections = append(sections, volLine)
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
	info := renderNowPlaying(z, ps.swipeOffset)

	if ps.showArt && ps.artRendered != "" {
		return lipgloss.JoinHorizontal(lipgloss.Top, ps.artRendered, "  ", info)
	}

	return info
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

func renderNowPlaying(z *roon.Zone, offset float64) string {
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

	track := pad + styleTrack.Render(np.ThreeLine.Line1)
	artist := pad + styleArtist.Render(np.ThreeLine.Line2)
	album := pad + styleAlbum.Render(np.ThreeLine.Line3)

	return "\n" + lipgloss.JoinVertical(lipgloss.Left, track, artist, album) + "\n"
}

// -- Progress --

func renderProgressBar(z *roon.Zone, prog progress.Model) string {
	if z.NowPlaying == nil || z.NowPlaying.Length == 0 {
		return ""
	}

	np := z.NowPlaying
	pct := float64(np.SeekPosition) / float64(np.Length)
	if pct > 1 {
		pct = 1
	}

	bar := prog.ViewAs(pct)
	timeStr := styleTime.Render(
		fmt.Sprintf("  %s / %s", fmtTime(np.SeekPosition), fmtTime(np.Length)),
	)

	return bar + timeStr
}

// -- Volume --

func renderVolumeBar(z *roon.Zone, width int, pulse float64) string {
	if len(z.Outputs) == 0 || z.Outputs[0].Volume == nil {
		return ""
	}

	v := z.Outputs[0].Volume

	label := "VOL"
	if v.IsMuted {
		label = "MUT"
	}

	barWidth := width - 12
	if barWidth < 10 {
		barWidth = 10
	}

	pct := (v.Value - v.Min) / (v.Max - v.Min)
	filled := int(pct * float64(barWidth))

	// Pulse glow from harmonica spring
	pulseExtra := int(math.Abs(pulse) * 3)
	if filled+pulseExtra > barWidth {
		pulseExtra = barWidth - filled
	}
	if pulseExtra < 0 {
		pulseExtra = 0
	}

	bar := styleVolFilled.Render(strings.Repeat("#", filled))
	if pulseExtra > 0 {
		bar += styleVolLabel.Render(strings.Repeat(":", pulseExtra))
	}
	bar += styleVolEmpty.Render(strings.Repeat(".", barWidth-filled-pulseExtra))

	valStr := fmt.Sprintf("%.0f", v.Value)

	return styleVolLabel.Render(label) + " [" + bar + "] " + styleVolLabel.Render(valStr)
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
