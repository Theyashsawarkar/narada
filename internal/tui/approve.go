package tui

import (
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/cli"
	"github.com/Theyashsawarkar/narada/internal/render"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// Approve is `narada approve` (and the popup an agent's send opens): one
// card per waiting send, exactly who gets what, answered with a key.
func Approve(a *app.App, items []store.OutboxItem) error {
	m := approver{a: a, items: items, styles: newStyles(a.Config.Colors, a.Config.NerdFont), diff: true, shown: time.Now()}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type approver struct {
	a        *app.App
	styles   styles
	items    []store.OutboxItem
	i        int
	out      *store.Outgoing // edited, not saved yet
	scroll   int
	facts    int // the details pane's scroll, when it holds more than fits
	diff     bool
	shown    time.Time // when the current one appeared
	w, h     int
	done     string // what happened to the current one, shown a moment
	doneInfo string
	err      string
	path     string // the file the editor has open
	inline   bool   // drawn inside the main screen, whose keys answer it
}

type approveNext struct{}

// armAfter: a popup can open while you're typing elsewhere, so what you were
// typing can't answer it.
const armAfter = 400 * time.Millisecond

func (m approver) Init() tea.Cmd { return nil }

func (m approver) cur() store.OutboxItem { return m.items[m.i] }

func (m approver) message() store.Outgoing {
	if m.out != nil {
		return *m.out
	}
	return m.cur().Out
}

type editedApproval struct{ err error }

func (m approver) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case approveNext:
		m.done, m.out, m.scroll, m.facts, m.err = "", nil, 0, 0, ""
		m.diff, m.shown = true, time.Now()
		m.i++
		if m.i >= len(m.items) {
			return m, tea.Quit
		}
	case editedApproval:
		b, err := os.ReadFile(m.path)
		os.Remove(m.path)
		if msg.err != nil || err != nil {
			m.err = "the editor failed"
			return m, nil
		}
		it := m.cur()
		base := m.message()
		if chatKind(it.Kind) {
			if text, ok := cli.ParseChatTemplate(string(b)); ok {
				base.Body = text
				m.out = &base
			}
		} else if out, ok := cli.ParseMailTemplate(string(b), base); ok {
			if out.Body != base.Body {
				out.HTML = "" // the HTML still says what the edit changed
			}
			m.out = &out
		}
	case tea.KeyMsg:
		if m.done != "" {
			return m, nil
		}
		it := m.cur()
		switch msg.String() {
		case "y", "Y", "n", "N":
			if time.Since(m.shown) < armAfter {
				return m, nil
			}
		}
		m.err = ""
		switch msg.String() {
		case "y", "Y":
			if err := m.a.Approve(it.ID, m.out); err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.done, m.doneInfo = "✓ Approved", "It goes out now."
			if wait := int(m.a.UndoWindow(it.Kind).Seconds()); wait > 0 {
				m.doneInfo = fmt.Sprintf("It goes in %ds. narada outbox cancel %d stops it.", wait, it.ID)
			}
			return m, tea.Tick(900*time.Millisecond, func(time.Time) tea.Msg { return approveNext{} })
		case "n", "N":
			if err := m.a.Decline(it.ID); err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.done, m.doneInfo = "✗ Declined", "Nothing was sent. The agent has been told."
			return m, tea.Tick(700*time.Millisecond, func(time.Time) tea.Msg { return approveNext{} })
		case "e", "E":
			out := m.message()
			var text, ext string
			if chatKind(it.Kind) {
				text, ext = cli.ChatTemplate(firstNonEmpty(out.SpaceName, out.Space), out.Body), "txt"
			} else {
				text, ext = cli.MailTemplate(out, ""), "eml"
			}
			path, err := cli.TempFile(m.a, ext, text)
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.path = path
			return m, tea.ExecProcess(cli.EditorCmd(m.a, path), func(err error) tea.Msg { return editedApproval{err} })
		case "s", "tab":
			// skip: leave it waiting, look at the next
			return m, func() tea.Msg { return approveNext{} }
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "u", "U":
			m.out, m.scroll = nil, 0
		case "d", "D":
			m.diff = !m.diff
		case "j", "down":
			m.scroll++
		case "k", "up":
			m.scroll = max(0, m.scroll-1)
		case "J":
			m.facts++
		case "K":
			m.facts = max(0, m.facts-1)
		}
	}
	return m, nil
}

