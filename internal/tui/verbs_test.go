package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Theyashsawarkar/narada/internal/store"
)

// : opens a floating bar in the middle: ghost text, suggestions, Tab picks,
// Enter runs; a count goes in front; history survives the run.
func TestCommandBar(t *testing.T) {
	m := model(t)
	m = press(m, ":")
	m = typeText(m, "arc")
	if m.mode != modeCommand || m.cmdGhost() != "hive" {
		t.Fatalf("ghost %q", m.cmdGhost())
	}
	v := m.View()
	if !strings.Contains(v, "╭─ : ") || !strings.Contains(v, "archive") {
		t.Fatalf("no floating bar:\n%s", v)
	}
	m = press(m, "right") // takes the grey
	if string(m.input) != "archive" {
		t.Fatalf("→: %q", string(m.input))
	}
	th, _ := m.curThread()
	m = press(m, "enter")
	if got, _ := m.a.Store.Thread(th.Account, th.ID); got.Has("INBOX") {
		t.Fatal(":archive didn't archive")
	}
	// history: on disk, and ↑ brings it back
	if b, _ := os.ReadFile(filepath.Join(m.a.Paths.State, "commands")); !strings.Contains(string(b), "archive") {
		t.Fatalf("history file: %q", b)
	}
	m = press(m, ":", "up")
	if string(m.input) != "archive" {
		t.Fatalf("↑: %q", string(m.input))
	}
	m = press(m, "esc")
	// arguments complete: :go shows the sections
	m = press(m, ":")
	m = typeText(m, "go Sen")
	if sug := m.cmdSuggestions(); len(sug) == 0 || sug[0].name != "Sent" {
		t.Fatalf(":go args: %+v", sug)
	}
	m = press(m, "enter")
	if m.currentSection().Name != "Sent" {
		t.Fatalf(":go Sent went to %q", m.currentSection().Name)
	}
	// :set and :map
	m = press(m, ":")
	m = typeText(m, "set scroll_lines=5")
	m = press(m, "enter")
	if m.a.Config.ScrollLines != 5 {
		t.Fatal(":set scroll_lines")
	}
	m = press(m, ":")
	m = typeText(m, "map gh go.inbox")
	m = press(m, "enter", "g", "h")
	if m.currentSection().Name != "Inbox" {
		t.Fatalf(":map gh: %q (%s)", m.currentSection().Name, m.toast)
	}
	// :news shows what moved
	m = press(m, ":")
	m = typeText(m, "news")
	m = press(m, "enter")
	if m.mode != modeNews || !strings.Contains(m.View(), "what moved") {
		t.Fatal(":news")
	}
	m = press(m, "q")
	if m.mode != modeNormal {
		t.Fatal("q closes :news")
	}
}

