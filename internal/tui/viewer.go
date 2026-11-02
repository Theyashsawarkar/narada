package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Theyashsawarkar/narada/internal/store"
)

// The picture viewer: a chat's photo, GIF or video poster as big as the
// screen allows (space, or enter, on a picked message). h/l go through
// the chat's pictures; esc goes back to the conversation.

type viewer struct {
	shots []shot // the chat's pictures, oldest first
	at    int
}

type shot struct {
	msg store.ChatMessage
	idx int // which of its files
}

// openViewer opens the viewer on message name's first picture; false when
// it has none.
func (m *Model) openViewer(name string) bool {
	v := &viewer{at: -1}
	for _, c := range m.chatMsgs {
		for i, f := range c.Files {
			if show, _, _ := imageKind(f); show {
				if c.Name == name && v.at < 0 {
					v.at = len(v.shots)
				}
				v.shots = append(v.shots, shot{c, i})
			}
		}
	}
	if v.at < 0 {
		return false
	}
	m.viewer, m.mode = v, modeView
	return true
}

func (m Model) viewKey(s string) (tea.Model, tea.Cmd) {
	v := m.viewer
	switch s {
	case "esc", "q", "space", " ", "enter":
		m.viewer, m.mode = nil, modeNormal
		m.fixView()
	case "h", "left", "k", "up":
		v.at = max(0, v.at-1)
	case "l", "right", "j", "down":
		v.at = min(len(v.shots)-1, v.at+1)
	case "v":
		// open it in its app, like v in the conversation
		for i, c := range m.chatMsgs {
			if c.Name == v.shots[v.at].msg.Name {
				m.chatSel = i
			}
		}
		return m, m.saveCmd(true)
	}
	return m, nil
}

func (m Model) viewerTitle() string {
	v := m.viewer
	c := v.shots[v.at].msg
	who := c.SenderName
	if c.Mine {
		who = "You"
	}
	return fmt.Sprintf("%s · %s · %d of %d", who, time.UnixMilli(c.Created).Format("2 Jan 15:04"), v.at+1, len(v.shots))
}

func (m *Model) viewerView(w, h int) string {
	s := m.styles
	v := m.viewer
	sh := v.shots[v.at]
	caption := captionOf(sh.msg.Text)
	room := h - 2
	if caption != "" {
		room -= 2
	}
	lines := m.mediaSized(sh.msg.Account, sh.msg.Name, sh.idx, sh.msg.Files[sh.idx], w, max(3, room))
	var out []string
	pad := max(0, (room-len(lines))/2)
	for range pad {
		out = append(out, "")
	}
	for _, l := range lines {
		out = append(out, strings.Repeat(" ", max(0, (w-lipgloss.Width(l))/2))+l)
	}
	if caption != "" {
		out = append(out, "", lipgloss.PlaceHorizontal(w, lipgloss.Center, s.text.Render(caption)))
	}
	return strings.Join(out, "\n")
}

// captionOf is a media message's caption: its text after "📷 Photo: ".
func captionOf(text string) string {
	for _, l := range []string{"📷 Photo", "GIF", "Sticker", "🎬 Video"} {
		if strings.HasPrefix(text, l) {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, l), ":"))
		}
	}
	return text
}
