package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/config"
	"github.com/Theyashsawarkar/narada/internal/render"
	"github.com/Theyashsawarkar/narada/internal/store"
)

type styles struct {
	c                                             config.Colors
	text, sub, muted, accent, accent2, bold, warn lipgloss.Style
	green, blue, yellow, red, peach               lipgloss.Style
	sky, teal, pink, mauve, lavender              lipgloss.Style
	tabOn, tabOff, sel, pill, keycap, banner      lipgloss.Style
	nerd                                          bool // round ends from Nerd Font half circles
}

// round renders a filled label with round ends (Nerd Font half circles
// in the fill colour) instead of square ones; plain when there's no
// Nerd Font.
func (s styles) round(st lipgloss.Style, text string) string {
	if !s.nerd {
		return st.Render(text)
	}
	cap := lipgloss.NewStyle().Foreground(st.GetBackground())
	return cap.Render("\ue0b6") + st.UnsetPadding().Render(text) + cap.Render("\ue0b4")
}

func newStyles(c config.Colors, nerd bool) styles {
	fg := func(col string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(col)) }
	return styles{
		c: c, text: fg(c.Text), sub: fg(c.Subtle), muted: fg(c.Muted), accent: fg(c.Accent), accent2: fg(c.Accent2),
		sky: fg(c.Sky), teal: fg(c.Teal), pink: fg(c.Pink), mauve: fg(c.Mauve), lavender: fg(c.Lavender),
		bold: fg(c.Text).Bold(true), warn: fg(c.Peach), green: fg(c.Green), blue: fg(c.Blue), yellow: fg(c.Yellow), red: fg(c.Red), peach: fg(c.Peach),
		tabOn:  lipgloss.NewStyle().Foreground(lipgloss.Color(c.Base)).Background(lipgloss.Color(c.Accent)).Bold(true).Padding(0, 1),
		tabOff: fg(c.Muted).Padding(0, 1),
		sel:    lipgloss.NewStyle().Background(lipgloss.Color(c.Surface)),
		pill:   lipgloss.NewStyle().Foreground(lipgloss.Color(c.Base)).Background(lipgloss.Color(c.Accent)).Bold(true).Padding(0, 1),
		keycap: lipgloss.NewStyle().Foreground(lipgloss.Color(c.Text)).Background(lipgloss.Color(c.Surface)).Padding(0, 1),
		banner: lipgloss.NewStyle().Foreground(lipgloss.Color(c.Base)).Background(lipgloss.Color(c.Peach)).Bold(true).Padding(0, 1),
		nerd:   nerd,
	}
}

