package tui

import (
	"fmt"
	"github.com/Theyashsawarkar/narada/internal/app"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/Theyashsawarkar/narada/internal/chatfmt"
	"github.com/Theyashsawarkar/narada/internal/cli"
	"github.com/Theyashsawarkar/narada/internal/mime"
	"github.com/Theyashsawarkar/narada/internal/render"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// Card heights: a mail card is 5 rows, a chat card 4, plus a blank row.
const (
	mailCardRows = 6
	chatCardRows = 5
)

func (m Model) cardRows() int {
	if !m.panelList() && m.tab == tabMail {
		return mailCardRows
	}
	if m.panelList() {
		if m.tab == tabMail {
			return 4 // three lines and a hairline
		}
		return 3
	}
	return chatCardRows
}

// panelList: the lists draw items as rows on the pane (one panel), not a
// box each.
func (m Model) panelList() bool { return m.a.Config.ListStyle != "cards" }

// listLabels is how many rows the chat list's pinned / today / earlier
// labels can take.
func (m Model) listLabels() int {
	if m.tab == tabChat {
		return 3
	}
	return 0
}

// hairline divides the rows of the panel list.
func (m Model) hairline(w int) string {
	return " " + m.styles.muted.Faint(true).Render(strings.Repeat("─", w+3))
}

// listItem draws one entry of the list: a card (and the gap after it), or
// on the panel, its rows and a hairline; the picked one is filled in the
// tab's colour (dimmer while the focus is on the preview).
func (m Model) listItem(lines []string, w int, border string, sel, dot bool) []string {
	if !m.panelList() {
		return append(m.cardBox(lines, w, border, sel, dot), "")
	}
	edge := fg(m.selEdge()).Bold(true)
	var out []string
	for i, l := range lines {
		// the picked row has a bar in the place's colour down its left;
		// an unread one a dot. Nothing is drawn after the text.
		gutter := "  "
		switch {
		case sel:
			gutter = edge.Render("▌") + " "
		case dot && i == 0:
			gutter = fg(m.partColor()).Bold(true).Render("●") + " "
		}
		out = append(out, gutter+l)
	}
	return append(out, m.hairline(w))
}

// appendItem adds an item to the list, one space in.
func (m Model) appendItem(out, lines []string, w int, border string, sel, dot bool) []string {
	for _, l := range m.listItem(lines, w, border, sel, dot) {
		out = append(out, " "+l)
	}
	return out
}

// selEdge is the picked card's border: the tab's colour, muted when the
// focus is on the preview.
func (m Model) selEdge() string {
	if m.focus != 0 {
		return m.styles.c.Muted
	}
	return m.partColor()
}

func fg(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }

// fit pads or cuts a styled string to exactly w cells on the terminal:
// cut by narada's count, padded by the terminal's, which is narrower for
// Indian scripts (see termText), so a border after it stays in line.
func fit(s string, w int) string {
	s = termText(ansi.Truncate(s, w, "…"))
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidthWc(s)))
}

// cardBox draws a card of inner width w around lines: rounded in the
// border colour, or heavy in the accent when selected. No background
// fills: they turn muddy on translucent terminals. dot marks it unread.
func (m Model) cardBox(lines []string, w int, border string, sel, dot bool) []string {
	s := m.styles
	b := fg(border)
	tl, tr, bl, br, hz, vt := "╭", "╮", "╰", "╯", "─", "│"
	if sel {
		b = b.Bold(true)
		hz, vt = "━", "┃" // heavy lines mark the selection; corners stay round
	}
	out := []string{b.Render(tl + strings.Repeat(hz, w+2) + tr)}
	for i, l := range lines {
		left := b.Render(vt)
		if dot && i == 0 {
			left = s.accent.Bold(true).Render("●")
		}
		out = append(out, left+" "+fit(l, w)+" "+b.Render(vt))
	}
	return append(out, b.Render(bl+strings.Repeat(hz, w+2)+br))
}

func rgb(hex string) string {
	hex = strings.TrimPrefix(hex, "#")
	var r, g, bl int
	if len(hex) != 6 {
		return "49;50;68"
	}
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &bl)
	return fmt.Sprintf("%d;%d;%d", r, g, bl)
}

func row(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		left = ansi.Truncate(left, max(1, w-lipgloss.Width(right)-1), "…")
		gap = max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	}
	return left + strings.Repeat(" ", gap) + right
}

// avatarColors are the theme's bright colours, picked per name: a person
// keeps theirs everywhere (avatar, name, the bar by their messages).
func (s styles) avatarColors() []string {
	c := s.c
	return []string{c.Pink, c.Peach, c.Green, c.Blue, c.Sky, c.Yellow, c.Teal, c.Lavender, c.Flamingo, c.Sapphire, c.Maroon, c.Rosewater}
}

// personColor is someone's colour (from their name).
func (m Model) personColor(name string) string {
	ac := m.styles.avatarColors()
	return ac[hash(strings.TrimPrefix(name, "~"))%len(ac)]
}

// accountEmail is the address of one of your Google accounts.
func (m Model) accountEmail(account string) string {
	if a, err := m.a.Store.Account(account); err == nil {
		return a.Email
	}
	return ""
}

// avatar is two initials on a colour picked from the name.
// sentChip says whether a sent mail got an answer.
func (m Model) sentChip(st store.SentStatus) string {
	s := m.styles
	if st.ReplyFrom != "" {
		return m.chip("↩ "+firstWord(st.ReplyFrom)+" replied "+cli.When(st.ReplyAt), s.c.Green)
	}
	d := time.Since(time.UnixMilli(st.SentAt))
	wait := "today"
	if d >= 24*time.Hour {
		wait = fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return m.chip("no reply yet · "+wait, s.c.Yellow)
}

func (m Model) outboxStatus(it store.OutboxItem) (string, string) {
	s := m.styles
	switch it.Status {
	case store.StatusQueued:
		if it.Out.ScheduledFor > 0 {
			return "⏰ " + app.SendTime(time.UnixMilli(it.Out.ScheduledFor), time.Now()), s.c.Blue
		}
		return fmt.Sprintf("sending in %ds", max(0, int(time.Until(time.UnixMilli(it.SendAt)).Seconds()+0.5))), s.c.Yellow
	case store.StatusSending:
		return "sending…", s.c.Yellow
	case store.StatusApproval:
		return "waiting for your OK", s.c.Peach
	}
	return "failed", s.c.Red
}

// toNames shortens a recipient list: "Priya, Arjun, Neha +2", with names
// from your contacts where the addresses have none.
func (m Model) toNames(account, list string) string {
	var names []string
	for _, a := range mime.ParseList(list) {
		r := m.recipFor(account, a)
		name := r.Name
		if name == "" {
			name = strings.Split(r.Addr, "@")[0]
		}
		names = append(names, firstWord(name))
	}
	if len(names) > 3 {
		return strings.Join(names[:3], ", ") + fmt.Sprintf(" +%d", len(names)-3)
	}
	return strings.Join(names, ", ")
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return strings.Trim(f[0], `"',`)
	}
	return s
}

