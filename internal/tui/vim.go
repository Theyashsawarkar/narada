package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Theyashsawarkar/narada/internal/cli"
	"github.com/Theyashsawarkar/narada/internal/config"
	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/vyakarana/popup"
)

// narada's keys work like Vim's: NORMAL moves and acts, i writes (INSERT),
// v picks several (VISUAL), : takes a command, and Space is the leader: a
// menu of everything else shows if you wait after it. The keys are data
// (keymap.go), the engine (vyakarana) resolves counts and sequences, and
// run (actions.go) does the action a key names.

// normalKey is a key in NORMAL mode. An open question (y or not) or
// the palette takes it first; on an AI request only its own keys answer
// it; VISUAL has a few keys of its own; everything else goes to the
// engine, which turns it into an action of keymap.go (with a count) or
// waits for more (gg, Space m s). A key that isn't in the keymap does
// nothing.
func (m Model) normalKey(s string) (tea.Model, tea.Cmd) {
	if m.confirm != nil {
		return m.confirmKey(s)
	}
	if m.pal != nil {
		return m.paletteKey(s)
	}
	if m.inAISection() && !m.eng.Waiting() && !m.visual {
		if mm, cmd, done := m.aiKey(s); done {
			return mm, cmd
		}
	}
	if m.visual && !m.eng.Waiting() {
		if mm, cmd, done := m.visualKey(s); done {
			return mm, cmd
		}
	}
	// d3j: a count after d multiplies the motion's
	if p := strings.Fields(m.eng.Pending()); len(p) > 0 && p[len(p)-1] == "d" && len(s) == 1 && s[0] >= '0' && s[0] <= '9' && (s != "0" || m.opCount > 0) {
		m.opCount = min(99999, m.opCount*10+int(s[0]-'0'))
		m.prefix = m.eng.Pending() + " " + itoa(m.opCount)
		return m, nil
	}
	op := m.opCount
	r := m.eng.Feed(s, m.where())
	m.syncPending()
	if !m.eng.Waiting() {
		m.opCount = 0
	} else if op > 0 {
		m.prefix += " " + itoa(op)
	}
	switch {
	case r.Pending:
		return m, popup.WhichAfter(r.Gen)
	case r.Cancel:
		return m, nil
	case r.Act != "":
		count := r.Count
		if op > 0 {
			count *= op
		}
		m.lastCounted = r.Counted || op > 0
		if repeatable[r.Act] {
			m.lastChange = change{r.Act, count}
		}
		return m.run(r.Act, count)
	case r.Message() != "":
		m.say(r.Message())
	case r.Unknown != "":
		if rs := []rune(r.Unknown); len(rs) == 1 && rs[0] > ' ' && rs[0] < 0x7f {
			m.say(r.Unknown + " does nothing here · ? lists the keys, / finds what you want")
		}
	}
	return m, nil
}

// syncPending mirrors what the engine is waiting for into the fields the
// screen draws: leader (a Space sequence: its menu), prefix (gg, dd, zz…:
// the bar's showcmd).
func (m *Model) syncPending() {
	p := m.eng.Pending()
	m.leader = strings.Contains(" "+p+" ", " Space ")
	m.prefix = ""
	if !m.leader {
		m.prefix = p
	}
	if !m.eng.Waiting() {
		m.leaderShown = false
	}
}

// pendingKeys is the sequence under way without its count ("Space m").
func (m Model) pendingKeys() string {
	f := strings.Fields(m.eng.Pending())
	for len(f) > 0 && f[0][0] >= '0' && f[0][0] <= '9' {
		f = f[1:]
	}
	return strings.Join(f, " ")
}

