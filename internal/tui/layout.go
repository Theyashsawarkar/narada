package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/store"
)

// ------------------------------------------------------------ text widths
//
// kitty puts a whole Indian syllable (का, स्क) in one cell; tmux gave the
// vowel sign and each joined consonant cells of their own, so a row with
// Hindi was drawn short in kitty and everything after it slid left. Vayu's
// tmux.conf gives vowel signs no cell (codepoint-widths); for joined
// consonants narada adds a ZWNJ after the halant, which splits them in both.
// Then a row's real width is its wcwidth, and fitTerm pads to that.

// viramas are the halants of the Indian scripts, Devanagari to Sinhala.
var viramas = map[rune]bool{'्': true, '্': true, '੍': true, '્': true, '୍': true,
	'்': true, '్': true, '್': true, '്': true, '්': true}

const zwnj = '‌'

// termText splits joined consonants with a ZWNJ, so tmux and kitty give
// them the same cells. Only for drawing: copies and sends keep the text.
func termText(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return viramas[r] }) {
		return s
	}
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		b.WriteRune(r)
		if viramas[r] && i+1 < len(rs) && unicode.IsLetter(rs[i+1]) {
			b.WriteRune(zwnj)
		}
	}
	return b.String()
}

// fitTerm makes a line exactly w cells wide on the terminal: cut or padded
// by narada's count, then padded again for what the terminal counts
// narrower (Indian vowel signs).
func fitTerm(s string, w int) string {
	s = termText(fit(s, w))
	if d := w - ansi.StringWidthWc(s); d > 0 {
		s += strings.Repeat(" ", d)
	}
	return s
}

// wideScreen is what the renderer is told the screen's width is: it cuts
// lines by its own count, which is wider than the terminal's for Indian
// scripts, so it must never cut. narada lays out to the real width itself.
const wideScreen = 4096

// widenRenderer follows a real resize with one the renderer reads as
// wideScreen; the model ignores it.
func widenRenderer(h int) tea.Cmd {
	return func() tea.Msg { return tea.WindowSizeMsg{Width: wideScreen, Height: h} }
}

// ------------------------------------------------------------- the places

// place is one of the top bar's places: Mail, Chat, WhatsApp, AI requests.
type place struct {
	key, name, icon, color string
	n                      int
	on                     bool
}

func (m Model) places() []place {
	s := m.styles
	ai := len(m.waitingOf("mail")) + len(m.waitingOf("chat")) + len(m.waitingOf("whatsapp"))
	return []place{
		{"1", "Mail", "󰇮", s.c.Blue, m.counts[m.sections[0].Name], m.tab == tabMail && !m.inAISection()},
		{"2", "Chat", "󰭹", s.c.Teal, m.chatN, m.tab == tabChat && !m.wa && !m.inAISection()},
		{"3", "WhatsApp", "󰖣", s.c.Green, m.waN, m.wa && !m.inAISection()},
		{"4", "AI requests", "✦", s.c.Peach, ai, m.inAISection()},
	}
}

// partColor is the colour of the place you're in; its pill, the section
// strip, the picked row's bar and the message box use it.
func (m Model) partColor() string {
	s := m.styles
	switch {
	case m.inAISection():
		return s.c.Peach
	case m.tab == tabMail:
		return s.c.Blue
	case m.wa:
		return s.c.Green
	}
	return s.c.Teal
}

