package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Theyashsawarkar/narada/internal/store"
)

func TestFileLineKeepsTheCaption(t *testing.T) {
	files := []store.ChatFile{{Name: "Payment: Evaluation.pdf"}}
	cases := []struct {
		line, name, caption string
		files               []store.ChatFile
	}{
		// the message's own file list says where the name ends, even with ": " in it
		{"📎 Payment: Evaluation.pdf: @Naem please review", "Payment: Evaluation.pdf", "@Naem please review", files},
		// without it, a name has an extension
		{"📎 report.pdf: please review", "report.pdf", "please review", nil},
		{"📎 Document: see this", "Document", "see this", nil},
		{"📎 notes.txt", "notes.txt", "", nil},
		{"📎 Meeting: notes", "Meeting: notes", "", nil}, // no extension: all of it is the name
	}
	for _, c := range cases {
		name, caption := fileLine(c.line, c.files, 0)
		if name != c.name || caption != c.caption {
			t.Errorf("%q: got %q / %q, want %q / %q", c.line, name, caption, c.name, c.caption)
		}
	}
}

func TestLongMessageFoldsAndOpens(t *testing.T) {
	m, _ := waModel(t, "919800000002@s.whatsapp.net")
	var rest []string
	for i := 1; i <= 12; i++ {
		rest = append(rest, fmt.Sprintf("paragraph line %d", i))
	}
	text := "📎 Evaluation.pdf: HEAD OF THE MESSAGE\n\n" + strings.Join(rest, "\n")
	if err := m.a.Store.PutChatMessages([]store.ChatMessage{{
		Account: store.WhatsAppAccount, Space: "919800000002@s.whatsapp.net", Name: "919800000002@s.whatsapp.net/DOC1",
		Sender: "me", Mine: true, Text: text, Created: time.Now().UnixMilli(),
		Files: []store.ChatFile{{Name: "Evaluation.pdf"}},
	}}); err != nil {
		t.Fatal(err)
	}
	m.loadPreview(true)
	m = press(pickMsg(t, m, "DOC1"), "z", "t") // the picked message at the top of the view
	v := m.View()
	// the head and the file show; the tail waits behind "more lines"
	for _, want := range []string{"HEAD OF THE MESSAGE", "Evaluation.pdf", "more lines", "za opens"} {
		if !strings.Contains(v, want) {
			t.Fatalf("folded message lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "paragraph line 12") || strings.Contains(v, "Evaluation.pdf: HEAD") {
		t.Fatalf("folded message shows its tail, or the caption inside the file name:\n%s", v)
	}
	m = press(m, "z", "a", "z", "t")
	if v = m.View(); !strings.Contains(v, "paragraph line 12") || strings.Contains(v, "more lines") {
		t.Fatalf("za didn't open it:\n%s", v)
	}
	m = press(m, "z", "c")
	if strings.Contains(m.View(), "paragraph line 12") {
		t.Fatal("zc didn't fold it")
	}
	m = press(m, "z", "R", "z", "t")
	if !strings.Contains(m.View(), "paragraph line 12") {
		t.Fatal("zR didn't open every message")
	}
	m = press(m, "z", "M")
	if strings.Contains(m.View(), "paragraph line 12") {
		t.Fatal("zM didn't fold every message")
	}
}
