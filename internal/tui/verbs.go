package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/app"
)

// Vim's verbs (plan §4.1, phase 5): d with a motion (dj, d3j, dG), . to
// repeat the last change, u / Ctrl+r over a stack of changes and their
// inverses, marks (m{a-z} / '{a-z}, kept in the store), the jump list
// (Ctrl+o / ]j), a search inside the open conversation (Space f h, then
// n / N), * / # through one sender's messages, gv for the last range.

// change is a change . repeats: the action and its count.
type change struct {
	act   string
	count int
}

// repeatable are the actions . repeats (on the item you're on now).
var repeatable = map[string]bool{
	"archive": true, "delete": true, "delete.down": true, "delete.up": true, "delete.bottom": true, "delete.top": true,
	"read.toggle": true, "read": true, "unread": true, "star": true, "pin": true, "wa.mute": true,
}

// jumpPos is a place in the jump list: where, which section, which item,
// and whether it was open.
type jumpPos struct {
	place, sec int
	key        string
	open       bool
}

// ------------------------------------------------------------- d{motion}

// deleteMotion is dj, dk, dG, dgg (with counts: 3dj, d3j): the threads
// from here to where the motion goes, behind one question.
func (m Model) deleteMotion(act string, count int) (tea.Model, tea.Cmd) {
	if m.tab != tabMail || m.focus != 0 {
		m.say("d with a motion trashes threads in the mail list; in a chat, dd deletes one message of yours")
		return m, nil
	}
	if !m.confirmed {
		from, to := m.sel, m.sel
		switch act {
		case "delete.down":
			to = min(m.count()-1, m.sel+count)
		case "delete.up":
			from = max(0, m.sel-count)
		case "delete.bottom":
			to = m.count() - 1
		case "delete.top":
			from = 0
		}
		m.selected = map[string]bool{}
		for i := from; i <= to; i++ {
			if k := m.keyAt(i); k != "" && !strings.Contains(k, outboxPrefix) {
				m.selected[k] = true
			}
		}
	}
	return m.deleteIt(1)
}

// ------------------------------------------------------------- . repeat

func (m Model) repeatChange() (tea.Model, tea.Cmd) {
	if m.lastChange.act == "" {
		m.say(". repeats the last change (e, dd, ~, Space m s…): nothing yet")
		return m, nil
	}
	m.lastCounted = m.lastChange.count > 1
	return m.run(m.lastChange.act, m.lastChange.count)
}

// ---------------------------------------------------------- undo / redo

func (m *Model) pushUndo(u undo) {
	m.undos = append(m.undos, u)
	if len(m.undos) > 100 {
		m.undos = m.undos[1:]
	}
	if u.what != "send" {
		m.redos = nil // a new change: what was undone can't come back
	}
}

