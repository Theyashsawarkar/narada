package tui

import (
	"strings"
	"testing"
)

func TestFitCells(t *testing.T) {
	if c, r := fitCells(480, 320, 42, 14); c != 42 || r != 14 {
		t.Fatalf("landscape: %d×%d", c, r)
	}
	if c, r := fitCells(300, 900, 42, 14); r != 14 || c > 10 {
		t.Fatalf("portrait keeps to the row limit: %d×%d", c, r)
	}
	if c, _ := fitCells(64, 64, 42, 14); c > 9 {
		t.Fatalf("a small sticker isn't blown up: %d cols", c)
	}
}

func TestPlaceholders(t *testing.T) {
	lines := placeholders(0x0a0b0c, 3, 2)
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "\x1b[38;2;10;11;12m") {
		t.Fatalf("%q", lines)
	}
	// each cell: the placeholder, its row, its column
	want := string([]rune{0x10EEEE, diacritics[1], diacritics[2]})
	if !strings.Contains(lines[1], want) || strings.Count(lines[0], "\U0010EEEE") != 3 {
		t.Fatalf("row 1: %q", lines[1])
	}
	e := &imgEntry{id: 7, frames: []string{"/a.png", "/b.png"}, delays: []int{80, 80}}
	cmds := strings.Join(kittyPlace(e, 10, 4), "")
	for _, w := range []string{"a=T,U=1,q=2,f=100,t=f,i=7,c=10,r=4;", "a=f,i=7", "s=3,v=1"} {
		if !strings.Contains(cmds, w) {
			t.Fatalf("missing %q in %q", w, cmds)
		}
	}
}
