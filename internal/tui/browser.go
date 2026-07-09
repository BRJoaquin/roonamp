package tui

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"roonamp/internal/roon"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"
)

// -- Messages --

type browseResultMsg struct {
	items         []roon.BrowseItem
	list          *roon.ListInfo
	err           error
	done          bool // action completed (e.g. play), go back to player
	fromSearch    bool
	switchSession string // if set, applyResult flips b.session to this value
}

// browseSession bundles the client, zone, and Roon multi_session_key for a
// sequence of browse calls, so multi-step drills (search, radio, artist
// albums) read as intent instead of repeated request plumbing. Every load
// fetches the first 100 items of the current level.
type browseSession struct {
	client  *roon.Client
	zoneID  string
	session string
}

func (s browseSession) popAll() error {
	_, err := s.client.Browse(roon.BrowseRequest{
		Hierarchy:       "browse",
		ZoneOrOutputID:  s.zoneID,
		PopAll:          true,
		MultiSessionKey: s.session,
	})
	return err
}

// browse descends into itemKey; input answers an input prompt (e.g. Search).
func (s browseSession) browse(itemKey, input string) (*roon.BrowseResponse, error) {
	return s.client.Browse(roon.BrowseRequest{
		Hierarchy:       "browse",
		ZoneOrOutputID:  s.zoneID,
		ItemKey:         &itemKey,
		Input:           input,
		MultiSessionKey: s.session,
	})
}

// load fetches the items of the current level.
func (s browseSession) load() (*roon.LoadResponse, error) {
	return s.client.Load(roon.LoadRequest{
		Hierarchy:       "browse",
		Offset:          0,
		Count:           100,
		MultiSessionKey: s.session,
	})
}

// open descends into itemKey and loads the resulting list.
func (s browseSession) open(itemKey, input string) (*roon.LoadResponse, error) {
	if _, err := s.browse(itemKey, input); err != nil {
		return nil, err
	}
	return s.load()
}

// findItemKey returns the item_key of the first item that has one and matches.
func findItemKey(items []roon.BrowseItem, match func(*roon.BrowseItem) bool) string {
	for i := range items {
		if it := &items[i]; it.ItemKey != nil && match(it) {
			return *it.ItemKey
		}
	}
	return ""
}

// browseLevel stores the state of one level in the browse hierarchy.
// sessionKey is the Roon multi_session_key that was active when this level
// was captured — restoring the level also restores its session, so the cached
// item_keys are still valid in their original Roon browse session.
type browseLevel struct {
	items      []roon.BrowseItem
	title      string
	cursor     int
	offset     int
	sessionKey string
}

// -- Browser model --

type browserModel struct {
	client         *roon.Client
	zoneID         string
	items          []roon.BrowseItem
	filtered       []int // indices into items matching filter
	title          string
	cursor         int
	offset         int           // scroll offset
	stack          []browseLevel // previous levels for going back
	session        string        // active Roon multi_session_key ("" = main)
	loading        bool
	filtering      bool
	filterBuf      string
	searching      bool
	searchBuf      string
	searchSnapshot *browseSnapshot
	statusMsg      string
	width          int
	height         int
}

// browseSnapshot captures the full browser state so a failed search can
// restore the user to exactly where they were before pressing s+enter.
type browseSnapshot struct {
	items   []roon.BrowseItem
	title   string
	cursor  int
	offset  int
	stack   []browseLevel
	session string
}

func newBrowser(client *roon.Client) browserModel {
	return browserModel{client: client}
}

func (b *browserModel) setSize(w, h int) {
	b.width = w
	b.height = h
}

func (b *browserModel) maxVisible() int {
	// Chrome around the items: border top + padding top (2), title +
	// separator (2), blank + info (2), blank + help (2), padding bottom +
	// border bottom (2) = 10 rows. Filter/search bar adds 2 (blank + bar),
	// status message adds 2 (blank + msg).
	overhead := 10
	if b.filtering || b.searching {
		overhead += 2
	}
	if b.statusMsg != "" {
		overhead += 2
	}
	v := b.height - overhead
	if v < 3 {
		v = 3
	}
	return v
}

