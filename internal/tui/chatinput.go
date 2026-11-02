package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// The chat message box: the text wraps, the box grows with it (up to
// a third of the conversation's height, then scrolls inside), and the
// cursor moves like a shell's. m.input is the text and m.back the cursor's
// distance from its end, so code that replaces m.input keeps the cursor
// sensible without knowing about it.

// cursor is where the cursor is in m.input.
func (m *Model) cursor() int { return max(0, len(m.input)-m.back) }

func (m *Model) setCursor(i int) { m.back = len(m.input) - max(0, min(i, len(m.input))) }

// insert types runes at the cursor.
func (m *Model) insert(r ...rune) {
	i := m.cursor()
	out := make([]rune, 0, len(m.input)+len(r))
	out = append(append(append(out, m.input[:i]...), r...), m.input[i:]...)
	m.input = out
}

// deleteBack removes the rune before the cursor (backspace).
func (m *Model) deleteBack() {
	if i := m.cursor(); i > 0 {
		m.input = append(m.input[:i-1:i-1], m.input[i:]...)
	}
}

// deleteFwd removes the rune under the cursor (delete).
func (m *Model) deleteFwd() {
	if i := m.cursor(); i < len(m.input) {
		m.input = append(m.input[:i:i], m.input[i+1:]...)
		m.back--
	}
}

// wordBack is where the word before i starts.
func wordBack(t []rune, i int) int {
	for i > 0 && unicode.IsSpace(t[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(t[i-1]) {
		i--
	}
	return i
}

// wordFwd is where the word after i ends.
func wordFwd(t []rune, i int) int {
	for i < len(t) && unicode.IsSpace(t[i]) {
		i++
	}
	for i < len(t) && !unicode.IsSpace(t[i]) {
		i++
	}
	return i
}

// deleteWordBack removes the word before the cursor (ctrl+w, alt+backspace).
func (m *Model) deleteWordBack() {
	i := m.cursor()
	j := wordBack(m.input, i)
	m.input = append(m.input[:j:j], m.input[i:]...)
}

// lineStart and lineEnd: the cursor's line in the text (between newlines).
func lineStart(t []rune, i int) int {
	for i > 0 && t[i-1] != '\n' {
		i--
	}
	return i
}

func lineEnd(t []rune, i int) int {
	for i < len(t) && t[i] != '\n' {
		i++
	}
	return i
}

// seg is one line of wrapped text: runes [start, end), the next line
// starting at next (past a newline, or the same place for a soft wrap).
type seg struct{ start, end, next int }

// wrapRunes word-wraps t into lines at most width cells wide.
func wrapRunes(t []rune, width int) []seg {
	width = max(1, width)
	var segs []seg
	i := 0
	for {
		j, w, space := i, 0, -1
		for j < len(t) && t[j] != '\n' {
			rw := runewidth.RuneWidth(t[j])
			if w+rw > width {
				break
			}
			if t[j] == ' ' {
				space = j
			}
			w += rw
			j++
		}
		switch {
		case j < len(t) && t[j] == '\n':
			segs = append(segs, seg{i, j, j + 1})
			i = j + 1
			if i == len(t) {
				return append(segs, seg{i, i, i}) // a newline at the end: an empty line to type on
			}
			continue
		case j >= len(t):
			return append(segs, seg{i, j, j})
		case space > i:
			segs = append(segs, seg{i, space + 1, space + 1})
			i = space + 1
		case j == i:
			segs = append(segs, seg{i, i + 1, i + 1}) // wider than the box: one rune anyway
			i++
		default:
			segs = append(segs, seg{i, j, j})
			i = j
		}
	}
}

// cursorRow finds the wrapped line the cursor is on.
func cursorRow(segs []seg, cur int) int {
	for k, sg := range segs {
		if cur >= sg.start && (cur < sg.next || k == len(segs)-1) {
			return k
		}
	}
	return len(segs) - 1
}

// moveLine moves the cursor up (-1) or down (+1) a wrapped line, keeping
// its column; false at the first or last line.
func (m *Model) moveLine(width, d int) bool {
	segs := wrapRunes(m.input, width)
	cur := m.cursor()
	row := cursorRow(segs, cur)
	to := row + d
	if to < 0 || to >= len(segs) {
		return false
	}
	col := runewidth.StringWidth(string(m.input[segs[row].start:cur]))
	sg := segs[to]
	i, w := sg.start, 0
	for i < sg.end && w+runewidth.RuneWidth(m.input[i]) <= col {
		w += runewidth.RuneWidth(m.input[i])
		i++
	}
	m.setCursor(i)
	return true
}

// chatBoxWidth is the text width inside the message box.
func (m Model) chatBoxWidth(inner int) int { return max(8, inner-4) }

// chatBoxLines draws the message box: a rounded frame around the wrapped
// text with the cursor, its placeholder when empty, and the hint line.
func (m Model) chatBoxLines(inner, maxRows int, focused bool, placeholder, hint string) []string {
	s := m.styles
	tw := m.chatBoxWidth(inner)
	border := s.muted
	if focused {
		border = lipgloss.NewStyle().Foreground(lipgloss.Color(m.chatAccent()))
	}
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	var rows []string
	if len(m.input) == 0 {
		ph := s.muted.Italic(true).Render(runewidth.Truncate(placeholder, tw-1, "…"))
		if focused {
			ph = cursorStyle.Render(" ") + ph
		}
		rows = []string{ph}
	} else {
		segs := wrapRunes(m.input, tw-1) // a cell spare for the cursor at a line's end
		cur := m.cursor()
		crow := cursorRow(segs, cur)
		top := 0
		if len(segs) > maxRows {
			top = max(0, min(crow-maxRows/2, len(segs)-maxRows))
		}
		for k := top; k < min(len(segs), top+maxRows); k++ {
			sg := segs[k]
			line := m.input[sg.start:sg.end]
			if !focused || k != crow {
				rows = append(rows, s.text.Render(string(line)))
				continue
			}
			c := cur - sg.start
			under, rest := " ", ""
			if c < len(line) {
				under, rest = string(line[c]), string(line[c+1:])
				if line[c] == ' ' {
					under = " "
				}
			}
			rows = append(rows, s.text.Render(string(line[:c]))+cursorStyle.Render(under)+s.text.Render(rest))
		}
		if len(segs) > maxRows {
			hint = s.muted.Render(strings.Repeat("·", 0)) + hint
			more := ""
			if top > 0 {
				more += "↑"
			}
			if top+maxRows < len(segs) {
				more += "↓"
			}
			hint = s.muted.Render(more+" ") + hint
		}
	}
	out := []string{border.Render("╭" + strings.Repeat("─", tw+2) + "╮")}
	for _, r := range rows {
		pad := max(0, tw-lipgloss.Width(r))
		out = append(out, border.Render("│ ")+r+strings.Repeat(" ", pad)+border.Render(" │"))
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", tw+2)+"╯"))
	if hint != "" {
		out = append(out, " "+hint)
	}
	return out
}

// chatAccent is the chat tab's colour: WhatsApp's is green, so you can
// tell at a glance which one you're typing into.
func (m Model) chatAccent() string { return m.partColor() }

// previewInner is the text width inside the preview pane.
func (m Model) previewInner() int { return m.w - m.listWidth() - 4 }
