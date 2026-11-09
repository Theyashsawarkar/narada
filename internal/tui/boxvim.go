package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Theyashsawarkar/narada/internal/cli"
)

// Vim in the message box (plan §4.3, phase 6): Esc while writing goes to
// the box's own NORMAL, keeping your text and the cursor in it; a second
// Esc (or q) leaves the box, the text kept as before. In the box's NORMAL:
// h l w b e 0 ^ $ gg G j k move; x dw dd cw ciw C D p edit; u / Ctrl+r
// undo and redo; i a I A o go back to typing; Enter still sends; Space e e
// (or Ctrl+x Ctrl+e while typing) edits the message in $EDITOR. The
// compose screen's body has the same keys (message_box_vim = false in
// config.toml turns both off).

// boxText is the text being edited and the cursor in it (a rune index).
type boxText struct {
	t   []rune
	cur int
}

func (b boxText) lineStart(i int) int {
	for i > 0 && b.t[i-1] != '\n' {
		i--
	}
	return i
}

func (b boxText) lineEnd(i int) int {
	for i < len(b.t) && b.t[i] != '\n' {
		i++
	}
	return i
}

// lastCol is the last character of the line (NORMAL's cursor sits on one).
func (b boxText) lastCol(i int) int {
	s, e := b.lineStart(i), b.lineEnd(i)
	return max(s, e-1)
}

