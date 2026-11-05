package tui

import (
	"os"
	"os/exec"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func keyMsg(m Model, k tea.KeyMsg) Model {
	nm, _ := m.Update(k)
	return nm.(Model)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// Ctrl+r records a voice note into the message; Ctrl+r again adds it, Esc drops it.
func TestRecordVoiceNote(t *testing.T) {
	recordCmd = func(path string) *exec.Cmd {
		// a stand-in microphone: writes the file, stops on "q" like ffmpeg
		return exec.Command("sh", "-c", `printf 'OggS OpusHead' > "$1"; read x`, "sh", path)
	}
	m := tagModel(t)
	m = keyMsg(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if m.rec == nil {
		t.Fatalf("not recording: %s", m.toast)
	}
	if !contains(ansi.Strip(m.recordingLine()), "● recording 0:0") {
		t.Fatal("the box doesn't say it's recording")
	}
	m = keyMsg(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if m.rec != nil || len(m.chatFiles) != 1 || !m.chatFiles[0].Voice {
		t.Fatalf("not added as a voice note: %+v", m.chatFiles)
	}
	if !contains(ansi.Strip(strings.Join(m.attachedLines(80), "\n")), "voice note") {
		t.Fatal("the box doesn't show the voice note")
	}
	// another, dropped with Esc
	m = keyMsg(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	p := m.rec.path
	m = keyMsg(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.rec != nil || len(m.chatFiles) != 1 {
		t.Fatalf("Esc kept it: %+v", m.chatFiles)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("the dropped recording is still on disk")
	}
}