// attachedLines shows the files about to go with the message: a small
// picture of each image, its name and size, side by side.
func (m *Model) attachedLines(w int) []string {
	s := m.styles
	var tiles []string
	for _, f := range m.chatFiles {
		path := expandHome(f.Source)
		name := filepath.Base(path)
		size := ""
		if fi, err := os.Stat(path); err == nil {
			size = " · " + app.HumanSize(fi.Size())
		}
		thumb := m.fileLines(path, 18, 5)
		width := 24
		if len(thumb) > 0 {
			width = max(14, ansi.StringWidth(thumb[0]))
		}
		icon := "📄 "
		if len(thumb) > 0 {
			icon = ""
		}
		if f.Voice {
			icon, name = "🎤 ", "voice note"
		}
		label := ansi.Truncate(icon+s.text.Render(name)+s.muted.Render(size), width, "…")
		tiles = append(tiles, lipgloss.JoinVertical(lipgloss.Left, append(thumb, label)...))
	}
	row := lipgloss.JoinHorizontal(lipgloss.Bottom, intersperse(tiles, "  ")...)
	out := strings.Split(row, "\n")
	for i := range out {
		out[i] = ansi.Truncate(out[i], w, "")
	}
	hint := fmt.Sprintf("%d file%s", len(m.chatFiles), map[bool]string{true: "s", false: ""}[len(m.chatFiles) != 1])
	if m.wa {
		hint += " · the text is the first one's caption"
	}
	return append(out, s.muted.Render(hint+" · backspace in an empty box drops the last"))
}

func intersperse(xs []string, sep string) []string {
	var out []string
	for i, x := range xs {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, x)
	}
	return out
}

// tickText is ticks as plain text, for inside a filled bubble.
func (m Model) tickText(level int) string {
	return map[int]string{store.TickFailed: " ✗ not sent", store.TickPending: " ◷", 1: " ✓", 2: " ✓✓", 3: " ✓✓"}[level]
}

// chatBubble draws a message in a rounded outline: a soft border for
// theirs, the accent for yours; the picked one is a heavy frame in the bright accent. The time (and your
// ticks, blue once read) sit at the bottom right inside it.
func (m Model) chatBubble(lines []string, maxW int, mine, sel bool, stamp string, read bool) []string {
	s := m.styles
	color := s.c.Border
	if mine {
		color = s.c.Accent2
	}
	if sel {
		color = s.c.Accent
	}
	iw := ansi.StringWidth(stamp)
	for _, l := range lines {
		iw = max(iw, ansi.StringWidth(l))
	}
	t := strings.TrimRight(stamp, " ✓")
	ticks := s.muted.Render(strings.TrimPrefix(stamp, t))
	if read {
		ticks = s.blue.Render(strings.TrimPrefix(stamp, t))
	}
	sw := ansi.StringWidth(stamp)
	stampText := s.muted.Render(t) + ticks
	// the time beside the last line if it fits, else on its own line
	last := len(lines) - 1
	lw := ansi.StringWidth(lines[last])
	picture := strings.ContainsAny(lines[last], "\U0010EEEE▀") // never beside a picture
	beside := lw+2+sw <= maxW-4 && !picture
	if beside {
		iw = max(iw, lw+2+sw)
	}
	iw = min(iw, maxW-4)
	body := append([]string{}, lines...)
	if beside {
		body[last] = lines[last] + strings.Repeat(" ", max(0, iw-lw-sw)) + stampText
	} else {
		body = append(body, strings.Repeat(" ", max(0, iw-sw))+stampText)
	}
	return bubble(body, iw+4, color, sel)
}

// chatMessage draws one message: its lines after a bar in the sender's
// colour (heavy and bright when picked). Its time and ticks follow its last
// line when that line is plain enough to measure, else get a line of their
// own; nothing else is ever drawn after text (see fitTerm).
func (m Model) chatMessage(lines []string, g store.ChatMessage, later, picked bool, color string, maxW int) []string {
	s := m.styles
	bar := fg(color).Render("▎") + " "
	if picked {
		bar = s.accent.Bold(true).Render("┃") + " "
	}
	stamp := ""
	if later { // the run's first message has its time in the name line
		stamp = s.muted.Render(time.UnixMilli(g.Created).Format("15:04"))
	}
	if g.Mine {
		t := strings.TrimSpace(m.tickText(g.Ticks))
		switch {
		case g.Ticks == 3:
			t = s.blue.Render(t)
		case g.Ticks == store.TickFailed:
			t = s.red.Render(t)
		default:
			t = s.muted.Render(t)
		}
		stamp = strings.TrimSpace(stamp + " " + t)
	}
	out := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		out = append(out, bar+l)
	}
	if stamp != "" {
		last := lines[len(lines)-1]
		plain := ansi.Strip(last)
		if ansi.StringWidth(plain) == ansi.StringWidthWc(plain) && ansi.StringWidth(last)+2+ansi.StringWidth(stamp) <= maxW &&
			!strings.ContainsAny(last, "\U0010EEEE▀") {
			out[len(out)-1] += "  " + stamp
		} else {
			out = append(out, bar+stamp)
		}
	}
	return out
}

// ticks is WhatsApp's mark for how far your message got: ✓ sent,
// ✓✓ delivered, blue ✓✓ read.
func (m Model) ticks(level int) string {
	s := m.styles
	switch level {
	case 1:
		return s.muted.Render(" ✓")
	case 2:
		return s.muted.Render(" ✓✓")
	case 3:
		return s.blue.Render(" ✓✓")
	}
	return ""
}

