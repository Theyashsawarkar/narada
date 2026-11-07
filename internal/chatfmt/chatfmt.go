// Package chatfmt is Google Chat's message formatting. Chat reads a message
// sent through its API as Markdown, and gives back Markdown (formattedText,
// hard-wrapped at 80 columns) beside a plain text with the structure gone.
// ForSend turns what you typed into Markdown that shows as you typed it;
// Parse and Lines turn what comes back into something to draw; Source turns
// it back into something to edit.
package chatfmt

import (
	"html"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Span is a run of text in one style.
type Span struct {
	Text                       string
	Bold, Italic, Strike, Code bool
	URL                        string // a link whose text isn't its address
}

// Kinds of block.
const (
	Para = iota
	Item
	Quote
	Code
	Heading
)

// Block is a paragraph, a list item, a quote, a code block or a heading.
// Level is how deep it sits in nested lists; Marker a list item's "1." or "-".
type Block struct {
	Kind   int
	Level  int
	Marker string
	Spans  []Span
	Raw    string // a code block's text
}

var (
	reItem    = regexp.MustCompile(`^(\s*)([-*+•·◦]|\d+[.)])\s+(.*)$`)
	reQuote   = regexp.MustCompile(`^\s*>\s?(.*)$`)
	reHeading = regexp.MustCompile(`^\s*#{1,6}\s+(.*?)\s*#*\s*$`)
	reRule    = regexp.MustCompile(`^\s*(?:(?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$`)
	reUser    = regexp.MustCompile(`<chat-user data-user="([^"]*)">[^<]*</chat-user>|<(users/[^>|\s]+)>`)
	reBullet  = regexp.MustCompile(`^(\s*)[•·◦]\s+`)
)

// Parse reads Chat's Markdown. A line break is a space unless it is a hard
// one (two spaces or a backslash at the end of the line).
func Parse(src string) []Block {
	var (
		blocks []Block
		para   strings.Builder
		kind   = Para
		level  int
		marker string
		stack  []int
		fence  = false
		code   []string
	)
	flush := func() {
		if para.Len() > 0 {
			blocks = append(blocks, Block{Kind: kind, Level: level, Marker: marker, Spans: inline(para.String(), Span{})})
		}
		para.Reset()
		kind, marker = Para, ""
	}
	add := func(l string) {
		l = strings.TrimLeft(l, " \t")
		hard := strings.HasSuffix(l, "  ") || strings.HasSuffix(l, "\\")
		l = strings.TrimRight(strings.TrimSuffix(strings.TrimRight(l, " "), "\\"), " ")
		if para.Len() > 0 {
			if prev := para.String(); !strings.HasSuffix(prev, "\n") {
				para.WriteByte(' ')
			}
		}
		para.WriteString(l)
		if hard {
			para.WriteByte('\n')
		}
	}
	for _, l := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		if fence {
			if strings.HasPrefix(strings.TrimSpace(l), "```") {
				blocks = append(blocks, Block{Kind: Code, Raw: strings.Join(code, "\n")})
				fence, code = false, nil
			} else {
				code = append(code, l)
			}
			continue
		}
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			flush()
			continue
		case strings.HasPrefix(t, "```") && !strings.Contains(t[3:], "```"):
			flush()
			fence = true
			continue
		case reRule.MatchString(l) && para.Len() == 0:
			blocks = append(blocks, Block{Kind: Para, Spans: []Span{{Text: "───"}}})
			continue
		}
		if m := reItem.FindStringSubmatch(l); m != nil {
			flush()
			in := len(m[1])
			for len(stack) > 0 && stack[len(stack)-1] > in {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 || stack[len(stack)-1] < in {
				stack = append(stack, in)
			}
			kind, level, marker = Item, len(stack)-1, m[2]
			add(m[3])
			continue
		}
		if m := reQuote.FindStringSubmatch(l); m != nil {
			if kind != Quote {
				flush()
			}
			kind, level = Quote, 0
			add(m[1])
			continue
		}
		if m := reHeading.FindStringSubmatch(l); m != nil {
			flush()
			blocks = append(blocks, Block{Kind: Heading, Spans: inline(m[1], Span{Bold: true})})
			continue
		}
		if kind == Item && (l[0] == ' ' || l[0] == '\t') {
			add(l) // a list item goes on
			continue
		}
		if kind == Quote {
			flush()
		}
		if kind != Item && para.Len() == 0 {
			stack, level = nil, 0
			if l[0] == ' ' && len(blocks) > 0 && blocks[len(blocks)-1].Kind == Item {
				level = blocks[len(blocks)-1].Level + 1 // a second paragraph in a list item
			}
		}
		add(l)
	}
	if fence {
		blocks = append(blocks, Block{Kind: Code, Raw: strings.Join(code, "\n")})
	}
	flush()
	return blocks
}

