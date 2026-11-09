package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/cli"
	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/vyakarana/keymap"
)

// The command bar (:), plan §5: a rounded box in the middle of the screen
// with the line you type, the rest of the best match in grey (ghost text)
// and up to eight suggestions, fuzzy on command names and their aliases;
// after a command's name, its arguments complete (labels, accounts,
// sections, chats, when, mute times, options, keys). Every action of the
// keymap is a command by its name (:go.inbox); the short names below are
// what you'd type. A count (:3archive) or VISUAL's range (:'<,'>read)
// goes in front. History is kept in ~/.local/state/narada/commands.

// command is one : command: its name, the arguments it takes, what it
// does, the action it runs (an action of keymap.go) or its own run.
type command struct {
	name, args, desc, act string
	argList               func(m Model) []string
	run                   func(m Model, arg string, count int) (tea.Model, tea.Cmd)
}

// cmdItem is one suggestion under the line.
type cmdItem struct {
	name, args, desc, keys string
	arg                    bool // an argument, not a command
}

// cmdRange is what VISUAL's : starts with: the picked range.
const cmdRange = "'<,'>"

var shortCommands = []command{
	{name: "archive", act: "archive"},
	{name: "trash", act: "delete"},
	{name: "read", act: "read"},
	{name: "unread", act: "unread"},
	{name: "star", act: "star"},
	{name: "label", args: "<label>", act: "label", argList: labelNames, run: runLabel},
	{name: "reply", act: "reply"},
	{name: "replyall", act: "reply.all"},
	{name: "forward", act: "msg.forward"},
	{name: "react", args: "<emoji>", act: "msg.react", argList: func(Model) []string { return quickEmoji }},
	{name: "edit", act: "msg.edit"},
	{name: "pin", act: "pin"},
	{name: "mute", args: "8h 1w always off", act: "wa.mute", argList: func(Model) []string { return []string{"8h", "1w", "always", "off"} }, run: runMute},
	{name: "rename", act: "chat.rename"},
	{name: "block", act: "wa.block"},
	{name: "later", args: "<when>", act: "send.later", argList: func(Model) []string {
		return []string{"tomorrow 9am", "tomorrow 6pm", "mon 9am", "fri 6pm", "in 1h", "in 2h"}
	}, run: runLater},
	{name: "go", args: "<section>", desc: "Go to a section of this place (Unread, Inbox, Sent…)", argList: sectionNames, run: runGo},
	{name: "chat", args: "<chat>", desc: "Open a chat by its name", argList: chatNames, run: runChat},
	{name: "account", args: "<account>", act: "account", argList: func(m Model) []string { return append([]string{"all"}, m.accounts...) }, run: runAccount},
	{name: "sync", act: "sync"},
	{name: "older", act: "older"},
	{name: "summary", act: "ai.summary"},
	{name: "draft", act: "ai.write"},
	{name: "digest", act: "ai.digest"},
	{name: "ask", act: "ai.ask"},
	{name: "open", act: "open"},
	{name: "save", act: "files.save"},
	{name: "copy", act: "copy"},
	{name: "fold", act: "fold"},
	{name: "quoted", act: "quoted"},
	{name: "new", act: "new"},
	{name: "mail", act: "place.mail"},
	{name: "whatsapp", act: "place.wa"},
	{name: "gchat", act: "place.chat"},
	{name: "ai", act: "place.ai"},
	{name: "set", args: "<option>[=value]", desc: "Set an option for this session: scroll_lines=5, quoted, compact, zoom, preview, images (no= turns off)", argList: setNames, run: runSet},
	{name: "map", args: "<keys> <action>", desc: "Bind keys to an action for this session (config.toml's [keys.normal] keeps it): :map gh go.inbox", argList: actionNames, run: runMap},
	{name: "help", args: "[key or words]", desc: "Show every key (filtered by what you type)", run: runHelp},
	{name: "keys", act: "help"},
	{name: "news", desc: "Show what moved in this version (the keys that changed)", run: func(m Model, _ string, _ int) (tea.Model, tea.Cmd) {
		m.mode, m.newsTop = modeNews, 0
		return m, nil
	}},
	{name: "quit", act: "quit"},
	{name: "q", act: "quit"},
	{name: "qa", act: "quit"},
}