func (b *boxText) clampNormal() {
	b.cur = max(0, min(b.cur, len(b.t)))
	if b.cur == len(b.t) || (b.cur < len(b.t) && b.t[b.cur] == '\n') {
		b.cur = max(b.lineStart(b.cur), b.cur-1)
		if b.cur < len(b.t) && b.t[b.cur] == '\n' && b.cur > b.lineStart(b.cur) {
			b.cur--
		}
	}
	if b.cur > 0 && b.cur <= len(b.t) && b.cur == b.lineEnd(b.cur) && b.cur > b.lineStart(b.cur) {
		b.cur--
	}
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// wordNext is w: the start of the next word.
func (b boxText) wordNext(i int) int {
	n := len(b.t)
	if i < n && !unicode.IsSpace(b.t[i]) {
		w := isWord(b.t[i])
		for i < n && !unicode.IsSpace(b.t[i]) && isWord(b.t[i]) == w {
			i++
		}
	}
	for i < n && unicode.IsSpace(b.t[i]) {
		i++
	}
	return i
}

// wordEnd is e: the end of this (or the next) word.
func (b boxText) wordEnd(i int) int {
	n := len(b.t)
	i++
	for i < n && unicode.IsSpace(b.t[i]) {
		i++
	}
	if i >= n {
		return max(0, n-1)
	}
	w := isWord(b.t[i])
	for i+1 < n && !unicode.IsSpace(b.t[i+1]) && isWord(b.t[i+1]) == w {
		i++
	}
	return i
}

// wordPrev is b: the start of this (or the previous) word.
func (b boxText) wordPrev(i int) int {
	if i > 0 {
		i--
	}
	for i > 0 && unicode.IsSpace(b.t[i]) {
		i--
	}
	if i < len(b.t) {
		w := isWord(b.t[i])
		for i > 0 && !unicode.IsSpace(b.t[i-1]) && isWord(b.t[i-1]) == w {
			i--
		}
	}
	return i
}

// innerWord is iw: the word (or run of spaces) under the cursor.
func (b boxText) innerWord(i int) (int, int) {
	if len(b.t) == 0 {
		return 0, 0
	}
	i = min(i, len(b.t)-1)
	s, e := i, i+1
	same := func(r rune) bool {
		if unicode.IsSpace(b.t[i]) {
			return unicode.IsSpace(r) && r != '\n'
		}
		return !unicode.IsSpace(r) && isWord(r) == isWord(b.t[i])
	}
	for s > 0 && same(b.t[s-1]) {
		s--
	}
	for e < len(b.t) && same(b.t[e]) {
		e++
	}
	return s, e
}

func (b *boxText) cut(from, to int) {
	from, to = max(0, min(from, len(b.t))), max(0, min(to, len(b.t)))
	if from > to {
		from, to = to, from
	}
	b.t = append(b.t[:from:from], b.t[to:]...)
	b.cur = from
}

// vertical is j / k: the same column on the next / previous line.
func (b boxText) vertical(i, d int) int {
	col := i - b.lineStart(i)
	if d > 0 {
		e := b.lineEnd(i)
		if e >= len(b.t) {
			return i
		}
		s := e + 1
		return min(s+col, b.lineEnd(s))
	}
	s := b.lineStart(i)
	if s == 0 {
		return i
	}
	ps := b.lineStart(s - 1)
	return min(ps+col, s-1)
}

// boxResult says what a key did to the box.
type boxResult struct {
	insert  bool // back to typing (INSERT)
	leave   bool // Esc / q: out of the box
	send    bool // Enter
	editor  bool // Space e e: $EDITOR
	changed bool // the text changed (for u)
	pending bool // waiting for the rest of a sequence (dw, ciw, gg)
	unknown bool
}

// boxApply does one NORMAL key of the box; pend is what came before it
// ("d", "c", "ci", "g", "Space", "Space e").
func boxApply(b *boxText, pend *string, key, clip string) boxResult {
	p := *pend
	*pend = ""
	switch p {
	case "d":
		switch key {
		case "w":
			b.cut(b.cur, b.wordNext(b.cur))
		case "d":
			s, e := b.lineStart(b.cur), b.lineEnd(b.cur)
			if e < len(b.t) {
				e++ // the line and its newline
			} else if s > 0 {
				s-- // the last line: the newline before it
			}
			b.cut(s, e)
			b.cur = b.lineStart(min(b.cur, len(b.t)))
		case "$":
			b.cut(b.cur, b.lineEnd(b.cur))
		case "b":
			b.cut(b.wordPrev(b.cur), b.cur)
		case "e":
			b.cut(b.cur, b.wordEnd(b.cur)+1)
		default:
			return boxResult{unknown: true}
		}
		b.clampNormal()
		return boxResult{changed: true}
	case "c":
		switch key {
		case "w", "e":
			b.cut(b.cur, b.wordEnd(b.cur)+1)
		case "i":
			*pend = "ci"
			return boxResult{pending: true}
		case "c":
			s := b.lineStart(b.cur)
			b.cut(s, b.lineEnd(b.cur))
		case "$":
			b.cut(b.cur, b.lineEnd(b.cur))
		default:
			return boxResult{unknown: true}
		}
		return boxResult{changed: true, insert: true}
	case "ci":
		if key != "w" {
			return boxResult{unknown: true}
		}
		s, e := b.innerWord(b.cur)
		b.cut(s, e)
		return boxResult{changed: true, insert: true}
	case "g":
		if key == "g" {
			b.cur = 0
			b.clampNormal()
			return boxResult{}
		}
		return boxResult{unknown: true}
	case "Space":
		if key == "e" {
			*pend = "Space e"
			return boxResult{pending: true}
		}
		return boxResult{unknown: true}
	case "Space e":
		if key == "e" {
			return boxResult{editor: true}
		}
		return boxResult{unknown: true}
	}
	switch key {
	case "h", "left":
		if b.cur > b.lineStart(b.cur) {
			b.cur--
		}
	case "l", "right":
		if b.cur < b.lastCol(b.cur) {
			b.cur++
		}
	case "w":
		b.cur = b.wordNext(b.cur)
		b.clampNormal()
	case "b":
		b.cur = b.wordPrev(b.cur)
	case "e":
		b.cur = b.wordEnd(b.cur)
	case "0", "home":
		b.cur = b.lineStart(b.cur)
	case "^":
		b.cur = b.lineStart(b.cur)
		for b.cur < b.lineEnd(b.cur) && unicode.IsSpace(b.t[b.cur]) {
			b.cur++
		}
	case "$", "end":
		b.cur = b.lastCol(b.cur)
	case "G":
		b.cur = b.lineStart(len(b.t))
		b.clampNormal()
	case "j", "down":
		b.cur = b.vertical(b.cur, 1)
		b.clampNormal()
	case "k", "up":
		b.cur = b.vertical(b.cur, -1)
		b.clampNormal()
	case "x", "delete":
		if b.cur < len(b.t) && b.t[b.cur] != '\n' {
			b.cut(b.cur, b.cur+1)
			b.clampNormal()
			return boxResult{changed: true}
		}
	case "D":
		b.cut(b.cur, b.lineEnd(b.cur))
		b.clampNormal()
		return boxResult{changed: true}
	case "C":
		b.cut(b.cur, b.lineEnd(b.cur))
		return boxResult{changed: true, insert: true}
	case "p":
		if clip == "" {
			return boxResult{}
		}
		at := min(len(b.t), b.cur+1)
		if len(b.t) == 0 {
			at = 0
		}
		r := []rune(clip)
		b.t = append(b.t[:at], append(r, b.t[at:]...)...)
		b.cur = at + len(r) - 1
		return boxResult{changed: true}
	case "i":
		return boxResult{insert: true}
	case "a":
		if len(b.t) > 0 && b.cur < b.lineEnd(b.cur) {
			b.cur++
		}
		return boxResult{insert: true}
	case "I":
		b.cur = b.lineStart(b.cur)
		return boxResult{insert: true}
	case "A":
		b.cur = b.lineEnd(b.cur)
		return boxResult{insert: true}
	case "o":
		e := b.lineEnd(b.cur)
		b.t = append(b.t[:e], append([]rune{'\n'}, b.t[e:]...)...)
		b.cur = e + 1
		return boxResult{changed: true, insert: true}
	case "d", "c", "g":
		*pend = key
		return boxResult{pending: true}
	case " ", "space":
		*pend = "Space"
		return boxResult{pending: true}
	case "enter":
		return boxResult{send: true}
	case "esc", "q":
		return boxResult{leave: true}
	default:
		return boxResult{unknown: true}
	}
	return boxResult{}
}

// ------------------------------------------------------------ the chat box

// boxKey is a key in the chat box's NORMAL.
func (m Model) boxKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	switch s {
	case "u":
		if n := len(m.boxUndo); n > 0 {
			m.boxRedo = append(m.boxRedo, append([]rune(nil), m.input...))
			m.input, m.boxUndo = m.boxUndo[n-1], m.boxUndo[:n-1]
			m.back = 0
			m.setCursor(min(len(m.input), m.cursor()))
		} else {
			m.say("nothing to undo in the box")
		}
		return m, nil
	case "ctrl+r":
		if n := len(m.boxRedo); n > 0 {
			m.boxUndo = append(m.boxUndo, append([]rune(nil), m.input...))
			m.input, m.boxRedo = m.boxRedo[n-1], m.boxRedo[:n-1]
			m.back = 0
		}
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "alt+esc", "alt+[":
		s = "esc" // two Escs close together arrive as one: leave the box
	}
	b := boxText{t: append([]rune(nil), m.input...), cur: m.cursor()}
	before := append([]rune(nil), m.input...)
	clip := ""
	if s == "p" {
		clip = clipboardText()
	}
	r := boxApply(&b, &m.boxPend, s, clip)
	if r.changed {
		m.boxUndo = append(m.boxUndo, before)
		m.boxRedo = nil
	}
	m.input = b.t
	m.back = 0
	m.setCursor(b.cur)
	switch {
	case r.leave:
		m.boxNormal = false
		m.back = 0
		m.keepDraft()
		m.mode = modeNormal
	case r.insert:
		m.boxNormal = false
	case r.send:
		m.boxNormal = false
		return m.inputKey(tea.KeyMsg{Type: tea.KeyEnter})
	case r.editor:
		return m, m.boxEditor()
	case r.unknown && len(s) == 1:
		m.say(s + " does nothing in the box's NORMAL · i types · Esc leaves the box")
	}
	return m, nil
}