// redoCmd is Ctrl+r: the change u last undid, done again. A send is never
// redone (u only takes it back inside its undo window).
func (m *Model) redoCmd() tea.Cmd {
	if len(m.redos) == 0 {
		m.say("nothing to redo")
		return nil
	}
	u := m.redos[len(m.redos)-1]
	m.redos = m.redos[:len(m.redos)-1]
	m.undos = append(m.undos, u)
	a := m.a
	for _, id := range u.ids {
		switch u.what {
		case "archive":
			_ = a.Store.SetThreadLabels(u.account, id, nil, []string{"INBOX"})
		case "trash":
			_ = a.Store.SetThreadLabels(u.account, id, []string{"TRASH"}, []string{"INBOX"})
		case "read":
			_ = a.Store.SetThreadLabels(u.account, id, nil, []string{"UNREAD"})
		case "unread":
			_ = a.Store.SetThreadLabels(u.account, id, []string{"UNREAD"}, nil)
		}
	}
	m.reload()
	return m.act("redone: "+u.what, func(ctx context.Context) error {
		for _, id := range u.ids {
			acc := u.account
			if t, err := a.Store.Thread(u.account, id); err == nil {
				acc = t.Account
			}
			var err error
			switch u.what {
			case "archive":
				err = a.Archive(ctx, acc, id)
			case "trash":
				err = a.Trash(ctx, acc, id)
			case "star":
				err = a.Star(ctx, acc, id, true)
			case "unstar":
				err = a.Star(ctx, acc, id, false)
			case "read":
				err = a.MarkRead(ctx, acc, id, true)
			case "unread":
				err = a.MarkRead(ctx, acc, id, false)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// ------------------------------------------------------------ gv

// rememberVisual keeps the range VISUAL had, for gv.
func (m *Model) rememberVisual() {
	m.lastVis = &jumpPos{place: m.place(), sec: m.sec, key: m.keyAt(m.visualFrom)}
	m.lastVisTo = m.keyAt(m.sel)
}

func (m Model) visualAgain() (tea.Model, tea.Cmd) {
	if m.lastVis == nil || m.lastVis.place != m.place() || m.lastVis.sec != m.sec {
		m.say("gv picks the last range again: there's none in this list")
		return m, nil
	}
	from, to := m.indexOf(m.lastVis.key), m.indexOf(m.lastVisTo)
	if from < 0 || to < 0 {
		m.say("the last range isn't in this list any more")
		return m, nil
	}
	m.visual, m.visualFrom, m.sel = true, from, to
	m.visualRange()
	m.fixView()
	return m, nil
}

func (m *Model) indexOf(key string) int {
	for i := range m.count() {
		if m.keyAt(i) == key {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------- marks, jump list

func (m Model) here() jumpPos {
	return jumpPos{place: m.place(), sec: m.sec, key: m.keyAt(m.sel), open: m.focus == 1}
}

// recordJump keeps where you are before a jump (open, gg, G, a mark…),
// so Ctrl+o comes back to it.
func (m *Model) recordJump() {
	if m.inAISection() {
		return
	}
	p := m.here()
	if p.key == "" {
		return
	}
	m.jumps = m.jumps[:min(m.jumpAt, len(m.jumps))]
	if n := len(m.jumps); n > 0 && m.jumps[n-1].key == p.key && m.jumps[n-1].place == p.place {
		m.jumps[n-1] = p
	} else {
		m.jumps = append(m.jumps, p)
	}
	if len(m.jumps) > 100 {
		m.jumps = m.jumps[1:]
	}
	m.jumpAt = len(m.jumps)
}

// goTo goes to a place, section and item; it opens it again if it was open.
func (m Model) goTo(p jumpPos) (tea.Model, tea.Cmd) {
	if p.place != m.place() || m.inAISection() {
		mm, _ := m.goPlace(p.place)
		m = mm.(Model)
	}
	if i := m.indexOf(p.key); i < 0 && p.sec != m.sec {
		m.sec, m.sel, m.offset, m.filter = p.sec, 0, 0, ""
		m.reload()
	}
	i := m.indexOf(p.key)
	if i < 0 && m.sec != 0 {
		m.sec, m.sel, m.offset = 0, 0, 0
		m.reload()
		i = m.indexOf(p.key)
	}
	if i < 0 {
		m.say("it isn't in this place's lists any more")
		return m, nil
	}
	m.sel = i
	m.focus = 0
	m.loadPreview(true)
	m.fixView()
	if p.open {
		return m.actKey("enter")
	}
	return m, nil
}

// jumpBy is Ctrl+o (step -1) and ]j (+1) through the jump list.
func (m Model) jumpBy(step int) (tea.Model, tea.Cmd) {
	if step < 0 && m.jumpAt >= len(m.jumps) {
		// leaving the newest place: keep it, so ]j can come back
		m.recordJump()
		m.jumpAt = len(m.jumps) - 1
	}
	to := m.jumpAt + step
	if to < 0 || to >= len(m.jumps) {
		m.say(map[bool]string{true: "at the oldest jump", false: "at the newest jump"}[step < 0])
		return m, nil
	}
	m.jumpAt = to
	jumps, at := m.jumps, m.jumpAt
	mm, cmd := m.goTo(jumps[to])
	if x, ok := mm.(Model); ok {
		x.jumps, x.jumpAt = jumps, at
		return x, cmd
	}
	return mm, cmd
}

func (m Model) setMark(letter string) (tea.Model, tea.Cmd) {
	k := m.keyAt(m.sel)
	if k == "" || m.inAISection() {
		m.say("m" + letter + " marks a thread or chat: pick one in the list")
		return m, nil
	}
	if err := m.a.Store.SetMark(letter, m.place(), m.sec, k); err != nil {
		m.err = err.Error()
		return m, nil
	}
	m.say("mark " + letter + " set · '" + letter + " comes back here")
	return m, nil
}

func (m Model) goMark(letter string) (tea.Model, tea.Cmd) {
	place, sec, item, ok, err := m.a.Store.Mark(letter)
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	if !ok {
		m.say("mark " + letter + " isn't set (m" + letter + " sets it)")
		return m, nil
	}
	m.recordJump()
	return m.goTo(jumpPos{place: place, sec: sec, key: item})
}

// ------------------------------------------- searching what's open (Space f h)

// convMatches is the lines of what's open that hold q.
func (m Model) convMatches(q string) []int {
	q = strings.ToLower(q)
	var out []int
	for i, l := range m.preview {
		if strings.Contains(strings.ToLower(ansi.Strip(l)), q) {
			out = append(out, i)
		}
	}
	return out
}

// convFind shows the match after (step 1) or before (-1) the one shown.
func (m Model) convFind(step int) (tea.Model, tea.Cmd) {
	hits := m.convMatches(m.convQ)
	if len(hits) == 0 {
		m.say("no " + fmt.Sprintf("%q", m.convQ) + " in what's open")
		return m, nil
	}
	cur := m.convHit
	if step > 0 {
		cur = slices.IndexFunc(hits, func(l int) bool { return l > m.convLine })
		if cur < 0 {
			cur = 0 // wraps to the first
		}
	} else {
		cur = -1
		for i, l := range hits {
			if l < m.convLine {
				cur = i
			}
		}
		if cur < 0 {
			cur = len(hits) - 1
		}
	}
	m.convHit, m.convLine = cur, hits[cur]
	m.focus = 1
	m.scroll = max(0, hits[cur]-2)
	m.say(fmt.Sprintf("%q: %d of %d · n / N next / previous · Esc stops", m.convQ, cur+1, len(hits)))
	return m, nil
}

// startConvSearch is Space f h with something open: what to look for.
func (m Model) startConvSearch() (tea.Model, tea.Cmd) {
	if m.curKey == "" || len(m.preview) == 0 {
		return m.findAnything(false)
	}
	m.mode, m.input, m.back = modeConvSearch, nil, 0
	return m, nil
}

// ------------------------------------------------------------ * and #

// senderStep is * (step 1) and # (-1): the next message from the picked
// message's sender in the open chat.
func (m Model) senderStep(step int) (tea.Model, tea.Cmd) {
	if m.tab != tabChat || len(m.chatMsgs) == 0 {
		m.say("* and # step through one sender's messages in a chat (star moved to Space m s)")
		return m, nil
	}
	if !m.pickingMsg() {
		m.startPicking()
	}
	who := m.chatMsgs[m.chatSel]
	for i := m.chatSel + step; i >= 0 && i < len(m.chatMsgs); i += step {
		c := m.chatMsgs[i]
		if c.Sender == who.Sender && c.Mine == who.Mine {
			m.chatSel = i
			m.fixView()
			m.placePicked("middle")
			return m, nil
		}
	}
	m.say("no " + map[bool]string{true: "later", false: "earlier"}[step > 0] + " message from " + firstNonEmpty(who.SenderName, "them"))
	return m, nil
}

// ------------------------------------------------------- :news (what moved)

// movedRows is the "what moved" table: old key → where it went.
func movedRows() [][2]string {
	var out [][2]string
	for _, b := range narKeys.Binds {
		if b.Moved != "" {
			out = append(out, [2]string{b.Seq, b.Moved})
		}
	}
	return append(out,
		[2]string{"1 – 4 (places)", "H / L, Space t m/c/w/a (digits are counts)"},
		[2]string{"m (mark read)", "Space m r or ~ (m{a-z} sets a mark now)"},
		[2]string{"* (star)", "Space m s (* steps through one sender's messages)"},
		[2]string{"# (trash)", "dd (# steps back through one sender's messages)"},
		[2]string{"x (pick)", "v (x trashes, asking first, like dd)"},
		[2]string{"Space d (Claude)", "Space k d (or A)"},
	)
}

func (m Model) newsPopup() []string {
	s := m.styles
	rows := movedRows()
	room := max(4, m.bodyHeight()-8)
	top := min(m.newsTop, max(0, len(rows)-room))
	body := []string{s.text.Bold(true).Render("narada " + app.Version + ": keys that moved"), s.muted.Render("the old key says where it went for this release; :news shows this again")}
	for _, r := range rows[top:min(len(rows), top+room)] {
		body = append(body, fit(s.round(s.keycap, r[0]), 20)+" "+s.muted.Render("→ ")+s.text.Render(r[1]))
	}
	body = append(body, s.muted.Render(fmt.Sprintf("%d–%d of %d · j / k scroll · q or Esc closes", top+1, min(len(rows), top+room), len(rows))))
	return m.popupFrame("what moved", body, min(m.w-4, 90))
}

func (m Model) newsKey(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "j", "down", "ctrl+j", "ctrl+d":
		m.newsTop = min(m.newsTop+1, max(0, len(movedRows())-1))
	case "k", "up", "ctrl+k", "ctrl+u":
		m.newsTop = max(0, m.newsTop-1)
	case "q", "esc", "enter", "ctrl+c":
		m.mode = modeNormal
	}
	return m, nil
}

// newsDue says whether this version's "what moved" is still unseen (an
// update, not a first start), and marks it seen.
func newsDue(state string) bool {
	if state == "" {
		return false
	}
	f := filepath.Join(state, "news-seen")
	b, err := os.ReadFile(f)
	seen := strings.TrimSpace(string(b))
	_ = os.MkdirAll(state, 0o700)
	_ = os.WriteFile(f, []byte(app.Version+"\n"), 0o600)
	if err == nil {
		return seen != app.Version
	}
	// no record: show it only to someone who used narada before
	for _, old := range []string{"sections.json", "agent.log", "commands"} {
		if _, err := os.Stat(filepath.Join(state, old)); err == nil {
			return true
		}
	}
	return false
}