func (m Model) avatar(name string) string {
	name = strings.TrimPrefix(name, "~") // a name they gave themselves
	var ini []rune
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return r == ' ' || r == '.' || r == '@' || r == '_' || r == ',' || r == '+' }) {
		ini = append(ini, []rune(strings.ToUpper(w))[0])
		if len(ini) == 2 {
			break
		}
	}
	if len(ini) == 1 { // one word: its first two letters
		if r := []rune(strings.ToUpper(strings.TrimSpace(name))); len(r) > 1 && r[1] != ' ' {
			ini = append(ini, r[1])
		}
	}
	if t := strings.TrimSpace(name); t != "" && (t[0] == '+' || t[0] >= '0' && t[0] <= '9') {
		ini = []rune{'#', ' '} // a phone number: no initials to show
		if m.a.Config.NerdFont {
			ini = []rune{'\uf095', ' '}
		}
	}
	if len(ini) > 0 && runewidth.RuneWidth(ini[0]) > 1 {
		ini = ini[:1] // an emoji fills both cells
	}
	for len(ini) < 2 && runewidth.StringWidth(string(ini)) < 2 {
		ini = append(ini, ' ')
	}
	ac := m.styles.avatarColors()
	col := lipgloss.Color(ac[hash(strings.TrimPrefix(name, "~"))%len(ac)])
	face := lipgloss.NewStyle().Background(col).Foreground(lipgloss.Color(m.styles.c.Base)).Bold(true).Render(string(ini))
	if !m.a.Config.NerdFont {
		return face
	}
	// round ends (Nerd Font half circles) make it a pill-shaped circle
	cap := lipgloss.NewStyle().Foreground(col)
	return cap.Render("\ue0b6") + face + cap.Render("\ue0b4")
}

// indent lines up text under a name that follows an avatar.
func (m Model) indent() string { return strings.Repeat(" ", lipgloss.Width(m.avatar("x"))+1) }

func (m Model) chip(text, color string) string {
	return fg(color).Render("‹" + text + "›")
}

// ------------------------------------------------------------------- list

