package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Theyashsawarkar/sandesh/internal/app"
	"github.com/Theyashsawarkar/sandesh/internal/fake"
)

func TestQRLines(t *testing.T) {
	lines := qrLines("2@hello,world")
	if len(lines) < 10 {
		t.Fatalf("%d lines", len(lines))
	}
	all := strings.Join(lines, "")
	// black on white, both ways: a real pattern, in fixed colours
	for _, want := range []string{"38;2;0;0;0m\x1b[48;2;255;255;255m", "38;2;255;255;255m\x1b[48;2;0;0;0m", "38;2;0;0;0m\x1b[48;2;0;0;0m"} {
		if !strings.Contains(all, want) {
			t.Fatalf("no %q cells", want)
		}
	}
}

// The WhatsApp tab links it: L shows the QR code, then the chats arrive.
func TestLinkFromTab(t *testing.T) {
	m := model(t)
	w := fake.NewWhatsApp()
	w.SeedWhatsApp()
	c, _ := w.Conn(context.Background())
	_ = c.Logout(context.Background())
	m.a.DialWhatsApp = func(ctx context.Context) (app.WhatsAppConn, error) { return w.Conn(ctx) }
	nm0, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	m = nm0.(Model)
	m = press(m, "tab", "tab")
	if !m.wa || !strings.Contains(m.View(), "link with a QR code") {
		t.Fatalf("the WhatsApp tab should offer linking:\n%s", m.View())
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("L")})
	m = nm.(Model)
	l := m.waLinking
	if l == nil {
		t.Fatal("L didn't start linking")
	}
	sawQR := false
	for msg := range l.ch {
		nm, _ = m.Update(msg)
		m = nm.(Model)
		if lm := msg.(waLinkMsg); lm.qr != "" {
			sawQR = strings.Contains(m.View(), "Scan with your phone")
		} else if lm.done {
			break
		}
	}
	if !sawQR {
		t.Fatal("no QR code shown")
	}
	if !m.waLinked || m.waLinking != nil || !strings.Contains(m.View(), "Weekend trek") {
		t.Fatalf("after linking, the chats:\n%s", m.View())
	}
}
