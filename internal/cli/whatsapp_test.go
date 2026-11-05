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