func (m *Model) listView(w, h int) string {
	s := m.styles
	if m.count() == 0 {
		msg := "Nothing here."
		switch {
		case m.tab == tabMail && m.currentSection().Label == "SCHEDULED":
			msg = "Nothing scheduled. Ctrl+t in a mail sends it later."
		case m.inAISection():
			msg = "No AI requests waiting. When an agent asks to send, it queues here."
		case m.tab == tabChat && chatSections[m.sec%len(chatSections)] == "Scheduled":
			msg = "Nothing scheduled. Ctrl+t while typing a message (i) sends it later."
		}
		if m.filter != "" {
			msg = "Nothing matches /" + m.filter + " (Esc clears)"
		} else if m.syncing {
			msg = "Syncing…"
		}
		return "\n " + s.muted.Render(msg)
	}
	cw := w - 6 // card inner width: margins, borders and padding on both sides
	var out []string
	visible := max(1, (h-1-m.listLabels())/m.cardRows())
	group := func(i int) string { // chat list: pinned, today, earlier
		if m.tab != tabChat || i >= len(m.spaces) || strings.HasPrefix(m.spaces[i].Name, outboxPrefix) {
			return ""
		}
		sp := m.spaces[i]
		switch {
		case sp.Pinned:
			return "pinned"
		case dayName(time.UnixMilli(sp.LastActive)) == "Today":
			return "today"
		}
		return "earlier"
	}
	for i := m.offset; i < min(m.count(), m.offset+visible); i++ {
		if g := group(i); g != "" && (i == m.offset || g != group(i-1)) {
			label := " " + s.muted.Bold(true).Render(strings.ToUpper(g))
			if n := len(out); n > 0 && (out[n-1] == "" || out[n-1] == " "+m.hairline(cw)) {
				out[n-1] = label // in the gap (or on the hairline) above it
			} else {
				out = append(out, label)
			}
		}
		sel := i == m.sel
		border := s.c.Border
		if sel && m.focus == 0 {
			border = s.c.Accent
		} else if sel {
			border = s.c.Muted
		}
		var lines []string
		dot := false
		if m.tab == tabMail {
			t := m.threads[i]
			picked := m.selected[t.Account+"/"+t.ID]
			if picked {
				border = s.c.Accent2
			}
			dot = t.Unread()
			who := s.sub.Render(t.Participants)
			subj := s.sub.Render(t.Subject)
			if t.Unread() {
				who, subj = s.bold.Render(t.Participants), s.text.Render(t.Subject)
			}
			if it, ok := m.outboxRows[t.ID]; ok {
				// still in the outbox: who it's for, and what's happening
				status, col := m.outboxStatus(it)
				if !sel {
					border = col
				}
				to := "→ " + m.toNames(it.Account, strings.Join(it.Out.To, ", "))
				if chatKind(it.Kind) {
					to = "→ # " + it.Out.SpaceName
				}
				hint := map[string]string{store.StatusQueued: "u cancels", store.StatusFailed: "R sends it again · ⏎ edits",
					store.StatusApproval: "y send · e edit · n decline", store.StatusSending: "…"}[it.Status]
				if it.Status == store.StatusQueued && it.Out.ScheduledFor > 0 {
					hint = "t moves · ⏎ edits · u cancels"
				}
				lines = []string{row(s.text.Render(to), fg(col).Render(status), cw), s.text.Render(t.Subject),
					ansi.Truncate(fg(col).Render(hint)+s.muted.Render("  "+oneLine(it.Out.Body)), cw, "…")}
				out = m.appendItem(out, lines, cw, border, sel, false)
				continue
			}
			sentSt, isSent := m.sentStat[t.Account+"/"+t.ID]
			if isSent {
				who = s.sub.Render("→ " + m.toNames(t.Account, sentSt.To))
			}
			if picked {
				who = s.accent2.Render("✓ ") + who
			}
			if t.Starred() {
				who += s.yellow.Render(" ★")
			}
			var chips []string
			if t.Count > 1 {
				chips = append(chips, m.chip(fmt.Sprintf("%d msgs", t.Count), s.c.Muted))
			}
			for _, l := range userLabels(t.Labels) {
				chips = append(chips, m.chip(l, s.c.Accent2))
			}
			if len(m.accounts) > 1 && m.acct < 0 {
				chips = append(chips, m.chip(t.Account, s.c.Blue))
			}
			if isSent {
				chips = append([]string{m.sentChip(sentSt)}, chips...)
			}
			chipStr := strings.Join(chips, " ")
			snipW := cw - lipgloss.Width(chipStr) - 1 - lipgloss.Width(m.avatar("x")) - 1
			snip := ansi.Truncate(s.muted.Render(oneLine(t.Snippet)), max(4, snipW), "…")
			face := m.avatar(firstNonEmpty(strings.Split(t.Participants, ",")[0], "?")) + " "
			pad := strings.Repeat(" ", lipgloss.Width(face))
			lines = []string{row(face+who, s.muted.Render(cli.When(t.LastDate)), cw), pad + subj, row(pad+snip, chipStr, cw)}
		} else {
			sp := m.spaces[i]
			if it, ok := m.outboxRows[sp.Name]; ok {
				// a scheduled message: where it goes, when, what
				status, col := m.outboxStatus(it)
				if !sel {
					border = col
				}
				lines = []string{row(m.avatar(sp.DisplayName)+" "+s.text.Render("→ "+sp.DisplayName), fg(col).Render(status), cw),
					m.indent() + ansi.Truncate(s.muted.Render(m.waTagged(sp.LastText)), cw-lipgloss.Width(m.indent()), "…")}
				out = m.appendItem(out, lines, cw, border, sel, false)
				continue
			}
			dot = sp.Unread > 0
			name := cli.SpaceTitle(sp)
			mark := s.muted.Render("# ")
			if sp.Type == "DIRECT_MESSAGE" {
				mark = s.muted.Render("@ ")
			}
			title := s.sub.Render(name)
			if sp.Unread > 0 {
				unread := lipgloss.NewStyle().Background(lipgloss.Color(m.partColor())).Foreground(lipgloss.Color(s.c.Crust)).Bold(true)
				title = s.bold.Render(name) + " " + s.round(unread, fmt.Sprint(sp.Unread))
			}
			if n := m.mentions[sp.Account+"/"+sp.Name]; n > 0 {
				badge := lipgloss.NewStyle().Background(lipgloss.Color(s.c.Pink)).Foreground(lipgloss.Color(s.c.Crust)).Bold(true)
				title = s.bold.Render(name) + " " + s.round(badge, fmt.Sprintf("@%d", n))
			}
			if sp.Pinned {
				title += s.accent.Render(" 📌")
			}
			if sp.Muted() {
				title += s.muted.Render(" 🔕")
			}
			if sp.Blocked {
				title += s.red.Render(" ⊘ blocked")
			}
			if m.selected[sp.Account+"/"+sp.Name] {
				title = s.accent2.Render("✓ ") + title
			}
			last := oneLine(sp.LastText)
			if sp.LastSender != "" {
				last = strings.SplitN(sp.LastSender, " ", 2)[0] + ": " + last
			}
			lines = []string{row(m.avatar(name)+" "+mark+title, s.muted.Render(cli.When(sp.LastActive)), cw), m.indent() + s.muted.Render(last)}
		}
		out = m.appendItem(out, lines, cw, border, sel, dot)
	}
	// the end of a mail list: older mail comes from Gmail on demand
	if m.tab == tabMail && m.offset+visible >= m.count() && !m.currentSection().Today && m.currentSection().Label != "SCHEDULED" && !m.inAISection() {
		sec := m.currentSection().Name
		var hint string
		switch {
		case m.loadingOlder:
			hint = s.accent.Render(fmt.Sprintf("  ⟳ loading %d older…", m.olderN))
		case m.noOlder[sec]:
			hint = s.muted.Render("  ✓ that's everything in " + sec)
		default:
			hint = s.muted.Render(fmt.Sprintf("  ↓ j at the end loads %d older", m.olderN)) + s.muted.Render(" · + / - for more or fewer")
		}
		if len(out) > 0 && out[len(out)-1] == "" {
			out = out[:len(out)-1]
		}
		out = append(out, "", ansi.Truncate(hint, w, "…"))
	}
	return strings.Join(out, "\n")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func userLabels(labels []string) []string {
	var out []string
	for _, l := range labels {
		switch {
		case l == "INBOX", l == "UNREAD", l == "STARRED", l == "SENT", l == "IMPORTANT", l == "DRAFT", strings.HasPrefix(l, "CATEGORY_"):
		default:
			out = append(out, strings.TrimPrefix(l, "Label_"))
		}
	}
	return out
}

// ---------------------------------------------------------------- preview

func (m Model) renderStyles() render.Styles { return renderStylesOf(m.styles) }

func renderStylesOf(s styles) render.Styles {
	return render.Styles{
		Text: s.text, Bold: s.bold, Italic: s.text.Italic(true), Heading: fg(s.c.Accent).Bold(true),
		Link: fg(s.c.Blue).Underline(true), Quote: s.muted, Muted: s.muted, Button: fg(s.c.Accent).Bold(true),
		Code: fg(s.c.Peach), TableHead: fg(s.c.Accent).Bold(true), Border: fg(s.c.Border),
	}
}

// labelNames maps label ids to names for the chips.
func (m *Model) labelName(account, id string) string {
	if m.labelNames == nil {
		m.labelNames = map[string]string{}
	}
	key := account + "/" + id
	if n, ok := m.labelNames[key]; ok {
		return n
	}
	name := strings.TrimPrefix(id, "Label_")
	if ls, err := m.a.Store.Labels(account); err == nil {
		for _, l := range ls {
			m.labelNames[account+"/"+l.ID] = l.Name
			if l.ID == id {
				name = l.Name
			}
		}
	}
	m.labelNames[key] = name
	return name
}

// buildPreview renders the selected thread or space at width w into
// m.preview (and m.links), so View only slices it.
func (m *Model) buildPreview(w int) {
	m.preview, m.links = nil, nil
	if t, ok := m.curThread(); ok {
		m.preview = m.mailLines(t, w)
	} else if it, ok := m.curOutbox(); ok {
		note := map[string]string{store.StatusQueued: "in its undo window · u cancels", store.StatusSending: "sending now",
			store.StatusApproval: "an agent's mail waiting for your approval (A)", store.StatusFailed: "failed: " + it.Error + " · R sends it again"}[it.Status]
		if it.Status == store.StatusQueued && it.Out.ScheduledFor > 0 {
			note = "⏰ scheduled for " + time.UnixMilli(it.Out.ScheduledFor).Format("Monday 02 January, 15:04") + " · t moves it · enter edits · u cancels"
		}
		m.preview = strings.Split(m.outgoingView(it.Kind, it.Out, note, w), "\n")
	} else if _, ok := m.curSpace(); ok {
		m.preview = m.chatLines(w)
	}
}

func (m *Model) mailLines(t store.Thread, w int) []string {
	s := m.styles
	var out []string // the pane's header has the subject
	var chips []string
	if t.Unread() {
		chips = append(chips, m.chip("unread", s.c.Red))
	}
	if t.Has("INBOX") {
		chips = append(chips, m.chip("Inbox", s.c.Muted))
	}
	for _, l := range userLabels(t.Labels) {
		chips = append(chips, m.chip(m.labelName(t.Account, l), s.c.Mauve))
	}
	if t.Starred() {
		chips = append(chips, m.chip("★ starred", s.c.Yellow))
	}
	if st, ok := m.sentStat[t.Account+"/"+t.ID]; ok {
		chips = append(chips, m.sentChip(st))
	}
	chips = append(chips, m.chip(fmt.Sprintf("%d message%s", len(m.msgs), map[bool]string{true: "s"}[len(m.msgs) != 1]), s.c.Muted))
	out = append(out, strings.Join(chips, " "), "")

	inner := w - 3 // the sender's bar and a space
	rs := m.renderStyles()
	isOpen := func(i int) bool {
		msg := m.msgs[i]
		return i == len(m.msgs)-1 || m.expanded[msg.ID] || m.expandAll || has(msg.Labels, "UNREAD")
	}
	for i, msg := range m.msgs {
		open := isOpen(i)
		from := msg.FromName
		if from == "" {
			from = msg.FromAddr
		}
		date := time.UnixMilli(msg.Date).Format("Mon 02 Jan, 15:04")
		color := m.personColor(from)
		if msg.FromAddr != "" && strings.EqualFold(msg.FromAddr, m.accountEmail(t.Account)) {
			from, color = "You", s.c.Mauve
		}
		head := row(m.avatar(from)+" "+fg(color).Bold(true).Render(from)+s.muted.Render(" "+msg.FromAddr), s.muted.Render(date), w)
		if !open {
			// read messages fold to one line each; a long run of them to one
			// line in all, as Gmail does (Enter opens every message)
			start := i
			for start > 0 && !isOpen(start-1) {
				start--
			}
			end := i
			for end+1 < len(m.msgs) && !isOpen(end+1) {
				end++
			}
			if end-start >= 3 && i > start && i < end {
				if i == start+1 {
					out = append(out, s.muted.Render(fmt.Sprintf("   ⋯ %d earlier messages · Enter opens them", end-start-1)))
				}
				continue
			}
			short := time.UnixMilli(msg.Date).Format("02 Jan")
			who := fg(color).Render("▏") + " " + fg(color).Bold(true).Render(from) + s.muted.Render(" · ")
			snip := ansi.Truncate(s.sub.Render(oneLine(msg.Body)), max(8, w-ansi.StringWidth(who)-ansi.StringWidth(short)-3), "…")
			out = append(out, row(who+snip, s.muted.Render(short), w))
			continue
		}
		if i > 0 && !isOpen(i-1) {
			out = append(out, "")
		}
		out = append(out, head)
		to := "to " + shortAddrs(msg.To)
		if msg.Cc != "" {
			to += " · cc " + shortAddrs(msg.Cc)
		}
		out = append(out, m.indent()+s.muted.Render(to))
		var lines []string
		var res render.Result
		if strings.TrimSpace(msg.HTML) != "" {
			res = render.HTML(msg.HTML, inner, rs, m.showQuoted)
		} else {
			res = render.Text(msg.Body, inner, rs, m.showQuoted)
		}
		// renumber this message's links after the earlier messages' ones
		base := len(m.links)
		body := res.Lines
		if base > 0 && len(res.Links) > 0 {
			body = renumber(body, base)
		}
		lines = append(lines, body...)
		if len(msg.Attachments) > 0 {
			lines = append(lines, "")
			for _, at := range msg.Attachments {
				lines = append(lines, s.yellow.Render("📎 "+at.Name)+s.muted.Render(" · "+sizeOf(at.Size)))
			}
			lines = append(lines, s.muted.Render("   gf opens · gF saves"))
		}
		if len(res.Links) > 0 {
			lines = append(lines, "")
			linkStart := len(lines)
			for j, l := range res.Links {
				lines = append(lines, s.muted.Render(fmt.Sprintf("[%d] ", base+j+1))+ansi.Truncate(s.sky.Underline(true).Render(l), inner-6, "…"))
			}
			m.links = append(m.links, res.Links...)
			_ = linkStart
		}
		// the body under a bar in the sender's colour: no frame to its right
		bar := fg(color).Render("▏") + " "
		for _, l := range lines {
			out = append(out, bar+l)
		}
		out = append(out, "")
	}
	return out
}

var reLinkNum = regexp.MustCompile(`\[(\d+)\]`)

// renumber shifts [n] link markers by base (several messages, one list).
func renumber(lines []string, base int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = reLinkNum.ReplaceAllStringFunc(l, func(s string) string {
			var n int
			fmt.Sscanf(s, "[%d]", &n)
			return fmt.Sprintf("[%d]", n+base)
		})
	}
	return out
}

