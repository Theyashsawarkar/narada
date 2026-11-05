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
			what := sendWhat(it.Kind, m.message())
			m.done, m.doneInfo = "✓ Approved", upFirst(what)+" goes out now."
			if when := m.message().ScheduledFor; when > 0 {
				m.doneInfo = upFirst(what) + " goes " + time.UnixMilli(when).Format("Mon 02 Jan 15:04") + fmt.Sprintf(".\nnarada outbox cancel %d stops it", it.ID)
			} else if wait := int(m.a.UndoWindow(it.Kind).Seconds()); wait > 0 {
				m.doneInfo = upFirst(what) + fmt.Sprintf(" goes in %ds.\nnarada outbox cancel %d stops it", wait, it.ID)
			}
			return m, tea.Tick(900*time.Millisecond, func(time.Time) tea.Msg { return approveNext{} })
		case "n", "N":
			if err := m.a.Decline(it.ID); err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.done, m.doneInfo = "✗ Declined", "Nothing was sent. "+firstNonEmpty(it.Out.Agent, "The agent")+" has been told."
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

// sendTarget: what a send does and to whom, as in "wants to email Priya".
func sendTarget(kind string, out store.Outgoing) (verb, target string) {
	name := firstNonEmpty(out.SpaceName, out.Space)
	switch {
	case kind == "whatsapp" && out.Action == app.ActForward:
		return "forward a message to", name
	case kind == "whatsapp" && out.Action == app.ActLocation:
		return "share a place with", name
	case kind == "whatsapp" && out.Action == app.ActContact:
		return "share a contact with", name
	case kind == "whatsapp" && out.Action == app.ActPoll:
		return "ask a poll in", name
	case kind == "whatsapp" && out.Action == app.ActVote:
		return "vote in a poll in", name
	case kind == "whatsapp" && out.Action == app.ActPin:
		return "pin a message, for everyone, in", name
	case kind == "whatsapp" && out.Action == app.ActEdit:
		return "edit your message in", name
	case kind == "whatsapp" && out.Action == app.ActDelete:
		return "delete your message, for everyone, in", name
	case kind == "whatsapp" && strings.HasSuffix(out.Space, "@g.us"):
		return "message the group", name
	case kind == "whatsapp":
		return "message", name
	case kind == "chat":
		return "post in", name
	}
	first := ""
	if len(out.To) > 0 {
		first = out.To[0]
		if a, err := mail.ParseAddress(first); err == nil && a.Name != "" {
			first = a.Name
		}
	}
	if others := len(out.To) + len(out.Cc) + len(out.Bcc) - 1; others > 0 {
		first += " and " + count(others, "other")
	}
	return "email", first
}

// sendWhat: a send in a few words, "the mail to Priya Nair".
func sendWhat(kind string, out store.Outgoing) string {
	verb, target := sendTarget(kind, out)
	switch verb {
	case "email":
		return "the mail to " + target
	case "post in":
		return "the post in " + target
	case "message the group":
		return "the message to the group " + target
	}
	return "the message to " + target
}

// queueLine: a waiting send in the queue, "Mail to Priya Nair".
func queueLine(s styles, it store.OutboxItem) string {
	name, _, _ := s.kindLook(it.Kind)
	verb, target := sendTarget(it.Kind, it.Out)
	if verb == "post in" {
		return name + " in " + target
	}
	return name + " to " + target
}

func upFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// chatInfo is what narada knows of the chat a send goes to.
type chatInfo struct {
	sp      store.Space
	tagged  [][2]string // each tag: their name (when known), their number
	last    []store.ChatMessage
	thread  string // the thread's first message
	replies int
}

func (m approver) chatOf(it store.OutboxItem, out store.Outgoing) (ci chatInfo) {
	st := m.a.Store
	ci.sp, _ = st.Space(it.Account, out.Space)
	for _, t := range reTag.FindAllStringSubmatch(out.Body, -1) {
		name, _, _ := st.User(it.Account, t[1]+"@s.whatsapp.net")
		ci.tagged = append(ci.tagged, [2]string{name, "+" + t[1]})
	}
	ci.last, _ = st.ChatMessages(it.Account, out.Space, 2)
	if first, n, err := st.ThreadStart(it.Account, out.Space, out.Thread); err == nil {
		ci.thread, ci.replies = first.Text, n-1
	}
	return ci
}

