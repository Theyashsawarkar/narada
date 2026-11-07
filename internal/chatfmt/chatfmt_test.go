package chatfmt

import (
	"strings"
	"testing"
)

// What Google really returned for a message sent with "- " lines.
const real = "Hi Naeem, today's update on onadsSuper:\n\n  - Two-way brief built as per the proposal you approved, merged (PR \\#30) and\n    deployed to staging\n\n  - Either side writes and sends first\n\nPending:\n\n  - Jignesh sir: client flow confirmation\n"

func plainLines(ls []Line) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.Prefix+Plain(l.Spans))
	}
	return out
}

func TestRealBullets(t *testing.T) {
	got := plainLines(Lines(Parse(real), 200))
	want := []string{"Hi Naeem, today's update on onadsSuper:", "",
		"• Two-way brief built as per the proposal you approved, merged (PR #30) and deployed to staging",
		"• Either side writes and sends first", "", "Pending:", "", "• Jignesh sir: client flow confirmation"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s", strings.Join(got, "\n"))
	}
}

func TestWrapKeepsTheHanging(t *testing.T) {
	got := plainLines(Lines(Parse("- one two three four five six"), 14))
	if got[0] != "• one two" || !strings.HasPrefix(got[1], "  three") {
		t.Fatalf("%q", got)
	}
}

func TestInline(t *testing.T) {
	b := Parse("**UnitFlow AI** and *soft* and _soft_ ~~gone~~ `a*b*c` AWS\\_REGION snake_case 2*3*4 [docs](https://x.io/a) <https://y.io> [z.io/a](http://z.io/a)")
	sp := b[0].Spans
	has := func(text string, ok func(Span) bool) {
		t.Helper()
		for _, s := range sp {
			if s.Text == text && ok(s) {
				return
			}
		}
		t.Fatalf("no %q in %+v", text, sp)
	}
	has("UnitFlow AI", func(s Span) bool { return s.Bold })
	has("soft", func(s Span) bool { return s.Italic })
	has("gone", func(s Span) bool { return s.Strike })
	has("a*b*c", func(s Span) bool { return s.Code })
	has("docs", func(s Span) bool { return s.URL == "https://x.io/a" })
	has("z.io/a", func(s Span) bool { return s.URL == "" })
	p := Plain(sp)
	for _, want := range []string{"AWS_REGION", "snake_case", "https://y.io"} {
		if !strings.Contains(p, want) {
			t.Fatalf("no %q in %q", want, p)
		}
	}
}

func TestBlocks(t *testing.T) {
	src := "# Title\n\n> said\n> this\n\n```\nx  y\n*z*\n```\n\n1. one\n2. two\n   - deep\n\nend\\\nnext"
	got := strings.Join(plainLines(Lines(Parse(src), 80)), "\n")
	want := "Title\n\n▏ said this\n\nx  y\n*z*\n\n1. one\n2. two\n  • deep\n\nend\nnext"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMentions(t *testing.T) {
	src := `<chat-user data-user="users/1"></chat-user> Update <users/2> <chat-user data-user="users/3"></chat-user>`
	got := Mentions(src, func(u string) string {
		if u == "users/3" {
			return ""
		}
		return "@N" + u[6:]
	})
	if p := Plain(Parse(got)[0].Spans); p != "@N1 Update @N2 @someone" {
		t.Fatalf("%q", p)
	}
}

func TestForSend(t *testing.T) {
	for _, c := range [][2]string{
		{"one line", "one line"},
		{"*What it is*\nThe pipeline", "**What it is**  \nThe pipeline"},
		{"Update:\n• a\n• b\nPending:\n- c", "Update:\n\n- a\n- b\n\nPending:\n\n- c"},
		{"a\n\nb", "a\n\nb"},
		{"2*3*4 and a * b * c and `*x*` and **y** and snake_case", "2*3*4 and a * b * c and `*x*` and **y** and snake_case"},
		{"look:\n```\n*a*\nb\n```\nend", "look:\n\n```\n*a*\nb\n```\nend"},
	} {
		if got := ForSend(c[0]); got != c[1] {
			t.Errorf("ForSend(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

// What you typed comes out the other side as you typed it.
func TestRoundTrip(t *testing.T) {
	src := "Title line\nsecond line\n\n- a *bold* one\n- b\n\nAfter"
	got := strings.Join(plainLines(Lines(Parse(ForSend(src)), 80)), "\n")
	want := "Title line\nsecond line\n\n• a bold one\n• b\n\nAfter"
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
	s := Source(real)
	if !strings.Contains(s, "- Two-way brief built as per the proposal you approved, merged (PR #30) and deployed to staging\n- Either") {
		t.Fatalf("source:\n%s", s)
	}
	if got := Source("**b** _i_ `c` [t](https://x.io)"); got != "**b** _i_ `c` [t](https://x.io)" {
		t.Fatalf("%q", got)
	}
}
