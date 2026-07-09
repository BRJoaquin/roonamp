package tui

import (
	"errors"
	"log"
	"sort"
	"strings"
	"time"

	"roonamp/internal/config"
	"roonamp/internal/lyrics"
	"roonamp/internal/roon"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
)

// -- Views --

const (
	viewPlayer = iota
	viewBrowser
	viewLyrics
)

// -- Messages --

type zonesUpdatedMsg struct{ zones map[string]*roon.Zone }
type seekTickMsg time.Time
type animTickMsg time.Time
type albumArtMsg struct {
	imageKey string
	rendered string
}
type lyricsLoadedMsg struct {
	sig    lyrics.Signature
	lyr    *lyrics.Lyrics
	errMsg string
}
type radioResultMsg struct{ err error }

// statusLine is a transient one-line message that auto-hides 5 seconds after
// it was last set; isErr selects the error styling.
type statusLine struct {
	msg   string
	isErr bool
	at    time.Time
}

func (s *statusLine) set(msg string, isErr bool) {
	s.msg = msg
	s.isErr = isErr
	s.at = time.Now()
}

func (s *statusLine) visible() bool {
	return s.msg != "" && time.Since(s.at) < 5*time.Second
}

// -- Model --

type Model struct {
	client *roon.Client
	zoneCh chan map[string]*roon.Zone
	zones  []*roon.Zone
	idx    int
	width  int
	height int
	view   int

	progress progress.Model
	browser  browserModel
	lyrics   lyricsState

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

	// Volume auto-hide
	volLastTouch time.Time
	volLastValue float64

	// Transient player status line (e.g. radio start feedback).
	status statusLine

	// Sub-second seek anchor for lyric line timing. See effectiveSeekPos and
	// updateSeekAnchor for how this is maintained and consumed.
	seekAnchorAt      time.Time
	seekAnchorPos     time.Duration
	seekAnchorPlaying bool
	lastTrackSig      lyrics.Signature

	savedZone string // zone ID to restore on startup
	connected bool   // WebSocket link state, refreshed on each seek tick
	err       error
}

func NewModel(client *roon.Client) Model {
	// The zone channel bridges the read-loop goroutine into the Bubble Tea
	// loop: the callback (installed once, here) pushes snapshots, and the
	// recurring listenForZones command receives them as messages. Latest-wins:
	// a queued update is replaced rather than the new one dropped. Pushes are
	// serialized (one read-loop goroutine), so the drain cannot race another
	// sender.
	zoneCh := make(chan map[string]*roon.Zone, 1)
	client.SetOnZonesUpdated(func(zones map[string]*roon.Zone) {
		select {
		case zoneCh <- zones:
		default:
			select {
			case <-zoneCh:
			default:
			}
			zoneCh <- zones
		}
	})

	return Model{
		client: client,
		zoneCh: zoneCh,
		progress: progress.New(
			progress.WithScaledGradient(colorProgressA, colorProgressB),
			progress.WithoutPercentage(),
		),
		browser:     newBrowser(client),
		showArt:     config.LoadShowArt(),
		savedZone:   config.LoadZone(),
		connected:   true,
		swipeSpring: harmonica.NewSpring(harmonica.FPS(60), 8.0, 0.6),
		volSpring:   harmonica.NewSpring(harmonica.FPS(60), 10.0, 0.4),
	}
}

