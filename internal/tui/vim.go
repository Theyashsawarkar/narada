package tui

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/cli"
	"github.com/Theyashsawarkar/narada/internal/config"
)

// narada's keys work like Vim's: NORMAL moves and acts, i writes (INSERT),
// v picks several (VISUAL), : takes a command, and Space is the leader: a
// menu of everything else shows if you wait after it. Keys can come in
// twos (gg, dd, yy, za, gf). vimKey turns these into the screen's own
// actions, so one action has one implementation whatever key reaches it.

// leaderWait is how long after Space the menu waits before showing:
// typed straight on, a leader key never flashes the menu.
const leaderWait = 350 * time.Millisecond

type leaderTick struct{ at int }

// leaderKey is one entry of the Space menu.
type leaderKey struct {
	key, desc string
	color     func(styles) string
	run       func(m *Model) (tea.Model, tea.Cmd)
}

func (m Model) leaderKeys() []leaderKey {
	place := func(i int) func(m *Model) (tea.Model, tea.Cmd) {
		return func(m *Model) (tea.Model, tea.Cmd) { return m.goPlace(i) }
	}
	keys := func(k string) func(m *Model) (tea.Model, tea.Cmd) {
		return func(m *Model) (tea.Model, tea.Cmd) { return m.actKey(k) }
	}
	c := func(f func(s styles) string) func(styles) string { return f }
	blue, teal, green, peach := c(func(s styles) string { return s.c.Blue }), c(func(s styles) string { return s.c.Teal }), c(func(s styles) string { return s.c.Green }), c(func(s styles) string { return s.c.Peach })
	mauve, yellow, sky, red, sub := c(func(s styles) string { return s.c.Mauve }), c(func(s styles) string { return s.c.Yellow }), c(func(s styles) string { return s.c.Sky }), c(func(s styles) string { return s.c.Red }), c(func(s styles) string { return s.c.Subtle })
	return []leaderKey{
		{"m", "Mail", blue, place(0)},
		{"c", "Chat", teal, place(1)},
		{"w", "WhatsApp", green, place(2)},
		{"a", "AI requests", peach, place(3)},
		{"f", "Find anything (also /)", mauve, func(m *Model) (tea.Model, tea.Cmd) { return m.findAnything(true) }},
		{"/", "Search here", mauve, func(m *Model) (tea.Model, tea.Cmd) { return m.findAnything(false) }},
		{"n", "Write a new mail or message", green, keys("c")},
		{"N", "Start a conversation", green, func(m *Model) (tea.Model, tea.Cmd) { m.tab = tabChat; m.openConvo(); return *m, nil }},
		{"S", "Star / unstar", yellow, keys("*")},
		{"l", "Label", mauve, keys("L")},
		{"u", "Unread only", red, func(m *Model) (tea.Model, tea.Cmd) { return m.goSection("Unread") }},
		{"o", "Open a link", sky, keys("o")},
		{"v", "Open the files", yellow, keys("v")},
		{"V", "Save the files", yellow, keys("V")},
		{"U", "Send a file here", yellow, keys("U")},
		{"d", "Draft a reply with Claude", yellow, keys("D")},
		{"s", "Summary by Claude", yellow, keys("S")},
		{"k", "Ask Claude", yellow, keys("@")},
		{"t", "Today's digest", yellow, keys("T")},
		{"R", "Rename this chat", sub, func(m *Model) (tea.Model, tea.Cmd) {
			if sp, ok := m.curSpace(); ok {
				m.mode, m.input = modeRename, []rune(cli.SpaceTitle(sp))
			}
			return *m, nil
		}},
		{"p", "Pin / unpin this chat", sub, keys("p")},
		{"x", "Switch account", sub, func(m *Model) (tea.Model, tea.Cmd) { return m.nextAccount() }},
		{"r", "Sync now", sub, func(m *Model) (tea.Model, tea.Cmd) { m.say("syncing…"); return *m, m.syncCmd() }},
		{"b", "Open in the browser", sky, keys("b")},
		{"y", "Copy the link", sky, keys("y")},
		{"␣", "Open the next unread", red, func(m *Model) (tea.Model, tea.Cmd) { return m.nextUnread(1, true) }},
		{"?", "All keys", sub, keys("?")},
		{"q", "Quit", red, func(m *Model) (tea.Model, tea.Cmd) { return *m, tea.Quit }},
	}
}

