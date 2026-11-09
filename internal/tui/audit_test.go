package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// The 0.11.1 audit: each test is one problem it found, so it stays fixed.

// No key but y sends an agent's message: copy, archive and undo keys on an
// AI request do their own thing or say they don't apply.
func TestAIRequestOnlyYSends(t *testing.T) {
	m := model(t)
	m.a.Config.SendPolicy = "confirm"
	it, err := m.a.Queue("demo", "mail", mailOut(), app.OriginAgent)
	if err != nil || it.Status != store.StatusApproval {
		t.Fatal(it, err)
	}
	m.reload()
	m = press(m, "4")
	if !m.inAISection() || len(m.threads) != 1 {
		t.Fatalf("4 goes to the AI requests: %v %d", m.inAISection(), len(m.threads))
	}
	// (y itself sends: it's the documented answer, with the undo window)
	for _, keys := range [][]string{{"Y"}, {" ", "y"}, {"d", "d"}, {"u"}, {"#"}, {"m"}, {"n"}} {
		m = press(m, keys...)
		m.mode, m.leader, m.prefix = modeNormal, false, ""
		if got, _ := m.a.Store.OutboxItem(it.ID); got.Status != store.StatusApproval {
			t.Fatalf("%v changed the request: %s", keys, got.Status)
		}
	}
}

// 4 opens the AI requests even when none wait, and gt reaches them.
func TestAIRequestsAlwaysReachable(t *testing.T) {
	m := model(t)
	m = press(m, "4")
	if !m.inAISection() || !strings.Contains(m.View(), "Waiting for you") {
		t.Fatalf("4 with nothing waiting:\n%s", m.View())
	}
	m = press(m, "1", "g", "T")
	if !m.inAISection() {
		t.Fatal("gT from Mail is the AI requests")
	}
	m = press(m, "g", "t")
	if m.tab != tabMail || m.inAISection() {
		t.Fatal("gt from the AI requests is Mail")
	}
}

// A chat's picked message never outlives the chat: switching to a shorter
// one and pressing e can't crash.
func TestNoStalePickedMessage(t *testing.T) {
	m, _ := waModel(t, "120363000000000001@g.us") // the 5-message group, last picked
	m = press(m, "1", "3")                        // away and back: Amma's, 3 messages
	m = press(m, "l", "e")
	if m.chatSel >= len(m.chatMsgs) {
		t.Fatalf("picked %d of %d", m.chatSel, len(m.chatMsgs))
	}
	m = press(m, "esc", "esc", "esc", "]", "[", "e")
	if m.focus != 0 && m.mode == modeNormal && m.pickingMsg() && m.chatSel >= len(m.chatMsgs) {
		t.Fatal("stale pick")
	}
}

// Esc keeps what you were writing, for that chat.
func TestEscKeepsTheDraft(t *testing.T) {
	m := model(t)
	m = press(m, "2", "i")
	m = typeText(m, "half written")
	m = press(m, "esc")
	if m.mode != modeNormal {
		t.Fatal("esc leaves INSERT")
	}
	m = press(m, "i")
	if string(m.input) != "half written" {
		t.Fatalf("the draft: %q", string(m.input))
	}
	m = press(m, "esc", "h", "j") // another chat: its own (empty) box
	m = press(m, "i")
	if string(m.input) != "" {
		t.Fatalf("another chat's box: %q", string(m.input))
	}
	m = press(m, "esc", "h", "k", "i") // back: the draft is there
	if string(m.input) != "half written" {
		t.Fatalf("back again: %q", string(m.input))
	}
}

// Tab moves through the sections, the digits through the places.
func TestTabIsSections(t *testing.T) {
	m := model(t)
	m = press(m, "tab")
	if m.tab != tabMail || m.sec != 1 {
		t.Fatalf("tab: tab %d sec %d", m.tab, m.sec)
	}
	m = press(m, "shift+tab")
	if m.sec != 0 {
		t.Fatal("shift+tab")
	}
	for range len(m.sections) + 1 { // never into the AI requests' hidden section
		m = press(m, "tab")
		if m.inAISection() {
			t.Fatal("tab reached the AI requests")
		}
	}
}

// Enter on a search runs the search, not an action; actions match whole
// word starts ("arch" isn't "search").
func TestFindPrefersResults(t *testing.T) {
	m := model(t)
	if got := m.matchActions("arch"); len(got) != 1 || got[0].key != "dd" {
		t.Fatalf("arch: %+v", got)
	}
	m = press(m, "/")
	m = typeText(m, "lunch")
	m = runSearch(t, m)
	if k, _ := m.find.at(0); k != "mail" {
		t.Fatalf("results first, got %s", k)
	}
	sec := m.sec
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if th, ok := m.curThread(); !ok || th.ID != "t-rohan" || m.sec != sec && m.search == nil {
		t.Fatalf("enter opened %+v", th)
	}
}

// Below 100 columns writing shows the message box, not the list.
func TestNarrowWriting(t *testing.T) {
	m := model(t)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	m = nm.(Model)
	m = press(m, "2", "i")
	m = typeText(m, "hello narrow")
	if v := m.View(); !strings.Contains(v, "hello narrow") {
		t.Fatalf("what you type isn't shown:\n%s", v)
	}
}

// The key sheet has every group, and fits a tiny screen.
func TestHelpSheet(t *testing.T) {
	m := model(t)
	m = press(m, "?")
	m = typeText(m, "space")
	if !strings.Contains(m.View(), "Space, then") {
		t.Fatal("the Space group is missing")
	}
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	_ = nm.(Model).View() // no panic
}

// Clicks: a place, a section, a list row (twice opens it).
func TestMouse(t *testing.T) {
	m := model(t)
	_ = m.View()
	click := func(x, y int) {
		t.Helper()
		nm, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		m = nm.(Model)
		_ = m.View()
	}
	// the WhatsApp place, wherever it was drawn
	p := m.hits.places[2]
	click(p.x0+1, placesRow)
	if !m.wa {
		t.Fatal("clicking WhatsApp")
	}
	click(m.hits.places[0].x0+1, placesRow)
	s := m.hits.sections[2]
	click(s.x0+1, sectionsRow)
	if m.sec != s.n {
		t.Fatalf("clicking a section: %d, want %d", m.sec, s.n)
	}
	m = press(m, "shift+tab", "shift+tab")
	click(5, topRows+1+6*2+1) // the third thread (six rows each: its first line)
	if m.sel != 2 || m.focus != 0 {
		t.Fatalf("clicking a row: sel %d", m.sel)
	}
	click(5, topRows+1+6*2+1)
	if m.focus != 1 {
		t.Fatal("a second click opens it")
	}
}

// Space N from Mail opens the people picker on a real Chat place.
func TestNewConversationFromMail(t *testing.T) {
	m := model(t)
	m = press(m, " ", "N")
	if m.mode != modeConvo || m.tab != tabChat || len(m.spaces) == 0 {
		t.Fatalf("mode %d tab %d spaces %d", m.mode, m.tab, len(m.spaces))
	}
}

// A two-key command shows on the bar while it waits; Esc drops it.
func TestShowcmd(t *testing.T) {
	m := model(t)
	m = press(m, "g")
	if f := ansi.Strip(m.footerView()); !strings.Contains(f, "g…") || !strings.Contains(f, "gg") {
		t.Fatalf("bar: %s", f)
	}
	m = press(m, "esc")
	if m.prefix != "" || strings.Contains(m.toast, "does nothing") {
		t.Fatalf("esc after g: %q", m.toast)
	}
}