func (m Model) Init() tea.Cmd {
	client := m.client
	loadExisting := func() tea.Msg {
		if zones := client.Zones(); len(zones) > 0 {
			return zonesUpdatedMsg{zones: zones}
		}
		return nil
	}

	return tea.Batch(
		loadExisting,
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
		if m.view == viewBrowser {
			m.browser.setSize(msg.Width, msg.Height)
		}
		return m, nil

	case zonesUpdatedMsg:
		m.applyZones(msg.zones)
		cmds := []tea.Cmd{m.listenForZones()}
		if artCmd := m.maybeUpdateArt(); artCmd != nil {
			cmds = append(cmds, artCmd)
		}
		if lyrCmd := m.maybeUpdateLyrics(); lyrCmd != nil {
			cmds = append(cmds, lyrCmd)
		}
		if z := m.currentZone(); z != nil {
			if len(z.Outputs) > 0 && z.Outputs[0].Volume != nil {
				if v := z.Outputs[0].Volume.Value; v != m.volLastValue {
					m.volLastTouch = time.Now()
					m.volLastValue = v
				}
			}
			m.updateSeekAnchor(z)
		}
		return m, tea.Batch(cmds...)

	case lyricsLoadedMsg:
		// Only apply if it matches what we asked for (track may have changed
		// while the fetch was in flight).
		if msg.sig == m.lyrics.pending {
			m.lyrics.sig = msg.sig
			m.lyrics.lyr = msg.lyr
			m.lyrics.errMsg = msg.errMsg
			m.lyrics.loading = false
			m.lyrics.pending = lyrics.Signature{}
		}
		return m, nil

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

	case radioResultMsg:
		if msg.err != nil {
			m.status.set("radio failed: "+msg.err.Error(), true)
		} else {
			m.status.set("radio started", false)
		}
		return m, nil

	case browseResultMsg:
		if msg.done {
			m.view = viewPlayer
			return m, nil
		}
		m.browser.applyResult(msg)
		return m, nil

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

	if m.view == viewBrowser {
		m.browser.setSize(w, h)
		return m.browser.view()
	}

	if m.view == viewLyrics {
		return renderLyrics(&m)
	}

	volVisible := !m.volLastTouch.IsZero() && time.Since(m.volLastTouch) < 5*time.Second

	status := ""
	if m.status.visible() {
		status = m.status.msg
	}

	return renderPlayer(playerState{
		zones:       m.zones,
		idx:         m.idx,
		width:       w,
		height:      h,
		prog:        m.progress,
		seekPos:     int(m.effectiveSeekPos() / time.Second),
		swipeOffset: m.swipePos,
		volPulse:    m.volPulse,
		volVisible:  volVisible,
		artRendered: m.artRendered,
		showArt:     m.showArt,
		connected:   m.connected,
		status:      status,
		statusErr:   m.status.isErr,
	})
}

// -- Key handling --

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global quit
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	// Browser view keys
	if m.view == viewBrowser {
		return m.handleBrowserKey(msg)
	}

	// Lyrics view keys
	if m.view == viewLyrics {
		return m.handleLyricsKey(msg)
	}

	// Player view keys
	switch msg.String() {
	case "q":
		return m, tea.Quit

	case "right", ">", ".":
		return m, m.switchZone(1)
	case "left", "<", ",":
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
		m.volPulse, m.volVel = 1, 0
		m.volLastTouch = time.Now()
		return m, m.volumeCmd(1)
	case "-":
		m.volPulse, m.volVel = -1, 0
		m.volLastTouch = time.Now()
		return m, m.volumeCmd(-1)

	case "a":
		m.showArt = !m.showArt
		config.SaveShowArt(m.showArt)
		return m, nil

	case "b":
		return m.openBrowser()

	case "L", "l":
		return m.openLyrics()

	case "r":
		return m.startRadio()
	}

	return m, nil
}

// startRadio kicks off Roon Radio seeded by the current track, replacing the
// zone's queue. The blocking browse round-trips happen inside startRadioCmd;
// here we just validate and set the optimistic status line.
func (m Model) startRadio() (tea.Model, tea.Cmd) {
	z := m.currentZone()
	if z == nil || z.NowPlaying == nil {
		return m.setStatus("nothing playing", true)
	}
	np := z.NowPlaying
	title := strings.TrimSpace(np.ThreeLine.Line1)
	if title == "" {
		title = strings.TrimSpace(np.TwoLine.Line1)
	}
	artist := strings.TrimSpace(np.ThreeLine.Line2)
	if artist == "" {
		artist = strings.TrimSpace(np.TwoLine.Line2)
	}
	if title == "" {
		return m.setStatus("no track for radio", true)
	}
	m.status.set("starting radio: "+title, false)
	return m, m.startRadioCmd(z.ZoneID, title, artist)
}

