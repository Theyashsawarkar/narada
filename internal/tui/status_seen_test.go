package tui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

// drain presses a key and feeds back what its commands answer, so the
// work they do in the background (marking read…) happens.
func drain(m Model, k string) Model {
	nm, cmd := m.Update(keyMsgOf(k))
	m = nm.(Model)
	var run func(c tea.Cmd, depth int)
	run = func(c tea.Cmd, depth int) {
		if c == nil || depth > 2 {
			return
		}
		ch := make(chan tea.Msg, 1)
		go func() { ch <- c() }()
		select {
		case msg := <-ch:
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, cc := range b {
					run(cc, depth+1)
				}
				return
			}
			if msg != nil {
				nm, c2 := m.Update(msg)
				m = nm.(Model)
				run(c2, depth+1)
			}
		case <-time.After(time.Second): // a tick: not work
		}
	}
	run(cmd, 0)
	return m
}

// Viewing statuses never tells their sender: listing, opening, picking,
// playing (v) and Space m r send no read receipt, and say nothing red.
func TestStatusesStayUnseen(t *testing.T) {
	m, w := waModel(t, "919800000002@s.whatsapp.net")
	w.Receive(whatsapp.Message{ID: "S1", Chat: whatsapp.StatusChat, Sender: "919800000002@s.whatsapp.net", SenderName: "Ravi", Text: "At the top!"})
	m.focus, m.picking = 0, false
	mm, _ := m.goSectionNamed("Status")
	m = mm.(Model)
	m.loadPreview(true)
	for _, k := range []string{"j", "k", "enter", "l", "k", "j", "v", "esc", "h", " ", "m", "r", "~", "enter"} {
		m = drain(m, k)
		if m.err != "" {
			t.Fatalf("%q: %q", k, m.err)
		}
	}
	// the daemon's queue too: nothing for a status, ever
	_ = m.a.MarkSpaceRead(context.Background(), store.WhatsAppAccount, whatsapp.StatusChat)
	_ = m.a.MarkSpaceRead(context.Background(), store.WhatsAppAccount, whatsapp.StatusChat+"|919800000002@s.whatsapp.net")
	if reads, _ := m.a.Store.WhatsAppReads(time.Hour); slices.ContainsFunc(reads, func(r store.WhatsAppRead) bool { return strings.HasPrefix(r.Space, whatsapp.StatusChat) }) {
		t.Fatalf("a status read is queued: %+v", reads)
	}
	if slices.ContainsFunc(w.Read, func(c string) bool { return strings.HasPrefix(c, whatsapp.StatusChat) }) {
		t.Fatalf("a read receipt went out for a status: %v", w.Read)
	}
	// the status pane says so, with today's keys
	m.mode, m.picking, m.focus = modeNormal, false, 1
	m.loadPreview(true)
	v := m.View()
	if !strings.Contains(v, "viewed it") || !strings.Contains(v, "Space i e reacts") {
		t.Fatalf("the status pane's hint:\n%s", v)
	}
}

// The leader shows as the word Space in every hint, never ␣ (which many
// fonts draw like an underscore).
func TestLeaderSpelledOut(t *testing.T) {
	m, _ := waModel(t, "919800000001@s.whatsapp.net")
	v := m.View()
	if strings.Contains(v, "␣") || !strings.Contains(v, "Space") {
		t.Fatalf("footer:\n%s", v)
	}
	m = press(m, " ")
	m.leaderShown = true
	if v := m.View(); strings.Contains(v, "␣") {
		t.Fatalf("which-key:\n%s", v)
	}
}

// Looking at statuses sends no presence either: no "online", no
// "typing", no watching the poster, which would tell them you're there.
func TestStatusesSendNoPresence(t *testing.T) {
	m, w := waModel(t, "919800000002@s.whatsapp.net")
	w.Receive(whatsapp.Message{ID: "S1", Chat: whatsapp.StatusChat, Sender: "919800000002@s.whatsapp.net", SenderName: "Ravi", Text: "At the top!"})
	m.focus, m.picking = 0, false
	mm, _ := m.goSectionNamed("Status")
	m = mm.(Model)
	m.loadPreview(true)
	m = drain(m, "enter")
	if !m.isStatus() {
		t.Fatal("no status open")
	}
	before := len(w.Did)
	m.watched = nil
	m.waHere()
	m.waTyping(false)
	m.waTyping(true)
	m.a.DeliverDue(context.Background())
	time.Sleep(300 * time.Millisecond)
	if did := w.Did[before:]; len(did) > 0 {
		t.Fatalf("presence went out while looking at statuses: %q", did)
	}
}