func shortAddrs(list string) string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		p = strings.TrimSpace(p)
		if i := strings.Index(p, "<"); i > 0 {
			p = strings.Trim(strings.TrimSpace(p[:i]), `"`)
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

func has(l []string, x string) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}

func sizeOf(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

func wrapStyled(st lipgloss.Style, text string, w int) []string {
	var out []string
	for _, l := range wrap(text, w) {
		out = append(out, st.Render(l))
	}
	return out
}

func wrap(text string, w int) []string {
	if w < 10 {
		w = 10
	}
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if l == "" {
			out = append(out, "")
			continue
		}
		out = append(out, strings.Split(ansi.Wrap(l, w, ""), "\n")...)
	}
	return out
}

// ------------------------------------------------------------------- chat

var reMention = regexp.MustCompile(`@[\p{L}\p{N}\p{M}_.\x{00A0}-]+`)

// chatLines draws a conversation as bubbles: a day line; a red "new" line
// where what you haven't read starts; then runs of messages by sender
// (name and time once), each message a rounded bubble: theirs on the left,
// yours on the right in the accent colour. Inside: the quote a reply
// answers, the text (mentions marked, links numbered for o), files, and
// "edited"; reactions under it. The picked message has a heavy frame and a ▶ beside it.
func (m *Model) chatLines(w int) []string {
	m.chatMsgLine = map[string]int{}
	s := m.styles
	if len(m.chatMsgs) == 0 {
		return []string{s.muted.Render("No recent messages synced.")}
	}
	byName := map[string]store.ChatMessage{}
	for _, c := range m.chatMsgs {
		byName[c.Name] = c
	}
	picking := m.pickingMsg()
	// who's here (from who wrote), at the top
	seen := map[string]bool{"You": true}
	people := []string{"You"}
	for _, c := range m.chatMsgs {
		if !c.Mine && c.SenderName != "" && !seen[c.SenderName] {
			seen[c.SenderName] = true
			people = append(people, c.SenderName)
		}
	}
	var head []string
	if sp, ok := m.curSpace(); ok && sp.Type == "DIRECT_MESSAGE" {
		// one to one: the header names them (and their number); here only
		// whether they're online or typing
		if t := m.presenceLine(sp); t != "" {
			head = []string{t}
		}
	} else if !m.isStatus() {
		head = []string{s.muted.Render(ansi.Truncate(strings.Join(people, ", "), w-14, "…") + fmt.Sprintf(" · %d people", len(people)))}
		if sp, ok := m.curSpace(); ok && sp.Account == store.WhatsAppAccount {
			if t := m.presenceLine(sp); t != "" {
				head = append(head, t)
			}
		}
	}
	newSince := int64(0)
	if sp, ok := m.curSpace(); ok {
		newSince = m.newMark[sp.Account+"/"+sp.Name]
	}
	mentionStyle := lipgloss.NewStyle().Background(lipgloss.Color(s.c.Pink)).Foreground(lipgloss.Color(s.c.Crust))
	mention := func(t string) string { return s.round(mentionStyle, t) }
	bw := max(24, w*68/100) // a message's widest
	out := head
	var lastDay string
	newShown := false
	m.newLine = 0
	for i := 0; i < len(m.chatMsgs); {
		c := m.chatMsgs[i]
		t := time.UnixMilli(c.Created)
		if day := dayName(t); day != lastDay {
			lastDay = day
			if !m.isStatus() { // statuses go by person: their times say when
				out = append(out, "", divider(day, w, s.muted))
			}
		}
		if !newShown && newSince > 0 && !c.Mine && c.Created > newSince {
			newShown = true
			m.newLine = len(out)
			out = append(out, divider("new", w, s.red))
		}
		// a run: same sender, same day, each within 10 minutes of the last
		j := i + 1
		status := m.isStatus() // one person's statuses stay together
		for j < len(m.chatMsgs) && m.chatMsgs[j].Sender == c.Sender && (status || m.chatMsgs[j].Created-m.chatMsgs[j-1].Created < 10*60*1000) &&
			(status || dayName(time.UnixMilli(m.chatMsgs[j].Created)) == lastDay) &&
			!(newSince > 0 && !newShown && !m.chatMsgs[j].Mine && m.chatMsgs[j].Created > newSince) {
			j++
		}
		name := c.SenderName
		if c.Mine {
			name = "You"
		}
		color := m.personColor(name)
		if c.Mine {
			color = s.c.Mauve
		}
		mineAt := w * 30 / 100 // yours start here, theirs at the left
		out = append(out, "")
		if c.Mine {
			out = append(out, strings.Repeat(" ", mineAt)+fg(color).Bold(true).Render("You")+s.muted.Render("  "+time.UnixMilli(c.Created).Format("15:04")))
		} else {
			out = append(out, m.avatar(name)+" "+fg(color).Bold(true).Render(name)+s.muted.Render("  "+time.UnixMilli(c.Created).Format("15:04")))
		}
		for k, g := range m.chatMsgs[i:j] {
			m.chatMsgLine[g.Name] = len(out)
			sel := picking && i+k == m.chatSel
			var lines []string
			if g.Quoted != "" {
				who, what := "", "a message"
				if q, ok := byName[g.Quoted]; ok {
					who, what = q.SenderName, oneLine(m.waTagged(q.Text))
					if q.Mine {
						who = "You"
					}
				}
				lines = append(lines, s.muted.Render("▏")+s.sub.Bold(true).Render(who)+s.muted.Render(" · "+ansi.Truncate(what, bw-lipgloss.Width(who)-8, "…")))
			}
			text := mime.Clean(g.Text)
			if g.Account == store.WhatsAppAccount {
				text = m.waTagged(text)
			}
			// photos, stickers and GIFs show as themselves, above the text
			shown := map[int]bool{}
			for fi, f := range g.Files {
				if img := m.mediaLines(g.Account, g.Name, fi, f, bw-4); img != nil {
					lines = append(lines, img...)
					shown[fi] = true
				}
			}
			if len(shown) > 0 && g.Account == store.WhatsAppAccount {
				text = dropMediaLabel(text)
			}
			var files []string
			n := 0
			for _, l := range strings.Split(text, "\n") {
				if strings.HasPrefix(l, "📎 ") {
					if !shown[n] {
						files = append(files, strings.TrimPrefix(l, "📎 "))
					}
					n++
				}
			}
			var body []string
			for _, l := range strings.Split(text, "\n") {
				if !strings.HasPrefix(l, "📎 ") {
					body = append(body, l)
				}
			}
			if g.Formatted != "" {
				lines = append(lines, m.chatFormatted(g.Formatted, bw-4, mention)...)
			} else if strings.TrimSpace(strings.Join(body, "\n")) != "" {
				for _, l := range wrap(strings.Join(body, "\n"), bw-4) {
					lines = append(lines, m.chatLinks(styleMentions(l, s.text, mention)))
				}
			}
			if g.Edited && len(lines) > 0 {
				lines[len(lines)-1] += s.muted.Render(" (edited)")
			}
			if g.Starred && len(lines) > 0 {
				lines[len(lines)-1] += s.yellow.Render(" ★")
			}
			if g.Pinned && len(lines) > 0 {
				lines[len(lines)-1] += s.accent.Render(" 📌")
			}
			for _, f := range files {
				lines = append(lines, "📎 "+s.peach.Render(ansi.Truncate(f, bw-16, "…"))+s.muted.Render(" · gf opens"))
			}
			if len(lines) == 0 {
				lines = []string{s.muted.Render("…")}
			}
			lit := sel || g.Name == m.flashMsg
			for n, l := range m.chatMessage(lines, g, k > 0, lit, color, bw) {
				// ▶ beside its first line, left of the bar
				mark := "  "
				if lit && n == 0 {
					mark = s.accent.Bold(true).Render("▶") + " "
				}
				if g.Mine {
					out = append(out, strings.Repeat(" ", max(0, mineAt-2))+mark+l)
				} else {
					ind := m.indent()
					out = append(out, ind[:len(ind)-min(2, len(ind))]+mark+l)
				}
			}
			if g.Reactions != "" {
				var chips []string
				for _, r := range strings.Split(g.Reactions, " · ") {
					chips = append(chips, s.muted.Render("‹")+s.sub.Render(r)+s.muted.Render("›"))
				}
				line := strings.Join(chips, " ")
				if g.Mine {
					out = append(out, strings.Repeat(" ", mineAt+2)+line)
				} else {
					out = append(out, m.indent()+"  "+line)
				}
			}
		}
		i = j
	}
	return append(out, m.pendingLines(w)...)
}