// kindLook: the name, icon and colour of a kind of send.
func (s styles) kindLook(kind string) (name, icon, color string) {
	switch kind {
	case "whatsapp":
		return "WhatsApp", "󰖣", s.c.Green
	case "chat":
		return "Google Chat", "󰭹", s.c.Accent2
	}
	return "Mail", "󰇮", s.c.Accent
}

var (
	reTag = regexp.MustCompile(`(?:^|[^\w@])@(\d{7,15})\b`)
	// addresses anyone can get: everyone else on them isn't "outside".
	publicMail = map[string]bool{"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true,
		"yahoo.com": true, "icloud.com": true, "proton.me": true, "protonmail.com": true}
)

func domainOf(addr string) string {
	if a, err := mail.ParseAddress(addr); err == nil {
		addr = a.Address
	}
	_, d, _ := strings.Cut(strings.ToLower(strings.TrimSpace(addr)), "@")
	return d
}

// outside: the recipients not at your account's own company. None when
// the account is a personal address: there's no company to be outside of.
func outside(account string, out store.Outgoing) []string {
	home := domainOf(account)
	if home == "" || publicMail[home] {
		return nil
	}
	var far []string
	for _, r := range slices.Concat(out.To, out.Cc, out.Bcc) {
		if d := domainOf(r); d != "" && d != home {
			far = append(far, r)
		}
	}
	return far
}

func count(n int, one string) string {
	switch {
	case n == 1:
		return "1 " + one
	case one == "person":
		return fmt.Sprintf("%d people", n)
	}
	return fmt.Sprintf("%d %ss", n, one)
}

// person: a recipient as name and address, the address dimmed.
func (s styles) person(r string, st lipgloss.Style) string {
	if a, err := mail.ParseAddress(r); err == nil && a.Name != "" {
		return st.Render(a.Name) + " " + s.muted.Render(a.Address)
	}
	return st.Render(r)
}

// lineDiff marks an edit: lines only in a with "-", only in b with "+",
// the rest with " ". Longest common subsequence, by lines.
func lineDiff(a, b []string) (out []string) {
	if len(a)*len(b) > 4_000_000 {
		for _, l := range b {
			out = append(out, " "+l)
		}
		return out
	}
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			out = append(out, " "+a[i])
			i, j = i+1, j+1
		case j < len(b) && (i == len(a) || lcs[i][j+1] >= lcs[i+1][j]):
			out = append(out, "+"+b[j])
			j++
		default:
			out = append(out, "-"+a[i])
			i++
		}
	}
	return out
}

func splitLines(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

// wrapped wraps text to w columns, keeping ANSI styling.
func wrapped(s string, w int) []string { return strings.Split(ansi.Wrap(s, max(1, w), ""), "\n") }

// message: the frame's lines. The HTML as it will show when there is one
// (tables and all), else the text; an edit marked line by line.
func (m approver) messageLines(w int) []string {
	s := m.styles
	it, out := m.cur(), m.message()
	if m.out != nil && m.diff && out.Body != it.Out.Body {
		var lines []string
		for _, l := range lineDiff(splitLines(it.Out.Body), splitLines(out.Body)) {
			st := s.text
			switch l[0] {
			case '+':
				st = s.green
			case '-':
				st = s.red.Strikethrough(true)
			}
			for _, part := range wrapped(l[1:], w-2) {
				lines = append(lines, st.Render(string(l[0])+" "+part))
			}
		}
		return lines
	}
	if out.HTML != "" {
		return render.HTML(out.HTML, w, renderStylesOf(s), true).Lines
	}
	var lines []string
	for _, l := range splitLines(out.Body) {
		lines = append(lines, wrapped(s.text.Render(l), w)...)
	}
	return lines
}

// chip is a small filled label; warn is for what you must not miss.
func (s styles) chip(text, bg string) string {
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(s.c.Text)).Background(lipgloss.Color(s.c.Surface))
	if bg != "" {
		st = lipgloss.NewStyle().Foreground(lipgloss.Color(s.c.Base)).Background(lipgloss.Color(bg)).Bold(true)
	}
	return s.round(st.Padding(0, 1), text)
}

