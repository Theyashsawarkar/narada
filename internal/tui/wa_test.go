package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/fake"
	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

// waModel is the screen on a linked fake WhatsApp, in the chat named
// title, picking its last message.
func waModel(t *testing.T, chat string) (Model, *fake.WhatsApp) {
	t.Helper()
	m := model(t)
	w := fake.NewWhatsApp()
	w.SeedWhatsApp()
	m.a.DialWhatsApp = func(ctx context.Context) (app.WhatsAppConn, error) { return w.Conn(ctx) }
	if err := m.a.Store.AddWhatsApp("+919876500000", w.Me); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if ok, err := m.a.StartWhatsApp(ctx); !ok || err != nil {
		t.Fatal(ok, err)
	}
	m.a.Config.ChatUndoSeconds = 0
	m.a.Config.Images = "off" // no pictures being prepared as the test ends
	sp, err := m.a.Store.Space(store.WhatsAppAccount, chat)
	if err != nil {
		t.Fatal(err)
	}
	m.showSpace(sp)
	m.mode, m.focus = modeNormal, 1
	m.loadPreview(true)
	m.chatSel = len(m.chatMsgs) - 1
	return m, w
}

func pickMsg(t *testing.T, m Model, id string) Model {
	t.Helper()
	for i, c := range m.chatMsgs {
		if strings.HasSuffix(c.Name, "/"+id) {
			m.chatSel = i
			return m
		}
	}
	t.Fatalf("no message %s in %d", id, len(m.chatMsgs))
	return m
}

func TestForwardFromTheScreen(t *testing.T) {
	m, w := waModel(t, "919800000001@s.whatsapp.net")
	m = pickMsg(t, m, "H3")
	m = press(m, "f")
	if m.mode != modeForward {
		t.Fatalf("f didn't ask where to: mode %d", m.mode)
	}
	m = typeText(m, "Ravi")
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if cmd == nil {
		t.Fatal("nothing queued")
	}
	if done, ok := cmd().(doneMsg); !ok || done.err != nil {
		t.Fatalf("forward: %+v", done)
	}
	m.a.DeliverDue(context.Background())
	if len(w.Sent) != 1 || !w.Sent[0].Forwarded || w.Sent[0].Chat != "919800000002@s.whatsapp.net" {
		t.Fatalf("sent %+v", w.Sent)
	}
}

func TestReplyPrivatelyFromTheScreen(t *testing.T) {
	m, w := waModel(t, "120363000000000001@g.us")
	m = pickMsg(t, m, "H6") // Ravi's, in the group
	m = press(m, "R")
	sp, ok := m.curSpace()
	if !ok || sp.Name != "919800000002@s.whatsapp.net" || m.mode != modeChatMsg || m.replyTo == nil {
		t.Fatalf("R should open the chat with Ravi, replying: %+v %v", sp, m.replyTo)
	}
	m = typeText(m, "just us")
	m = press(m, "enter")
	m.a.DeliverDue(context.Background())
	if len(w.Sent) != 1 || w.Sent[0].Quoted != "H6" || w.Quotes[0].Chat != "120363000000000001@g.us" {
		t.Fatalf("sent %+v quotes %+v", w.Sent, w.Quotes)
	}
}

func TestDeleteForMeFromTheScreen(t *testing.T) {
	m, w := waModel(t, "919800000002@s.whatsapp.net")
	m = pickMsg(t, m, "H4") // Ravi's
	m = press(m, "d")
	if !strings.Contains(m.toast, "for you") {
		t.Fatalf("toast %q", m.toast)
	}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = nm.(Model)
	if cmd != nil {
		cmd()
	}
	if _, err := m.a.Store.ChatMessage(store.WhatsAppAccount, "919800000002@s.whatsapp.net/H4"); err == nil || len(w.Revoked) != 0 {
		t.Fatal("delete for me must remove it here and tell no one")
	}
}

func TestEditFromTheScreen(t *testing.T) {
	m, w := waModel(t, "919800000002@s.whatsapp.net")
	it, err := m.a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: "919800000002@s.whatsapp.net", Body: "at 5"}, app.OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	m.a.DeliverDue(context.Background())
	it, _ = m.a.Store.OutboxItem(it.ID)
	m.loadPreview(true)
	m = pickMsg(t, m, strings.TrimPrefix(it.Result, "919800000002@s.whatsapp.net/"))
	m = press(m, "E")
	if m.editingMsg == "" {
		t.Fatalf("E didn't start editing: %q", m.toast)
	}
	m = press(m, "ctrl+u")
	m = typeText(m, "at 6")
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if cmd != nil {
		cmd()
	}
	m.a.DeliverDue(context.Background())
	if len(w.Edited) != 1 || !strings.HasSuffix(w.Edited[0], " at 6") {
		t.Fatalf("edited %v", w.Edited)
	}
}