// kindNote: what kind of chat it is, and how many are in it.
func (ci chatInfo) kindNote(kind string) string {
	what := "chat"
	switch ci.sp.Type {
	case "DIRECT_MESSAGE":
		return "direct message"
	case "SPACE":
		what = "space"
	case "GROUP_CHAT", "GROUP":
		what = "group"
	}
	if kind == "whatsapp" && ci.sp.Type == "" {
		return ""
	}
	if ci.sp.Members > 0 {
		return what + ", " + count(ci.sp.Members, "person")
	}
	return what
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
	reTag   = regexp.MustCompile(`(?:^|[^\w@])@(\d{7,15})\b`)
	reTagAt = regexp.MustCompile(`@(\d{7,15})\b`)
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
	case one == "reply":
		return fmt.Sprintf("%d replies", n)
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
	var names map[string]string
	if chatKind(it.Kind) {
		names = map[string]string{}
		for _, t := range m.chatOf(it, out).tagged {
			if t[0] != "" {
				names[t[1][1:]] = t[0]
			}
		}
	}
	var lines []string
	for _, l := range splitLines(out.Body) {
		lines = append(lines, wrapped(m.tagged(l, names), w)...)
	}
	return lines
}

// tagged renders a line with its @tags marked, by name: WhatsApp shows
// "@919876543210" to everyone as "@Dana Reyes".
func (m approver) tagged(l string, names map[string]string) string {
	s := m.styles
	if len(names) == 0 {
		return s.text.Render(l)
	}
	var b strings.Builder
	at := 0
	for _, ix := range reTagAt.FindAllStringSubmatchIndex(l, -1) {
		name, ok := names[l[ix[2]:ix[3]]]
		if !ok {
			continue
		}
		b.WriteString(s.text.Render(l[at:ix[0]]))
		b.WriteString(s.peach.Render("@" + name))
		at = ix[1]
	}
	b.WriteString(s.text.Render(l[at:]))
	return b.String()
}

// chip is a small filled label; warn is for what you must not miss.
func (s styles) chip(text, bg string) string {
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(s.c.Text)).Background(lipgloss.Color(s.c.Surface))
	if bg != "" {
		st = lipgloss.NewStyle().Foreground(lipgloss.Color(s.c.Base)).Background(lipgloss.Color(bg)).Bold(true)
	}
	return s.round(st.Padding(0, 1), text)
}

