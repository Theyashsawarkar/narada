package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Ctrl+v in the chat box: an image on the clipboard (a screenshot) is
// saved and attached; text is typed in, for terminals where Ctrl+v
// doesn't paste on its own.

type pastedMsg struct {
	path, text string
	err        error
}

// clipboard tools: how to list what the clipboard holds and how to read
// one type of it, Wayland first.
var clipTools = []struct{ list, read []string }{
	{[]string{"wl-paste", "--list-types"}, []string{"wl-paste", "--no-newline", "--type"}},
	{[]string{"xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"}, []string{"xclip", "-selection", "clipboard", "-o", "-t"}},
}

func (m *Model) pasteImage() tea.Cmd {
	dir := filepath.Join(m.a.Paths.Cache, "pasted")
	return func() tea.Msg {
		for _, t := range clipTools {
			if _, err := exec.LookPath(t.list[0]); err != nil {
				continue
			}
			out, err := exec.Command(t.list[0], t.list[1:]...).Output()
			if err != nil {
				return pastedMsg{err: errors.New("the clipboard is empty")}
			}
			kind := pickClipType(strings.Fields(string(out)))
			if kind == "" {
				return pastedMsg{err: errors.New("nothing on the clipboard to paste")}
			}
			data, err := exec.Command(t.read[0], append(t.read[1:], kind)...).Output()
			if err != nil || len(data) == 0 {
				return pastedMsg{err: fmt.Errorf("reading the clipboard: %v", err)}
			}
			if !strings.HasPrefix(kind, "image/") {
				return pastedMsg{text: string(data)}
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return pastedMsg{err: err}
			}
			p := filepath.Join(dir, "pasted-"+time.Now().Format("20060102-150405")+"."+strings.TrimPrefix(kind, "image/"))
			if err := os.WriteFile(p, data, 0o600); err != nil {
				return pastedMsg{err: err}
			}
			return pastedMsg{path: p}
		}
		return pastedMsg{err: errors.New("pasting an image needs wl-paste (wl-clipboard) or xclip")}
	}
}

// pickClipType prefers an image (PNG first), then plain text.
func pickClipType(types []string) string {
	for _, want := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		for _, t := range types {
			if t == want {
				return t
			}
		}
	}
	for _, want := range []string{"text/plain;charset=utf-8", "UTF8_STRING", "text/plain"} {
		for _, t := range types {
			if t == want {
				return t
			}
		}
	}
	return ""
}