// activate opens the browse root for the given zone
func (b *browserModel) activate(zoneID string) tea.Cmd {
	b.zoneID = zoneID
	b.cursor = 0
	b.offset = 0
	b.loading = true
	b.items = nil
	b.stack = nil
	b.session = ""
	b.statusMsg = ""

	sess := browseSession{client: b.client, zoneID: zoneID}
	return func() tea.Msg {
		if err := sess.popAll(); err != nil {
			return browseResultMsg{err: err}
		}
		lr, err := sess.load()
		if err != nil {
			return browseResultMsg{err: err}
		}
		return browseResultMsg{items: lr.Items, list: lr.List}
	}
}

// hintArtistAlbums marks the synthetic "Show all albums for this artist" row
// we prepend to artist pages. The artist's name is carried in Subtitle so
// selectCurrent can route the click without any side-channel state.
const hintArtistAlbums = "artist_albums"

// selectCurrent navigates into the selected item or triggers an action.
//
// Two escape hatches for artist pages, both routed through a separate Roon
// browse session ("synthetic") so they don't disturb the main session:
//   - the synthetic "Show all albums (incl. streaming)" row prepended to
//     non-empty artist pages — intercepted at the top of this function.
//   - an automatic fallback when an artist drill returns zero items, so the
//     user lands on real content instead of a blank screen.
func (b *browserModel) selectCurrent() tea.Cmd {
	item := b.selectedItem()
	if item == nil {
		return nil
	}

	// Synthetic action injected onto artist pages: jump into a SEPARATE Roon
	// browse session so the side trip's pop_all + search doesn't invalidate
	// the main session's item_keys.
	if item.Hint == hintArtistAlbums {
		artistName := item.Subtitle
		if artistName == "" {
			return nil
		}
		b.stack = append(b.stack, browseLevel{
			items:      b.items,
			title:      b.title,
			cursor:     b.cursor,
			offset:     b.offset,
			sessionKey: b.session,
		})
		b.clearFilter()
		b.loading = true
		b.session = "synthetic"
		sess := browseSession{client: b.client, zoneID: b.zoneID, session: b.session}
		return func() tea.Msg {
			page, err := openAlbumsForArtist(sess, artistName)
			if err != nil {
				return browseResultMsg{err: fmt.Errorf("albums: %w", err)}
			}
			return browseResultMsg{items: page.Items, list: page.List}
		}
	}

	if item.ItemKey == nil {
		return nil
	}

	// Push current level onto stack before navigating forward
	b.stack = append(b.stack, browseLevel{
		items:      b.items,
		title:      b.title,
		cursor:     b.cursor,
		offset:     b.offset,
		sessionKey: b.session,
	})
	b.clearFilter()

	b.loading = true
	sess := browseSession{client: b.client, zoneID: b.zoneID, session: b.session}
	key := *item.ItemKey
	isAction := item.Hint == "action"
	artistName := ""
	if isArtistItem(item) {
		artistName = item.Title
	}

	return func() tea.Msg {
		br, err := sess.browse(key, "")
		if err != nil {
			return browseResultMsg{err: err}
		}
		if isAction || br.Action == "message" || br.List == nil {
			return browseResultMsg{done: true}
		}
		lr, err := sess.load()
		if err != nil {
			return browseResultMsg{err: err}
		}
		if len(lr.Items) == 0 {
			if artistName != "" {
				// Empty-page fallback uses the synthetic session so the
				// main session stays positioned at the artist page.
				syn := browseSession{client: sess.client, zoneID: sess.zoneID, session: "synthetic"}
				if page, terr := openAlbumsForArtist(syn, artistName); terr == nil {
					return browseResultMsg{items: page.Items, list: page.List, switchSession: "synthetic"}
				}
			}
			return browseResultMsg{done: true}
		}
		if artistName != "" {
			lr.Items = prependArtistAlbumsAction(lr.Items, artistName)
		}
		return browseResultMsg{items: lr.Items, list: lr.List}
	}
}