// quickEmoji are the reactions :react completes.
var quickEmoji = []string{"👍", "❤️", "😂", "😮", "😢", "🙏", "🎉", "🔥", "👀", "✅"}

// allCommands is the short names, then every action of the keymap by its
// name (a short name for the same action keeps its own entry).
func allCommands() []command {
	out := slices.Clone(shortCommands)
	seen := map[string]bool{}
	for _, c := range out {
		seen[c.name] = true
	}
	for _, b := range narKeys.Binds {
		if b.Act == "" || b.Moved != "" || b.Hide || seen[b.Act] {
			continue
		}
		seen[b.Act] = true
		out = append(out, command{name: b.Act, desc: b.Desc, act: b.Act})
	}
	return out
}

// actionBinding is the first binding of an action, for its keys and words.
func actionBinding(act string) (keymap.Binding, bool) {
	for _, b := range narKeys.Binds {
		if b.Act == act && b.Moved == "" {
			return b, true
		}
	}
	return keymap.Binding{}, false
}

func (c command) item() cmdItem {
	it := cmdItem{name: c.name, args: c.args, desc: c.desc}
	if b, ok := actionBinding(c.act); ok {
		if it.desc == "" {
			it.desc = b.Desc
		}
		it.keys = b.Keys()
	}
	return it
}

// fuzzyScore says how well q matches s: 0 not at all; a prefix beats a
// word start, which beats letters in order.
func fuzzyScore(s, q string) int {
	s, q = strings.ToLower(s), strings.ToLower(q)
	switch {
	case q == "":
		return 1
	case s == q:
		return 1000
	case strings.HasPrefix(s, q):
		return 500 - len(s)
	case strings.Contains(s, "."+q) || strings.Contains(s, " "+q):
		return 300 - len(s)
	case strings.Contains(s, q):
		return 200 - len(s)
	}
	i := 0
	for _, r := range s {
		if i < len(q) && r == rune(q[i]) {
			i++
		}
	}
	if i == len(q) {
		return 100 - len(s)
	}
	return 0
}

// parseCommandLine splits a : line into its range, count, command name and
// argument: "'<,'>read", "3archive", "label Work".
func parseCommandLine(line string) (ranged bool, count int, name, arg string) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, cmdRange) {
		ranged, line = true, strings.TrimSpace(line[len(cmdRange):])
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 {
		count, _ = strconv.Atoi(line[:i])
		line = line[i:]
	}
	name, arg, _ = strings.Cut(strings.TrimSpace(line), " ")
	return ranged, count, name, strings.TrimSpace(arg)
}

// findCommand is the command called name (exactly), else nil.
func findCommand(name string) *command {
	for _, c := range allCommands() {
		if c.name == name {
			return &c
		}
	}
	return nil
}

// cmdSuggestions is what shows under the line: commands matching the name
// typed so far, or, after a command and a space, its arguments.
func (m Model) cmdSuggestions() []cmdItem {
	_, _, name, arg := parseCommandLine(string(m.input))
	line := string(m.input)
	if strings.Contains(strings.TrimLeft(line, "'<,>0123456789"), " ") {
		c := findCommand(name)
		if c == nil || c.argList == nil {
			return nil
		}
		type scored struct {
			s   int
			arg string
		}
		var list []scored
		for _, a := range c.argList(m) {
			if sc := fuzzyScore(a, arg); sc > 0 {
				list = append(list, scored{sc, a})
			}
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].s > list[j].s })
		var out []cmdItem
		for _, x := range list[:min(8, len(list))] {
			out = append(out, cmdItem{name: x.arg, desc: c.name + " " + x.arg, arg: true})
		}
		return out
	}
	type scored struct {
		s  int
		it cmdItem
	}
	var list []scored
	for _, c := range allCommands() {
		it := c.item()
		sc := fuzzyScore(c.name, name)
		if name != "" && sc == 0 {
			// the words it's described with count too, a little
			if fuzzyScore(it.desc, name) >= 200 {
				sc = 50
			}
		}
		if sc > 0 {
			list = append(list, scored{sc, it})
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].s > list[j].s })
	var out []cmdItem
	for _, x := range list[:min(8, len(list))] {
		out = append(out, x.it)
	}
	return out
}