// inline reads bold, italic, strikes, code and links in a run of text.
func inline(s string, st Span) []Span {
	var out []Span
	var b strings.Builder
	emit := func() {
		if b.Len() > 0 {
			sp := st
			sp.Text = html.UnescapeString(b.String())
			out = append(out, sp)
		}
		b.Reset()
	}
	styled := func(inner string, set func(*Span)) {
		emit()
		n := st
		set(&n)
		out = append(out, inline(inner, n)...)
	}
	word := func(r byte) bool {
		return r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= 0x80
	}
	for i := 0; i < len(s); {
		c := s[i]
		rest := s[i:]
		switch {
		case c == '\\' && i+1 < len(s) && strings.IndexByte("\\`*_{}[]()#+-.!<>~|&\"'", s[i+1]) >= 0:
			b.WriteByte(s[i+1])
			i += 2
			continue
		case c == '`':
			n := len(rest) - len(strings.TrimLeft(rest, "`"))
			if end := strings.Index(rest[n:], rest[:n]); end >= 0 {
				emit()
				sp := st
				sp.Code, sp.Text = true, strings.TrimSpace(rest[n:n+end])
				out = append(out, sp)
				i += n + end + n
				continue
			}
		case strings.HasPrefix(rest, "**") || strings.HasPrefix(rest, "__"):
			d := rest[:2]
			if end := strings.Index(rest[2:], d); end > 0 && rest[2] != ' ' {
				styled(rest[2:2+end], func(n *Span) { n.Bold = true })
				i += 2 + end + 2
				continue
			}
		case strings.HasPrefix(rest, "~~"):
			if end := strings.Index(rest[2:], "~~"); end > 0 {
				styled(rest[2:2+end], func(n *Span) { n.Strike = true })
				i += 2 + end + 2
				continue
			}
		case c == '*' || c == '_' || c == '~':
			if i+1 < len(s) && s[i+1] != ' ' && (i == 0 || !word(s[i-1]) || c == '*') {
				end := closing(rest[1:], c, word)
				if end > 0 {
					styled(rest[1:1+end], func(n *Span) {
						if c == '~' {
							n.Strike = true
						} else {
							n.Italic = true
						}
					})
					i += 1 + end + 1
					continue
				}
			}
		case c == '[':
			if mid := strings.Index(rest, "]("); mid > 0 {
				if end := strings.IndexByte(rest[mid:], ')'); end > 0 {
					emit()
					url, text := rest[mid+2:mid+end], rest[1:mid]
					spans := inline(text, st)
					if !sameAddress(url, Plain(spans)) {
						for k := range spans {
							spans[k].URL = url
						}
					}
					out = append(out, spans...)
					i += mid + end + 1
					continue
				}
			}
		case c == '<':
			if m := reUser.FindString(rest); m != "" && strings.HasPrefix(rest, m) {
				b.WriteString("@someone")
				i += len(m)
				continue
			}
			if end := strings.IndexByte(rest, '>'); end > 0 {
				in := rest[1:end]
				if addr, text, ok := strings.Cut(in, "|"); ok && strings.Contains(addr, "://") {
					emit()
					spans := inline(text, st)
					for k := range spans {
						spans[k].URL = addr
					}
					out = append(out, spans...)
					i += end + 1
					continue
				}
				if strings.Contains(in, "://") && !strings.ContainsAny(in, " \t") {
					b.WriteString(in)
					i += end + 1
					continue
				}
			}
		}
		b.WriteByte(c)
		i++
	}
	emit()
	return out
}

// sameAddress: a link's text is only its address (Chat links bare domains).
func sameAddress(url, text string) bool {
	bare := func(s string) string {
		if _, rest, ok := strings.Cut(s, "://"); ok {
			s = rest
		}
		return strings.TrimSuffix(s, "/")
	}
	return bare(url) == bare(text)
}

// closing finds the closing delimiter of a one-character style: not after a
// space, and for _ and ~ not inside a word.
func closing(s string, c byte, word func(byte) bool) int {
	for i := 1; i < len(s); i++ {
		if s[i] != c || s[i-1] == ' ' || s[i-1] == '\\' {
			continue
		}
		if c != '*' && i+1 < len(s) && word(s[i+1]) {
			continue
		}
		if c == '*' && i+1 < len(s) && s[i+1] == '*' {
			i++
			continue
		}
		return i
	}
	return -1
}