// prependArtistAlbumsAction puts a "Show all albums (incl. streaming)" row at
// the top of an artist page. Clicking it triggers a library search and drills
// into the Albums category, which Roon populates from all configured sources.
func prependArtistAlbumsAction(items []roon.BrowseItem, artistName string) []roon.BrowseItem {
	dummy := "_albums:" + artistName
	syn := roon.BrowseItem{
		Title:    "Show all albums (incl. streaming)",
		Subtitle: artistName,
		Hint:     hintArtistAlbums,
		ItemKey:  &dummy,
	}
	out := make([]roon.BrowseItem, 0, len(items)+1)
	out = append(out, syn)
	out = append(out, items...)
	return out
}

// isArtistItem returns true when a browse item's subtitle matches the
// "<n> Album" / "<n> Albums" pattern Roon uses for artist rows.
func isArtistItem(item *roon.BrowseItem) bool {
	if item == nil {
		return false
	}
	fields := strings.Fields(item.Subtitle)
	if len(fields) != 2 {
		return false
	}
	if _, err := strconv.Atoi(fields[0]); err != nil {
		return false
	}
	w := strings.ToLower(fields[1])
	return w == "album" || w == "albums"
}

// openAlbumsForArtist runs, in the given multi_session_key (so the main
// session is undisturbed): pop_all -> find Search -> search by artist name
// -> drill into the "Albums" category. Roon's library Search returns results
// across all sources (library + TIDAL + Qobuz), so the Albums bucket is the
// most reliable way to get the full set of albums for an artist — including
// streaming albums that don't appear on a library-only artist page.
func openAlbumsForArtist(s browseSession, artistName string) (*roon.LoadResponse, error) {
	if err := s.popAll(); err != nil {
		return nil, fmt.Errorf("pop_all: %w", err)
	}

	searchKey, err := findSearchKey(s)
	if err != nil {
		return nil, err
	}

	results, err := s.open(searchKey, artistName)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	albumsKey := pickItemByTitlePrefix(results.Items, "Albums")
	if albumsKey == "" {
		// No Albums bucket — return the raw search results so the user
		// at least sees something useful (categories or top matches).
		return results, nil
	}
	albums, err := s.open(albumsKey, "")
	if err != nil {
		return nil, fmt.Errorf("open Albums: %w", err)
	}
	return albums, nil
}

func pickItemByTitle(items []roon.BrowseItem, title string) string {
	return findItemKey(items, func(it *roon.BrowseItem) bool {
		return strings.EqualFold(it.Title, title)
	})
}

// composeItemLine returns a single-row rendering of title + " -- " + subtitle,
// truncated with "..." so the visible text never exceeds maxWidth runes.
// Title is rendered in the default style; subtitle in styleDim. If subtitle
// has to be cut, only the kept prefix is dimmed; if the title itself doesn't
// fit, no subtitle is rendered.
func composeItemLine(title, subtitle string, maxWidth int) string {
	if maxWidth < 4 {
		maxWidth = 4
	}
	titleR := []rune(title)
	if subtitle == "" {
		if len(titleR) <= maxWidth {
			return title
		}
		return string(titleR[:maxWidth-3]) + "..."
	}
	subFull := " -- " + subtitle
	subR := []rune(subFull)
	if len(titleR)+len(subR) <= maxWidth {
		return title + styleDim.Render(subFull)
	}
	if len(titleR) >= maxWidth-3 {
		return string(titleR[:maxWidth-3]) + "..."
	}
	keep := maxWidth - len(titleR) - 3
	if keep < 1 {
		keep = 1
	}
	return title + styleDim.Render(string(subR[:keep])+"...")
}

func pickItemByTitlePrefix(items []roon.BrowseItem, prefix string) string {
	return findItemKey(items, func(it *roon.BrowseItem) bool {
		return strings.HasPrefix(it.Title, prefix)
	})
}

// searchCmd performs a global library search. The "Search" item is the one
// whose input_prompt is populated; on stock Roon it lives inside the Library
// entry (not at the very root, where you find Library/Playlists/TIDAL/etc).
// So: pop back to root, look for Search there, otherwise drill into Library
// and look there. Then browse the Search item with the user's `input`.
func (b *browserModel) searchCmd(query string) tea.Cmd {
	sess := browseSession{client: b.client, zoneID: b.zoneID}

	return func() tea.Msg {
		if err := sess.popAll(); err != nil {
			return browseResultMsg{fromSearch: true, err: fmt.Errorf("pop_all: %w", err)}
		}

		searchKey, err := findSearchKey(sess)
		if err != nil {
			return browseResultMsg{fromSearch: true, err: err}
		}

		lr, err := sess.open(searchKey, query)
		if err != nil {
			return browseResultMsg{fromSearch: true, err: fmt.Errorf("search: %w", err)}
		}
		return browseResultMsg{fromSearch: true, items: lr.Items, list: lr.List}
	}
}