// whichMenu is the which-key menu: what can follow the keys typed so far,
// a group as +its name, in columns, over the bottom of the screen.
func (m Model) whichMenu() []string {
	s := m.styles
	hints := m.eng.Hints(m.where())
	w := m.w - 4
	cols := max(1, min(4, (w-4)/30))
	colW := (w - 4) / cols
	rows := (len(hints) + cols - 1) / cols
	border := fg(s.c.Mauve)
	var body []string
	title := s.accent2.Bold(true).Foreground(lipgloss.Color(s.c.Mauve)).Render(m.eng.Pending())
	if name := narKeys.GroupName(m.pendingKeys()); name != "" {
		title += s.text.Render("  " + name)
	}
	body = append(body, title+s.muted.Render("  then…   ⌫ up a level · esc never mind"))
	for r := range rows {
		line := ""
		for c := range cols {
			i := c*rows + r
			if i >= len(hints) {
				break
			}
			h := hints[i]
			key := h.Key
			cell := fg(s.c.Mauve).Bold(true).Render(key) + "  "
			if h.Group {
				cell += s.sky.Render("+" + h.Desc)
			} else {
				cell += s.text.Render(h.Desc)
			}
			line += fit(cell, colW-2) + "  "
		}
		body = append(body, line)
	}
	inner := w - 2
	out := []string{border.Render("╭" + strings.Repeat("─", inner) + "╮")}
	for _, l := range body {
		out = append(out, border.Render("│")+" "+fit(l, inner-2)+" "+border.Render("│"))
	}
	return append(out, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
}

// ------------------------------------------------------------ the palette

// openPalette opens Space Space: every action that works here, searched
// as you type; Enter runs the picked one.
func (m *Model) openPalette() {
	p := popup.NewPalette(narKeys, m.where())
	m.pal = &p
}

// paletteMsg: typed (or pasted) text goes into the palette's query.
func (m Model) paletteMsg(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.Type == tea.KeyRunes && !k.Alt && len(k.Runes) > 1 {
		p := *m.pal
		p.Query, p.Sel = p.Query+string(k.Runes), 0
		m.pal = &p
		return m, nil
	}
	return m.paletteKey(k.String())
}

func (m Model) paletteKey(s string) (tea.Model, tea.Cmd) {
	p := *m.pal
	act, closed := p.Key(s)
	m.pal = &p
	if !closed {
		return m, nil
	}
	m.pal = nil
	if act == "" {
		return m, nil
	}
	m.lastCounted = false
	return m.run(act, 1)
}

// palettePopup draws the palette in the middle of the screen.
func (m Model) palettePopup() []string {
	s := m.styles
	w := min(m.w-4, 96)
	inner := w - 2
	border := fg(s.c.Mauve)
	matches := m.pal.Matches()
	room := max(3, min(12, m.bodyHeight()-8))
	top := max(0, min(m.pal.Sel-room+1, len(matches)-room))
	body := []string{s.accent2.Bold(true).Foreground(lipgloss.Color(s.c.Mauve)).Render("❯ ") + s.text.Render(m.pal.Query) +
		lipgloss.NewStyle().Reverse(true).Render(" ") + s.muted.Render(fmt.Sprintf("   %d of %d", len(matches), len(m.pal.Items)))}
	for i := top; i < min(len(matches), top+room); i++ {
		b := matches[i]
		row := fit(s.round(s.keycap, b.Keys()), 18) + " " + s.text.Render(b.Desc)
		if i == m.pal.Sel {
			row = fg(s.c.Mauve).Bold(true).Render("▶ ") + row
		} else {
			row = "  " + row
		}
		body = append(body, row)
	}
	if len(matches) == 0 {
		body = append(body, s.muted.Render("  nothing matches"))
	}
	label := " Space Space · every action here "
	out := []string{border.Render("╭─" + label + strings.Repeat("─", max(0, inner-1-lipgloss.Width(label))) + "╮")}
	for _, l := range body {
		out = append(out, border.Render("│")+" "+fit(l, inner-2)+" "+border.Render("│"))
	}
	return append(out, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
}

// visualKey: with a range picked (v), j/k grow it and a key acts on all of
// it. done is false for keys VISUAL leaves to NORMAL (Space…).
func (m Model) visualKey(s string) (tea.Model, tea.Cmd, bool) {
	if m.vg {
		m.vg = false
		if s == "g" {
			m.sel = 0
			m.visualRange()
		}
		return m, nil, true
	}
	switch s {
	case "esc", "v", "q":
		m.rememberVisual()
		m.visual = false
		m.selected = map[string]bool{}
		return m, nil, true
	case "g":
		m.vg = true
		return m, nil, true
	case "j", "k", "down", "up", "G":
		switch s {
		case "j", "down":
			m.sel = min(m.count()-1, m.sel+1)
		case "k", "up":
			m.sel = max(0, m.sel-1)
		case "G":
			m.sel = max(0, m.count()-1)
		}
		m.visualRange()
		return m, nil, true
	case "d", "e", "~", "y":
		m.rememberVisual()
		m.visual = false
		switch s {
		case "y":
			// what's picked, as you'd write it: subjects, or chat names
			var names []string
			for i := range m.count() {
				if !m.selected[m.keyAt(i)] {
					continue
				}
				if m.tab == tabMail && i < len(m.threads) {
					names = append(names, m.threads[i].Participants+": "+m.threads[i].Subject)
				} else if i < len(m.spaces) {
					names = append(names, cli.SpaceTitle(m.spaces[i]))
				}
			}
			m.selected = map[string]bool{}
			m.say(copyAs(strings.Join(names, "\n"), fmt.Sprintf("%d %s", len(names), map[bool]string{true: "subjects", false: "chat names"}[m.tab == tabMail])))
			return m, nil, true
		case "d":
			if m.tab != tabMail {
				m.say("d trashes mail; in a chat, dd deletes a picked message of yours")
				m.selected = map[string]bool{}
				return m, nil, true
			}
			mm, cmd := m.deleteIt(1)
			return mm, cmd, true
		case "e":
			if m.tab != tabMail {
				m.say("e archives one WhatsApp chat at a time")
				m.selected = map[string]bool{}
				return m, nil, true
			}
			cmd := m.modify("e")
			m.selected = map[string]bool{}
			return m, cmd, true
		}
		mm, cmd := m.toggleRead()
		return mm, cmd, true
	}
	return m, nil, false
}

// visualRange picks everything between where v started and here.
func (m *Model) visualRange() {
	m.selected = map[string]bool{}
	for i := min(m.visualFrom, m.sel); i <= max(m.visualFrom, m.sel); i++ {
		if k := m.keyAt(i); k != "" && !strings.Contains(k, outboxPrefix) {
			m.selected[k] = true
		}
	}
	m.loadPreview(true)
}

// ------------------------------------------------------------ moving

// placeAt is the place you're in: 0 Mail, 1 Chat, 2 WhatsApp, 3 AI requests.
func (m Model) placeAt() int {
	if m.inAISection() {
		return 3
	}
	return m.place()
}

// goPlace goes to Mail, Chat, WhatsApp or the AI requests (always: an
// empty list says how requests arrive).
func (m *Model) goPlace(i int) (tea.Model, tea.Cmd) {
	m.visual = false
	m.eng.Reset()
	m.syncPending()
	if i == 3 {
		kind := ""
		if len(m.waiting) > 0 {
			kind = m.waiting[0].Kind
		}
		m.goToAI(kind)
		return *m, nil
	}
	if i == m.place() && !m.inAISection() {
		return *m, nil
	}
	m.tab, m.wa = tabChat, i == 2
	if i == 0 {
		m.tab = tabMail
	}
	m.sec, m.sel, m.offset, m.filter, m.search, m.ai = 0, 0, 0, "", nil, ""
	m.selected = map[string]bool{}
	m.toList()
	m.reload()
	return *m, nil
}

// aiKey: on an AI request only its own keys answer it, the raw key and
// nothing that a key turns into, so no other key can send an agent's
// message. done is false for keys that move around as anywhere else.
func (m Model) aiKey(s string) (tea.Model, tea.Cmd, bool) {
	it, ok := m.curOutbox()
	switch s {
	case "y", "e", "n":
		if !ok || it.Status != store.StatusApproval {
			m.say("no AI request picked")
			return m, nil, true
		}
		m.apID = it.ID
		if s == "n" && m.armDecline != it.ID {
			m.armDecline = it.ID
			m.say("n again declines it: nothing is sent, and the agent is told")
			return m, nil, true
		}
		m.armDecline = 0
		mm, cmd := m.approveKey(s)
		return mm, cmd, true
	case "j", "k", "down", "up", "g", "G", "enter", "1", "2", "3", "4", "5", "6", "7", "8", "9", "tab", "shift+tab", " ", "space", "/", "?", ":",
		"q", "esc", "ctrl+d", "ctrl+u", "ctrl+f", "ctrl+b", "ctrl+j", "ctrl+k", "ctrl+e", "ctrl+y", "pgup", "pgdown", "home", "end",
		"J", "K", "H", "L", "h", "l", "[", "]", "z", "ctrl+n", "ctrl+p", "ctrl+c", "alt+m", "alt+c", "alt+w":
		m.armDecline = 0
		return m, nil, false
	}
	m.say("on an AI request: y sends it · e edits it first · n declines · Enter shows it all")
	return m, nil, true
}

// goSection opens the place's section of that name (Unread…).
func (m *Model) goSection(name string) (tea.Model, tea.Cmd) {
	if m.tab == tabMail {
		if i := slices.IndexFunc(m.sections, func(s config.Section) bool { return s.Name == name }); i >= 0 {
			m.sec, m.search = i, nil
		}
	} else if i := slices.Index(chatSections, name); i >= 0 {
		m.sec = i
	}
	m.sel, m.offset, m.filter = 0, 0, ""
	m.reload()
	return *m, nil
}

// nextUnread moves to the next (or previous) unread thread or chat in the
// list; open opens it too (Space Space).
func (m *Model) nextUnread(step int, open bool) (tea.Model, tea.Cmd) {
	n := m.count()
	for k := 1; k <= n; k++ {
		i := ((m.sel+step*k)%n + n) % n
		unread := false
		if m.tab == tabMail && i < len(m.threads) {
			unread = m.threads[i].Unread()
		} else if m.tab == tabChat && i < len(m.spaces) {
			unread = m.spaces[i].Unread > 0
		}
		if unread {
			m.sel = i
			m.loadPreview(true)
			m.fixView()
			if open {
				return m.actKey("enter")
			}
			return *m, nil
		}
	}
	m.say("nothing unread here")
	return *m, nil
}

// nextAccount switches between your Google accounts (all, then each).
func (m *Model) nextAccount() (tea.Model, tea.Cmd) {
	if len(m.accounts) < 2 {
		m.say("only one account (narada auth add <name> adds another)")
		return *m, nil
	}
	m.acct++
	if m.acct >= len(m.accounts) {
		m.acct = -1
	}
	m.sel, m.offset = 0, 0
	m.reload()
	if m.acct < 0 {
		m.say("all accounts")
	} else {
		m.say("account " + m.accounts[m.acct])
	}
	return *m, nil
}

// findAnything opens the finder: everywhere (/), or only here (Space /).
func (m *Model) findAnything(everywhere bool) (tea.Model, tea.Cmd) {
	m.openFinder()
	if everywhere {
		if m.tab == tabMail {
			m.find.scope = scopeAllMail
		} else {
			m.find.scope = scopeAllChat
		}
	}
	m.find.placeholder()
	return *m, nil
}

// runKeys plays keys as if typed, for the finder's actions and : commands:
// "gg", "Space m", "ctrl+x".
func (m Model) runKeys(seq string) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var mm tea.Model = m
	for _, tok := range strings.Fields(seq) {
		var keys []string
		switch low := strings.ToLower(tok); {
		case low == "space":
			keys = []string{" "}
		case strings.Contains(low, "+") && len(low) > 1, low == "enter", low == "tab", low == "esc", low == "backspace",
			low == "home", low == "end", low == "up", low == "down":
			keys = []string{low}
		default:
			for _, r := range tok {
				keys = append(keys, string(r))
			}
		}
		for _, k := range keys {
			var cmd tea.Cmd
			mm, cmd = mm.(Model).key(keyMsgOf(k))
			cmds = append(cmds, cmd)
		}
	}
	return mm, tea.Batch(cmds...)
}

// keyMsgOf turns "ctrl+x", "enter" or "g" into the key Bubble Tea sends.
func keyMsgOf(k string) tea.KeyMsg {
	named := map[string]tea.KeyType{"enter": tea.KeyEnter, "tab": tea.KeyTab, "esc": tea.KeyEsc, "backspace": tea.KeyBackspace,
		"ctrl+x": tea.KeyCtrlX, "ctrl+k": tea.KeyCtrlK, "ctrl+d": tea.KeyCtrlD, "ctrl+u": tea.KeyCtrlU, "ctrl+n": tea.KeyCtrlN, "ctrl+p": tea.KeyCtrlP,
		"ctrl+j": tea.KeyCtrlJ, "ctrl+f": tea.KeyCtrlF, "ctrl+b": tea.KeyCtrlB, "ctrl+e": tea.KeyCtrlE, "ctrl+y": tea.KeyCtrlY,
		"shift+tab": tea.KeyShiftTab, "up": tea.KeyUp, "down": tea.KeyDown, "home": tea.KeyHome, "end": tea.KeyEnd,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "left": tea.KeyLeft, "right": tea.KeyRight, "delete": tea.KeyDelete,
		"ctrl+s": tea.KeyCtrlS, "ctrl+t": tea.KeyCtrlT, "ctrl+c": tea.KeyCtrlC, "ctrl+o": tea.KeyCtrlO, "ctrl+v": tea.KeyCtrlV,
		"ctrl+w": tea.KeyCtrlW, "ctrl+a": tea.KeyCtrlA, "ctrl+r": tea.KeyCtrlR, "ctrl+g": tea.KeyCtrlG}
	if t, ok := named[k]; ok {
		return tea.KeyMsg{Type: t}
	}
	if strings.HasPrefix(k, "alt+") {
		r, _ := utf8.DecodeRuneInString(k[4:])
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true}
	}
	if k == " " {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}
