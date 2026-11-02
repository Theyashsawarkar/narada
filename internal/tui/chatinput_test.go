package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestWrapRunes(t *testing.T) {
	text := []rune("hello there general kenobi\nsecond")
	var got []string
	for _, sg := range wrapRunes(text, 12) {
		got = append(got, string(text[sg.start:sg.end]))
	}
	if strings.Join(got, "|") != "hello there |general |kenobi|second" {
		t.Fatalf("%q", got)
	}
	// a trailing newline gives an empty line to type on
	if segs := wrapRunes([]rune("hi\n"), 10); len(segs) != 2 || segs[1].start != 3 {
		t.Fatalf("%+v", segs)
	}
	// one long word breaks hard
	if segs := wrapRunes([]rune("abcdefghij"), 4); len(segs) != 3 {
		t.Fatalf("%+v", segs)
	}
}

func TestChatBoxEditing(t *testing.T) {
	m := model(t)
	m = press(m, "tab") // chat
	m = press(m, "i")
	if m.mode != modeChatMsg {
		t.Fatal("i should start typing")
	}
	m = typeText(m, "hello world")
	m = press(m, "alt+b") // to "world"
	m = typeText(m, "big ")
	if string(m.input) != "hello big world" {
		t.Fatalf("insert at the cursor: %q", string(m.input))
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = nm.(Model)
	m = press(m, "end")
	m = typeText(m, "!")
	if string(m.input) != "hello big \nworld!" {
		t.Fatalf("alt+enter adds a line: %q", string(m.input))
	}
	// a long message wraps and the box grows
	m = press(m, "ctrl+u")
	m = typeText(m, strings.Repeat("lorem ipsum dolor ", 30))
	v := m.View()
	if n := strings.Count(v, "lorem"); n < 10 {
		t.Fatalf("the message isn't wrapped onto several lines (%d shown):\n%s", n, v)
	}
}
