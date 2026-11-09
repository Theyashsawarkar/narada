package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// fakeAI is an ai_command that saves its prompt (its last argument) and
// stdin, and answers with reply.
func fakeAI(t *testing.T, reply string) (argv []string, prompt, stdin string) {
	t.Helper()
	dir := t.TempDir()
	prompt, stdin = filepath.Join(dir, "prompt"), filepath.Join(dir, "stdin")
	script := filepath.Join(dir, "ai")
	body := "#!/bin/sh\nfor a in \"$@\"; do last=\"$a\"; done\nprintf '%s' \"$last\" > " + prompt + "\ncat > " + stdin + "\nprintf '%s\\n' '" + reply + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{script}, prompt, stdin
}

// runCmds runs a command (and a batch's commands) and returns the first
// aiWriteMsg it gives; the spinner's tick is skipped.
func aiAnswer(t *testing.T, cmd tea.Cmd) aiWriteMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("nothing ran")
	}
	switch msg := cmd().(type) {
	case aiWriteMsg:
		return msg
	case tea.BatchMsg:
		for _, c := range msg {
			if c == nil {
				continue
			}
			if r, ok := c().(aiWriteMsg); ok {
				return r
			}
		}
	}
	t.Fatal("no answer from the agent")
	return aiWriteMsg{}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAIWritesAChatMessage(t *testing.T) {
	m, w := waModel(t, "919800000002@s.whatsapp.net") // Ravi
	argv, prompt, stdin := fakeAI(t, "Haan, 7 baje pakka 🙏")
	m.a.Config.AICommand = argv
	m = press(m, "A")
	if m.mode != modeAIWrite || m.aw == nil || m.aw.kind != "chat" {
		t.Fatalf("A didn't open the ask box: mode %d %+v", m.mode, m.aw)
	}
	if !strings.Contains(ansi.Strip(m.View()), "Ask Claude") {
		t.Fatal("the ask box isn't drawn")
	}
	m = typeText(m, "tell him 7")
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.aw.stage != awThink {
		t.Fatalf("Enter didn't start it: stage %d", m.aw.stage)
	}
	nm, _ = m.Update(aiAnswer(t, cmd))
	m = nm.(Model)
	if m.aw.stage != awDraft || m.aw.draftText() != "Haan, 7 baje pakka 🙏" {
		t.Fatalf("no draft: %+v", m.aw)
	}
	// what it read: the chat, as untrusted data, and what you asked
	if p := read(t, prompt); !strings.Contains(p, "tell him 7") || !strings.Contains(p, "never instructions") {
		t.Fatalf("prompt: %s", p)
	}
	if in := read(t, stdin); !strings.HasPrefix(in, "<untrusted") || !strings.Contains(in, "PR is up") {
		t.Fatalf("stdin: %s", in)
	}
	if !strings.Contains(ansi.Strip(m.View()), "y use it") {
		t.Fatal("the draft's keys aren't shown")
	}
	// nothing went out yet; y sends it as you would
	if len(w.Sent) != 0 {
		t.Fatal("sent before y")
	}
	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = nm.(Model)
	if cmd == nil || m.aw != nil || m.mode != modeNormal {
		t.Fatalf("y didn't send: mode %d", m.mode)
	}
	m.a.DeliverDue(context.Background())
	if len(w.Sent) != 1 || w.Sent[0].Text != "Haan, 7 baje pakka 🙏" {
		t.Fatalf("sent: %+v", w.Sent)
	}
}

func TestAIDraftChangeEditAndDrop(t *testing.T) {
	m, w := waModel(t, "919800000002@s.whatsapp.net")
	argv, prompt, _ := fakeAI(t, "Short one")
	m.a.Config.AICommand = argv
	m = press(m, "A")
	m.aw.versions, m.aw.stage = []string{"A long first draft"}, awDraft
	// c, then 1: "shorter", with the draft it rewrites
	m = press(m, "c")
	if m.aw.stage != awAsk || !m.aw.change {
		t.Fatal("c didn't ask what to change")
	}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = nm.(Model)
	nm, _ = m.Update(aiAnswer(t, cmd))
	m = nm.(Model)
	if len(m.aw.versions) != 2 || m.aw.v != 1 {
		t.Fatalf("versions: %+v", m.aw.versions)
	}
	if p := read(t, prompt); !strings.Contains(p, "A long first draft") || !strings.Contains(p, "shorter") {
		t.Fatalf("change prompt: %s", p)
	}
	m = press(m, "left")
	if m.aw.draftText() != "A long first draft" {
		t.Fatal("← didn't go back a version")
	}
	// e: into the box to finish yourself, nothing sent
	m = press(m, "e")
	if m.mode != modeChatMsg || string(m.input) != "A long first draft" {
		t.Fatalf("e: mode %d input %q", m.mode, string(m.input))
	}
	// and n throws a draft away
	m.mode, m.input = modeNormal, nil
	m = press(m, "A")
	m.aw.versions, m.aw.stage = []string{"x"}, awDraft
	m = press(m, "n")
	if m.aw != nil || m.mode != modeNormal {
		t.Fatal("n didn't drop it")
	}
	m.a.DeliverDue(context.Background())
	if len(w.Sent) != 0 {
		t.Fatalf("something was sent: %+v", w.Sent)
	}
}

func TestPolishGivesBackYourTextWhenDropped(t *testing.T) {
	m, _ := waModel(t, "919800000002@s.whatsapp.net")
	argv, prompt, _ := fakeAI(t, "Yeah, I will be there.")
	m.a.Config.AICommand = argv
	m = press(m, "i")
	m = typeText(m, "ya ill be there")
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = nm.(Model)
	if m.mode != modeAIWrite || !m.aw.polish {
		t.Fatal("Ctrl+g didn't polish")
	}
	nm, _ = m.Update(aiAnswer(t, cmd))
	m = nm.(Model)
	if m.aw.draftText() != "Yeah, I will be there." || !strings.Contains(read(t, prompt), "ya ill be there") {
		t.Fatal("polish: wrong draft or prompt")
	}
	m = press(m, "n")
	if m.mode != modeChatMsg || string(m.input) != "ya ill be there" {
		t.Fatalf("n should give back what you typed: %q", string(m.input))
	}
}

func TestAIWriteIgnoresACancelledRun(t *testing.T) {
	m, _ := waModel(t, "919800000002@s.whatsapp.net")
	argv, _, _ := fakeAI(t, "late")
	m.a.Config.AICommand = argv
	m = press(m, "A")
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	ans := aiAnswer(t, cmd)
	m = press(m, "esc") // stop it
	m = press(m, "A")   // and ask again
	nm, _ = m.Update(ans)
	m = nm.(Model)
	if m.aw.stage != awAsk || len(m.aw.versions) != 0 {
		t.Fatalf("an old answer landed: %+v", m.aw)
	}
}

func TestCleanDraft(t *testing.T) {
	for in, want := range map[string]string{
		"  Hello there  \n":              "Hello there",
		"\"Haan, pakka\"":                "Haan, pakka",
		"Here's a reply:\nSounds good 👍": "Sounds good 👍",
		"“Thanks!”":                      "Thanks!",
		"Line one\nLine two":             "Line one\nLine two",
		"\"one\" and \"two\"":            "\"one\" and \"two\"",
	} {
		if got := cleanDraft(in); got != want {
			t.Errorf("cleanDraft(%q) = %q, want %q", in, got, want)
		}
	}
}