func (m Model) renamePrompt() string {
	sp, ok := m.curSpace()
	if !ok {
		return "name: "
	}
	switch {
	case sp.Account == store.WhatsAppAccount && sp.Type == "GROUP_CHAT":
		return "rename the group on WhatsApp (everyone sees it): "
	case sp.Account == store.WhatsAppAccount:
		return "name this chat (only here; empty: back to the default): "
	}
	switch sp.Type {
	case "SPACE":
		return "rename on Google Chat (everyone sees it): "
	case "GROUP_CHAT":
		return "name this group on Google Chat (everyone sees it; it becomes a space): "
	}
	return "name this DM (only here; empty: back to the default): "
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func (m Model) listWidth() int { return max(34, min(60, m.w*36/100)) }

func (m Model) bodyHeight() int {
	h := m.h - 3 // the places, the sections, the footer
	if m.err != "" {
		h--
	}
	if m.inputMode() {
		h--
	}
	return max(5, h)
}

func (m Model) previewHeight() int { return m.bodyHeight() - 2 }

func (m Model) inputMode() bool {
	switch m.mode {
	case modeFilter, modeAsk, modeSearch, modeSaveName, modeDraftHint, modeLink, modeRename, modePhone, modeForward, modeVote, modeCommand:
		return true
	}
	return false
}

func (m Model) View() string {
	if m.w == 0 {
		return ""
	}
	rows := []string{m.topBar(), m.sectionStrip()}
	bh := m.bodyHeight()
	// under a popup, draw what it was opened over
	under := m.mode
	switch {
	case m.mode == modeWhen && m.when != nil:
		under = m.when.back
	case m.mode == modePick && m.pick != nil:
		under = m.pickMode
	case m.mode == modeConvo && m.convo != nil:
		under = m.convo.back
	}
	var body []string
	switch {
	case m.mode == modeView && m.viewer != nil:
		body = m.pane(m.w, bh, m.viewerTitle(), m.viewerView(m.w-4, bh-1))
	case m.tab == tabChat && m.wa && !m.waLinked:
		body = m.pane(m.w, bh, "WhatsApp · not linked", m.linkPanel(m.w-2, bh-1))
	case under == modeCompose && m.comp != nil:
		body = strings.Split(m.composerPanes(bh), "\n")
	case m.narrow():
		// one pane at a time: the list, or what's open
		if m.focus == 0 && under != modeReview && under != modeApprove {
			body = m.pane(m.w, bh, m.listTitle(), m.listView(m.w-2, bh-1))
		} else {
			body = m.rightPane(under, m.w, bh)
		}
	default:
		lw := m.listWidth()
		left := m.pane(lw, bh, m.listTitle(), m.listView(lw-2, bh-1))
		right := m.rightPane(under, m.w-lw, bh)
		for i := range left {
			body = append(body, left[i]+right[i])
		}
	}
	switch {
	case m.mode == modeFind && m.find != nil:
		body = strings.Split(overlay(strings.Join(body, "\n"), m.finderView(), m.w), "\n")
	case m.mode == modeHelp:
		box := m.helpPopup()
		body = strings.Split(overlayAt(strings.Join(body, "\n"), box, m.w, max(0, (bh-len(box))/2)), "\n")
	case m.mode == modeReact:
		box := m.reactPopup()
		body = strings.Split(overlayAt(strings.Join(body, "\n"), box, m.w, max(0, bh/2-2)), "\n")
	case m.mode == modeConvo && m.convo != nil:
		box := m.convoPopup()
		body = strings.Split(overlayAt(strings.Join(body, "\n"), box, m.w, max(0, (bh-len(box))/3)), "\n")
	case m.mode == modeWhen && m.when != nil:
		box := m.whenPopup()
		body = strings.Split(overlayAt(strings.Join(body, "\n"), box, m.w, max(0, (bh-len(box))/2)), "\n")
	case m.mode == modePick && m.pick != nil:
		box := m.pickerPopup()
		body = strings.Split(overlayAt(strings.Join(body, "\n"), box, m.w, max(0, (bh-len(box))/2)), "\n")
	case m.leader && m.leaderShown:
		box := m.leaderMenu()
		body = strings.Split(overlayAt(strings.Join(body, "\n"), box, m.w, max(0, bh-len(box))), "\n")
	}
	rows = append(rows, body...)
	if m.err != "" {
		e := m.styles.red.Render("● " + m.err)
		rows = append(rows, " "+ansi.Truncate(e, m.w-14, "…")+m.styles.muted.Render("  esc hides"))
	}
	if m.inputMode() {
		rows = append(rows, m.inputView())
	}
	rows = append(rows, m.footerView())
	for i, r := range rows {
		rows[i] = fitTerm(r, m.w)
	}
	return strings.Join(rows, "\n")
}

// previewWidth is how wide what's open is drawn: beside the list, or the
// whole screen when there's room for only one pane.
func (m Model) previewWidth() int {
	if m.narrow() {
		return m.w - 4
	}
	return m.w - m.listWidth() - 4
}

// narrow: too small for the list and what's open side by side.
func (m Model) narrow() bool { return m.w < 100 }

// rightPane is what's open: a thread, a chat, an AI request or a review.
func (m Model) rightPane(under mode, w, h int) []string {
	switch under {
	case modeReview:
		return m.pane(w, h, "review", m.reviewView(w-4, h-1))
	case modeApprove:
		if it, ok := m.curWaiting(); ok {
			return strings.Split(m.aiCard(it, w, h), "\n")
		}
	default:
		if it, ok := m.curOutbox(); ok && it.Status == store.StatusApproval && m.inAISection() {
			return strings.Split(m.aiCard(it, w, h), "\n")
		}
	}
	return m.previewPane(w, h)
}

// pane lays out a pane w wide and h tall: a title line in the place's
// colour, then its lines, each exactly w cells. No border: nothing is drawn
// to the right of text (see fitTerm).
func (m Model) pane(w, h int, title, content string) []string {
	s := m.styles
	out := []string{fitTerm(" "+fg(m.partColor()).Bold(true).Render(ansi.Truncate(title, max(1, w-3), "…")), w)}
	lines := strings.Split(content, "\n")
	for i := 0; i < h-1; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, fitTerm(" "+l, w))
	}
	_ = s
	return out
}

// box draws a rounded border with a title in it.
func (m Model) box(w, h int, title, content string, focus bool, color string) string {
	s := m.styles
	bc := s.c.Border
	if focus {
		bc = color
	}
	b := lipgloss.NewStyle().Foreground(lipgloss.Color(bc))
	tstyle := s.muted
	if focus {
		tstyle = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true)
	}
	inner := w - 2
	t := ansi.Truncate(" "+title+" ", max(0, inner-2), "…")
	top := b.Render("╭─") + tstyle.Render(t) + b.Render(strings.Repeat("─", max(0, inner-1-lipgloss.Width(t)))+"╮")
	lines := strings.Split(content, "\n")
	var out []string
	out = append(out, top)
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		l = ansi.Truncate(l, inner, "…")
		out = append(out, b.Render("│")+l+strings.Repeat(" ", max(0, inner-lipgloss.Width(l)))+b.Render("│"))
	}
	out = append(out, b.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(out, "\n")
}