// cmdGhost is the rest of the best match, shown in grey after the cursor.
func (m Model) cmdGhost() string {
	line := string(m.input)
	if line == "" || m.back != 0 {
		return ""
	}
	sug := m.cmdSuggestions()
	if len(sug) == 0 {
		return ""
	}
	prefix, last := line, line
	if i := strings.LastIndex(line, " "); i >= 0 {
		prefix, last = line[:i+1], line[i+1:]
	} else {
		r, c, _, _ := parseCommandLine(line)
		lead := ""
		if r {
			lead = cmdRange
		}
		if c > 0 {
			lead += strconv.Itoa(c)
		}
		prefix, last = lead, strings.TrimPrefix(line, lead)
	}
	_ = prefix
	best := sug[0].name
	if strings.HasPrefix(strings.ToLower(best), strings.ToLower(last)) && len(best) > len(last) {
		return best[len(last):]
	}
	return ""
}

// ---------------------------------------------------------------- keys

// openCommand opens the bar; from VISUAL it starts with the range.
func (m *Model) openCommand() {
	m.loadCmdHist()
	m.mode, m.input, m.back, m.cmdAt, m.cmdSel, m.cmdQ = modeCommand, nil, 0, len(m.cmdHist), -1, ""
	if m.visual && len(m.selected) > 0 {
		m.input = []rune(cmdRange)
	}
}

// commandKey edits the : line; Enter runs it.
func (m Model) commandKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	sug := m.cmdSuggestions()
	switch s := k.String(); s {
	case "esc", "ctrl+c":
		m.mode, m.input, m.back, m.cmdSel = modeNormal, nil, 0, -1
		return m, nil
	case "tab", "ctrl+n", "ctrl+j":
		if len(sug) > 0 {
			m.cmdSel = (m.cmdSel + 1) % len(sug)
		}
		return m, nil
	case "shift+tab", "ctrl+p", "ctrl+k":
		if len(sug) > 0 {
			m.cmdSel = (m.cmdSel - 1 + len(sug) + 1) % len(sug)
			if m.cmdSel < 0 {
				m.cmdSel = len(sug) - 1
			}
		}
		return m, nil
	case "right", "ctrl+e":
		if g := m.cmdGhost(); g != "" {
			m.input = append(m.input, []rune(g)...)
			m.cmdSel = -1
			return m, nil
		}
		if s == "ctrl+e" {
			m.back = 0
			return m, nil
		}
	case "up", "down":
		// history: further back with ↑, forward again with ↓
		if n := len(m.cmdHist); n > 0 && (len(m.input) == 0 || m.cmdAt < n) {
			if s == "up" {
				m.cmdAt = max(0, min(n, m.cmdAt)-1)
			} else {
				m.cmdAt = min(n, m.cmdAt+1)
			}
			m.input, m.back, m.cmdSel = nil, 0, -1
			if m.cmdAt < n {
				m.input = []rune(m.cmdHist[m.cmdAt])
			}
		}
		return m, nil
	case "ctrl+r":
		// search the history for what's typed (again: further back)
		if m.cmdQ == "" {
			m.cmdQ = string(m.input)
		}
		for i := min(m.cmdAt, len(m.cmdHist)) - 1; i >= 0; i-- {
			if strings.Contains(m.cmdHist[i], m.cmdQ) {
				m.cmdAt, m.input, m.back = i, []rune(m.cmdHist[i]), 0
				return m, nil
			}
		}
		m.say("no older command has " + strconv.Quote(m.cmdQ))
		return m, nil
	case "enter":
		line := string(m.input)
		if m.cmdSel >= 0 && m.cmdSel < len(sug) {
			it := sug[m.cmdSel]
			if it.arg {
				if i := strings.LastIndex(line, " "); i >= 0 {
					line = line[:i+1] + it.name
				}
			} else {
				r, c, _, arg := parseCommandLine(line)
				lead := ""
				if r {
					lead = cmdRange
				}
				if c > 0 {
					lead += strconv.Itoa(c)
				}
				line = lead + it.name
				if arg != "" {
					line += " " + arg
				}
			}
		}
		m.mode, m.input, m.back, m.cmdSel, m.cmdQ = modeNormal, nil, 0, -1, ""
		return m.runCommandLine(line)
	}
	m.cmdSel, m.cmdQ = -1, ""
	return m.inputKey(k)
}

