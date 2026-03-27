package tui

import (
	"log"
	"sort"
	"time"

	"roonamp/internal/config"
	"roonamp/internal/roon"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
)

// -- Messages --

type zonesUpdatedMsg struct{ zones map[string]*roon.Zone }
type seekTickMsg time.Time
type animTickMsg time.Time
type albumArtMsg struct {
	imageKey string
	rendered string
}

// -- Model --

type Model struct {
	client *roon.Client
	zones  []*roon.Zone
	idx    int
	width  int
	height int

	progress progress.Model

	// Album art
	artRendered    string
	artImageKey    string
	artFetchingKey string
	showArt        bool

	// Harmonica springs
	swipeSpring harmonica.Spring
	swipePos    float64
	swipeVel    float64
	volSpring   harmonica.Spring
	volPulse    float64
	volVel      float64

	savedZone string // zone ID to restore on startup
	err       error
}

func NewModel(client *roon.Client) Model {
	return Model{
		client: client,
		progress: progress.New(
			progress.WithScaledGradient(colorProgressA, colorProgressB),
			progress.WithoutPercentage(),
			progress.WithFillCharacters('=', '-'),
		),
		showArt:     true,
		savedZone:   config.LoadZone(),
		swipeSpring: harmonica.NewSpring(harmonica.FPS(60), 8.0, 0.6),
		volSpring:   harmonica.NewSpring(harmonica.FPS(60), 10.0, 0.4),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.listenForZones(),
		seekTickCmd(),
		animTickCmd(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case zonesUpdatedMsg:
		m.applyZones(msg.zones)
		cmd := m.listenForZones()
		if artCmd := m.maybeUpdateArt(); artCmd != nil {
			return m, tea.Batch(cmd, artCmd)
		}
		return m, cmd

	case seekTickMsg:
		m.tickSeek()
		return m, seekTickCmd()

	case animTickMsg:
		m.tickAnim()
		return m, animTickCmd()

	case albumArtMsg:
		if msg.imageKey == m.artFetchingKey {
			m.artRendered = msg.rendered
			m.artImageKey = msg.imageKey
		}
		return m, nil

	case progress.FrameMsg:
		model, cmd := m.progress.Update(msg)
		m.progress = model.(progress.Model)
		return m, cmd
	}

	return m, nil
}

func (m Model) View() string {
	w, h := m.width, m.height
	if w == 0 {
		w = 60
	}
	if h == 0 {
		h = 24
	}

	if m.err != nil {
		return styleApp.Width(w - 2).Render(
			styleStatusStopped.Render("Error: ") + m.err.Error() + "\n\n" +
				styleDim.Render("[q] quit"),
		)
	}

	return renderPlayer(playerState{
		zones:       m.zones,
		idx:         m.idx,
		width:       w,
		height:      h,
		prog:        m.progress,
		swipeOffset: m.swipePos,
		volPulse:    m.volPulse,
		artRendered: m.artRendered,
		showArt:     m.showArt,
	})
}

// -- Key handling --

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "right", "l", ">", ".":
		return m, m.switchZone(1)
	case "left", "h", "<", ",":
		return m, m.switchZone(-1)

	case " ":
		return m, m.controlCmd("playpause")
	case "n":
		return m, m.controlCmd("next")
	case "p":
		return m, m.controlCmd("previous")
	case "s":
		return m, m.controlCmd("stop")

	case "+", "=":
		m.volPulse, m.volVel = 5, 0
		return m, m.volumeCmd(5)
	case "-":
		m.volPulse, m.volVel = -5, 0
		return m, m.volumeCmd(-5)

	case "a":
		m.showArt = !m.showArt
		return m, nil
	}

	return m, nil
}

// -- Zone switching --

func (m *Model) switchZone(delta int) tea.Cmd {
	if len(m.zones) <= 1 {
		return nil
	}
	m.idx = (m.idx + delta + len(m.zones)) % len(m.zones)
	if delta > 0 {
		m.swipePos, m.swipeVel = 20, 0
	} else {
		m.swipePos, m.swipeVel = -20, 0
	}
	m.saveCurrentZone()
	return m.maybeUpdateArt()
}