// Vim notation for :map and [keys.normal].
func TestVimKeys(t *testing.T) {
	for in, want := range map[string]string{"gh": "g h", "<C-j>": "Ctrl+j", "<Space>mr": "Space m r", "g h": "g h", "Ctrl+x": "Ctrl+x"} {
		if got := vimKeys(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// The plan's conformance moves: counts, dj / d3j behind one question,
// . after dd, u then Ctrl+r, marks, the jump list.
func TestVimVerbs(t *testing.T) {
	m := model(t)
	n := m.count()
	if n < 3 {
		t.Fatalf("seed has %d threads", n)
	}
	// d3j: this one and the next (clamped), one question for all
	m = press(m, "d", "3", "j")
	if m.confirm == nil || !strings.Contains(m.confirm.question, "threads") {
		t.Fatalf("d3j asks for several: %+v", m.confirm)
	}
	m = press(m, "n")
	if len(m.selected) != 0 || m.confirm != nil {
		t.Fatal("n keeps them, and nothing stays picked")
	}
	// dd y, then . repeats it on the next one
	th0, _ := m.curThread()
	m = press(m, "d", "d", "y")
	if got, _ := m.a.Store.Thread(th0.Account, th0.ID); !got.Has("TRASH") {
		t.Fatal("dd y")
	}
	th1, _ := m.curThread()
	m = press(m, ".")
	if m.confirm == nil {
		t.Fatal(". asks again, as dd did")
	}
	m = press(m, "y")
	if got, _ := m.a.Store.Thread(th1.Account, th1.ID); !got.Has("TRASH") {
		t.Fatal(". y didn't trash the next one")
	}
	// u takes the newest back; Ctrl+r does it again
	if len(m.undos) < 2 {
		t.Fatalf("undo stack: %d", len(m.undos))
	}
	m = press(m, "u")
	if len(m.redos) != 1 {
		t.Fatal("u should leave a redo")
	}
	m = press(m, "ctrl+r")
	if len(m.redos) != 0 || len(m.undos) < 2 {
		t.Fatalf("Ctrl+r: undos %d redos %d", len(m.undos), len(m.redos))
	}
	if got, _ := m.a.Store.Thread(th1.Account, th1.ID); !got.Has("TRASH") {
		t.Fatal("Ctrl+r: the thread isn't trashed again")
	}
}

func TestMarksAndJumps(t *testing.T) {
	m := model(t)
	m = press(m, "m", "a")
	first, _ := m.curThread()
	m = press(m, "G")
	if m.sel == 0 {
		t.Fatal("G")
	}
	m = press(m, "'", "a")
	if th, _ := m.curThread(); th.ID != first.ID {
		t.Fatalf("'a went to %s", th.ID)
	}
	// G then Ctrl+o comes back; ]j goes forward again
	m = press(m, "G")
	last := m.sel
	m = press(m, "ctrl+o")
	if m.sel != 0 {
		t.Fatalf("Ctrl+o after G: sel %d", m.sel)
	}
	m = press(m, "]", "j")
	if m.sel != last {
		t.Fatalf("]j: sel %d, want %d", m.sel, last)
	}
	// marks last across runs: a new screen on the same store finds it
	m2 := New(m.a, "")
	m2.w, m2.h = m.w, m.h
	m2 = press(m2, "G", "'", "a")
	if th, _ := m2.curThread(); th.ID != first.ID {
		t.Fatal("mark a didn't last")
	}
}

// Space f h with a thread open searches inside it; n / N step through.
func TestSearchWhatsOpen(t *testing.T) {
	m := model(t)
	m = press(m, "enter")
	if m.focus != 1 {
		t.Fatal("open")
	}
	m = press(m, " ", "f", "h")
	if m.mode != modeConvSearch {
		t.Fatalf("Space f h with a thread open: mode %d", m.mode)
	}
	word := ""
	for _, f := range strings.Fields(strings.Join(m.preview, " ")) {
		if len(f) > 5 && !strings.ContainsAny(f, "\x1b[") {
			word = f
			break
		}
	}
	m = typeText(m, word)
	m = press(m, "enter")
	if m.convQ != word || !strings.Contains(m.toast, "of") {
		t.Fatalf("search %q: %q", word, m.toast)
	}
	m = press(m, "n", "N", "esc")
	if m.convQ != "" {
		t.Fatal("Esc stops the search")
	}
}

// y answers the question about what was asked: if the list or the chat
// moved under the question (a sync, a new message, a click), y does
// nothing, rather than trash or delete something else.
func TestConfirmIsAboutWhatWasAsked(t *testing.T) {
	m := model(t)
	first, _ := m.curThread()
	m = press(m, "d", "d")
	if m.confirm == nil {
		t.Fatal("dd should ask")
	}
	m.sel = 1 // a sync put another thread under the cursor
	m.loadPreview(true)
	other, _ := m.curThread()
	m = press(m, "y")
	for _, th := range []store.Thread{first, other} {
		if got, _ := m.a.Store.Thread(th.Account, th.ID); got.Has("TRASH") {
			t.Fatalf("y trashed %s after the list moved", th.Subject)
		}
	}

	w, wa := waModel(t, "919800000002@s.whatsapp.net")
	w = pickMsg(t, w, "H4")
	w = press(w, "d", "d")
	if w.confirm == nil {
		t.Fatal("dd should ask")
	}
	w.chatSel++ // the chat's window moved: your own H5 sits at that index now
	nm, cmd := w.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	w = nm.(Model)
	if cmd != nil {
		cmd()
	}
	if len(wa.Revoked) != 0 {
		t.Fatal("y deleted another message")
	}
	for _, id := range []string{"H4", "H5"} {
		if _, err := w.a.Store.ChatMessage(store.WhatsAppAccount, "919800000002@s.whatsapp.net/"+id); err != nil {
			t.Fatalf("%s gone: y acted on what it wasn't asked about", id)
		}
	}
}

// d with a motion trashes every thread its question named, not only the
// one under the cursor.
func TestDeleteMotionTrashesAllItAsked(t *testing.T) {
	m := model(t)
	var want []store.Thread
	for i := 0; i < 3; i++ {
		want = append(want, m.threads[i])
	}
	m = press(m, "d", "2", "j")
	if m.confirm == nil || !strings.Contains(m.confirm.question, "3 threads") {
		t.Fatalf("d2j asks for three: %+v", m.confirm)
	}
	m = press(m, "y")
	for _, th := range want {
		if got, _ := m.a.Store.Thread(th.Account, th.ID); !got.Has("TRASH") {
			t.Errorf("%s was in the question but isn't in the trash", th.Subject)
		}
	}
}