// runCommandLine runs one : line (also from tests and the finder).
func (m Model) runCommandLine(line string) (tea.Model, tea.Cmd) {
	line = strings.TrimSpace(line)
	if line == "" {
		return m, nil
	}
	m.saveCmdHist(line)
	ranged, count, name, arg := parseCommandLine(line)
	if !ranged && !m.visual {
		m.selected = map[string]bool{}
	}
	c := findCommand(name)
	if c == nil {
		if sug := m.cmdSuggestionsFor(name); len(sug) == 1 {
			c = findCommand(sug[0])
		}
	}
	if c == nil {
		m.say(":" + name + " isn't a command · Tab completes")
		return m, nil
	}
	m.lastCounted = count > 0
	var mm tea.Model
	var cmd tea.Cmd
	switch {
	case c.run != nil && (arg != "" || c.act == ""):
		mm, cmd = c.run(m, arg, max(1, count))
	default:
		mm, cmd = m.run(c.act, max(1, count))
	}
	if x, ok := mm.(Model); ok && ranged {
		x.visual = false
		mm = x
	}
	return mm, cmd
}

// cmdSuggestionsFor is the command names a typed name could be (prefix).
func (m Model) cmdSuggestionsFor(name string) []string {
	var out []string
	for _, c := range allCommands() {
		if strings.HasPrefix(c.name, name) {
			out = append(out, c.name)
		}
	}
	return out
}

// ------------------------------------------------------------ history

func (m Model) cmdHistFile() string {
	if m.a == nil || m.a.Paths.State == "" {
		return ""
	}
	return filepath.Join(m.a.Paths.State, "commands")
}

func (m *Model) loadCmdHist() {
	if m.cmdHistLoaded {
		return
	}
	m.cmdHistLoaded = true
	if f := m.cmdHistFile(); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			for l := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					m.cmdHist = append(m.cmdHist, l)
				}
			}
		}
	}
}

// saveCmdHist keeps line as the newest (once), the last 200, on disk too.
func (m *Model) saveCmdHist(line string) {
	m.loadCmdHist()
	m.cmdHist = append(slices.DeleteFunc(m.cmdHist, func(s string) bool { return s == line }), line)
	if len(m.cmdHist) > 200 {
		m.cmdHist = m.cmdHist[len(m.cmdHist)-200:]
	}
	m.cmdAt = len(m.cmdHist)
	if f := m.cmdHistFile(); f != "" {
		_ = os.MkdirAll(filepath.Dir(f), 0o700)
		_ = os.WriteFile(f, []byte(strings.Join(m.cmdHist, "\n")+"\n"), 0o600)
	}
}

// ------------------------------------------------------------ the view