// pendingHere lists this chat's messages still on their way: in their
// undo window, sending, or waiting for your approval.
func (m *Model) pendingHere() []store.OutboxItem {
	sp, ok := m.curSpace()
	if !ok {
		return nil
	}
	items, _ := m.a.Store.Outbox(store.StatusQueued, store.StatusSending, store.StatusApproval)
	var out []store.OutboxItem
	for _, it := range slices.Backward(items) { // oldest first, as the chat reads
		if it.Account == sp.Account && it.Out.Space == sp.Name && it.Out.ScheduledFor == 0 {
			out = append(out, it)
		}
	}
	return out
}

// pendingLines shows them under the conversation, counting down, so the
// undo window is where you're looking, not only in a notification.
func (m *Model) pendingLines(w int) []string {
	s := m.styles
	items := m.pendingHere()
	m.pending = len(items) > 0
	var out []string
	for _, it := range items {
		var body []string
		for _, f := range it.Out.Files {
			body = append(body, "📎 "+firstNonEmpty(f.Name, filepath.Base(f.Source)))
		}
		if t := strings.TrimSpace(it.Out.Body); t != "" {
			if it.Account == store.WhatsAppAccount {
				t = m.waTagged(t)
			}
			body = append(body, wrap(t, max(10, w*2/3-4))...)
		}
		who := "You"
		if it.Origin == "agent" {
			who = "An agent"
		}
		var note string
		switch it.Status {
		case store.StatusQueued:
			secs := max(0, int(time.Until(time.UnixMilli(it.SendAt)).Seconds()+0.5))
			note = s.yellow.Render(fmt.Sprintf("sending in %ds", secs)) + s.muted.Render(" · u cancels")
		case store.StatusSending:
			note = s.muted.Render("sending…")
		case store.StatusApproval:
			note = s.peach.Render("waiting for your approval") + s.muted.Render(" · A")
		}
		out = append(out, "", padLeft(s.muted.Render(who+" · ")+note, w))
		for _, l := range bubble(body, w*2/3, m.styles.c.Border, false) {
			out = append(out, padLeft(s.muted.Render(l), w))
		}
	}
	return out
}

