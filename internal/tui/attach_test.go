package tui

import (
	"testing"

	"github.com/Theyashsawarkar/narada/internal/app"
)

// The file picker warns at WhatsApp's limit in a WhatsApp chat, and at
// mail's 25 MB elsewhere.
func TestAttachLimitFollowsTheAccount(t *testing.T) {
	m := tagModel(t)
	m.pickFiles("chatbox")
	if got := m.attachLimit(); got != app.MaxWhatsAppAttach {
		t.Fatalf("WhatsApp chat: limit %d, want %d", got, int64(app.MaxWhatsAppAttach))
	}
	m.pick.then = "compose"
	m.comp = &composer{account: "work"}
	if got := m.attachLimit(); got != app.MaxAttach {
		t.Fatalf("mail: limit %d, want %d", got, app.MaxAttach)
	}
}