// flow lays chips out in rows of at most w columns, two spaces apart.
func flow(chips []string, w int) []string {
	var rows []string
	cur := ""
	for _, c := range chips {
		if cur != "" && lipgloss.Width(cur)+3+lipgloss.Width(c) > w {
			rows, cur = append(rows, cur), ""
		}
		if cur != "" {
			cur += "   "
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
	group := strings.HasSuffix(out.Space, "@g.us")
	var ci chatInfo
	if chat {
		ci = m.chatOf(it, out)
	}

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
		info, tiny, _ := strings.Cut(m.doneInfo, "\n")
		card := []string{st.Bold(true).Render(m.done), "", s.sub.Render(ansi.Truncate(info, cw, "…"))}
		after := "closing"
		if len(m.items) > m.i+1 {
			after = fmt.Sprintf("next: %d of %d opens now", m.i+2, len(m.items))
		}
		if tiny != "" {
			after = tiny + " · " + after
		}
		card = append(card, s.muted.Render(ansi.Truncate(after, cw, "…")))
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
	verb, target := sendTarget(it.Kind, out)
	verb = " wants to " + verb + " "
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
	switch {
	case group && ci.sp.Members > 0:
		chips = append(chips, s.chip("a group of "+count(ci.sp.Members, "person"), s.c.Peach))
	case group:
		chips = append(chips, s.chip("a group: everyone in it sees this", s.c.Peach))
	case it.Kind == "chat" && ci.sp.Type == "SPACE" && ci.sp.Members > 0:
		chips = append(chips, s.chip("space, "+count(ci.sp.Members, "person"), ""))
	}
	switch n := len(ci.tagged); {
	case n == 1 || n == 2:
		var names []string
		for _, t := range ci.tagged {
			names = append(names, firstNonEmpty(t[0], t[1]))
		}
		chips = append(chips, s.chip("tags "+strings.Join(names, " and "), s.c.Peach))
	case n > 2:
		chips = append(chips, s.chip("tags "+count(n, "person"), s.c.Peach))
	}
	if n := len(out.Bcc); n > 0 {
		chips = append(chips, s.chip(fmt.Sprintf("%d in Bcc", n), ""))
	}
	if n := len(out.Files); n > 0 {
		chips = append(chips, s.chip(count(n, "attachment"), ""))
	}
	switch {
	case out.ThreadID != "":
		chips = append(chips, s.chip("a reply in an existing thread", ""))
	case out.Thread != "":
		chips = append(chips, s.chip("in a thread", ""))
	}
	if out.ScheduledFor > 0 {
		chips = append(chips, s.chip("goes "+time.UnixMilli(out.ScheduledFor).Format("Mon 02 Jan 15:04"), ""))
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
	head := func(title string) {
		if len(facts) > 0 {
			facts = append(facts, "")
		}
		facts = append(facts, s.sub.Bold(true).Render(title))
	}
	if chat {
		field("To", s.text.Render(firstNonEmpty(out.SpaceName, out.Space))+" "+s.muted.Render(ci.kindNote(it.Kind)))
		if out.Thread != "" {
			if ci.thread != "" {
				field("Thread", s.text.Render(ansi.Truncate(firstLine(ci.thread), max(8, factW-24), "…"))+" "+s.muted.Render(count(ci.replies, "reply")))
			} else {
				field("Thread", s.text.Render("replying in the thread"))
			}
		}
		if out.Quote != "" {
			field("Replying", s.text.Render("to a message (quoted)"))
		}
		if len(ci.tagged) > 0 {
			var v []string
			for _, t := range ci.tagged {
				if t[0] != "" {
					v = append(v, s.peach.Render(t[0])+" "+s.muted.Render(t[1]))
				} else {
					v = append(v, s.peach.Render(t[1]))
				}
			}
			field("Tags", v...)
		}
		if it.Kind == "whatsapp" {
			me := "your WhatsApp"
			if wa, ok := m.a.Store.WhatsApp(); ok && wa.Email != "" {
				me += " " + s.muted.Render(wa.Email)
			}
			field("As", s.text.Render(me))
		} else {
			field("From", s.text.Render(firstNonEmpty(it.Account, "your account"))+" "+s.muted.Render(strings.TrimPrefix(account, it.Account)))
		}
	} else {
		from := s.person(account, s.text)
		if it.Account != "" && it.Account != account {
			from = s.text.Render(it.Account) + " " + s.muted.Render(account)
		}
		field("From", from)
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
		head("Your edit")
		if add > 0 {
			facts = append(facts, s.green.Render("+ ")+s.text.Render(count(add, "line")+" added"))
		}
		if del > 0 {
			facts = append(facts, s.red.Render("- ")+s.text.Render(count(del, "line")+" removed"))
		}
	}
	if len(ci.last) > 0 && it.Kind == "whatsapp" {
		head("Last in this chat")
		for _, c := range ci.last {
			who := "someone"
			if f := strings.Fields(c.SenderName); len(f) > 0 {
				who = f[0]
			}
			if c.Mine {
				who = "you"
			}
			text := firstLine(firstNonEmpty(c.Text, "(a file)"))
			facts = append(facts, s.muted.Render(fmt.Sprintf("%-9s", ansi.Truncate(who, 8, "…"))+ansi.Truncate(text, max(4, factW-9), "…")))
		}
	}
	if it.Reason != "" {
		facts = append(facts, "")
		for _, l := range wrapped(s.muted.Render("Asks because "+it.Reason), factW) {
			facts = append(facts, l)
		}
	}
	if m.i+1 < len(m.items) {
		head("Next in the queue")
		nx := m.items[m.i+1]
		facts = append(facts, s.muted.Render(fmt.Sprintf("%-9s", fmt.Sprintf("%d of %d", m.i+2, len(m.items)))+queueLine(s, nx)))
	}

	// ---- the actions, pinned to the bottom
	send := "Send"
	if m.out != nil {
		send = "Send my version"
	}
	if u := int(m.a.UndoWindow(it.Kind).Seconds()); u > 0 && out.ScheduledFor == 0 && two {
		send += fmt.Sprintf(" (%ds to undo)", u)
	}
	edit := "Edit"
	switch {
	case m.out != nil:
		edit = "Edit again"
	case two:
		edit = "Edit first"
	}
	acts := []string{s.answer("y", send, s.c.Green), s.answer("e", edit, "")}
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
		later := "Later"
		if two {
			later = "Decide later"
		}
		acts = append(acts, s.answer("q", later, ""))
	}
	actRows := flow(acts, cw)
	if hint := "j/k scroll"; two && len(actRows) == 1 && lipgloss.Width(actRows[0])+len(hint)+4 <= cw {
		actRows[0] += strings.Repeat(" ", cw-lipgloss.Width(actRows[0])-len(hint)) + s.muted.Render(hint)
	}
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
	if out := m.message(); m.out != nil && out.Body != m.cur().Out.Body {
		note = "changes marked · d hides them"
		if !m.diff {
			note = "d marks the changes"
		}
	}
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