// topBar: the places with their counts on the left, who you are on the
// right. Small screens show the places as icons and counts.
func (m Model) topBar() string {
	s := m.styles
	small := m.w < 110
	var parts []string
	for _, p := range m.places() {
		label := p.name
		if small && !p.on {
			label = ""
		}
		if s.nerd || small {
			label = strings.TrimSpace(p.icon + " " + label)
		}
		if p.on {
			if p.n > 0 {
				label += fmt.Sprintf(" %d", p.n)
			}
			pill := lipgloss.NewStyle().Foreground(lipgloss.Color(s.c.Crust)).Background(lipgloss.Color(p.color)).Bold(true).Padding(0, 1)
			parts = append(parts, s.round(pill, label))
			continue
		}
		cell := fg(p.color).Render(label)
		if !s.nerd && !small {
			cell = s.sub.Render(label)
		}
		if p.n > 0 {
			cell += " " + fg(p.color).Bold(true).Render(fmt.Sprint(p.n))
		}
		parts = append(parts, " "+cell+" ")
	}
	left := " " + strings.Join(parts, " ")
	right := m.whoAmI(small)
	if m.syncing {
		right = s.muted.Render("syncing… ") + right
	}
	if m.launch == "demo" {
		right = s.peach.Render("demo · example mail  ") + right
	}
	if !m.older.Done && m.older.Total > m.older.Have && !small {
		const n = 8
		filled := min(n, m.older.Have*n/max(1, m.older.Total))
		bar := s.accent.Render(strings.Repeat("▰", filled)) + s.muted.Render(strings.Repeat("▱", n-filled))
		right = s.muted.Render("older mail ") + bar + s.muted.Render(fmt.Sprintf(" %s/%s   ", thousands(m.older.Have), thousands(m.older.Total))) + right
	}
	return row(left, right+" ", m.w)
}

// whoAmI: your avatar and name for the place you're in: your WhatsApp
// name, your Google name, else the address or the number.
func (m Model) whoAmI(small bool) string {
	s := m.styles
	name, where := m.myName()
	dot := s.green.Render("●")
	if m.err != "" {
		dot = s.red.Render("●")
	}
	if small {
		return dot + " " + s.bold.Render(ansi.Truncate(name, 18, "…"))
	}
	out := dot + " " + m.avatar(name) + " " + s.bold.Render(ansi.Truncate(name, 28, "…"))
	if where != "" && where != name {
		out += s.muted.Render(" · " + ansi.Truncate(where, 28, "…"))
	}
	return out
}

// myName is who you are where you are, and the account it is.
func (m Model) myName() (name, where string) {
	if m.wa {
		wa, _ := m.a.Store.WhatsApp()
		return firstNonEmpty(m.nameOf(store.WhatsAppAccount), wa.Email, "WhatsApp"), "WhatsApp"
	}
	acct := ""
	if f := m.accountFilter(); len(f) > 0 {
		acct = f[0]
	} else if len(m.accounts) == 1 {
		acct = m.accounts[0]
	}
	if acct == "" {
		return "All accounts", ""
	}
	email := acct
	if a, err := m.a.Store.Account(acct); err == nil && a.Email != "" {
		email = a.Email
	}
	return firstNonEmpty(m.nameOf(acct), email), acct + " · " + email
}

// nameOf is your name on an account, as the app knows it; "" until it does.
func (m Model) nameOf(acct string) string {
	if n, ok := any(m.a).(interface{ MyName(string) string }); ok {
		return n.MyName(acct)
	}
	if a, err := m.a.Store.Account(acct); err == nil && a.ChatUser != "" {
		if name, _, ok := m.a.Store.User(acct, a.ChatUser); ok {
			return name
		}
	}
	return ""
}

// ----------------------------------------------------------- the sections

// sectionStrip: the place's sections as one row of tabs, the current one
// filled in the place's colour, counts coloured by what they count.
func (m Model) sectionStrip() string {
	s := m.styles
	col := m.partColor()
	var parts []string
	add := func(label string, n int, ncol string, on bool) {
		if on {
			if n > 0 {
				label += fmt.Sprintf(" %d", n)
			}
			pill := lipgloss.NewStyle().Foreground(lipgloss.Color(s.c.Crust)).Background(lipgloss.Color(col)).Bold(true).Padding(0, 1)
			parts = append(parts, s.round(pill, label))
			return
		}
		cell := s.sub.Render(label)
		if n > 0 {
			cell += " " + fg(ncol).Bold(true).Render(fmt.Sprint(n))
		}
		parts = append(parts, " "+cell+" ")
	}
	switch {
	case m.tab == tabMail:
		for i, sec := range m.sections {
			ncol := col
			if sec.Name == "Unread" {
				ncol = s.c.Red
			}
			add(sec.Name, m.counts[sec.Name], ncol, i == m.sec && m.search == nil)
		}
		if m.search != nil {
			add("⌕ "+m.search.Name, 0, col, true)
		}
	case m.wa && !m.waLinked:
		parts = append(parts, s.muted.Render(" WhatsApp isn't linked yet: L links it with a QR code, P with a code"))
	default:
		unread := m.chatN
		if m.wa {
			unread = m.waN
		}
		for i, name := range chatSections {
			if (name == "Archived" || name == "Status") && !m.wa {
				continue
			}
			label, n, ncol := name, 0, col
			switch {
			case name == "Unread":
				n, ncol = unread, s.c.Red
			case name == "DMs" && m.wa:
				label = "People"
			case name == "Spaces" && m.wa:
				label = "Groups"
			case name == "Scheduled":
				n, ncol = m.chatScheduled, s.c.Sky
			case name == aiSection:
				n, ncol = len(m.waitingOf(m.aiKind())), s.c.Peach
			case name == "Status":
				n = m.statusN
			}
			add(label, n, ncol, i == m.sec)
		}
	}
	left := " " + strings.Join(parts, "")
	hint := s.keycap.Render("[ ]") + s.muted.Render(" sections")
	if m.w < 110 {
		hint = ""
	}
	return row(left, hint+" ", m.w)
}