func (m Model) listTitle() string {
	if m.tab == tabMail {
		n := len(m.threads)
		t := fmt.Sprintf("%s · %d", m.currentSection().Name, n)
		if m.filter != "" {
			t += " · /" + m.filter
		}
		if len(m.selected) > 0 {
			t += fmt.Sprintf(" · %d selected", len(m.selected))
		}
		return t
	}
	t := fmt.Sprintf("%s · %d chat%s", chatSections[m.sec%len(chatSections)], len(m.spaces), plural(len(m.spaces)))
	if m.filter != "" {
		t += " · /" + m.filter
	}
	if len(m.selected) > 0 {
		t += fmt.Sprintf(" · %d picked", len(m.selected))
	}
	return t
}

func (m Model) aiLines(w int) []string {
	if !m.aiBusy && m.ai == "" {
		return nil
	}
	s := m.styles
	text := m.ai
	if m.aiBusy {
		text = s.muted.Render("thinking…")
	}
	head := s.accent2.Bold(true).Render(m.aiTitle)
	lines := []string{s.accent2.Render(strings.Repeat("┄", min(w, 60))), head}
	body := wrap(text, w-2)
	if len(body) > 14 {
		body = append(body[:14], s.muted.Render("… (Esc closes)"))
	}
	for _, l := range body {
		lines = append(lines, s.text.Render(l))
	}
	return lines
}

// helpView lists every key by group, in two columns when one is too tall.

func stripANSI(s string) string { return ansi.Strip(s) }

func (m Model) reviewView(w, h int) string {
	r := m.review
	if r == nil {
		return ""
	}
	return m.outgoingView(r.kind, r.out, "", w)
}

func (m Model) outgoingView(kind string, out store.Outgoing, note string, w int) string {
	s := m.styles
	var lines []string
	if note != "" {
		lines = append(lines, s.muted.Render(note), "")
	}
	field := func(k, v string) {
		if v != "" {
			lines = append(lines, s.muted.Render(fmt.Sprintf("%-8s", k))+s.bold.Render(v))
		}
	}
	if chatKind(kind) {
		field("To", firstNonEmpty(out.SpaceName, out.Space))
		if out.Thread != "" {
			field("Thread", "replying in the latest thread")
		}
	} else {
		field("To", strings.Join(out.To, ", "))
		field("Cc", strings.Join(out.Cc, ", "))
		field("Bcc", strings.Join(out.Bcc, ", "))
		field("Subject", out.Subject)
	}
	for _, f := range out.Files {
		name, size := f.Name, f.Size
		if name == "" { // not copied yet: what's on disk
			name = filepath.Base(f.Source)
			if st, err := os.Stat(app.ExpandPath(f.Source)); err == nil {
				size = st.Size()
			} else {
				name += " (not found)"
			}
		}
		info := ""
		if size > 0 {
			info = "  " + app.HumanSize(size)
		}
		if dir := filepath.Dir(f.Source); dir != "." {
			info += "  in " + tilde(dir)
		}
		lines = append(lines, ansi.Truncate(s.muted.Render(fmt.Sprintf("%-8s", "File"))+s.peach.Render("📎 "+name)+s.muted.Render(info), w, "…"))
	}
	lines = append(lines, s.muted.Render(strings.Repeat("─", min(w, 60))))
	if strings.TrimSpace(out.HTML) != "" { // as it will show: its tables and formatting
		lines = append(lines, render.HTML(out.HTML, w, renderStylesOf(s), true).Lines...)
		return strings.Join(lines, "\n")
	}
	body := out.Body
	if strings.Contains(kind, "whatsapp") || !strings.Contains(out.Space, "spaces/") && chatKind(kind) {
		body = m.waTagged(body) // WhatsApp's tags by name (Google Chat spaces are "spaces/…")
	}
	for _, l := range wrap(body, w) {
		if strings.HasPrefix(l, ">") {
			lines = append(lines, s.muted.Render(l))
		} else {
			lines = append(lines, s.text.Render(l))
		}
	}
	return strings.Join(lines, "\n")
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func (m Model) inputView() string {
	s := m.styles
	if m.mode == modeCommand {
		return m.commandView()
	}
	prompt := map[mode]string{modeFilter: "/", modeAsk: "@claude ", modeSearch: "search gmail: ", modeSaveName: "section name (Enter: the search itself): ",
		modeDraftHint: "draft a reply that says (Enter for Claude's pick): ", modeLink: fmt.Sprintf("open link (1-%d): ", len(m.links)), modeRename: m.renamePrompt(),
		modePhone: "your WhatsApp number, with its country code: ", modeForward: "forward to (a contact, a group or a number): ",
		modeVote: "vote for (option numbers: 2, or 1,3 when it takes several; empty takes your vote back): "}[m.mode]
	cur := m.cursor()
	under, rest := " ", ""
	if cur < len(m.input) {
		under, rest = string(m.input[cur]), string(m.input[cur+1:])
	}
	return " " + s.accent2.Bold(true).Render(prompt) + s.text.Render(string(m.input[:cur])) + lipgloss.NewStyle().Reverse(true).Render(under) + s.text.Render(rest)
}

// nextTab: where Tab goes from here.
func (m Model) nextTab() string {
	switch {
	case m.tab == tabMail:
		return "chat"
	case !m.wa:
		return "whatsapp"
	}
	return "mail"
}