// bubble draws a rounded box around lines, as wide as the widest line.
func bubble(lines []string, max_ int, color string, bold bool) []string {
	b := fg(color)
	if bold {
		b = b.Bold(true)
	}
	iw := 0
	for _, l := range lines {
		iw = max(iw, lipgloss.Width(l))
	}
	iw = min(iw, max_-4)
	tl, tr, bl, br, hz, vt := "╭", "╮", "╰", "╯", "─", "│"
	if bold { // the picked one: heavy lines, seen at a glance; corners stay round (Unicode has no heavy arc)
		hz, vt = "━", "┃"
	}
	out := []string{b.Render(tl + strings.Repeat(hz, iw+2) + tr)}
	for _, l := range lines {
		out = append(out, b.Render(vt)+" "+fit(l, iw)+" "+b.Render(vt))
	}
	return append(out, b.Render(bl+strings.Repeat(hz, iw+2)+br))
}

func divider(label string, w int, st lipgloss.Style) string {
	label = " " + label + " "
	side := max(2, (w-lipgloss.Width(label))/2)
	return st.Render(strings.Repeat("─", side) + label + strings.Repeat("─", max(0, w-side-lipgloss.Width(label))))
}

var reURL = regexp.MustCompile(`https?://[^\s<>"]+`)

// chatFormatted draws a Google Chat message's formatting: bullets, bold,
// italics, strikes, code (yellow), quotes.
func (m *Model) chatFormatted(src string, w int, mention func(string) string) []string {
	s := m.styles
	var out []string
	for _, l := range chatfmt.Lines(chatfmt.Parse(src), w) {
		var b strings.Builder
		if l.Kind == chatfmt.Quote {
			b.WriteString(s.muted.Render(l.Prefix))
		} else {
			b.WriteString(fg(m.partColor()).Render(l.Prefix))
		}
		for _, sp := range l.Spans {
			st := s.text
			switch {
			case sp.Code:
				st = s.yellow
			case l.Kind == chatfmt.Quote:
				st = s.sub
			}
			st = st.Bold(sp.Bold || l.Kind == chatfmt.Heading).Italic(sp.Italic).Strikethrough(sp.Strike)
			b.WriteString(styleMentions(sp.Text, st, mention))
		}
		out = append(out, m.chatLinks(b.String()))
	}
	return out
}

