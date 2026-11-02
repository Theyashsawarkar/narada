package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/cli"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// Approve is `narada approve` (and the popup an agent's send opens): one
// card per waiting send, exactly who gets what, answered with a key.
func Approve(a *app.App, items []store.OutboxItem) error {
	m := approver{a: a, items: items, styles: newStyles(a.Config.Colors, a.Config.NerdFont)}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type approver struct {
	a      *app.App
	styles styles
	items  []store.OutboxItem
	i      int
	out    *store.Outgoing // edited, not saved yet
	scroll int
	w, h   int
	done   string // what happened to the current one, shown a moment
	err    string
	path   string // the file the editor has open
}

type approveNext struct{}

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
		m.done, m.out, m.scroll = "", nil, 0
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
			m.out = &out
		}
	case tea.KeyMsg:
		if m.done != "" {
			return m, nil
		}
		it := m.cur()
		switch msg.String() {
		case "y", "Y", "enter":
			if err := m.a.Approve(it.ID, m.out); err != nil {
				m.err = err.Error()
				return m, nil
			}
			wait := int(time.Until(time.Now().Add(m.a.UndoWindow(it.Kind))).Seconds() + 0.5)
			m.done = "✓ approved"
			if wait > 0 {
				m.done += fmt.Sprintf(" · goes in %ds (narada outbox cancel %d stops it)", wait, it.ID)
			}
			return m, tea.Tick(1200*time.Millisecond, func(time.Time) tea.Msg { return approveNext{} })
		case "n", "N":
			if err := m.a.Decline(it.ID); err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.done = "✗ declined · nothing was sent"
			return m, tea.Tick(900*time.Millisecond, func(time.Time) tea.Msg { return approveNext{} })
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
		case "j", "down":
			m.scroll++
		case "k", "up":
			m.scroll = max(0, m.scroll-1)
		}
	}
	return m, nil
}

// kindLook: the name, icon and colour of a kind of send.
func (s styles) kindLook(kind string) (name, icon, color string) {
	switch kind {
	case "whatsapp":
		return "WhatsApp message", "󰖣", s.c.Green
	case "chat":
		return "Google Chat message", "󰭹", s.c.Accent2
	}
	return "mail", "󰇮", s.c.Accent
}