// flow lays chips out in rows of at most w columns.
func flow(chips []string, w int) []string {
	var rows []string
	cur := ""
	for _, c := range chips {
		if cur != "" && lipgloss.Width(cur)+1+lipgloss.Width(c) > w {
			rows, cur = append(rows, cur), ""
		}
		if cur != "" {
			cur += " "
		}
		cur += c
	}
	if cur != "" {
		rows = append(rows, cur)
	}
	return rows
}

// box frames lines with a title on the top edge, title and note muted.
func box(lines []string, title, note string, w int, border, label lipgloss.Style) []string {
	inner := max(1, w-2)
	top := "╭─"
	head := label.Render(" " + title + " ")
	tail := ""
	if note != "" {
		tail = label.Render(" "+note+" ") + border.Render("─")
	}
	fill := max(0, inner-1-lipgloss.Width(head)-lipgloss.Width(tail))
	out := []string{border.Render(top) + head + border.Render(strings.Repeat("─", fill)) + tail + border.Render("╮")}
	for _, l := range lines {
		l = ansi.Truncate(l, max(1, inner-2), "…")
		out = append(out, border.Render("│ ")+l+strings.Repeat(" ", max(0, inner-2-lipgloss.Width(l)))+border.Render(" │"))
	}
	return append(out, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
}

// answer is an action: its key, then what it does, spelled out.
func (s styles) answer(key, what, bg string) string {
	if bg != "" {
		return s.round(lipgloss.NewStyle().Foreground(lipgloss.Color(s.c.Base)).Background(lipgloss.Color(bg)).Bold(true).Padding(0, 1), key+"  "+what)
	}
	return s.keycap.Render(key) + " " + s.sub.Render(what)
}

func (m approver) View() string {
	if m.w == 0 || m.h == 0 || m.i >= len(m.items) {
		return ""
	}
	s := m.styles
	it, out := m.cur(), m.message()
	account := it.Account
	if accs, err := m.a.Store.Accounts(); err == nil {
		for _, a := range accs {
			if a.Name == it.Account {
				account = a.Email
			}
		}
	}
	name, icon, color := s.kindLook(it.Kind)
	if !s.nerd {
		icon = ""
	}
	chat := chatKind(it.Kind)
	far := outside(account, out)
	tags := reTag.FindAllStringSubmatch(out.Body, -1)
	group := strings.HasSuffix(out.Space, "@g.us")

	// The border says how far this reaches: past your company, or to many.
	tone := s.c.Accent
	if len(far) > 0 || it.Kind == "whatsapp" {
		tone = s.c.Peach
	}
	toneSt := lipgloss.NewStyle().Foreground(lipgloss.Color(tone))
	w := max(30, m.w-2) // inside the border
	cw := max(26, w-4)  // inside its padding
	two := cw >= 90     // details and message side by side

	if m.done != "" {
		st := s.green
		if strings.HasPrefix(m.done, "✗") {
			st = s.red
		}
		card := []string{st.Bold(true).Render(m.done), s.sub.Render(ansi.Truncate(m.doneInfo, cw, "…"))}
		if len(m.items) > m.i+1 {
			card = append(card, s.muted.Render(fmt.Sprintf("next: %d of %d", m.i+2, len(m.items))))
		}
		return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, strings.Join(card, "\n"))
	}

	// ---- top: what kind, then who wants what in one sentence
	pill := s.round(s.pill.Background(lipgloss.Color(color)), strings.TrimSpace(icon+" "+name))
	top := pill + s.muted.Render(fmt.Sprintf("  %d of %d", m.i+1, len(m.items)))
	clock := "waits for you · nothing sends until you answer"
	if lipgloss.Width(top)+len(clock)+2 <= cw {
		top += strings.Repeat(" ", cw-lipgloss.Width(top)-lipgloss.Width(clock)) + s.muted.Render(clock)
	}
	who := firstNonEmpty(out.Agent, "An agent")
	var verb, target string
	switch {
	case group:
		verb, target = " wants to message the group ", firstNonEmpty(out.SpaceName, out.Space)
	case it.Kind == "whatsapp":
		verb, target = " wants to message ", firstNonEmpty(out.SpaceName, out.Space)
	case it.Kind == "chat":
		verb, target = " wants to post in ", firstNonEmpty(out.SpaceName, out.Space)
	default:
		verb = " wants to email "
		first := ""
		if len(out.To) > 0 {
			first = out.To[0]
			if a, err := mail.ParseAddress(first); err == nil && a.Name != "" {
				first = a.Name
			}
		}
		target = first
		if others := len(out.To) + len(out.Cc) + len(out.Bcc) - 1; others > 0 {
			target += " and " + count(others, "other")
		}
	}
	header := []string{top, ""}
	header = append(header, wrapped(s.bold.Render(who+verb)+toneSt.Bold(true).Render(target), cw)...)
	if out.Why != "" {
		header = append(header, wrapped(s.muted.Render("You asked: ")+s.sub.Render("“"+out.Why+"”"), cw)...)
	}

	// ---- what's at stake
	var chips []string
	if m.out != nil {
		chips = append(chips, s.chip("edited by you: y sends your version", s.c.Yellow))
	}
	if n := len(far); n > 0 {
		chips = append(chips, s.chip(count(n, "person")+" outside your company", s.c.Peach))
	}
	if group {
		chips = append(chips, s.chip("a group: everyone in it sees this", s.c.Peach))
	}
	if n := len(tags); n > 0 {
		chips = append(chips, s.chip("tags "+count(n, "person"), s.c.Peach))
	}
	if n := len(out.Bcc); n > 0 {
		chips = append(chips, s.chip(fmt.Sprintf("%d in Bcc", n), ""))
	}
	if n := len(out.Files); n > 0 {
		chips = append(chips, s.chip(count(n, "attachment"), ""))
	}
	if out.ThreadID != "" || out.Thread != "" {
		chips = append(chips, s.chip("a reply in a thread", ""))
	}
	if out.ScheduledFor > 0 {
		chips = append(chips, s.chip("goes "+time.UnixMilli(out.ScheduledFor).Format("Mon 02 Jan 15:04"), ""))
	}
	if it.Reason != "" {
		chips = append(chips, s.chip(it.Reason, ""))
	}
	if rows := flow(chips, cw); len(rows) > 0 {
		header = append(header, "")
		header = append(header, rows...)
	}
	header = append(header, "")

	// ---- the details
	factW := cw
	if two {
		factW = min(46, cw/3+4)
	}
	var facts []string
	field := func(k string, vals ...string) {
		for i, v := range vals {
			label := ""
			if i == 0 {
				label = k
			}
			for j, part := range wrapped(v, factW-9) {
				if j > 0 {
					label = ""
				}
				facts = append(facts, s.muted.Render(fmt.Sprintf("%-9s", label))+part)
			}
		}
	}
	people := func(rs []string) []string {
		var v []string
		for _, r := range rs {
			st := s.text
			if slices.Contains(far, r) {
				st = s.peach
			}
			v = append(v, s.person(r, st))
		}
		return v
	}
	if chat {
		field("To", s.text.Render(firstNonEmpty(out.SpaceName, out.Space)))
		if out.Thread != "" {
			field("Thread", s.text.Render("replying in the thread"))
		}
		if out.Quote != "" {
			field("Replying", s.text.Render("to a message (quoted)"))
		}
		if len(tags) > 0 {
			var v []string
			for _, t := range tags {
				v = append(v, s.peach.Render("@"+t[1]))
			}
			field("Tags", v...)
		}
		field("As", s.text.Render(firstNonEmpty(it.Account, "your account")))
	} else {
		field("From", s.person(account, s.text))
		field("To", people(out.To)...)
		if len(out.Cc) > 0 {
			field("Cc", people(out.Cc)...)
		}
		if len(out.Bcc) > 0 {
			field("Bcc", people(out.Bcc)...)
		}
		field("Subject", s.text.Render(firstNonEmpty(out.Subject, "(no subject)")))
	}
	for i, f := range out.Files {
		n := firstNonEmpty(f.Name, filepath.Base(f.Source))
		k := ""
		if i == 0 {
			k = "Files"
		}
		field(k, s.text.Render(n)+" "+s.muted.Render(app.HumanSize(f.Size)))
	}
	if m.out != nil && out.Body != it.Out.Body {
		add, del := 0, 0
		for _, l := range lineDiff(splitLines(it.Out.Body), splitLines(out.Body)) {
			switch l[0] {
			case '+':
				add++
			case '-':
				del++
			}
		}
		field("Edited", s.green.Render(fmt.Sprintf("+%d", add))+" "+s.red.Render(fmt.Sprintf("-%d", del)))
	}
	if m.i+1 < len(m.items) {
		nx := m.items[m.i+1]
		nm, _, _ := s.kindLook(nx.Kind)
		field("Next", s.muted.Render(fmt.Sprintf("%d of %d: ", m.i+2, len(m.items))+nm))
	}

	// ---- the actions, pinned to the bottom
	send := "Send"
	if m.out != nil {
		send = "Send my version"
	}
	if u := int(m.a.UndoWindow(it.Kind).Seconds()); u > 0 && out.ScheduledFor == 0 && two {
		send += fmt.Sprintf(" (%ds to undo)", u)
	}
	acts := []string{s.answer("y", send, s.c.Green), s.answer("e", "Edit", "")}
	if m.out != nil {
		acts = append(acts, s.answer("u", "Undo my edit", ""))
		if out.Body != it.Out.Body {
			acts = append(acts, s.answer("d", "Changes", ""))
		}
	}
	acts = append(acts, s.answer("n", "Decline", s.c.Red))
	if len(m.items) > 1 && !m.inline {
		acts = append(acts, s.answer("s", "Skip", ""))
	}
	if !m.inline {
		acts = append(acts, s.answer("q", "Later", ""))
	}
	actRows := flow(acts, cw)
	foot := []string{}
	if m.err != "" {
		foot = append(foot, wrapped(s.red.Render("! "+m.err), cw)...)
	}
	foot = append(foot, actRows...)

	// ---- the middle takes what's left
	room := max(3, m.h-2-len(header)-len(foot)-1)
	mid := m.middle(facts, room, cw, factW, two)

	lines := append(append(append([]string{}, header...), mid...), "")
	lines = append(lines, foot...)
	// Too small for all of it: the actions and the message win over the top.
	if extra := len(lines) - max(1, m.h-2); extra > 0 {
		lines = lines[min(extra, len(lines)-1):]
	}
	for len(lines) < m.h-2 {
		lines = append(lines[:len(lines)-len(foot)], append([]string{""}, lines[len(lines)-len(foot):]...)...)
	}
	padded := make([]string, len(lines))
	for i, l := range lines {
		padded[i] = " " + l
	}
	return strings.Join(box(padded, "narada · approve a send", "", m.w, toneSt, toneSt), "\n")
}