// chatLinks underlines the links in a line and numbers them for o.
func (m *Model) chatLinks(l string) string {
	plain := stripANSI(l)
	if !reURL.MatchString(plain) {
		return l
	}
	s := m.styles
	out := reURL.ReplaceAllStringFunc(plain, func(u string) string {
		m.links = append(m.links, u)
		return s.sky.Underline(true).Render(u) + s.muted.Render(fmt.Sprintf("[%d]", len(m.links)))
	})
	return s.text.Render("") + out
}

func hash(s string) int {
	h := 0
	for _, r := range s {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return h
}

// styleMentions colours @names in a plain line.
func styleMentions(l string, text lipgloss.Style, mention func(string) string) string {
	var b strings.Builder
	last := 0
	for _, ix := range reMention.FindAllStringIndex(l, -1) {
		b.WriteString(text.Render(l[last:ix[0]]))
		b.WriteString(mention(l[ix[0]:ix[1]]))
		last = ix[1]
	}
	b.WriteString(text.Render(l[last:]))
	return b.String()
}

func padLeft(s string, w int) string {
	return strings.Repeat(" ", max(0, w-lipgloss.Width(s))) + s
}

func dayName(t time.Time) string {
	now := time.Now()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "Today"
	case y1 == y2 && m1 == m2 && d1 == d2-1:
		return "Yesterday"
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Monday")
	}
	return t.Format("Mon 02 Jan")
}

// previewBox draws the preview pane: the pre-rendered lines, scrolled,
// with the Claude box and (in chat) the message line under them.
// previewPane is what's open, under a header: the thread's subject, or the
// chat with its avatar and who's in it.
func (m Model) previewPane(w, h int) []string {
	s := m.styles
	inner := w - 4
	head := s.muted.Render("Nothing open")
	if _, ok := m.curOutbox(); ok {
		head = s.peach.Bold(true).Render("Outbox")
	}
	if t, ok := m.curThread(); ok {
		head = s.bold.Render(t.Subject)
		if t.Count > 1 {
			head += s.muted.Render(fmt.Sprintf(" · %d messages", t.Count))
		}
	} else if sp, ok := m.curSpace(); ok {
		name := cli.SpaceTitle(sp)
		head = m.avatar(name) + " " + s.bold.Render(name) + " " + s.muted.Render(m.spaceMeta(sp))
	}
	aiLines := m.aiLines(inner)
	var bottom []string
	if sp, ok := m.curSpace(); ok {
		rows := max(1, min(8, (h-2)/3))
		if m.mode == modeChatMsg {
			// one frame: what it replies to, the files, then the text (the
			// keys are in the bar below: no second hint here)
			var top []string
			tw := m.chatBoxWidth(inner)
			if r := m.replyTo; r != nil {
				who := r.SenderName
				if r.Mine {
					who = "your message"
				}
				line := s.accent.Render("▍↩ replying to ") + s.bold.Render(who) + s.muted.Render(": "+oneLine(r.Text))
				top = append(top, ansi.Truncate(line, tw-10, "…")+s.muted.Render("  esc drops"))
			}
			if m.editingMsg != "" {
				top = append(top, s.peach.Render("✎ editing your message")+s.muted.Render(" · enter saves · esc"))
			}
			if len(m.chatFiles) > 0 {
				top = append(top, m.attachedLines(tw)...)
			}
			if m.rec != nil {
				top = append(top, m.recordingLine())
			}
			bottom = m.chatBoxLines(inner, rows, true, "Message "+cli.SpaceTitle(sp)+"…", "", top...)
		} else {
			hint := ""
			if m.a.ChatAppMissing.Load() && !m.wa {
				hint = s.yellow.Render("! ") + s.muted.Render("read and unread stay on this machine until Chat API → Configuration is filled in (narada doctor)")
			}
			bottom = m.chatBoxLines(inner, 1, false, "Message "+cli.SpaceTitle(sp)+"…  (i)", hint)
		}
	}
	if m.tagStart >= 0 && m.mode == modeChatMsg {
		bottom = append(m.tagBox(min(inner, max(30, inner*2/3))), bottom...)
	}
	avail := h - 2 - len(aiLines) - len(bottom) // the header and its rule
	if avail < 3 {
		aiLines = aiLines[:max(0, len(aiLines)-(3-avail))]
		avail = 3
	}
	lines := m.preview
	scroll := max(0, min(m.scroll, len(lines)-avail))
	end := min(len(lines), scroll+avail)
	view := append([]string{}, lines[scroll:end]...)
	for len(view) < avail {
		view = append(view, "")
	}
	if max := len(lines) - avail; max > 0 {
		head += s.muted.Render(fmt.Sprintf("  %d%%", scroll*100/max))
	}
	bar := s.muted.Render("  ")
	if m.focus == 1 || m.mode == modeChatMsg {
		bar = fg(m.partColor()).Render("▎ ")
	}
	out := []string{fitTerm(bar+head, w), fitTerm(" "+s.muted.Faint(true).Render(strings.Repeat("─", max(1, w-3))), w)}
	for _, l := range view {
		out = append(out, fitTerm("  "+l, w))
	}
	for _, l := range aiLines {
		out = append(out, fitTerm("  "+l, w))
	}
	for _, l := range bottom {
		out = append(out, fitTerm("  "+l, w))
	}
	for len(out) < h {
		out = append(out, strings.Repeat(" ", w))
	}
	return out[:h]
}

// spaceMeta: what kind of chat it is and how many are in it.
func (m Model) spaceMeta(sp store.Space) string {
	switch {
	case sp.Type == store.TypeStatus:
		return "· status updates from the last day"
	case sp.Type == "DIRECT_MESSAGE" && sp.Account == store.WhatsAppAccount && strings.HasSuffix(sp.Name, "@s.whatsapp.net"):
		return "· +" + strings.TrimSuffix(sp.Name, "@s.whatsapp.net")
	case sp.Type == "DIRECT_MESSAGE":
		return ""
	case sp.Members > 0:
		return fmt.Sprintf("· %d people", sp.Members)
	}
	return ""
}

// dropMediaLabel leaves a WhatsApp message's caption once its photo or
// GIF shows: "📷 Photo: the curtains" → "the curtains".
func dropMediaLabel(text string) string {
	for _, l := range []string{"📷 Photo", "GIF", "Sticker", "🎬 Video"} {
		if text == l {
			return ""
		}
		if strings.HasPrefix(text, l+": ") {
			return strings.TrimPrefix(text, l+": ")
		}
	}
	return text
}