// findSearchKey locates the Search item's item_key in the given browse
// session. It assumes the session is already at the browse root (pop_all
// just issued). It loads the root and returns the Search item if present,
// otherwise drills into "Library" and looks again.
func findSearchKey(s browseSession) (string, error) {
	root, err := s.load()
	if err != nil {
		return "", fmt.Errorf("load root: %w", err)
	}
	if key := pickSearchKey(root.Items); key != "" {
		return key, nil
	}

	libraryKey := pickItemByTitle(root.Items, "Library")
	if libraryKey == "" {
		return "", fmt.Errorf("no Search item at root and no Library to drill into")
	}

	lib, err := s.open(libraryKey, "")
	if err != nil {
		return "", fmt.Errorf("open Library: %w", err)
	}
	if key := pickSearchKey(lib.Items); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("no Search item in Library")
}

func pickSearchKey(items []roon.BrowseItem) string {
	return findItemKey(items, func(it *roon.BrowseItem) bool {
		return it.InputPrompt != nil || it.Title == "Search"
	})
}

// goBack pops one level from the local stack
func (b *browserModel) goBack() bool {
	if len(b.stack) == 0 {
		return false
	}
	prev := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	b.items = prev.items
	b.title = prev.title
	b.cursor = prev.cursor
	b.offset = prev.offset
	b.session = prev.sessionKey
	return true
}

func (b *browserModel) applyResult(msg browseResultMsg) {
	b.loading = false
	if msg.err != nil {
		log.Printf("browse error: %v", msg.err)
		if msg.fromSearch && b.searchSnapshot != nil {
			s := b.searchSnapshot
			b.items = s.items
			b.title = s.title
			b.cursor = s.cursor
			b.offset = s.offset
			b.stack = s.stack
			b.session = s.session
			b.searchSnapshot = nil
			b.statusMsg = "search failed: " + msg.err.Error()
			return
		}
		// Undo the stack push from selectItem and surface the error so the
		// user (or us during debugging) can see what went wrong instead of
		// the click silently snapping back.
		b.statusMsg = msg.err.Error()
		if len(b.stack) > 0 {
			b.goBack()
		}
		return
	}
	if msg.done {
		// Action completed, pop the stack push since we're leaving
		if len(b.stack) > 0 {
			b.stack = b.stack[:len(b.stack)-1]
		}
		return
	}

	if msg.fromSearch {
		b.stack = nil
		b.searchSnapshot = nil
		b.session = ""
	}
	if msg.switchSession != "" {
		b.session = msg.switchSession
	}

	b.items = msg.items
	b.cursor = 0
	b.offset = 0

	if msg.list != nil {
		b.title = msg.list.Title
	}
}

// visibleItems returns the items currently shown (filtered or all)
func (b *browserModel) visibleItems() []roon.BrowseItem {
	if len(b.filtered) > 0 || b.filterBuf != "" {
		out := make([]roon.BrowseItem, len(b.filtered))
		for i, idx := range b.filtered {
			out[i] = b.items[idx]
		}
		return out
	}
	return b.items
}

// browseSearchSource implements fuzzy.Source for fuzzy matching
type browseSearchSource []roon.BrowseItem

func (s browseSearchSource) String(i int) string {
	item := s[i]
	if item.Subtitle != "" {
		return item.Title + " " + item.Subtitle
	}
	return item.Title
}

func (s browseSearchSource) Len() int { return len(s) }

func (b *browserModel) applyFilter() {
	if b.filterBuf == "" {
		b.filtered = nil
		return
	}
	matches := fuzzy.FindFrom(b.filterBuf, browseSearchSource(b.items))
	b.filtered = make([]int, len(matches))
	for i, m := range matches {
		b.filtered[i] = m.Index
	}
	b.cursor = 0
	b.offset = 0
}