// leaderMenu draws the Space menu above the bar, in columns.
func (m Model) leaderMenu() []string {
	s := m.styles
	ks := m.leaderKeys()
	w := m.w - 4
	cols := max(1, min(4, (w-4)/32))
	colW := (w - 4) / cols
	rows := (len(ks) + cols - 1) / cols
	border := fg(s.c.Mauve)
	var body []string
	body = append(body, s.accent2.Bold(true).Foreground(lipgloss.Color(s.c.Mauve)).Render("Space")+s.muted.Render("  then…"))
	for r := range rows {
		line := ""
		for c := range cols {
			i := c*rows + r
			if i >= len(ks) {
				break
			}
			k := ks[i]
			cell := fg(k.color(s)).Bold(true).Render(k.key) + "  " + s.text.Render(k.desc)
			line += fit(cell, colW)
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

// startLeader: Space was pressed; the menu shows if nothing follows soon.
func (m *Model) startLeader() tea.Cmd {
	m.leader, m.leaderShown = true, false
	m.leaderAt++
	at := m.leaderAt
	return tea.Tick(leaderWait, func(time.Time) tea.Msg { return leaderTick{at} })
}

func (m Model) leaderKey(s string) (tea.Model, tea.Cmd) {
	m.leader, m.leaderShown = false, false
	if s == "esc" || s == "ctrl+c" {
		return m, nil
	}
	if s == " " || s == "space" {
		s = "␣"
	}
	for _, k := range m.leaderKeys() {
		if k.key == s {
			return k.run(&m)
		}
	}
	m.say("Space " + s + " does nothing · Space and wait shows the menu")
	return m, nil
}

// ------------------------------------------------------------ the keys

// vimKey handles narada's Vim keys in NORMAL mode. done is false for a key
// it leaves to normalKey (under the name normalKey knows it by).
func (m Model) vimKey(s string) (tea.Model, tea.Cmd, string, bool) {
	if m.leader {
		mm, cmd := m.leaderKey(s)
		return mm, cmd, "", true
	}
	if m.prefix != "" {
		two := m.prefix + s
		m.prefix = ""
		return m.twoKeys(two)
	}
	if m.visual {
		mm, cmd, done := m.visualKey(s)
		if done {
			return mm, cmd, "", true
		}
	}
	switch s {
	case " ", "space":
		return m, m.startLeader(), "", true
	case "g", "d", "y", "z":
		m.prefix = s
		return m, nil, "", true
	case ":":
		m.mode, m.input = modeCommand, nil
		return m, nil, "", true
	case "/", "ctrl+k":
		mm, cmd := m.findAnything(true)
		return mm, cmd, "", true
	case "[", "]":
		return m, nil, map[string]string{"[": "h", "]": "l"}[s], false // the old h / l: sections
	case "ctrl+p", "ctrl+n":
		return m, nil, map[string]string{"ctrl+p": "[", "ctrl+n": "]"}[s], false // the old [ / ]: chats
	case "h":
		m.focus = 0
		return m, nil, "", true
	case "l":
		if m.focus == 0 {
			return m, nil, "2", false // the old 2: what's open
		}
		return m, nil, "", true
	case "1", "2", "3", "4":
		mm, cmd := m.goPlace(int(s[0] - '1'))
		return mm, cmd, "", true
	case "n", "N":
		mm, cmd := m.nextUnread(map[string]int{"n": 1, "N": -1}[s], false)
		return mm, cmd, "", true
	case "u":
		return m, nil, "z", false // the old z: undo, or cancel a send
	case "#":
		return m, nil, "d", false // the old d: to the trash
	case "Y":
		return m, nil, "y", false
	case "v":
		if m.pickingMsg() {
			return m, nil, "", true
		}
		m.visual, m.visualFrom = true, m.sel
		m.selected = map[string]bool{}
		if k := m.keyAt(m.sel); k != "" {
			m.selected[k] = true
		}
		return m, nil, "", true
	case "V":
		return m, nil, "ctrl+x", false // the whole list
	case "a":
		if m.tab == tabChat {
			return m, nil, "i", false
		}
		return m, nil, "a", false // mail: reply to everyone
	case "R":
		if m.tab == tabMail {
			if _, ok := m.curOutbox(); !ok {
				return m, nil, "a", false
			}
		}
		return m, nil, "R", false // private reply, or send a failed one again
	case "s", "Q", "x":
		return m, nil, s, false
	}
	return m, nil, s, false
}

// twoKeys runs gg, gt, gT, gf, gF, gx, dd, yy, za and zq.
func (m Model) twoKeys(two string) (tea.Model, tea.Cmd, string, bool) {
	switch two {
	case "gg":
		return m, nil, "g", false
	case "gt", "gT":
		step := map[string]int{"gt": 1, "gT": 3}[two]
		mm, cmd := m.goPlace((m.placeAt() + step) % 4)
		return mm, cmd, "", true
	case "gf", "gF":
		return m, nil, map[string]string{"gf": "v", "gF": "V"}[two], false
	case "gx":
		return m, nil, "o", false
	case "dd":
		switch {
		case m.pickingMsg():
			// the first dd asks, the second deletes: a message deleted for
			// everyone can't be brought back
			return m, nil, "d", false
		case m.tab == tabMail || m.wa:
			return m, nil, "e", false // archive
		}
		m.say("dd archives mail and WhatsApp chats; Google Chat has no archive")
		return m, nil, "", true
	case "yy":
		return m, nil, "y", false
	case "za":
		if m.tab == tabMail {
			return m, nil, "E", false
		}
		return m, nil, "", true
	case "zq":
		return m, nil, "Q", false
	}
	if p := two[:1]; p == "g" || p == "d" || p == "y" || p == "z" {
		m.say(two + " does nothing · ? shows every key")
	}
	return m, nil, "", true
}

// visualKey: with a range picked (v), j/k grow it and a key acts on all of it.
func (m Model) visualKey(s string) (tea.Model, tea.Cmd, bool) {
	switch s {
	case "esc", "v", "q":
		m.visual = false
		m.selected = map[string]bool{}
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
		m.selected = map[string]bool{}
		for i := min(m.visualFrom, m.sel); i <= max(m.visualFrom, m.sel); i++ {
			if k := m.keyAt(i); k != "" && !strings.Contains(k, outboxPrefix) {
				m.selected[k] = true
			}
		}
		m.loadPreview(true)
		return m, nil, true
	case "d", "m", "M", "*", "y":
		m.visual = false
		switch {
		case s == "y":
			var keys []string
			for k := range m.selected {
				keys = append(keys, k)
			}
			m.selected = map[string]bool{}
			m.say(copyText(strings.Join(keys, "\n")))
			return m, nil, true
		case s == "d" && m.tab == tabMail:
			return m, m.modify("e"), true
		case s == "d":
			m.say("dd archives one chat at a time on WhatsApp")
			m.selected = map[string]bool{}
			return m, nil, true
		}
		mm, cmd := m.actKey(s)
		return mm, cmd, true
	}
	return m, nil, false
}

// ------------------------------------------------------------ moving

// placeAt is the place you're in: 0 Mail, 1 Chat, 2 WhatsApp, 3 AI requests.
func (m Model) placeAt() int {
	if m.inAISection() {
		return 3
	}
	return m.place()
}

// goPlace goes to Mail, Chat, WhatsApp or the AI requests.
func (m *Model) goPlace(i int) (tea.Model, tea.Cmd) {
	if i == 3 {
		if len(m.waiting) == 0 {
			m.say("no AI requests waiting")
			return *m, nil
		}
		m.goToAI(m.waiting[0].Kind)
		return *m, nil
	}
	m.visual, m.leader = false, false
	if i == m.place() && !m.inAISection() {
		return *m, nil
	}
	m.tab, m.wa = tabChat, i == 2
	if i == 0 {
		m.tab = tabMail
	}
	m.sec, m.sel, m.offset, m.filter, m.search, m.ai, m.focus = 0, 0, 0, "", nil, "", 0
	m.selected = map[string]bool{}
	m.reload()
	return *m, nil
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
		case strings.Contains(low, "+") && len(low) > 1, low == "enter", low == "tab", low == "esc", low == "backspace":
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
		"shift+tab": tea.KeyShiftTab, "up": tea.KeyUp, "down": tea.KeyDown}
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

// ------------------------------------------------------- : commands

// command is one : command: what you type, what it does, the keys it runs.
type command struct{ name, args, desc, keys string }

var commands = []command{
	{"archive", "", "Archive the thread or chat (dd)", "dd"},
	{"trash", "", "Move the thread to the trash (#)", "#"},
	{"read", "", "Mark it read (m)", "m"},
	{"unread", "", "Mark it unread (M)", "M"},
	{"star", "", "Star or unstar it (*)", "*"},
	{"label", "", "Add or remove a label (L)", "L"},
	{"later", "", "Move a scheduled send to another time (t)", "t"},
	{"mute", "", "Mute the WhatsApp chat: 8 hours, then longer (Z)", "Z"},
	{"rename", "", "Rename the chat (Space R)", "space R"},
	{"account", "", "Switch account (Space x)", "space x"},
	{"sync", "", "Sync now (Space r)", "space r"},
	{"mail", "", "Go to Mail (1)", "1"},
	{"chat", "", "Go to Google Chat (2)", "2"},
	{"whatsapp", "", "Go to WhatsApp (3)", "3"},
	{"ai", "", "Go to the AI requests (4)", "4"},
	{"new", "", "Write a new mail or message (c)", "c"},
	{"digest", "", "Today's digest by Claude (T)", "T"},
	{"keys", "", "Show every key (?)", "?"},
	{"quit", "", "Quit narada (Space q)", "space q"},
	{"q", "", "Quit narada", "space q"},
}

// commandKey edits the : line; Enter runs it.
func (m Model) commandKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "ctrl+c":
		m.mode, m.input = modeNormal, nil
		return m, nil
	case "tab":
		if c := matchCommands(string(m.input)); len(c) > 0 {
			m.input = []rune(c[0].name)
		}
		return m, nil
	case "up":
		if n := len(m.cmdHist); n > 0 {
			m.input = []rune(m.cmdHist[n-1])
		}
		return m, nil
	case "enter":
		line := strings.TrimSpace(string(m.input))
		m.mode, m.input = modeNormal, nil
		if line == "" {
			return m, nil
		}
		m.cmdHist = append(m.cmdHist, line)
		name := strings.Fields(line)[0]
		for _, c := range commands {
			if c.name == name {
				return m.runKeys(c.keys)
			}
		}
		if c := matchCommands(name); len(c) == 1 {
			return m.runKeys(c[0].keys)
		}
		m.say(":" + name + " isn't a command · Tab completes")
		return m, nil
	}
	return m.inputKey(k)
}

func matchCommands(prefix string) []command {
	var out []command
	for _, c := range commands {
		if strings.HasPrefix(c.name, strings.TrimSpace(prefix)) {
			out = append(out, c)
		}
	}
	return out
}

// commandView is the : line, with what Tab would complete to.
func (m Model) commandView() string {
	s := m.styles
	line := s.sky.Bold(true).Render(":") + s.text.Render(string(m.input)) + lipgloss.NewStyle().Reverse(true).Render(" ")
	if c := matchCommands(string(m.input)); len(c) > 0 && len(m.input) > 0 {
		var names []string
		for _, x := range c[:min(5, len(c))] {
			names = append(names, x.name)
		}
		line += s.muted.Render("   " + strings.Join(names, " · "))
		if len(c) == 1 {
			line += s.muted.Render("  — " + c[0].desc)
		}
	}
	return " " + ansi.Truncate(line, m.w-2, "…")
}