// Plain is the text of some spans.
func Plain(spans []Span) string {
	var b strings.Builder
	for _, sp := range spans {
		b.WriteString(sp.Text)
	}
	return b.String()
}

// Mentions replaces Chat's mention tags with names (name: "users/123" to
// "@Dana Reyes"), before anything else reads the text.
func Mentions(src string, name func(user string) string) string {
	return reUser.ReplaceAllStringFunc(src, func(tag string) string {
		m := reUser.FindStringSubmatch(tag)
		if n := name(m[1] + m[2]); n != "" {
			return n
		}
		return tag
	})
}

// Line is a line to draw: what goes before it (a bullet, its indent), then
// the text.
type Line struct {
	Kind   int
	Prefix string
	Spans  []Span
}

// Lines lays blocks out w columns wide: list items sit together, a blank
// line sets apart everything else.
func Lines(blocks []Block, w int) []Line {
	w = max(w, 10)
	var out []Line
	for i, bl := range blocks {
		if i > 0 && !(bl.Kind == Item && blocks[i-1].Kind == Item) {
			out = append(out, Line{})
		}
		switch bl.Kind {
		case Code:
			for _, l := range strings.Split(bl.Raw, "\n") {
				for _, part := range strings.Split(ansi.Hardwrap(l, w, false), "\n") {
					out = append(out, Line{Kind: Code, Spans: []Span{{Text: part, Code: true}}})
				}
			}
			continue
		}
		first, rest := "", ""
		switch bl.Kind {
		case Item:
			mark := bl.Marker
			if !(len(mark) > 1 || mark[0] >= '0' && mark[0] <= '9') {
				mark = "•"
			}
			in := strings.Repeat("  ", bl.Level)
			first, rest = in+mark+" ", in+strings.Repeat(" ", ansi.StringWidth(mark)+1)
		case Quote:
			first, rest = "▏ ", "▏ "
		default:
			first = strings.Repeat("  ", bl.Level)
			rest = first
		}
		for j, spans := range wrapSpans(withLinks(bl.Spans), max(w-ansi.StringWidth(first), 4)) {
			p := rest
			if j == 0 {
				p = first
			}
			out = append(out, Line{Kind: bl.Kind, Prefix: p, Spans: spans})
		}
	}
	return out
}

// withLinks writes a link's address after its text, where a terminal can
// show it and click it.
func withLinks(spans []Span) []Span {
	var out []Span
	for i, sp := range spans {
		out = append(out, sp)
		if sp.URL != "" && (i+1 == len(spans) || spans[i+1].URL != sp.URL) {
			out = append(out, Span{Text: " (" + sp.URL + ")"})
		}
	}
	return out
}

// wrapSpans breaks spans into lines at most w wide, on spaces and on hard
// line breaks, each word keeping its style.
func wrapSpans(spans []Span, w int) [][]Span {
	var lines [][]Span
	var cur []Span
	width := 0
	push := func(sp Span, text string) {
		if n := len(cur); n > 0 && sameStyle(cur[n-1], sp) {
			cur[n-1].Text += text
		} else {
			sp.Text = text
			cur = append(cur, sp)
		}
		width += ansi.StringWidth(text)
	}
	breakLine := func() {
		if n := len(cur); n > 0 {
			cur[n-1].Text = strings.TrimRight(cur[n-1].Text, " ")
		}
		lines = append(lines, cur)
		cur, width = nil, 0
	}
	for _, sp := range spans {
		for pi, part := range strings.Split(sp.Text, "\n") {
			if pi > 0 {
				breakLine()
			}
			for _, tok := range tokens(part) {
				tw := ansi.StringWidth(tok)
				if strings.TrimSpace(tok) == "" {
					if width > 0 && width+tw <= w {
						push(sp, tok)
					}
					continue
				}
				if width > 0 && width+tw > w {
					breakLine()
				}
				for tw > w { // one word wider than the line
					head := ansi.Truncate(tok, w, "")
					push(sp, head)
					breakLine()
					tok = tok[len(head):]
					tw = ansi.StringWidth(tok)
				}
				push(sp, tok)
			}
		}
	}
	if len(cur) > 0 || len(lines) == 0 {
		breakLine()
	}
	return lines
}

func sameStyle(a, b Span) bool {
	return a.Bold == b.Bold && a.Italic == b.Italic && a.Strike == b.Strike && a.Code == b.Code
}

// tokens splits text into words and the spaces between them.
func tokens(s string) []string {
	var out []string
	for s != "" {
		i := strings.IndexByte(s, ' ')
		switch {
		case i < 0:
			return append(out, s)
		case i == 0:
			j := len(s) - len(strings.TrimLeft(s, " "))
			out, s = append(out, s[:j]), s[j:]
		default:
			out, s = append(out, s[:i]), s[i:]
		}
	}
	return out
}

