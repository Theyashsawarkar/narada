package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"rsc.io/qr"
)

// Linking WhatsApp from its tab: before it's linked the tab is this
// panel. L shows the QR code to scan, P a code to type on the phone;
// then a progress bar while the phone sends your recent chats.

type waLink struct {
	state       string // "qr", "code", "syncing"
	qr, code    string
	qrAt        time.Time // when this QR code came (they last ~20 s)
	chats, msgs int
	pct         int
	err         string
	cancel      context.CancelFunc
	ch          chan tea.Msg
	frame       int
}

type waLinkMsg struct {
	qr, code         string
	progress         bool
	chats, msgs, pct int
	done             bool
	err              error
}

type waLinkTick struct{}

func (m *Model) startLink(phone string) tea.Cmd {
	if m.waLinking != nil && m.waLinking.cancel != nil {
		m.waLinking.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	l := &waLink{cancel: cancel, ch: make(chan tea.Msg, 32), state: "qr"}
	if phone != "" {
		l.state = "code"
	}
	m.waLinking = l
	a := m.a
	go func() {
		err := a.LinkWhatsApp(ctx, phone, func(q, code string) {
			l.ch <- waLinkMsg{qr: q, code: code}
		}, func(chats, msgs, pct int) {
			select {
			case l.ch <- waLinkMsg{progress: true, chats: chats, msgs: msgs, pct: pct}:
			default: // the screen is behind: the next one carries the counts
			}
		})
		l.ch <- waLinkMsg{done: true, err: err}
		cancel()
	}()
	return tea.Batch(listenLink(l.ch), linkTick())
}

func listenLink(ch chan tea.Msg) tea.Cmd { return func() tea.Msg { return <-ch } }

func linkTick() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return waLinkTick{} })
}

func (m Model) linkUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	l := m.waLinking
	switch msg := msg.(type) {
	case waLinkTick:
		if l == nil || l.err != "" {
			return m, nil
		}
		l.frame++
		return m, linkTick()
	case waLinkMsg:
		if l == nil {
			return m, nil
		}
		switch {
		case msg.done && msg.err != nil:
			l.err = msg.err.Error()
			if strings.Contains(l.err, "context canceled") {
				m.waLinking = nil
			}
			return m, nil
		case msg.done:
			m.waLinking = nil
			m.reload()
			m.say("WhatsApp is linked")
			a := m.a
			// stay online while the screen is open (the daemon takes over
			// when it runs)
			return m, func() tea.Msg { _, _ = a.StartWhatsApp(context.Background()); return nil }
		case msg.progress:
			l.state = "syncing"
			l.chats, l.msgs, l.pct = msg.chats, msg.msgs, msg.pct
		case msg.qr != "":
			l.qr, l.qrAt = msg.qr, time.Now()
		case msg.code != "":
			l.code = msg.code
		}
		return m, listenLink(l.ch)
	}
	return m, nil
}

// linkKey handles keys on the WhatsApp tab while it isn't linked.
func (m Model) linkKey(s string) (tea.Model, tea.Cmd, bool) {
	switch s {
	case "L", "enter":
		if m.waLinking == nil || m.waLinking.err != "" {
			return m, m.startLink(""), true
		}
	case "P":
		if m.waLinking == nil || m.waLinking.err != "" {
			m.waLinking = nil
			m.mode, m.input, m.back = modePhone, []rune("+"), 0
			return m, nil, true
		}
	case "esc", "q":
		if m.waLinking != nil {
			if m.waLinking.cancel != nil && m.waLinking.state != "syncing" {
				m.waLinking.cancel()
			}
			if m.waLinking.state != "syncing" {
				m.waLinking = nil
			}
			return m, nil, true
		}
	}
	return m, nil, false
}

// qrLines draws a QR code with half blocks: two modules a line, black on
// white whatever the terminal's colours (a phone won't read it inverted).
func qrLines(text string) []string {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return nil
	}
	const quiet = 2
	n := code.Size
	black := func(x, y int) bool {
		return x >= 0 && y >= 0 && x < n && y < n && code.Black(x, y)
	}
	var out []string
	for y := -quiet; y < n+quiet; y += 2 {
		var b strings.Builder
		for x := -quiet; x < n+quiet; x++ {
			top, bot := "255;255;255", "255;255;255"
			if black(x, y) {
				top = "0;0;0"
			}
			if black(x, y+1) {
				bot = "0;0;0"
			}
			fmt.Fprintf(&b, "\x1b[38;2;%sm\x1b[48;2;%sm▀", top, bot)
		}
		b.WriteString("\x1b[0m")
		out = append(out, b.String())
	}
	return out
}