// ------------------------------------------------------------ the footer

// modeLook: the name the bar shows for a mode, and its colour. Vim's
// names where Vim has one.
func (m Model) modeLook() (string, string) {
	s := m.styles
	switch {
	case m.leader:
		return "LEADER", s.c.Mauve
	case m.visual:
		return "VISUAL", s.c.Peach
	}
	switch m.mode {
	case modeNormal:
		if m.pickingMsg() {
			return "PICK", m.partColor()
		}
		return "NORMAL", m.partColor()
	case modeChatMsg, modeCompose:
		return "INSERT", s.c.Mauve
	case modeFind:
		return "FIND", s.c.Mauve
	case modeCommand:
		return "COMMAND", s.c.Sky
	case modeFilter, modeSearch:
		return "SEARCH", s.c.Mauve
	case modeHelp:
		return "KEYS", s.c.Mauve
	case modeApprove:
		return "APPROVE", s.c.Peach
	case modeReview:
		return "REVIEW", s.c.Mauve
	case modeView:
		return "PICTURE", m.partColor()
	case modeReact:
		return "REACT", s.c.Yellow
	}
	names := map[mode]string{modeAsk: "ASK", modeSaveName: "SAVE", modeDraftHint: "DRAFT", modePhone: "LINK", modeLink: "LINK",
		modeRename: "RENAME", modePick: "FILES", modeWhen: "LATER", modeConvo: "NEW CHAT", modeForward: "FORWARD", modeVote: "VOTE"}
	return firstNonEmpty(names[m.mode], "NORMAL"), s.c.Mauve
}

// footerView: the mode in its colour, the few keys that matter now, and
// where the rest are.
func (m Model) footerView() string {
	s := m.styles
	name, col := m.modeLook()
	pill := lipgloss.NewStyle().Foreground(lipgloss.Color(s.c.Crust)).Background(lipgloss.Color(col)).Bold(true).Padding(0, 1)
	left := " " + s.round(pill, name)
	for _, k := range m.barKeys() {
		left += "  " + s.keycap.Render(k[0]) + s.sub.Render(" "+k[1])
	}
	right := ""
	switch {
	case m.toast != "":
		right = s.green.Render(m.toast)
	case m.mode == modeNormal && !m.leader && !m.visual:
		right = s.keycap.Render("/") + s.muted.Render(" find  ") + s.keycap.Render("␣") + s.muted.Render(" menu  ") + s.keycap.Render("?") + s.muted.Render(" keys")
	}
	if rw := lipgloss.Width(right); rw > m.w/2 {
		right = ansi.Truncate(right, m.w/2, "…")
	}
	return row(ansi.Truncate(left, max(10, m.w-lipgloss.Width(right)-2), ""), right+" ", m.w)
}