// commandPopup is the bar, a third of the way down the screen.
func (m Model) commandPopup() []string {
	s := m.styles
	w := min(m.w-4, max(50, m.w*3/5))
	inner := w - 2
	border := s.sky
	cur := m.cursor()
	in := string(m.input)
	under, rest := " ", ""
	if cur < len(m.input) {
		under, rest = string(m.input[cur]), string(m.input[cur+1:])
	}
	ghost := m.cmdGhost()
	line := s.sky.Bold(true).Render(":") + s.text.Render(string([]rune(in)[:cur])) + lipgloss.NewStyle().Reverse(true).Render(under) + s.text.Render(rest)
	if ghost != "" {
		line += s.muted.Faint(true).Render(ghost)
	}
	body := []string{line}
	sug := m.cmdSuggestions()
	for i, it := range sug {
		row := s.text.Render(it.name)
		if it.args != "" {
			row += " " + s.muted.Render(it.args)
		}
		if !it.arg {
			row = fit(row, 26) + " " + s.muted.Render(it.desc)
			if it.keys != "" {
				row = fit(row, inner-20) + " " + s.round(s.keycap, it.keys)
			}
		}
		if i == m.cmdSel {
			row = s.sky.Bold(true).Render("▶ ") + row
		} else {
			row = "  " + row
		}
		body = append(body, row)
	}
	if len(sug) == 0 && strings.TrimSpace(in) != "" {
		body = append(body, s.muted.Render("  no command matches · Enter tries it anyway"))
	}
	body = append(body, s.muted.Render("  Tab next · → takes the grey · ↑ history · Ctrl+r searches it · Enter runs · Esc closes"))
	label := " : "
	out := []string{border.Render("╭─") + border.Bold(true).Render(label) + border.Render(strings.Repeat("─", max(0, inner-1-lipgloss.Width(label)))+"╮")}
	for _, l := range body {
		out = append(out, border.Render("│")+" "+ansi.Truncate(fit(l, inner-2), inner-2, "…")+" "+border.Render("│"))
	}
	return append(out, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
}

// --------------------------------------------------------- arguments

func labelNames(m Model) []string {
	ts := m.targets()
	acc := ""
	if len(ts) > 0 {
		acc = ts[0].Account
	} else if len(m.accounts) > 0 {
		acc = m.accounts[0]
	}
	if m.a == nil || acc == "" {
		return nil
	}
	labels, _ := m.a.Store.Labels(acc)
	var out []string
	for _, l := range labels {
		if l.Type == "user" || l.ID == "IMPORTANT" {
			out = append(out, l.Name)
		}
	}
	return out
}

func sectionNames(m Model) []string {
	if m.tab == tabMail {
		var out []string
		for _, s := range m.sections {
			out = append(out, s.Name)
		}
		return out
	}
	return slices.Clone(chatSections)
}

func chatNames(m Model) []string {
	var out []string
	for _, sp := range m.spaces {
		out = append(out, cli.SpaceTitle(sp))
	}
	return out
}

func actionNames(m Model) []string {
	var out []string
	seen := map[string]bool{}
	for _, b := range narKeys.Binds {
		if b.Act != "" && !seen[b.Act] {
			seen[b.Act] = true
			out = append(out, b.Act)
		}
	}
	return out
}

func runLabel(m Model, arg string, _ int) (tea.Model, tea.Cmd) {
	ts := m.targets()
	if m.tab != tabMail || len(ts) == 0 {
		m.say(":label works on a mail thread")
		return m, nil
	}
	labels, _ := m.a.Store.Labels(ts[0].Account)
	i := slices.IndexFunc(labels, func(l store.Label) bool { return strings.EqualFold(l.Name, arg) })
	if i < 0 {
		m.say("no label " + strconv.Quote(arg) + " in this account")
		return m, nil
	}
	id := labels[i].ID
	on := !ts[0].Has(id)
	a := m.a
	return m, m.act(map[bool]string{true: "label added", false: "label removed"}[on], func(ctx context.Context) error {
		for _, t := range ts {
			var err error
			if on {
				err = a.Modify(ctx, t.Account, t.ID, []string{id}, nil)
			} else {
				err = a.Modify(ctx, t.Account, t.ID, nil, []string{id})
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func runMute(m Model, arg string, _ int) (tea.Model, tea.Cmd) {
	sp, ok := m.curSpace()
	if !ok || !m.wa {
		m.say(":mute works on a WhatsApp chat")
		return m, nil
	}
	a, name := m.a, sp.Name
	switch arg {
	case "8h":
		return m, m.act("muted for 8 hours", func(ctx context.Context) error { return a.MuteWhatsApp(ctx, name, 8*time.Hour, false) })
	case "1w":
		return m, m.act("muted for a week", func(ctx context.Context) error { return a.MuteWhatsApp(ctx, name, 7*24*time.Hour, false) })
	case "always":
		return m, m.act("muted always", func(ctx context.Context) error { return a.MuteWhatsApp(ctx, name, 0, false) })
	case "off":
		return m, m.act("unmuted", func(ctx context.Context) error { return a.MuteWhatsApp(ctx, name, 0, true) })
	}
	m.say(":mute takes 8h, 1w, always or off")
	return m, nil
}

func runLater(m Model, arg string, count int) (tea.Model, tea.Cmd) {
	mm, cmd := m.run("send.later", count)
	if x, ok := mm.(Model); ok && x.when != nil {
		x.when.input.SetValue(arg)
		x.when.input.CursorEnd()
		return x, cmd
	}
	return mm, cmd
}

func runGo(m Model, arg string, _ int) (tea.Model, tea.Cmd) {
	best, name := 0, ""
	for _, n := range sectionNames(m) {
		if sc := fuzzyScore(n, arg); sc > best {
			best, name = sc, n
		}
	}
	if name != "" {
		return m.goSectionNamed(name)
	}
	m.say("no section " + strconv.Quote(arg) + " here")
	return m, nil
}

func runChat(m Model, arg string, _ int) (tea.Model, tea.Cmd) {
	if m.tab != tabChat {
		m.say(":chat opens a chat: go to Chat or WhatsApp first (H / L)")
		return m, nil
	}
	best, bi := 0, -1
	for i, sp := range m.spaces {
		if sc := fuzzyScore(cli.SpaceTitle(sp), arg); sc > best {
			best, bi = sc, i
		}
	}
	if bi < 0 {
		m.say("no chat " + strconv.Quote(arg))
		return m, nil
	}
	m.recordJump()
	m.sel = bi
	m.loadPreview(true)
	m.fixView()
	return m.actKey("enter")
}

func runAccount(m Model, arg string, _ int) (tea.Model, tea.Cmd) {
	if arg == "all" {
		m.acct = -1
	} else if i := slices.Index(m.accounts, arg); i >= 0 {
		m.acct = i
	} else {
		m.say("no account " + strconv.Quote(arg))
		return m, nil
	}
	m.sel, m.offset = 0, 0
	m.reload()
	m.say("account " + arg)
	return m, nil
}

func runHelp(m Model, arg string, _ int) (tea.Model, tea.Cmd) {
	mm, cmd := m.run("help", 1)
	if x, ok := mm.(Model); ok && x.mode == modeHelp {
		x.helpQ, x.helpTop = arg, 0
		return x, cmd
	}
	return mm, cmd
}

// ------------------------------------------------------- :set and :map

// options :set knows; a bool flips with :set name, :set noname turns off.
var setOptions = []string{"scroll_lines", "quoted", "folded", "compact", "zoom", "preview", "images"}

func setNames(Model) []string {
	out := slices.Clone(setOptions)
	for _, o := range setOptions[1:] {
		out = append(out, "no"+o)
	}
	return append(out, "scroll_lines=1", "scroll_lines=3", "scroll_lines=5")
}

func runSet(m Model, arg string, _ int) (tea.Model, tea.Cmd) {
	name, val, hasVal := strings.Cut(arg, "=")
	name = strings.TrimSpace(name)
	on := true
	if strings.HasPrefix(name, "no") && slices.Contains(setOptions, name[2:]) {
		name, on = name[2:], false
	}
	if hasVal {
		on = val == "1" || val == "on" || val == "true" || val == "yes"
	}
	switch name {
	case "scroll_lines":
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if !hasVal || err != nil || n < 1 || n > 100 {
			m.say(fmt.Sprintf("scroll_lines=%d (:set scroll_lines=5 changes it)", m.a.Config.ScrollLines))
			return m, nil
		}
		m.a.Config.ScrollLines = n
		m.say(fmt.Sprintf("Ctrl+j / Ctrl+k scroll %d lines", n))
		return m, nil
	case "quoted":
		m.showQuoted = on
		m.loadPreview(false)
	case "folded":
		m.expandAll = !on
	case "compact":
		m.compact = on
	case "zoom":
		m.zoom = on
	case "preview":
		m.noPreview = !on
	case "images":
		if on {
			m.a.Config.Images = "auto"
		} else {
			m.a.Config.Images = "off"
		}
		m.loadPreview(false)
	default:
		m.say(":set knows " + strings.Join(setOptions, ", "))
		return m, nil
	}
	m.say(map[bool]string{true: name + " on", false: name + " off"}[on])
	return m, nil
}

// vimKeys turns Vim's notation (gh, <C-j>, <Space>mr) or narada's
// ("g h", "Ctrl+j") into the keymap's spelling.
func vimKeys(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(s, " ") || (strings.Contains(s, "+") && len(s) > 1 && !strings.HasPrefix(s, "<")) {
		return s
	}
	var toks []string
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '<' {
			if j := strings.IndexRune(string(rs[i:]), '>'); j > 1 {
				name := string(rs[i+1 : i+j])
				i += j
				low := strings.ToLower(name)
				switch {
				case low == "space" || low == "leader":
					toks = append(toks, "Space")
				case low == "cr" || low == "enter":
					toks = append(toks, "Enter")
				case low == "esc":
					toks = append(toks, "Esc")
				case low == "tab":
					toks = append(toks, "Tab")
				case low == "bs":
					toks = append(toks, "Backspace")
				case strings.HasPrefix(low, "c-"):
					toks = append(toks, "Ctrl+"+name[2:])
				case strings.HasPrefix(low, "a-"), strings.HasPrefix(low, "m-"):
					toks = append(toks, "Alt+"+name[2:])
				default:
					toks = append(toks, name)
				}
				continue
			}
		}
		if unicode.IsSpace(rs[i]) {
			continue
		}
		toks = append(toks, string(rs[i]))
	}
	return strings.Join(toks, " ")
}

// mapKeys binds seq to act for this session (it wins over the default).
func mapKeys(seq, act string) error {
	if _, ok := actionBinding(act); !ok {
		return fmt.Errorf("no action %q (Tab after :map gh lists them)", act)
	}
	if seq == "" {
		return fmt.Errorf("no keys")
	}
	b, _ := actionBinding(act)
	nb := keymap.Binding{Seq: seq, Act: act, Desc: b.Desc, Where: b.Where, Count: b.Count, Hide: true}
	narKeys.Binds = append([]keymap.Binding{nb}, narKeys.Binds...)
	return nil
}

func runMap(m Model, arg string, _ int) (tea.Model, tea.Cmd) {
	f := strings.Fields(arg)
	if len(f) < 2 {
		m.say(":map takes keys and an action: :map gh go.inbox")
		return m, nil
	}
	seq := vimKeys(strings.Join(f[:len(f)-1], " "))
	if err := mapKeys(seq, f[len(f)-1]); err != nil {
		m.say(err.Error())
		return m, nil
	}
	m.say(seq + " → " + f[len(f)-1] + " (this session; config.toml's [keys.normal] keeps it)")
	return m, nil
}

// applyConfigKeys binds config.toml's [keys.normal] (once per run).
func applyConfigKeys(keys map[string]string) []string {
	var bad []string
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if err := mapKeys(vimKeys(k), keys[k]); err != nil {
			bad = append(bad, k+": "+err.Error())
		}
	}
	return bad
}