// linkPanel is the WhatsApp tab before linking.
func (m Model) linkPanel(w, h int) string {
	s := m.styles
	green := lipgloss.NewStyle().Foreground(lipgloss.Color(s.c.Green))
	l := m.waLinking
	var lines []string
	center := func(xs ...string) {
		for _, x := range xs {
			pad := max(0, (w-2-lipgloss.Width(x))/2)
			lines = append(lines, strings.Repeat(" ", pad)+x)
		}
	}
	keys := func(kv ...[2]string) string {
		var k []string
		for _, p := range kv {
			k = append(k, s.keycap.Render(p[0])+" "+s.sub.Render(p[1]))
		}
		return strings.Join(k, "   ")
	}
	spin := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	switch {
	case l != nil && l.err != "":
		center("", s.red.Bold(true).Render("Linking didn't work"), "", s.text.Render(l.err), "",
			keys([2]string{"L", "try again with a QR code"}, [2]string{"P", "with your phone number"}))
	case l != nil && l.state == "syncing":
		bar := 44
		filled := bar * l.pct / 100
		var b string
		if l.pct > 0 {
			b = green.Render(strings.Repeat("━", filled)) + s.muted.Render(strings.Repeat("━", bar-filled)) + "  " + s.bold.Render(fmt.Sprintf("%d%%", l.pct))
		} else { // the phone hasn't said how far: a moving segment
			pos := l.frame % (bar + 8)
			for i := 0; i < bar; i++ {
				if i >= pos-8 && i < pos {
					b += green.Render("━")
				} else {
					b += s.muted.Render("━")
				}
			}
		}
		center("", green.Bold(true).Render("✓ Linked"), "", s.text.Render("Your phone is sending your recent chats"), "", b, "",
			s.sub.Render(fmt.Sprintf("%d chats · %d messages", l.chats, l.msgs)), "",
			s.muted.Render("Keep the phone online. This takes up to two minutes; the chats show here as they come."))
	case l != nil && l.state == "code":
		if l.code == "" {
			center("", s.muted.Render(spin[l.frame%len(spin)]+" asking WhatsApp for a code…"))
			break
		}
		big := s.pill.Background(lipgloss.Color(s.c.Green)).Padding(0, 3).Render(strings.Join(strings.Split(l.code, ""), " "))
		center("", s.bold.Render("On your phone"), s.sub.Render("WhatsApp → Settings → Linked devices → Link a device → Link with phone number instead"), "",
			s.text.Render("type this code:"), "", big, "", s.muted.Render(spin[l.frame%len(spin)]+" waiting for the phone"), "", keys([2]string{"esc", "cancel"}))
	case l != nil && l.state == "qr":
		if l.qr == "" {
			center("", s.muted.Render(spin[l.frame%len(spin)]+" asking WhatsApp for a QR code…"))
			break
		}
		q := qrLines(l.qr)
		left := 20 - int(time.Since(l.qrAt).Seconds())
		head := []string{"", s.bold.Render("Scan with your phone"), s.sub.Render("WhatsApp → Settings → Linked devices → Link a device"), ""}
		foot := []string{"", s.muted.Render(fmt.Sprintf("%s a new code in %ds", spin[l.frame%len(spin)], max(0, left))), "",
			keys([2]string{"P", "use a phone number instead"}, [2]string{"esc", "cancel"})}
		if len(q)+len(head)+len(foot) > h-2 {
			head, foot = head[1:3], foot[1:2]
		}
		if len(q)+len(head)+len(foot) > h-2 {
			center("", s.peach.Render(fmt.Sprintf("The QR code needs %d lines: make the window taller (or press P to type a code instead).", len(q)+2)))
			break
		}
		center(head...)
		center(q...)
		center(foot...)
	default:
		logo := green.Bold(true).Render("󰖣  WhatsApp")
		if !s.nerd {
			logo = green.Bold(true).Render("WhatsApp")
		}
		center("", "", logo, "",
			s.text.Render("Link your WhatsApp to read and send your chats here, like WhatsApp Web."),
			s.sub.Render("Your phone stays the main device; narada shows up under Linked devices."), "",
			keys([2]string{"L", "link with a QR code"}, [2]string{"P", "link with your phone number"}), "", "",
			s.peach.Render("Know the risk"),
			s.muted.Render("WhatsApp doesn't allow programs other than its own to link. Normal use looks like"),
			s.muted.Render("WhatsApp Web, but an account that sends a lot from one can be banned: narada caps"),
			s.muted.Render(fmt.Sprintf("sends at %d an hour and sends one chat at a time.", m.a.Config.WhatsAppPerHour)))
	}
	return strings.Join(lines, "\n")
}