func (b *browserModel) clearFilter() {
	b.filtering = false
	b.filterBuf = ""
	b.filtered = nil
	b.cursor = 0
	b.offset = 0
}

// selectedItem returns the actual item at the cursor (accounting for filter)
func (b *browserModel) selectedItem() *roon.BrowseItem {
	vis := b.visibleItems()
	if b.cursor < len(vis) {
		return &vis[b.cursor]
	}
	return nil
}

func (b *browserModel) moveUp() {
	if b.cursor > 0 {
		b.cursor--
	}
	if b.cursor < b.offset {
		b.offset = b.cursor
	}
}

func (b *browserModel) moveDown() {
	if b.cursor < len(b.visibleItems())-1 {
		b.cursor++
	}
	max := b.maxVisible()
	if b.cursor >= b.offset+max {
		b.offset = b.cursor - max + 1
	}
}

func (b *browserModel) view() string {
	w := b.width - 6
	if w < 30 {
		w = 30
	}

	var s strings.Builder

	// Title
	title := styleHeader.Render(b.title)
	if b.title == "" {
		title = styleHeader.Render("Browse")
	}
	s.WriteString(title)
	s.WriteString("\n")
	s.WriteString(styleDim.Render(strings.Repeat("-", w)))
	s.WriteString("\n")

	if b.loading {
		s.WriteString(styleDim.Render("Loading..."))
		s.WriteString("\n")
		return styleApp.Width(b.width - 2).Render(s.String())
	}

	vis := b.visibleItems()

	if len(vis) == 0 {
		if b.filterBuf != "" {
			s.WriteString(styleDim.Render("no matches"))
		} else {
			s.WriteString(styleDim.Render("(empty)"))
		}
		s.WriteString("\n")
	} else {
		max := b.maxVisible()
		end := b.offset + max
		if end > len(vis) {
			end = len(vis)
		}

		for i := b.offset; i < end; i++ {
			item := vis[i]

			// Icon based on hint
			icon := " "
			switch item.Hint {
			case "list":
				icon = "+"
			case "action", "action_list":
				icon = ">"
			case hintArtistAlbums:
				icon = "*"
			}

			// Compose the line, truncating so the whole row (including
			// the 7-char "   [X] " prefix) fits within one terminal row —
			// otherwise Lipgloss wraps long subtitles and the top of the
			// view scrolls off the alt-screen.
			line := composeItemLine(item.Title, item.Subtitle, w-7)

			if i == b.cursor {
				s.WriteString(styleZoneActive.Render(fmt.Sprintf(" > [%s] %s", icon, line)))
			} else {
				s.WriteString(fmt.Sprintf("   [%s] %s", icon, line))
			}
			s.WriteString("\n")
		}

		// Item count + scroll indicator
		s.WriteString("\n")
		info := fmt.Sprintf("%d items", len(vis))
		if b.filterBuf != "" && len(vis) != len(b.items) {
			info = fmt.Sprintf("%d/%d items", len(vis), len(b.items))
		}
		if len(vis) > b.maxVisible() {
			info += fmt.Sprintf("  (%d-%d)", b.offset+1, end)
		}
		s.WriteString(styleDim.Render(info))
		s.WriteString("\n")
	}

	// Filter / search bar
	if b.filtering {
		s.WriteString("\n")
		s.WriteString(styleArtist.Render("/ ") + b.filterBuf + "_")
	} else if b.searching {
		s.WriteString("\n")
		s.WriteString(styleArtist.Render("search: ") + b.searchBuf + "_")
	}

	// Help
	s.WriteString("\n")
	switch {
	case b.filtering:
		s.WriteString(styleDim.Render("[type] filter  [enter] accept  [esc] clear"))
	case b.searching:
		s.WriteString(styleDim.Render("[type] search  [enter] go  [esc] cancel"))
	default:
		help := "[j/k] navigate  [l/enter] open  [h/bksp] back  [/] filter  [s] search  [esc/q] player"
		s.WriteString(styleDim.Render(help))
	}

	if b.statusMsg != "" {
		s.WriteString("\n")
		s.WriteString(styleError.Render(b.statusMsg))
	}

	return styleApp.Width(b.width - 2).Render(s.String())
}
