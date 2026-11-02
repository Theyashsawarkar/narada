package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Theyashsawarkar/sandesh/internal/app"
	"github.com/Theyashsawarkar/sandesh/internal/store"
)

func TestApproveCard(t *testing.T) {
	m := model(t)
	m.a.Config.SendPolicy = "confirm"
	it, err := m.a.Queue("demo", "mail", store.Outgoing{To: []string{"priya.k@example.com"}, Subject: "Re: Release cut on Thursday?",
		Body: "Yes, Thursday works.\n\nI'll cut the branch at 10."}, app.OriginAgent)
	if err != nil || it.Status != store.StatusApproval {
		t.Fatal(it, err)
	}
	ap := approver{a: m.a, items: []store.OutboxItem{it}, styles: m.styles}
	nm, _ := ap.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	v := nm.View()
	for _, want := range []string{"wants to send a mail", "priya.k@example.com", "Release cut", "I'll cut the branch", "decline", "asking because"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	if testing.Verbose() {
		t.Log("\n" + v)
	}
	nm, _ = nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if got, _ := m.a.Store.OutboxItem(it.ID); got.Status != store.StatusQueued || !strings.Contains(nm.View(), "approved") {
		t.Fatalf("y: %s\n%s", got.Status, nm.View())
	}
}