// boxEditedMsg is the message box back from $EDITOR.
type boxEditedMsg struct {
	path string
	err  error
}

// boxEditor opens the message being written in $EDITOR.
func (m *Model) boxEditor() tea.Cmd {
	path, err := cli.TempFile(m.a, "txt", string(m.input))
	if err != nil {
		m.err = err.Error()
		return nil
	}
	return execEditor(m, path, func(err error) tea.Msg { return boxEditedMsg{path: path, err: err} })
}

func (m Model) afterBoxEdit(msg boxEditedMsg) (tea.Model, tea.Cmd) {
	defer os.Remove(msg.path)
	if msg.err != nil {
		m.setErr(fmt.Sprintf("the editor (%s) failed: %v", cli.EditorName(m.a), msg.err))
		return m, nil
	}
	b, err := os.ReadFile(msg.path)
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	m.boxUndo = append(m.boxUndo, append([]rune(nil), m.input...))
	m.input = []rune(strings.TrimRight(string(b), "\n"))
	m.back = 0
	m.mode, m.boxNormal = modeChatMsg, true
	m.say("back from the editor · Enter sends · i types")
	return m, nil
}

// clipboardText is the text that's copied (wl-paste or xclip), or "";
// tests replace it, so they never read the real clipboard.
var clipboardText = func() string {
	for _, argv := range [][]string{{"wl-paste", "--no-newline", "--type", "text/plain"}, {"xclip", "-selection", "clipboard", "-o"}} {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		if out, err := exec.Command(argv[0], argv[1:]...).Output(); err == nil {
			return strings.TrimRight(string(out), "\n")
		}
	}
	return ""
}