func TestChatStateKeys(t *testing.T) {
	m, w := waModel(t, "919800000002@s.whatsapp.net")
	m.focus = 0
	sp := func() store.Space {
		s, _ := m.a.Store.Space(store.WhatsAppAccount, "919800000002@s.whatsapp.net")
		return s
	}
	run := func(keys ...string) {
		for _, k := range keys {
			nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
			m = nm.(Model)
			if cmd != nil {
				if msg := cmd(); msg != nil {
					nm, _ = m.Update(msg)
					m = nm.(Model)
				}
			}
		}
	}
	run("Z")
	if !sp().Muted() || sp().MutedUntil < 0 {
		t.Fatalf("Z: %+v", sp())
	}
	run("Z", "Z")
	if sp().MutedUntil != -1 {
		t.Fatalf("Z Z Z is always: %d", sp().MutedUntil)
	}
	run("Z")
	if sp().Muted() {
		t.Fatal("Z after always unmutes")
	}
	run("B")
	if sp().Blocked {
		t.Fatal("one B blocked at once")
	}
	run("B")
	if !sp().Blocked {
		t.Fatalf("B B didn't block: %q", m.toast)
	}
	run("p")
	if !sp().Pinned {
		t.Fatal("p pins")
	}
	run("e")
	if !sp().Archived || sp().Pinned {
		t.Fatal("e archives (and unpins)")
	}
	for _, s := range m.spaces {
		if s.Name == "919800000002@s.whatsapp.net" {
			t.Fatal("an archived chat still shows in All")
		}
	}
	m.sec = len(chatSections) - 1 // Archived
	m.reload()
	if len(m.spaces) != 1 || m.spaces[0].Name != "919800000002@s.whatsapp.net" {
		t.Fatalf("Archived shows %+v", m.spaces)
	}
	m.a.DeliverDue(context.Background())
	did := strings.Join(w.Did, "\n")
	for _, x := range []string{"mute ", "block 919800000002@s.whatsapp.net 1", "pin 919800000002@s.whatsapp.net 1", "archive 919800000002@s.whatsapp.net 1"} {
		if !strings.Contains(did, x) {
			t.Errorf("WhatsApp wasn't told %q:\n%s", x, did)
		}
	}
	// * stars the picked message
	m.focus = 1
	m.loadPreview(true)
	m = pickMsg(t, m, "H4")
	run("*")
	if msg, _ := m.a.Store.ChatMessage(store.WhatsAppAccount, "919800000002@s.whatsapp.net/H4"); !msg.Starred {
		t.Fatal("* didn't star it")
	}
}

func TestTypingAndPresence(t *testing.T) {
	m, w := waModel(t, "919800000002@s.whatsapp.net")
	sp, _ := m.curSpace()
	w.Emit(whatsapp.Event{Presence: &whatsapp.Presence{Who: sp.Name, Chat: sp.Name, Online: true, Typing: "composing"}})
	if got := ansi.Strip(m.presenceLine(sp)); !strings.Contains(got, "typing…") {
		t.Fatalf("presence %q", got)
	}
	_ = m.a.Store.SetTyping(sp.Name, sp.Name, "")
	if got := ansi.Strip(m.presenceLine(sp)); !strings.Contains(got, "online") {
		t.Fatalf("presence %q", got)
	}
	// typing tells them, once
	m.mode = modeChatMsg
	m = typeText(m, "hel")
	m.a.DeliverDue(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(strings.Join(w.Did, "\n"), "typing "+sp.Name+" composing") {
		if time.Now().After(deadline) {
			t.Fatalf("WhatsApp wasn't told you're typing: %q", w.Did)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := strings.Count(strings.Join(w.Did, "\n"), "composing"); n != 1 {
		t.Fatalf("told %d times: %q", n, w.Did)
	}
}

func TestRenameWhatsAppGroup(t *testing.T) {
	m, w := waModel(t, "120363000000000001@g.us")
	m.focus = 0
	m = press(m, "n")
	if m.mode != modeRename || !strings.Contains(m.renamePrompt(), "WhatsApp") {
		t.Fatalf("n: mode %d, prompt %q", m.mode, m.renamePrompt())
	}
	m = press(m, "ctrl+u")
	m = typeText(m, "Rajmachi crew")
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if done, ok := cmd().(doneMsg); !ok || done.err != nil || done.msg != "renamed on WhatsApp" {
		t.Fatalf("%+v", done)
	}
	if w.Groups["120363000000000001@g.us"] != "Rajmachi crew" {
		t.Fatalf("WhatsApp has %q", w.Groups["120363000000000001@g.us"])
	}
}

func TestGroupInfoKey(t *testing.T) {
	m, _ := waModel(t, "120363000000000001@g.us")
	m.focus = 0
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	m = nm.(Model)
	msg, ok := cmd().(aiMsg)
	if !ok || msg.err != nil || !strings.Contains(msg.text, "4 people") || !strings.Contains(msg.text, "Ravi Kulkarni") {
		t.Fatalf("%+v", msg)
	}
}