func (m approver) View() string {
	if m.w == 0 || m.i >= len(m.items) {
		return ""
	}
	s := m.styles
	it, out := m.cur(), m.message()
	w := min(m.w-4, 100)
	name, icon, color := s.kindLook(it.Kind)
	if !s.nerd {
		icon = ""
	}
	// one sentence: who wants to send what to whom, and why it asks
	who := firstNonEmpty(out.Agent, "An agent")
	to := firstNonEmpty(out.SpaceName, out.Space)
	if !chatKind(it.Kind) {
		to = strings.Join(out.To, ", ")
	}
	pill := s.round(s.pill.Background(lipgloss.Color(color)), strings.TrimSpace(icon+" "+name))
	count := ""
	if len(m.items) > 1 {
		count = s.muted.Render(fmt.Sprintf("  %d of %d", m.i+1, len(m.items)))
	}
	head := pill + count
	head += strings.Repeat(" ", max(1, w-lipgloss.Width(head)-6)) + s.muted.Render(fmt.Sprintf("#%d", it.ID))
	sentence := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(who) + s.text.Render(" wants to send a "+name+" to ") +
		s.bold.Render(ansi.Truncate(to, max(10, w-lipgloss.Width(who)-len(name)-24), "…"))
	lines := []string{head, "", sentence}
	if it.Reason != "" {
		badge := lipgloss.NewStyle().Background(lipgloss.Color(s.c.Peach)).Foreground(lipgloss.Color(s.c.Base))
		lines = append(lines, s.round(badge, it.Reason))
	}
	if out.Why != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("▍")+s.sub.Italic(true).Render(" “"+ansi.Truncate(out.Why, w-8, "…")+"”"))
	}
	if m.out != nil {
		lines = append(lines, s.yellow.Render("✎ edited by you: y sends your version"))
	}
	lines = append(lines, "")
	field := func(k, v string) {
		if v != "" {
			lines = append(lines, s.muted.Render(fmt.Sprintf("  %-9s", k))+s.bold.Render(ansi.Truncate(v, w-12, "…")))
		}
	}
	if chatKind(it.Kind) {
		field("To", firstNonEmpty(out.SpaceName, out.Space))
		if out.Thread != "" {
			field("Thread", "replying in the thread")
		}
		if out.Quote != "" {
			field("Replying", "to a message (quoted)")
		}
	} else {
		field("From", it.Account)
		field("To", strings.Join(out.To, ", "))
		field("Cc", strings.Join(out.Cc, ", "))
		field("Bcc", strings.Join(out.Bcc, ", "))
		field("Subject", out.Subject)
	}
	if out.ScheduledFor > 0 {
		field("When", time.UnixMilli(out.ScheduledFor).Format("Mon 02 Jan 15:04"))
	}
	if len(out.Files) > 0 {
		var chips []string
		for _, f := range out.Files {
			n := firstNonEmpty(f.Name, filepath.Base(f.Source))
			chips = append(chips, s.keycap.Render("📎 "+n+" "+app.HumanSize(f.Size)))
		}
		lines = append(lines, s.muted.Render(fmt.Sprintf("  %-9s", "Files"))+strings.Join(chips, " "))
	}
	lines = append(lines, "")
	// the message, framed and scrollable
	bodyW := w - 6
	var body []string
	for _, para := range strings.Split(strings.TrimRight(out.Body, "\n"), "\n") {
		if para == "" {
			body = append(body, "")
			continue
		}
		body = append(body, strings.Split(lipgloss.NewStyle().Width(bodyW).Render(para), "\n")...)
	}
	room := max(3, m.h-len(lines)-8)
	scroll := min(m.scroll, max(0, len(body)-room))
	shown := body[scroll:min(len(body), scroll+room)]
	border := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	title := " message "
	if len(body) > room {
		title = fmt.Sprintf(" message · %d–%d of %d lines (j/k) ", scroll+1, scroll+len(shown), len(body))
	}
	lines = append(lines, "  "+border.Render("╭─")+s.muted.Render(title)+border.Render(strings.Repeat("─", max(0, bodyW+1-lipgloss.Width(title)))+"╮"))
	for _, l := range shown {
		lines = append(lines, "  "+border.Render("│ ")+s.text.Render(l)+strings.Repeat(" ", max(0, bodyW-lipgloss.Width(l)))+border.Render(" │"))
	}
	lines = append(lines, "  "+border.Render("╰"+strings.Repeat("─", bodyW+2)+"╯"), "")
	switch {
	case m.done != "":
		st := s.green
		if strings.HasPrefix(m.done, "✗") {
			st = s.red
		}
		lines = append(lines, "  "+st.Bold(true).Render(m.done))
	case m.err != "":
		lines = append(lines, "  "+s.red.Render("! "+m.err))
	default:
		send := "send"
		if u := int(m.a.UndoWindow(it.Kind).Seconds()); u > 0 && out.ScheduledFor == 0 {
			send = fmt.Sprintf("send in %ds (z undoes)", u)
		}
		keys := [][2]string{{"y", send}, {"e", "edit first"}, {"n", "decline"}, {"q", "later"}}
		if len(m.items) > 1 {
			keys = append(keys, [2]string{"s", "skip"})
		}
		var k []string
		for _, kv := range keys {
			k = append(k, s.keycap.Render(kv[0])+" "+s.sub.Render(kv[1]))
		}
		lines = append(lines, "  "+strings.Join(k, "   "))
	}
	card := strings.Join(lines, "\n")
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, lipgloss.NewStyle().Padding(1, 2).Render(card))
}