func (m Model) setStatus(msg string, isErr bool) (tea.Model, tea.Cmd) {
	m.status.set(msg, isErr)
	return m, nil
}

func (m Model) startRadioCmd(zoneID, title, artist string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		if err := startSongRadio(client, zoneID, title, artist); err != nil {
			log.Printf("start radio: %v", err)
			return radioResultMsg{err: err}
		}
		return radioResultMsg{}
	}
}

func (m Model) openLyrics() (tea.Model, tea.Cmd) {
	m.view = viewLyrics
	if cmd := m.fetchLyricsIfNeeded(); cmd != nil {
		return m, cmd
	}
	return m, nil
}

func (m Model) handleLyricsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "L", "l", "h", "left", "backspace":
		m.view = viewPlayer
		return m, nil
	case " ":
		return m, m.controlCmd("playpause")
	case "n":
		return m, m.controlCmd("next")
	case "p":
		return m, m.controlCmd("previous")
	}
	return m, nil
}

func (m Model) openBrowser() (tea.Model, tea.Cmd) {
	z := m.currentZone()
	if z == nil {
		return m, nil
	}
	m.view = viewBrowser
	m.browser.setSize(m.width, m.height)
	cmd := m.browser.activate(z.ZoneID)
	return m, cmd
}

func (m Model) handleBrowserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	b := &m.browser

	// Any key dismisses a transient status (e.g. "search failed: ...").
	if b.statusMsg != "" {
		b.statusMsg = ""
	}

	// Filter input mode
	if b.filtering {
		switch msg.String() {
		case "esc":
			b.clearFilter()
			return m, nil
		case "enter":
			b.filtering = false
			return m, nil
		case "backspace":
			if len(b.filterBuf) > 0 {
				b.filterBuf = b.filterBuf[:len(b.filterBuf)-1]
				b.applyFilter()
			}
			return m, nil
		default:
			if r := msg.Runes; len(r) > 0 {
				b.filterBuf += string(r)
				b.applyFilter()
			}
			return m, nil
		}
	}

	// Search input mode (global library search)
	if b.searching {
		switch msg.String() {
		case "esc":
			b.searching = false
			b.searchBuf = ""
			return m, nil
		case "enter":
			q := strings.TrimSpace(b.searchBuf)
			b.searching = false
			b.searchBuf = ""
			if q == "" {
				return m, nil
			}
			// Snapshot pre-search state so a failure restores the user
			// to exactly where they were. applyResult clears the stack
			// on success since search results are a fresh root.
			b.searchSnapshot = &browseSnapshot{
				items:   b.items,
				title:   b.title,
				cursor:  b.cursor,
				offset:  b.offset,
				stack:   b.stack,
				session: b.session,
			}
			b.clearFilter()
			b.loading = true
			b.title = "Searching..."
			return m, b.searchCmd(q)
		case "backspace":
			if len(b.searchBuf) > 0 {
				b.searchBuf = b.searchBuf[:len(b.searchBuf)-1]
			}
			return m, nil
		default:
			if r := msg.Runes; len(r) > 0 {
				b.searchBuf += string(r)
			}
			return m, nil
		}
	}

	// Normal navigation
	switch msg.String() {
	case "j", "down":
		b.moveDown()
		return m, nil
	case "k", "up":
		b.moveUp()
		return m, nil
	case "enter", "l", "right":
		cmd := b.selectCurrent()
		if cmd != nil {
			return m, cmd
		}
		return m, nil
	case "h", "left", "backspace":
		if b.goBack() {
			return m, nil
		}
		m.view = viewPlayer
		return m, nil
	case "/":
		b.filtering = true
		b.filterBuf = ""
		return m, nil
	case "s":
		b.searching = true
		b.searchBuf = ""
		return m, nil
	case "esc", "q":
		m.view = viewPlayer
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
	// Re-anchor seek interpolation to the newly selected zone's position.
	if z := m.currentZone(); z != nil {
		m.updateSeekAnchor(z)
	}
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

// -- Lyrics --

// updateSeekAnchor refreshes the wall-clock anchor used to interpolate the
// playback position between server ticks. Re-anchoring on every zone update
// would erase sub-second interpolation, so the same-track path only updates
// when something actually changed. A track change is always a force-reset:
// Roon's first update for a new track sometimes omits seek_position or
// carries a stale value, and we don't want lyrics scrolling at the previous
// song's elapsed time.
func (m *Model) updateSeekAnchor(z *roon.Zone) {
	sig := trackSignature(z)
	if sig != m.lastTrackSig {
		m.lastTrackSig = sig
		m.seekAnchorAt = time.Now()
		m.seekAnchorPos = 0
		if z.NowPlaying != nil && z.NowPlaying.SeekPosition != nil {
			m.seekAnchorPos = time.Duration(*z.NowPlaying.SeekPosition) * time.Second
		}
		m.seekAnchorPlaying = z.State == "playing"
		return
	}
	if z.NowPlaying == nil {
		return
	}
	hasSeek := z.NowPlaying.SeekPosition != nil
	playing := z.State == "playing"
	var serverPos time.Duration
	if hasSeek {
		serverPos = time.Duration(*z.NowPlaying.SeekPosition) * time.Second
	}
	if !m.seekAnchorAt.IsZero() &&
		(!hasSeek || serverPos == m.seekAnchorPos) &&
		playing == m.seekAnchorPlaying {
		return
	}
	m.seekAnchorAt = time.Now()
	if hasSeek {
		m.seekAnchorPos = serverPos
	}
	m.seekAnchorPlaying = playing
}

// maybeUpdateLyrics fires a fetch if the lyrics view is open and the track
// differs from what's loaded. Otherwise we'd hit LRCLIB on every zone update
// for users who never open the lyrics view.
func (m *Model) maybeUpdateLyrics() tea.Cmd {
	if m.view != viewLyrics {
		return nil
	}
	return m.fetchLyricsIfNeeded()
}

func (m *Model) fetchLyricsIfNeeded() tea.Cmd {
	sig := trackSignature(m.currentZone())
	if sig.Empty() {
		m.lyrics.reset()
		return nil
	}
	if sig == m.lyrics.sig && m.lyrics.lyr != nil {
		return nil
	}
	if sig == m.lyrics.pending && m.lyrics.loading {
		return nil
	}
	m.lyrics.reset()
	m.lyrics.pending = sig
	m.lyrics.loading = true
	return lyricsCmd(sig)
}

func lyricsCmd(sig lyrics.Signature) tea.Cmd {
	return func() tea.Msg {
		if cached, err := lyrics.LoadCache(sig); err == nil && cached != nil {
			return lyricsLoadedMsg{sig: sig, lyr: cached}
		} else if errors.Is(err, lyrics.ErrNotFound) {
			return lyricsLoadedMsg{sig: sig}
		}

		lyr, err := lyrics.Fetch(sig)
		if errors.Is(err, lyrics.ErrNotFound) {
			lyrics.SaveCache(sig, nil, true)
			return lyricsLoadedMsg{sig: sig}
		}
		if err != nil {
			return lyricsLoadedMsg{sig: sig, errMsg: err.Error()}
		}
		lyrics.SaveCache(sig, lyr, false)
		return lyricsLoadedMsg{sig: sig, lyr: lyr}
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
	ch := m.zoneCh
	return func() tea.Msg {
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

// tickSeek runs once a second. The displayed playback position comes from the
// wall-clock anchor (effectiveSeekPos), so nothing needs advancing here — the
// tick's job is refreshing the link state and forcing a re-render.
func (m *Model) tickSeek() {
	m.connected = m.client.Connected()
}
