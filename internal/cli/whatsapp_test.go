package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/fake"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// waApp is narada with a linked fake WhatsApp (never a real phone).
func waApp(t *testing.T) (*app.App, *fake.WhatsApp) {
	t.Helper()
	t.Setenv("NARADA_HOME", t.TempDir())
	t.Setenv("NARADA_GOOGLE_URL", "http://127.0.0.1:1")
	t.Setenv("NARADA_DOWNLOADS", t.TempDir())
	t.Setenv("TMUX", "")
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.Config.ChatUndoSeconds = 0
	w := fake.NewWhatsApp()
	w.SeedWhatsApp()
	a.DialWhatsApp = func(ctx context.Context) (app.WhatsAppConn, error) { return w.Conn(ctx) }
	if err := a.Store.AddWhatsApp("+919876500000", w.Me); err != nil {
		t.Fatal(err)
	}
	// the phone's history arrives once, as on linking
	ctx, cancel := context.WithCancel(context.Background())
	if ok, err := a.StartWhatsApp(ctx); !ok || err != nil {
		t.Fatal(ok, err)
	}
	cancel()
	a.StopWhatsApp()
	return a, w
}

// run runs a whatsapp command and returns what it printed.
func run(t *testing.T, a *app.App, args ...string) (string, error) {
	t.Helper()
	r, wr, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = wr
	err := whatsApp(a, args)
	wr.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	return string(b), err
}

func TestWhatsAppReadShowsIdsAndFiles(t *testing.T) {
	a, _ := waApp(t)
	out, err := run(t, a, "read", "Amma")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[919800000001@s.whatsapp.net/H3]") || !strings.Contains(out, "narada whatsapp save 919800000001@s.whatsapp.net/H3 1") {
		t.Fatalf("read doesn't show ids and files:\n%s", out)
	}
}

func TestWhatsAppSaveCommand(t *testing.T) {
	a, w := waApp(t)
	out, err := run(t, a, "save", "919800000001@s.whatsapp.net/H3")
	if err != nil {
		t.Fatal(err)
	}
	p := strings.TrimSpace(out)
	b, _ := os.ReadFile(p)
	if string(b) != string(w.Files["demo-photo"]) || filepath.Dir(p) != os.Getenv("NARADA_DOWNLOADS") {
		t.Fatalf("saved %q (%d bytes)", p, len(b))
	}
	if _, err := run(t, a, "save", "919800000001@s.whatsapp.net/H3", "2"); err == nil {
		t.Fatal("there is no file 2")
	}
}

func TestWhatsAppSendVoice(t *testing.T) {
	a, w := waApp(t)
	note := filepath.Join(t.TempDir(), "note.ogg")
	_ = os.WriteFile(note, []byte("OggS OpusHead"), 0o600)
	doc := filepath.Join(t.TempDir(), "plan.pdf")
	_ = os.WriteFile(doc, []byte("%PDF-1.4"), 0o600)
	if _, err := run(t, a, "send", "Ravi", "listen", "--attach", doc, "--voice", note, "--now"); err != nil {
		t.Fatal(err)
	}
	if len(w.Sent) != 2 || w.Sent[0].Media.Kind != "document" || w.Sent[0].Text != "listen" || w.Sent[1].Media.Kind != "voice" {
		t.Fatalf("sent %+v", w.Sent)
	}
	items, _ := a.Store.Outbox(store.StatusSent)
	if len(items) != 1 {
		t.Fatalf("outbox: %+v", items)
	}
}

func TestWhatsAppEditDeleteForwardCommands(t *testing.T) {
	a, w := waApp(t)
	if _, err := run(t, a, "send", "Ravi", "see you at 5", "--now"); err != nil {
		t.Fatal(err)
	}
	items, _ := a.Store.Outbox(store.StatusSent)
	sent := items[0].Result
	if _, err := run(t, a, "edit", sent, "see", "you", "at", "6"); err != nil {
		t.Fatal(err)
	}
	if len(w.Edited) != 1 || !strings.HasSuffix(w.Edited[0], "see you at 6") {
		t.Fatalf("edited %v", w.Edited)
	}
	if _, err := run(t, a, "forward", "919800000001@s.whatsapp.net/H3", "Ravi"); err != nil {
		t.Fatal(err)
	}
	if last := w.Sent[len(w.Sent)-1]; !last.Forwarded || last.Chat != "919800000002@s.whatsapp.net" {
		t.Fatalf("forwarded %+v", last)
	}
	if _, err := run(t, a, "delete", "919800000002@s.whatsapp.net/H4"); err == nil {
		t.Fatal("deleted Ravi's message for everyone")
	}
	if _, err := run(t, a, "delete", "919800000002@s.whatsapp.net/H4", "--for-me"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, a, "delete", sent); err != nil || len(w.Revoked) != 1 {
		t.Fatalf("delete for everyone: %v %v", err, w.Revoked)
	}
	// a private reply to the group's message
	if _, err := run(t, a, "send", "Ravi", "between us", "--reply-to", "120363000000000001@g.us/H6", "--now"); err != nil {
		t.Fatal(err)
	}
	if q := w.Quotes[len(w.Quotes)-1]; q.ID != "H6" || q.Chat != "120363000000000001@g.us" {
		t.Fatalf("quoted %+v", q)
	}
}

func TestWhatsAppChatStateCommands(t *testing.T) {
	a, w := waApp(t)
	for _, args := range [][]string{{"archive", "Ravi"}, {"pin", "Amma"}, {"mute", "trek", "--for", "1w"}, {"block", "Ravi"}, {"star", "919800000001@s.whatsapp.net/H2"}} {
		if _, err := run(t, a, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	did := strings.Join(w.Did, "\n")
	for _, x := range []string{"archive 919800000002@s.whatsapp.net 1", "pin 919800000001@s.whatsapp.net 1", "mute 120363000000000001@g.us ",
		"block 919800000002@s.whatsapp.net 1", "star 919800000001@s.whatsapp.net H2"} {
		if !strings.Contains(did, x) {
			t.Errorf("WhatsApp wasn't told %q:\n%s", x, did)
		}
	}
	out, _ := run(t, a, "list")
	if strings.Contains(out, "Ravi") || !strings.Contains(out, "📌") || !strings.Contains(out, "🔕") {
		t.Fatalf("list:\n%s", out)
	}
	if out, _ = run(t, a, "list", "--archived"); !strings.Contains(out, "Ravi") || !strings.Contains(out, "⊘") {
		t.Fatalf("list --archived:\n%s", out)
	}
	if out, _ = run(t, a, "starred"); !strings.Contains(out, "Did you eat?") {
		t.Fatalf("starred:\n%s", out)
	}
	if _, err := run(t, a, "mute", "trek", "--for", "soon"); err == nil {
		t.Fatal("--for soon")
	}
	if _, err := run(t, a, "block", "trek"); err == nil {
		t.Fatal("blocked a group")
	}
}
