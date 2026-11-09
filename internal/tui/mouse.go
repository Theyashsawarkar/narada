package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// hits is where things were drawn last, so a click can find them. View
// works on a copy of the model; this is shared through the pointer.
type hits struct {
	places   []span      // the top bar's places, by number
	sections []span      // the strip's sections; span.n is the section
	listX    int         // the list's right edge (0: no list on screen)
	listRows map[int]int // a row of the list (0: its first) → the item on it
	rightX   int         // where the open pane starts
	msgRows  map[int]string
}

// span is a stretch of a row: [x0, x1) and what it is.
type span struct{ x0, x1, n int }

func (h *hits) reset() {
	if h == nil {
		return
	}
	h.places, h.sections, h.listX, h.rightX = nil, nil, 0, 0
	h.listRows, h.msgRows = map[int]int{}, map[int]string{}
}

// addRow adds list item i and remembers which rows it took (for clicks).
func (m Model) addRow(out []string, i int, lines []string, w int, border string, sel, dot bool) []string {
	start := len(out)
	out = m.appendItem(out, lines, w, border, sel, dot)
	if m.hits != nil && m.hits.listRows != nil {
		for k := start; k < len(out); k++ {
			m.hits.listRows[k] = i
		}
	}
	return out
}

// spanOf is the stretch a cell takes when drawn at x.
func spanOf(x int, cell string, n int) span { return span{x, x + lipgloss.Width(cell), n} }

// mouse: a click on a place, a section, a list row or a message goes
// there (a second click on a row opens it); the wheel scrolls what's
// under it.
func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	h := m.hits
	if h == nil || m.mode != modeNormal && m.mode != modeChatMsg {
		return m, nil
	}
	ev := tea.MouseEvent(msg)
	inList := h.listX > 0 && ev.X < h.listX
	switch {
	case ev.Button == tea.MouseButtonWheelUp || ev.Button == tea.MouseButtonWheelDown:
		if m.mode != modeNormal || ev.Action != tea.MouseActionPress {
			return m, nil
		}
		up := ev.Button == tea.MouseButtonWheelUp
		if inList {
			m.focus = 0
			return m.actKey(map[bool]string{true: "k", false: "j"}[up])
		}
		if up {
			m.scroll = max(0, m.scroll-3)
		} else {
			m.scroll += 3
		}
		return m, nil
	case ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft:
		return m, nil
	}
	if m.mode == modeChatMsg && ev.Y > sectionsRow && !inList {
		return m, nil // a click in the chat while writing: keep writing
	}
	m.eng.Reset()
	m.syncPending()
	switch ev.Y {
	case placesRow:
		for _, p := range h.places {
			if ev.X >= p.x0 && ev.X < p.x1 {
				m.mode = modeNormal
				return m.goPlace(p.n)
			}
		}
		return m, nil
	case sectionsRow:
		for _, sec := range h.sections {
			if ev.X >= sec.x0 && ev.X < sec.x1 && sec.n != m.sec {
				m.mode, m.sec, m.sel, m.offset, m.filter, m.search = modeNormal, sec.n, 0, 0, "", nil
				m.selected = map[string]bool{}
				m.toList()
				m.reload()
				return m, nil
			}
		}
		return m, nil
	}
	if inList {
		// the list's rows start under the places, the sections and its title
		if i, ok := h.listRows[ev.Y-topRows-1]; ok {
			m.mode, m.visual = modeNormal, false
			if i == m.sel && m.focus == 0 {
				return m.actKey("enter") // a second click opens it
			}
			m.sel, m.focus, m.picking = i, 0, false
			m.loadPreview(true)
			m.fixView()
		}
		return m, nil
	}
	if ev.X >= h.rightX {
		if name, ok := h.msgRows[ev.Y]; ok && m.selectChatMsg(name) {
			m.mode = modeNormal
			return m, nil
		}
		if m.focus == 0 {
			return m.actKey("2") // into what's open
		}
	}
	return m, nil
}
