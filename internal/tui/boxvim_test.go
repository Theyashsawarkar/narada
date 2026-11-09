package tui

import (
	"strings"
	"testing"
)

func apply(t *testing.T, text string, cur int, keys ...string) (boxText, boxResult) {
	t.Helper()
	b := boxText{t: []rune(text), cur: cur}
	pend := ""
	var r boxResult
	for _, k := range keys {
		r = boxApply(&b, &pend, k, "PASTE")
	}
	return b, r
}

// The box's NORMAL keys, as Vim does them.
func TestBoxVimKeys(t *testing.T) {
	cases := []struct {
		text string
		cur  int
		keys []string
		want string
		at   int
	}{
		{"hello world", 0, []string{"w"}, "hello world", 6},
		{"hello world", 10, []string{"b"}, "hello world", 6},
		{"hello world", 0, []string{"e"}, "hello world", 4},
		{"hello world", 0, []string{"$"}, "hello world", 10},
		{"hello world", 6, []string{"0"}, "hello world", 0},
		{"hello world", 0, []string{"x"}, "ello world", 0},
		{"hello world", 0, []string{"d", "w"}, "world", 0},
		{"hello world", 6, []string{"D"}, "hello ", 5},
		{"one\ntwo\nthree", 5, []string{"d", "d"}, "one\nthree", 4},
		{"one\ntwo", 1, []string{"j"}, "one\ntwo", 5},
		{"one\ntwo", 5, []string{"k"}, "one\ntwo", 1},
		{"one\ntwo", 5, []string{"g", "g"}, "one\ntwo", 0},
		{"one\ntwo", 1, []string{"G"}, "one\ntwo", 4},
		{"ab", 0, []string{"p"}, "aPASTEb", 5},
	}
	for _, c := range cases {
		b, _ := apply(t, c.text, c.cur, c.keys...)
		if string(b.t) != c.want || b.cur != c.at {
			t.Errorf("%q at %d, %v → %q at %d; want %q at %d", c.text, c.cur, c.keys, string(b.t), b.cur, c.want, c.at)
		}
	}
	// the ones that go back to typing
	for _, c := range []struct {
		keys []string
		want string
		at   int
	}{
		{[]string{"c", "w"}, " world", 0},
		{[]string{"c", "i", "w"}, " world", 0},
		{[]string{"C"}, "", 0},
		{[]string{"A"}, "hello world", 11},
		{[]string{"o"}, "hello world\n", 12},
	} {
		b, r := apply(t, "hello world", 0, c.keys...)
		if !r.insert || string(b.t) != c.want || b.cur != c.at {
			t.Errorf("%v → %q at %d insert=%v", c.keys, string(b.t), b.cur, r.insert)
		}
	}
}

// Esc while writing a chat message: the box's NORMAL, where the keys edit
// the text; u / Ctrl+r; i types again; Enter sends; a second Esc leaves.
// INSERT keeps its keys: Ctrl+j a new line, Ctrl+k to the end, Space a space.
func TestMessageBoxVim(t *testing.T) {
	clipboardText = func() string { return "" }
	m, _ := waModel(t, "919800000001@s.whatsapp.net")
	m = press(m, "i")
	m = typeText(m, "hello")
	m = press(m, " ")
	m = typeText(m, "big world")
	if string(m.input) != "hello big world" {
		t.Fatalf("Space types a space: %q", string(m.input))
	}
	m = press(m, "esc")
	if !m.boxNormal || m.mode != modeChatMsg || !strings.Contains(m.View(), "leave the box") {
		t.Fatal("esc: the box's NORMAL")
	}
	m = typeText(m, "bbdw") // typed fast: one message
	if string(m.input) != "hello world" {
		t.Fatalf("b b dw: %q", string(m.input))
	}
	m = press(m, "u")
	if string(m.input) != "hello big world" {
		t.Fatalf("u: %q", string(m.input))
	}
	m = press(m, "ctrl+r")
	if string(m.input) != "hello world" {
		t.Fatalf("Ctrl+r: %q", string(m.input))
	}
	m = press(m, "A")
	if m.boxNormal {
		t.Fatal("A types again")
	}
	m = press(m, "ctrl+j")
	m = typeText(m, "x")
	if string(m.input) != "hello world\nx" {
		t.Fatalf("Ctrl+j is a new line: %q", string(m.input))
	}
	m = press(m, "left", "left", "ctrl+k")
	if string(m.input) != "hello world" {
		t.Fatalf("Ctrl+k deletes to the end: %q", string(m.input))
	}
	m = press(m, "esc", "esc")
	if m.mode != modeNormal || m.boxNormal {
		t.Fatal("a second esc leaves the box")
	}
	m = press(m, "i")
	if string(m.input) != "hello world" {
		t.Fatalf("the draft stays: %q", string(m.input))
	}
	// Enter in the box's NORMAL sends
	m = press(m, "esc", "enter")
	if len(m.input) != 0 {
		t.Fatalf("enter sends from the box's NORMAL: %q", string(m.input))
	}
}

// The compose body has the same NORMAL; Esc twice more warns, then discards.
func TestComposeBodyVim(t *testing.T) {
	m := model(t)
	m = press(m, "o", "tab", "tab", "tab")
	m = typeText(m, "dear team")
	m = press(m, "esc", "0", "x")
	if got := m.comp.body.Value(); got != "ear team" {
		t.Fatalf("x in the body: %q", got)
	}
	m = press(m, "u")
	if got := m.comp.body.Value(); got != "dear team" {
		t.Fatalf("u in the body: %q", got)
	}
	m = press(m, "w", "c", "w")
	m = typeText(m, "all")
	if got := m.comp.body.Value(); got != "dear all" {
		t.Fatalf("cw: %q", got)
	}
}