// barKeys are the keys the bar shows for what you're doing now.
func (m Model) barKeys() [][2]string {
	if m.leader {
		return [][2]string{{"a key", "from the menu"}, {"esc", "never mind"}}
	}
	if m.visual {
		return [][2]string{{"j/k", "pick more"}, {"d", "archive"}, {"m/M", "read/unread"}, {"y", "copy"}, {"esc", "stop"}}
	}
	switch m.mode {
	case modeReview:
		if m.review != nil && m.review.kind == "chat" {
			return [][2]string{{"y", "send"}, {"t", "later"}, {"e", "edit"}, {"+", "file"}, {"-", "drop file"}, {"n", "discard"}}
		}
		return [][2]string{{"y", "send"}, {"t", "later"}, {"e", "edit"}, {"+", "file"}, {"-", "drop file"}, {"d", "save draft"}, {"n", "discard"}}
	case modeApprove:
		return [][2]string{{"y", "send"}, {"e", "edit first"}, {"n", "decline"}, {"q", "decide later"}}
	case modeConvo:
		return [][2]string{{"↑↓", "pick"}, {"tab", "add person"}, {"enter", "open"}, {"esc", "cancel"}}
	case modeWhen:
		return [][2]string{{"↑↓", "pick"}, {"enter", "schedule"}, {"esc", "cancel"}}
	case modePick:
		return [][2]string{{"↑↓", "move"}, {"tab", "pick several"}, {"enter", "add"}, {"esc", "close"}}
	case modeFind:
		return [][2]string{{"↑↓", "pick"}, {"enter", "do it"}, {"tab", "where"}, {"esc", "close"}}
	case modeCommand:
		return [][2]string{{"tab", "complete"}, {"↑", "last ones"}, {"enter", "run"}, {"esc", "cancel"}}
	case modeCompose:
		if m.comp != nil && m.comp.approveID > 0 {
			return [][2]string{{"tab", "next field"}, {"ctrl+s", "keep changes"}, {"ctrl+e", "$EDITOR"}, {"esc", "back"}}
		}
		keys := [][2]string{{"tab", "next field"}, {"ctrl+s", "send"}, {"ctrl+t", "send later"}, {"ctrl+o", "attach"}, {"ctrl+e", "$EDITOR"}, {"esc", "close"}}
		if m.comp != nil && m.comp.reply {
			keys = append(keys, [2]string{"pgup/dn", "original"})
		}
		return keys
	case modeFilter, modeAsk, modeSearch, modeSaveName, modeDraftHint, modeLink, modeRename, modePhone, modeForward, modeVote:
		return [][2]string{{"enter", "done"}, {"esc", "cancel"}, {"ctrl+u", "clear"}}
	case modeView:
		return [][2]string{{"h/l", "previous/next"}, {"gf", "open in its app"}, {"esc", "back"}}
	case modeChatMsg:
		if m.tagStart >= 0 {
			return [][2]string{{"↑↓", "pick"}, {"enter/tab", "tag them"}, {"esc", "no tag"}}
		}
		if m.editingMsg != "" {
			return [][2]string{{"enter", "save the edit"}, {"esc", "cancel"}}
		}
		return [][2]string{{"enter", "send"}, {"esc", "normal mode"}, {"alt+enter", "new line"}, {"ctrl+o", "file"}, {"ctrl+t", "later"}}
	case modeHelp:
		return [][2]string{{"j/k", "scroll"}, {"esc", "close"}}
	}
	switch {
	case m.pickingMsg() && m.isStatus():
		return [][2]string{{"j/k", "pick"}, {"r", "reply"}, {"e", "react"}, {"gf", "open"}, {"esc", "back"}}
	case m.pickingMsg():
		return [][2]string{{"j/k", "pick"}, {"r", "reply"}, {"e", "react"}, {"E", "edit"}, {"yy", "copy"}, {"esc", "back"}}
	case m.inAISection():
		return [][2]string{{"j/k", "pick"}, {"y", "send"}, {"e", "edit first"}, {"n", "decline"}}
	case m.focus == 1 && m.tab == tabMail:
		return [][2]string{{"j/k", "scroll"}, {"r", "reply"}, {"R", "reply all"}, {"za", "fold"}, {"h", "list"}}
	case m.focus == 1:
		return [][2]string{{"i", "write"}, {"j/k", "pick a message"}, {"h", "list"}}
	case m.tab == tabMail:
		return [][2]string{{"j/k", "move"}, {"enter", "open"}, {"r", "reply"}, {"dd", "archive"}, {"c", "write"}}
	}
	return [][2]string{{"j/k", "move"}, {"enter", "open"}, {"i", "write"}, {"r", "reply"}}
}

// isStatus: the chat open is WhatsApp's status updates.
func (m Model) isStatus() bool {
	sp, ok := m.curSpace()
	return ok && sp.Type == store.TypeStatus
}