// -- Transport commands --

func (m Model) controlCmd(control string) tea.Cmd {
	return func() tea.Msg {
		if z := m.currentZone(); z != nil {
			if err := m.client.Control(z.ZoneID, control); err != nil {
				log.Printf("control %s: %v", control, err)
			}
		}
		return nil
	}
}

func (m Model) volumeCmd(delta float64) tea.Cmd {
	return func() tea.Msg {
		z := m.currentZone()
		if z == nil || len(z.Outputs) == 0 || z.Outputs[0].Volume == nil {
			return nil
		}
		if err := m.client.ChangeVolume(z.Outputs[0].OutputID, "relative", delta); err != nil {
			log.Printf("volume: %v", err)
		}
		return nil
	}
}

// -- Album art --

func (m *Model) maybeUpdateArt() tea.Cmd {
	z := m.currentZone()
	if z == nil || z.NowPlaying == nil {
		m.artRendered = ""
		m.artImageKey = ""
		return nil
	}

	key := z.NowPlaying.ImageKey
	if key == "" {
		m.artRendered = renderPlaceholder()
		m.artImageKey = ""
		return nil
	}
	if key == m.artImageKey || key == m.artFetchingKey {
		return nil
	}

	m.artFetchingKey = key
	client := m.client
	return func() tea.Msg {
		rendered, err := FetchAndRenderArt(client, key)
		if err != nil {
			log.Printf("album art: %v", err)
		}
		return albumArtMsg{imageKey: key, rendered: rendered}
	}
}

// -- Ticks --

func seekTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return seekTickMsg(t) })
}

func animTickCmd() tea.Cmd {
	return tea.Tick(time.Second/60, func(t time.Time) tea.Msg { return animTickMsg(t) })
}

func (m Model) listenForZones() tea.Cmd {
	return func() tea.Msg {
		ch := make(chan map[string]*roon.Zone, 1)
		m.client.OnZonesUpdated = func(zones map[string]*roon.Zone) {
			select {
			case ch <- zones:
			default:
			}
		}
		return zonesUpdatedMsg{zones: <-ch}
	}
}

// -- Animation --

func (m *Model) tickAnim() {
	m.swipePos, m.swipeVel = m.swipeSpring.Update(m.swipePos, m.swipeVel, 0)
	if nearZero(m.swipePos, 0.5) && nearZero(m.swipeVel, 0.1) {
		m.swipePos, m.swipeVel = 0, 0
	}

	m.volPulse, m.volVel = m.volSpring.Update(m.volPulse, m.volVel, 0)
	if nearZero(m.volPulse, 0.3) {
		m.volPulse, m.volVel = 0, 0
	}
}

func nearZero(v, threshold float64) bool {
	return v > -threshold && v < threshold
}

// -- Zone helpers --

func (m *Model) applyZones(zoneMap map[string]*roon.Zone) {
	ids := make([]string, 0, len(zoneMap))
	for id := range zoneMap {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	m.zones = make([]*roon.Zone, len(ids))
	for i, id := range ids {
		m.zones[i] = zoneMap[id]
	}

	// Restore saved zone on first load
	if m.savedZone != "" {
		for i, id := range ids {
			if id == m.savedZone {
				m.idx = i
				break
			}
		}
		m.savedZone = ""
	}

	if m.idx >= len(m.zones) {
		m.idx = 0
	}
}

func (m *Model) currentZone() *roon.Zone {
	if m.idx < len(m.zones) {
		return m.zones[m.idx]
	}
	return nil
}

func (m *Model) saveCurrentZone() {
	if z := m.currentZone(); z != nil {
		config.SaveZone(z.ZoneID)
	}
}

func (m *Model) tickSeek() {
	z := m.currentZone()
	if z == nil || z.NowPlaying == nil || z.State != "playing" {
		return
	}
	if z.NowPlaying.SeekPosition < z.NowPlaying.Length {
		z.NowPlaying.SeekPosition++
	}
}