// ForSend turns what you typed into Markdown that Chat shows the way you
// typed it: a line break stays one, "•" is a bullet and *this* is bold, as
// in Chat's own box. Code stays as it is.
func ForSend(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	var out []string
	fence := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") && !strings.Contains(t[3:], "```") {
			fence = !fence
			if !fence || i == 0 || strings.TrimSpace(lines[i-1]) == "" {
				out = append(out, l)
			} else {
				out = append(out, "", l)
			}
			continue
		}
		if fence {
			out = append(out, l)
			continue
		}
		l = reBullet.ReplaceAllString(l, "$1- ")
		l = boldStars(l)
		if i+1 < len(lines) && t != "" {
			next := strings.TrimSpace(lines[i+1])
			item, nextItem := reItem.MatchString(l), reItem.MatchString(lines[i+1])
			switch {
			case next == "" || strings.HasPrefix(next, "```"):
			case item != nextItem:
				out = append(out, strings.TrimRight(l, " "), "")
				continue
			case !item && !strings.HasPrefix(t, "#"):
				l = strings.TrimRight(l, " ") + "  "
			}
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// boldStars makes *this* **this**, leaving code, ** and stray stars alone.
func boldStars(l string) string {
	var b strings.Builder
	for i := 0; i < len(l); {
		c := l[i]
		switch {
		case c == '\\' && i+1 < len(l):
			b.WriteString(l[i : i+2])
			i += 2
			continue
		case c == '`':
			if end := strings.IndexByte(l[i+1:], '`'); end >= 0 {
				b.WriteString(l[i : i+end+2])
				i += end + 2
				continue
			}
		case strings.HasPrefix(l[i:], "**"):
			b.WriteString("**")
			i += 2
			if end := strings.Index(l[i:], "**"); end >= 0 {
				b.WriteString(l[i : i+end+2])
				i += end + 2
			}
			continue
		case c == '*' && i+1 < len(l) && l[i+1] != ' ' && l[i+1] != '*' && (i == 0 || !wordByte(l[i-1])):
			if end := strings.IndexByte(l[i+1:], '*'); end > 0 && l[i+end] != ' ' {
				after := i + end + 2
				if after == len(l) || !wordByte(l[after]) {
					b.WriteString("**" + l[i+1:i+end+1] + "**")
					i = after
					continue
				}
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

func wordByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '*' || c >= 0x80
}

// Source is Chat's Markdown back as something to read, copy or edit: its
// hard wrapping and escapes gone, the bullets and styles kept.
func Source(formatted string) string {
	var b strings.Builder
	blocks := Parse(formatted)
	for i, bl := range blocks {
		if i > 0 {
			if bl.Kind == Item && blocks[i-1].Kind == Item {
				b.WriteByte('\n')
			} else {
				b.WriteString("\n\n")
			}
		}
		in := strings.Repeat("  ", bl.Level)
		switch bl.Kind {
		case Code:
			b.WriteString("```\n" + bl.Raw + "\n```")
		case Item:
			mark := bl.Marker
			if !(len(mark) > 1 || mark[0] >= '0' && mark[0] <= '9') {
				mark = "-"
			}
			b.WriteString(in + mark + " " + sourceSpans(bl.Spans))
		case Quote:
			b.WriteString("> " + strings.ReplaceAll(sourceSpans(bl.Spans), "\n", "\n> "))
		case Heading:
			for k := range bl.Spans {
				bl.Spans[k].Bold = false
			}
			b.WriteString("# " + sourceSpans(bl.Spans))
		default:
			b.WriteString(in + sourceSpans(bl.Spans))
		}
	}
	return b.String()
}

func sourceSpans(spans []Span) string {
	var b strings.Builder
	for _, sp := range spans {
		t := sp.Text
		if sp.Code {
			t = "`" + t + "`"
		} else if strings.TrimSpace(t) != "" {
			lead := t[:len(t)-len(strings.TrimLeft(t, " "))]
			trail := t[len(strings.TrimRight(t, " ")):]
			t = strings.TrimSpace(t)
			if sp.URL != "" {
				t = "[" + t + "](" + sp.URL + ")"
			}
			if sp.Strike {
				t = "~~" + t + "~~"
			}
			if sp.Italic {
				t = "_" + t + "_"
			}
			if sp.Bold {
				t = "**" + t + "**"
			}
			t = lead + t + trail
		}
		b.WriteString(t)
	}
	return b.String()
}
