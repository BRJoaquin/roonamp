package tui

import (
	"fmt"
	"log"
	"strings"

	"roonamp/internal/roon"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"
)

// -- Messages --

type browseResultMsg struct {
	items []roon.BrowseItem
	list  *roon.ListInfo
	err   error
	done  bool // action completed (e.g. play), go back to player
}

// browseLevel stores the state of one level in the browse hierarchy
type browseLevel struct {
	items  []roon.BrowseItem
	title  string
	cursor int
	offset int
}

// -- Browser model --

type browserModel struct {
	client    *roon.Client
	zoneID    string
	items     []roon.BrowseItem
	filtered  []int // indices into items matching filter
	title     string
	cursor    int
	offset    int // scroll offset
	stack     []browseLevel // previous levels for going back
	loading   bool
	filtering bool
	filterBuf string
	width     int
	height    int
}

func newBrowser(client *roon.Client) browserModel {
	return browserModel{client: client}
}

func (b *browserModel) setSize(w, h int) {
	b.width = w
	b.height = h
}

func (b *browserModel) maxVisible() int {
	v := b.height - 8 // border, title, separator, help, padding
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

	client := b.client
	return func() tea.Msg {
		_, err := client.Browse(roon.BrowseRequest{
			Hierarchy:      "browse",
			ZoneOrOutputID: zoneID,
			PopAll:         true,
		})
		if err != nil {
			return browseResultMsg{err: err}
		}
		lr, err := client.Load(roon.LoadRequest{
			Hierarchy: "browse",
			Offset:    0,
			Count:     100,
		})
		if err != nil {
			return browseResultMsg{err: err}
		}
		return browseResultMsg{items: lr.Items, list: lr.List}
	}
}

// selectCurrent navigates into the selected item or triggers an action
func (b *browserModel) selectCurrent() tea.Cmd {
	item := b.selectedItem()
	if item == nil || item.ItemKey == nil {
		return nil
	}

	// Push current level onto stack before navigating forward
	b.stack = append(b.stack, browseLevel{
		items:  b.items,
		title:  b.title,
		cursor: b.cursor,
		offset: b.offset,
	})
	b.clearFilter()

	b.loading = true
	client := b.client
	zoneID := b.zoneID
	key := *item.ItemKey
	isAction := item.Hint == "action"

	return func() tea.Msg {
		br, err := client.Browse(roon.BrowseRequest{
			Hierarchy:      "browse",
			ZoneOrOutputID: zoneID,
			ItemKey:        &key,
		})
		if err != nil {
			return browseResultMsg{err: err}
		}
		if isAction || br.Action == "message" || br.List == nil {
			return browseResultMsg{done: true}
		}
		lr, err := client.Load(roon.LoadRequest{
			Hierarchy: "browse",
			Offset:    0,
			Count:     100,
		})
		if err != nil {
			return browseResultMsg{err: err}
		}
		if len(lr.Items) == 0 {
			return browseResultMsg{done: true}
		}
		return browseResultMsg{items: lr.Items, list: lr.List}
	}
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
	return true
}

func (b *browserModel) applyResult(msg browseResultMsg) {
	b.loading = false
	if msg.err != nil {
		log.Printf("browse error: %v", msg.err)
		// Undo the stack push from selectItem
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
			if item.Hint == "list" {
				icon = "+"
			} else if item.Hint == "action" || item.Hint == "action_list" {
				icon = ">"
			}

			// Title + subtitle
			line := item.Title
			if item.Subtitle != "" {
				line += styleDim.Render(" -- " + item.Subtitle)
			}

			// Truncate
			maxLine := w - 6
			if len(item.Title) > maxLine {
				line = item.Title[:maxLine-3] + "..."
				if item.Subtitle != "" {
					line += styleDim.Render(" -- " + item.Subtitle)
				}
			}

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

	// Filter bar
	if b.filtering {
		s.WriteString("\n")
		s.WriteString(styleArtist.Render("/ ") + b.filterBuf + "_")
	}

	// Help
	s.WriteString("\n")
	if b.filtering {
		s.WriteString(styleDim.Render("[type] filter  [enter] accept  [esc] clear"))
	} else {
		help := "[j/k] navigate  [l/enter] open  [h/bksp] back  [/] filter  [esc/q] player"
		s.WriteString(styleDim.Render(help))
	}

	return styleApp.Width(b.width - 2).Render(s.String())
}
