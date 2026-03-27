package tui

import "github.com/charmbracelet/lipgloss"

// -- Color palette (Catppuccin Mocha inspired) --

var (
	colorBorder    = lipgloss.Color("#585b70")
	colorBorderHi  = lipgloss.Color("#89b4fa")
	colorTitle     = lipgloss.Color("#89b4fa")
	colorTrack     = lipgloss.Color("#cdd6f4")
	colorArtist    = lipgloss.Color("#f9e2af")
	colorAlbum     = lipgloss.Color("#a6adc8")
	colorPlaying   = lipgloss.Color("#a6e3a1")
	colorPaused    = lipgloss.Color("#f9e2af")
	colorStopped   = lipgloss.Color("#f38ba8")
	colorDim       = lipgloss.Color("#6c7086")
	colorVolume    = lipgloss.Color("#fab387")
	colorProgressA = "#89b4fa"
	colorProgressB = "#cba6f7"
)

// -- Custom Winamp-style border --

var winampBorder = lipgloss.Border{
	Top: "-", Bottom: "-", Left: "|", Right: "|",
	TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
}

// -- Styles --

var (
	styleApp = lipgloss.NewStyle().
			BorderStyle(winampBorder).
			BorderForeground(colorBorderHi).
			Padding(1, 2)

	styleHeader = lipgloss.NewStyle().Bold(true).Foreground(colorTitle)
	styleTrack  = lipgloss.NewStyle().Bold(true).Foreground(colorTrack)
	styleArtist = lipgloss.NewStyle().Foreground(colorArtist)
	styleAlbum  = lipgloss.NewStyle().Foreground(colorAlbum)
	styleDim    = lipgloss.NewStyle().Foreground(colorDim)
	styleTime   = lipgloss.NewStyle().Foreground(colorAlbum)

	styleVolLabel  = lipgloss.NewStyle().Foreground(colorVolume).Bold(true)
	styleVolFilled = lipgloss.NewStyle().Foreground(colorVolume)
	styleVolEmpty  = lipgloss.NewStyle().Foreground(colorDim)

	styleZoneActive   = lipgloss.NewStyle().Foreground(colorBorderHi).Bold(true)
	styleZoneInactive = lipgloss.NewStyle().Foreground(colorBorder)
	styleSeparator    = lipgloss.NewStyle().Foreground(colorBorder)

	styleStatusPlaying = lipgloss.NewStyle().Foreground(colorPlaying).Bold(true)
	styleStatusPaused  = lipgloss.NewStyle().Foreground(colorPaused).Bold(true)
	styleStatusStopped = lipgloss.NewStyle().Foreground(colorStopped).Bold(true)
)

func stateIcon(state string) string {
	switch state {
	case "playing":
		return styleStatusPlaying.Render("[>]")
	case "paused":
		return styleStatusPaused.Render("[=]")
	case "loading":
		return styleDim.Render("[..]")
	default:
		return styleStatusStopped.Render("[x]")
	}
}

func stateLabel(state string) string {
	switch state {
	case "playing":
		return styleStatusPlaying.Render("PLAYING")
	case "paused":
		return styleStatusPaused.Render("PAUSED")
	case "loading":
		return styleDim.Render("LOADING")
	default:
		return styleStatusStopped.Render("STOPPED")
	}
}