// middle lays the details beside the message, or above it when narrow. The
// message scrolls with j/k; details that don't fit scroll with J/K, and say so.
func (m approver) middle(facts []string, room, cw, factW int, two bool) []string {
	s := m.styles
	frameW := cw
	frameRoom := room
	if two {
		frameW = cw - factW - 2
	} else {
		frameRoom = max(3, room-min(len(facts), room/2)-1)
	}
	factRoom := room
	if !two {
		factRoom = max(1, room-frameRoom-1)
	}
	if len(facts) > factRoom {
		first := min(m.facts, len(facts)-factRoom+1)
		shown := append([]string{}, facts[first:first+factRoom-1]...)
		facts = append(shown, s.yellow.Render(fmt.Sprintf("… %d more (J/K)", len(facts)-len(shown)-first)))
	}
	body := m.messageLines(max(1, frameW-4))
	inside := max(1, frameRoom-2)
	scroll := min(m.scroll, max(0, len(body)-inside))
	shown := body[scroll:min(len(body), scroll+inside)]
	note := ""
	if len(body) > inside {
		note = fmt.Sprintf("%d–%d of %d · j/k", scroll+1, scroll+len(shown), len(body))
	}
	border := lipgloss.NewStyle().Foreground(lipgloss.Color(s.c.Border))
	for len(shown) < inside {
		shown = append(shown, "")
	}
	title := "the message, exactly"
	if m.out != nil {
		title = "the message, with your edit"
	}
	frame := box(shown, title, note, frameW, border, s.muted)
	if !two {
		return append(append(append([]string{}, facts...), ""), frame...)
	}
	var out []string
	for i := range frame {
		l := ""
		if i < len(facts) {
			l = facts[i]
		}
		out = append(out, l+strings.Repeat(" ", max(0, factW-lipgloss.Width(l)))+"  "+frame[i])
	}
	return out
}
