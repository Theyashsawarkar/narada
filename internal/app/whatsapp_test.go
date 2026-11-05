package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Theyashsawarkar/narada/internal/fake"
	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

func setupWhatsApp(t *testing.T) (*App, *fake.WhatsApp) {
	t.Helper()
	linkQuiet = 50 * time.Millisecond
	a, _ := setup(t)
	w := fake.NewWhatsApp()
	w.SeedWhatsApp()
	c, _ := w.Conn(context.Background())
	_ = c.Logout(context.Background()) // seeded linked; link it through narada
	a.DialWhatsApp = func(ctx context.Context) (WhatsAppConn, error) { return w.Conn(ctx) }
	var shown string
	if err := a.LinkWhatsApp(context.Background(), "", func(qr, code string) { shown = qr }, nil); err != nil {
		t.Fatal(err)
	}
	if shown == "" {
		t.Fatal("no QR code shown")
	}
	return a, w
}

func TestWhatsAppHistoryAndNames(t *testing.T) {
	a, _ := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if ok, err := a.StartWhatsApp(ctx); !ok || err != nil {
		t.Fatal(ok, err)
	}
	// WhatsApp stays out of the Google accounts
	if names := a.AccountNames(); len(names) != 1 || names[0] != "work" {
		t.Fatalf("google accounts: %v", names)
	}
	wa, ok := a.Store.WhatsApp()
	if !ok || wa.Email != "+919876500000" || wa.ChatUser != "919876500000@s.whatsapp.net" {
		t.Fatalf("whatsapp account: %+v %v", wa, ok)
	}
	sps, _ := a.Store.Spaces([]string{store.WhatsAppAccount}, nil)
	got := map[string]store.Space{}
	for _, sp := range sps {
		got[sp.DisplayName] = sp
	}
	if len(sps) != 3 || got["Amma"].Unread != 2 || got["Weekend trek"].Unread != 3 || got["Ravi Kulkarni"].Unread != 0 {
		t.Fatalf("chats: %+v", sps)
	}
	if got["Weekend trek"].Type != "GROUP_CHAT" || got["Amma"].Type != "DIRECT_MESSAGE" {
		t.Fatalf("types: %+v", got)
	}
	msgs, _ := a.Store.ChatMessages(store.WhatsAppAccount, got["Ravi Kulkarni"].Name, 0)
	if len(msgs) != 2 || !msgs[1].Mine || msgs[1].Quoted != msgs[0].Name || msgs[0].SenderName != "Ravi Kulkarni" {
		t.Fatalf("ravi's chat: %+v", msgs)
	}
}

func TestWhatsAppReceiveEditRevoke(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	stranger := "15550001111@s.whatsapp.net"
	w.Receive(whatsapp.Message{ID: "N1", Chat: stranger, Sender: stranger, SenderName: "Courier", Text: "At your gate"})
	sp, err := a.Store.Space(store.WhatsAppAccount, stranger)
	if err != nil || sp.DisplayName != "~Courier" || sp.Unread != 1 {
		t.Fatalf("new chat: %+v %v", sp, err)
	}
	if m, _ := a.Store.ChatMessages(store.WhatsAppAccount, stranger, 0); len(m) != 1 || m[0].SenderName != "~Courier" {
		t.Fatalf("their own name, marked: %+v", m)
	}
	w.Receive(whatsapp.Message{ID: "N1", Chat: stranger, Sender: stranger, Text: "At your gate now", Edit: true})
	m, _ := a.Store.ChatMessage(store.WhatsAppAccount, stranger+"/N1")
	if m.Text != "At your gate now" || !m.Edited {
		t.Fatalf("edit: %+v", m)
	}
	w.Receive(whatsapp.Message{ID: "N1", Chat: stranger, Sender: stranger, Revoke: true})
	if _, err := a.Store.ChatMessage(store.WhatsAppAccount, stranger+"/N1"); err == nil {
		t.Fatal("revoked message still there")
	}
	// read on the phone
	w.Receive(whatsapp.Message{ID: "N2", Chat: stranger, Sender: stranger, Text: "Hello?"})
	w.Emit(whatsapp.Event{Read: stranger, ReadAt: time.Now().Add(time.Second)})
	if sp, _ := a.Store.Space(store.WhatsAppAccount, stranger); sp.Unread != 0 {
		t.Fatalf("read elsewhere: %d unread", sp.Unread)
	}
}

func TestWhatsAppSendThroughOutbox(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	sp, err := a.WhatsAppChat(ctx, "ravi")
	if err != nil || sp.DisplayName != "Ravi Kulkarni" {
		t.Fatalf("find by name: %+v %v", sp, err)
	}
	it, err := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: sp.Name, SpaceName: "Ravi Kulkarni", Body: "Looking now",
		Quote: sp.Name + "/H4"}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	a.DeliverDue(ctx)
	if it, _ = a.Store.OutboxItem(it.ID); it.Status != store.StatusSent {
		t.Fatalf("status %s %s", it.Status, it.Error)
	}
	if len(w.Sent) != 1 || w.Sent[0].Text != "Looking now" || w.Sent[0].Quoted != "H4" || w.Sent[0].Chat != sp.Name {
		t.Fatalf("sent: %+v", w.Sent)
	}
	msgs, _ := a.Store.ChatMessages(store.WhatsAppAccount, sp.Name, 0)
	if last := msgs[len(msgs)-1]; !last.Mine || last.Text != "Looking now" {
		t.Fatalf("sent message in the copy: %+v", last)
	}
	// an agent always asks, whatever the policy
	a.Config.SendPolicy = "allow"
	it, err = a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: sp.Name, Body: "hi"}, OriginAgent)
	if err != nil || it.Status != store.StatusApproval {
		t.Fatalf("agent send: %+v %v", it, err)
	}
	// a phone number starts a chat
	sp, err = a.WhatsAppChat(ctx, "+1 555 000 2222")
	if err != nil || sp.Name != "15550002222@s.whatsapp.net" {
		t.Fatalf("by number: %+v %v", sp, err)
	}
}

func TestWhatsAppRateLimit(t *testing.T) {
	a, _ := setupWhatsApp(t)
	a.Config.WhatsAppPerHour = 2
	out := store.Outgoing{Space: "919800000002@s.whatsapp.net", Body: "hi"}
	for i := 0; i < 2; i++ {
		if _, err := a.Queue(store.WhatsAppAccount, "whatsapp", out, OriginYou); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Queue(store.WhatsAppAccount, "whatsapp", out, OriginYou); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("third send in an hour: %v", err)
	}
}

func TestWhatsAppOneConnection(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if ok, _ := a.StartWhatsApp(ctx); !ok {
		t.Fatal("first process didn't connect")
	}
	// a second process (same home) leaves it to the first
	b, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.DialWhatsApp = func(ctx context.Context) (WhatsAppConn, error) { return w.Conn(ctx) }
	if ok, _ := b.StartWhatsApp(ctx); ok {
		t.Fatal("two processes hold WhatsApp")
	}
	// and doesn't claim WhatsApp sends
	it, _ := b.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: "919800000001@s.whatsapp.net", Body: "hi"}, OriginYou)
	b.DeliverDue(ctx)
	if it, _ = b.Store.OutboxItem(it.ID); it.Status != store.StatusQueued {
		t.Fatalf("offline process took the send: %s", it.Status)
	}
	a.DeliverDue(ctx)
	if it, _ = a.Store.OutboxItem(it.ID); it.Status != store.StatusSent {
		t.Fatalf("online process: %s %s", it.Status, it.Error)
	}
	// answering a chat read it, as on the phone
	if len(w.Read) != 1 || w.Read[0] != "919800000001@s.whatsapp.net" {
		t.Fatalf("receipts after answering: %v", w.Read)
	}
	// reading a chat in the process without the connection reaches WhatsApp
	if err := b.MarkSpaceRead(ctx, store.WhatsAppAccount, "120363000000000001@g.us"); err != nil {
		t.Fatal(err)
	}
	if len(w.Read) != 1 {
		t.Fatalf("offline process sent receipts: %v", w.Read)
	}
	a.DeliverDue(ctx)
	if len(w.Read) != 2 || w.Read[1] != "120363000000000001@g.us" {
		t.Fatalf("receipts: %v", w.Read)
	}
}

func TestWhatsAppLink(t *testing.T) {
	if got := WhatsAppLink("+91 98765-43210", "on my way & late"); got != "https://wa.me/919876543210?text=on+my+way+%26+late" {
		t.Fatal(got)
	}
}

// Linking keeps the history the phone sends right away, and a logout
// (the phone removing the device) frees WhatsApp for a later connect.
func TestWhatsAppLinkKeepsHistoryAndLogoutFrees(t *testing.T) {
	a, w := setupWhatsApp(t)
	// setupWhatsApp linked through LinkWhatsApp: history arrived then
	if sps, _ := a.Store.Spaces([]string{store.WhatsAppAccount}, nil); len(sps) != 3 {
		t.Fatalf("chats after linking: %d", len(sps))
	}
	if msgs, _ := a.Store.ChatMessages(store.WhatsAppAccount, "919800000001@s.whatsapp.net", 0); len(msgs) != 3 || !msgs[0].Mine {
		t.Fatalf("history while linking (yours marked mine): %+v", msgs)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if ok, _ := a.StartWhatsApp(ctx); !ok {
		t.Fatal("not online")
	}
	w.Emit(whatsapp.Event{State: "logged-out", Err: errors.New("the phone unlinked this device")})
	for i := 0; i < 50 && a.WhatsAppOnline(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if a.WhatsAppOnline() {
		t.Fatal("still online after a logout")
	}
	if wa, _ := a.Store.WhatsApp(); !strings.Contains(wa.LastError, "unlinked") {
		t.Fatalf("logout not shown: %q", wa.LastError)
	}
	// the lock is free again: a retry gets as far as "not linked"
	c, _ := w.Conn(ctx)
	_ = c.Logout(ctx)
	if ok, _ := a.StartWhatsApp(ctx); ok {
		t.Fatal("online without keys")
	}
	if wa, _ := a.Store.WhatsApp(); !strings.Contains(wa.LastError, "not linked") {
		t.Fatalf("retry after logout: %q (lock still held?)", wa.LastError)
	}
}

// send_policy trust: an agent's send goes without asking (after a
// moment to cancel it), unless it was told to ask first.
func TestTrustPolicy(t *testing.T) {
	a, _ := setup(t)
	a.Config.SendPolicy = "trust"
	a.Config.AgentUndoSeconds = 5
	a.Config.ChatUndoSeconds = 0
	out := store.Outgoing{To: []string{"stranger@example.org"}, Subject: "hi", Body: "hello"}
	it, err := a.Queue("work", "mail", out, OriginAgent)
	if err != nil || it.Status != store.StatusQueued {
		t.Fatalf("trusted agent mail to a stranger: %+v %v", it, err)
	}
	chat, _ := a.Queue("work", "chat", store.Outgoing{Space: "spaces/x", Body: "yo"}, OriginAgent)
	if wait := time.Until(time.UnixMilli(chat.SendAt)); chat.Status != store.StatusQueued || wait < 4*time.Second {
		t.Fatalf("an agent's chat message waits agent_undo_seconds: %s %v", chat.Status, wait)
	}
	out.AskFirst = true
	if it, _ := a.Queue("work", "mail", out, OriginAgent); it.Status != store.StatusApproval || !strings.Contains(it.Reason, "asked") {
		t.Fatalf("ask_first: %+v", it)
	}
	// yours are as before: no extra wait
	mine, _ := a.Queue("work", "chat", store.Outgoing{Space: "spaces/x", Body: "yo"}, OriginYou)
	if time.Until(time.UnixMilli(mine.SendAt)) > time.Second {
		t.Fatal("your chat message waits")
	}
}

func TestWhatsAppSendFiles(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	sp, err := a.WhatsAppChat(ctx, "ravi")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	one, two := filepath.Join(dir, "one.txt"), filepath.Join(dir, "Phase 1 Flow.pdf")
	if err := os.WriteFile(one, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := a.Store.ChatMessages(store.WhatsAppAccount, sp.Name, 0)
	it, err := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: sp.Name, Body: "both",
		Files: []store.File{{Source: one}, {Source: two}}}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	a.DeliverDue(ctx)
	if it, _ = a.Store.OutboxItem(it.ID); it.Status != store.StatusSent {
		t.Fatalf("status %s %s", it.Status, it.Error)
	}
	// each file is its own message: its own preview, the text on the first only
	msgs, _ := a.Store.ChatMessages(store.WhatsAppAccount, sp.Name, 0)
	if len(msgs) != len(before)+2 {
		t.Fatalf("%d messages, want %d", len(msgs), len(before)+2)
	}
	// the other side sees your file's name, not narada's private copy's
	for i, want := range []string{"one.txt", "Phase_1_Flow.pdf"} {
		if got := msgs[len(before)+i].Files[0].Name; got != want && got != "Phase 1 Flow.pdf" {
			t.Errorf("file %d is called %q", i, got)
		}
	}
	for _, m := range w.Sent {
		if m.Media != nil && (strings.HasPrefix(m.Media.Name, "0-") || strings.HasPrefix(m.Media.Name, "1-")) {
			t.Errorf("sent as %q: the private copy's name", m.Media.Name)
		}
	}
	for i, want := range []string{"first", "second"} {
		m := msgs[len(before)+i]
		if len(m.Files) != 1 || !m.Mine {
			t.Fatalf("message %d: %+v", i, m)
		}
		if strings.Contains(m.Text, "both") != (i == 0) {
			t.Fatalf("message %d text: %q", i, m.Text)
		}
		p, ok := a.CachedChatFile(store.WhatsAppAccount, m.Name, 0)
		if !ok {
			t.Fatalf("message %d isn't cached", i)
		}
		if b, _ := os.ReadFile(p); string(b) != want {
			t.Fatalf("message %d shows %q, want %q", i, b, want)
		}
	}
}

func TestWhatsAppTicks(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	sp, _ := a.WhatsAppChat(ctx, "ravi")
	it, err := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: sp.Name, Body: "on my way"}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	a.DeliverDue(ctx)
	it, _ = a.Store.OutboxItem(it.ID)
	ticks := func() int {
		m, err := a.Store.ChatMessage(store.WhatsAppAccount, it.Result)
		if err != nil {
			t.Fatalf("sent message %q: %v", it.Result, err)
		}
		return m.Ticks
	}
	if got := ticks(); got != whatsapp.TickSent {
		t.Fatalf("just sent: %d ticks", got)
	}
	_, id, _ := strings.Cut(strings.TrimPrefix(it.Result, sp.Name), "/")
	w.Receipt(sp.Name, whatsapp.TickRead, id)
	w.Receipt(sp.Name, whatsapp.TickDelivered, id) // late: never goes back down
	if got := ticks(); got != whatsapp.TickRead {
		t.Fatalf("after read: %d ticks", got)
	}
	// history keeps the phone's ticks; Ravi read "Yes, after standup"
	h5, _ := a.Store.ChatMessage(store.WhatsAppAccount, sp.Name+"/H5")
	if h5.Ticks != whatsapp.TickDelivered {
		t.Fatalf("history ticks: %d", h5.Ticks)
	}
}

// The daemon usually holds the connection: marking read from the screen
// (which doesn't) must still reach WhatsApp, through whoever holds it.
func TestWhatsAppMarkReadWithoutTheConnection(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if ok, _ := a.StartWhatsApp(ctx); !ok {
		t.Fatal("not online")
	}
	sps, _ := a.Store.Spaces([]string{store.WhatsAppAccount}, nil)
	var amma store.Space
	for _, sp := range sps {
		if sp.DisplayName == "Amma" {
			amma = sp
		}
	}
	a.StopWhatsApp() // another process holds it now
	if err := a.MarkSpaceRead(ctx, amma.Account, amma.Name); err != nil {
		t.Fatal(err)
	}
	if sp, _ := a.Store.Space(amma.Account, amma.Name); sp.Unread != 0 {
		t.Fatalf("still unread here: %d", sp.Unread)
	}
	a.DeliverDue(ctx)
	if len(w.Read) != 0 {
		t.Fatalf("receipts sent with no connection: %v", w.Read)
	}
	// whoever holds the connection sends them, once
	if ok, _ := a.StartWhatsApp(ctx); !ok {
		t.Fatal("not online")
	}
	a.DeliverDue(ctx)
	a.DeliverDue(ctx)
	if len(w.Read) != 1 || w.Read[0] != amma.Name {
		t.Fatalf("receipts: %v", w.Read)
	}
}

// A failed receipt stays queued and goes out on a later try.
func TestWhatsAppMarkReadRetries(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if ok, _ := a.StartWhatsApp(ctx); !ok {
		t.Fatal("not online")
	}
	sps, _ := a.Store.Spaces([]string{store.WhatsAppAccount}, nil)
	var unread store.Space
	for _, sp := range sps {
		if sp.Unread > 0 {
			unread = sp
		}
	}
	w.ReadErr = errors.New("connection lost")
	if err := a.MarkSpaceRead(ctx, unread.Account, unread.Name); err != nil {
		t.Fatal("marked read here, so no error to show:", err)
	}
	a.DeliverDue(ctx)
	w.ReadErr = nil
	a.DeliverDue(ctx)
	if len(w.Read) != 1 {
		t.Fatalf("receipts: %v", w.Read)
	}
}

func pendingIn(t *testing.T, a *App, space string) []store.ChatMessage {
	t.Helper()
	msgs, err := a.Store.ChatMessages(store.WhatsAppAccount, space, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.ChatMessage
	for _, m := range msgs {
		if store.IsPending(m.Name) {
			out = append(out, m)
		}
	}
	return out
}

// What you send shows in the chat at once, before it has gone out.
func TestWhatsAppOptimisticSend(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const chat = "919800000042@s.whatsapp.net"
	out := store.Outgoing{Space: chat, SpaceName: "Someone", Body: "On my way"}
	it, err := a.Queue(store.WhatsAppAccount, "whatsapp", out, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	got := pendingIn(t, a, chat)
	if len(got) != 1 || got[0].Text != "On my way" || !got[0].Mine || got[0].Ticks != store.TickPending {
		t.Fatalf("not shown at once: %+v", got)
	}
	if sp, err := a.Store.Space(store.WhatsAppAccount, chat); err != nil || sp.LastActive == 0 {
		t.Fatalf("the chat isn't listed: %+v %v", sp, err)
	}

	// offline it stays pending; delivered, the real message replaces it
	a.DeliverDue(ctx)
	if len(pendingIn(t, a, chat)) != 1 {
		t.Fatal("pending message gone before it was sent")
	}
	if ok, _ := a.StartWhatsApp(ctx); !ok {
		t.Fatal("not online")
	}
	a.DeliverDue(ctx)
	if got := pendingIn(t, a, chat); len(got) != 0 {
		t.Fatalf("still pending: %+v", got)
	}
	msgs, _ := a.Store.ChatMessages(store.WhatsAppAccount, chat, 0)
	if len(msgs) != 1 || msgs[0].Text != "On my way" || msgs[0].Ticks != whatsapp.TickSent {
		t.Fatalf("after sending: %+v", msgs)
	}
	if sent, _ := a.Store.OutboxItem(it.ID); sent.Status != store.StatusSent {
		t.Fatalf("status: %s", sent.Status)
	}
	if len(w.Sent) != 1 {
		t.Fatalf("sent: %+v", w.Sent)
	}
}

// A failed send says so in the chat, a retry clears it, a cancel removes it.
func TestWhatsAppOptimisticFailureAndCancel(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if ok, _ := a.StartWhatsApp(ctx); !ok {
		t.Fatal("not online")
	}
	const chat = "919800000042@s.whatsapp.net"
	out := store.Outgoing{Space: chat, SpaceName: "Someone", Body: "Hello"}
	w.SendErr = errors.New("no network")
	it, err := a.Queue(store.WhatsAppAccount, "whatsapp", out, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	a.DeliverDue(ctx)
	if got := pendingIn(t, a, chat); len(got) != 1 || got[0].Ticks != store.TickFailed {
		t.Fatalf("failure not shown: %+v", got)
	}
	w.SendErr = nil
	if err := a.Retry(it.ID); err != nil {
		t.Fatal(err)
	}
	if got := pendingIn(t, a, chat); len(got) != 1 || got[0].Ticks != store.TickPending {
		t.Fatalf("retry not shown: %+v", got)
	}
	a.DeliverDue(ctx)
	if got := pendingIn(t, a, chat); len(got) != 0 {
		t.Fatalf("still pending after the retry: %+v", got)
	}

	// cancelled in the undo window: nothing left behind
	a.Config.ChatUndoSeconds = 60
	it, err = a.Queue(store.WhatsAppAccount, "whatsapp", out, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingIn(t, a, chat)) != 1 {
		t.Fatal("not shown during the undo window")
	}
	if err := a.Cancel(it.ID); err != nil {
		t.Fatal(err)
	}
	if got := pendingIn(t, a, chat); len(got) != 0 {
		t.Fatalf("cancelled but shown: %+v", got)
	}
}

// Nobody looked at an agent's send yet, and a scheduled one isn't sent
// yet: neither shows as if on its way.
func TestWhatsAppNotOptimistic(t *testing.T) {
	a, _ := setupWhatsApp(t)
	const chat = "919800000042@s.whatsapp.net"
	out := store.Outgoing{Space: chat, SpaceName: "Someone", Body: "Hello", Agent: "Claude"}
	if _, err := a.Queue(store.WhatsAppAccount, "whatsapp", out, OriginAgent); err != nil {
		t.Fatal(err)
	}
	later := store.Outgoing{Space: chat, SpaceName: "Someone", Body: "Later", ScheduledFor: time.Now().Add(time.Hour).UnixMilli()}
	if _, err := a.Queue(store.WhatsAppAccount, "whatsapp", later, OriginYou); err != nil {
		t.Fatal(err)
	}
	if got := pendingIn(t, a, chat); len(got) != 0 {
		t.Fatalf("shown: %+v", got)
	}
}

// Reacting: yours shows at once and goes out; another replaces it; picking
// yours takes it back; other people's are counted beside it.
func TestWhatsAppReactions(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	chat, them, other := "15550001111@s.whatsapp.net", "15550001111@s.whatsapp.net", "15550002222@s.whatsapp.net"
	w.Receive(whatsapp.Message{ID: "R1", Chat: chat, Sender: them, SenderName: "Courier", Text: "At your door"})
	name := waName(chat, "R1")
	const acc = store.WhatsAppAccount
	summary := func() string {
		m, err := a.Store.ChatMessage(acc, name)
		if err != nil {
			t.Fatal(err)
		}
		return m.Reactions
	}
	added, err := a.ToggleReaction(ctx, acc, name, "👍")
	if err != nil || !added || summary() != "👍 1" {
		t.Fatalf("react: %v %v %q", added, err, summary())
	}
	if mine, _ := a.MyReactions(ctx, acc, name); len(mine) != 1 || mine[0] != "👍" {
		t.Fatalf("mine: %v", mine)
	}
	if len(w.Reacted) != 1 || w.Reacted[0] != chat+"/R1 "+them+" 👍" {
		t.Fatalf("sent: %v", w.Reacted)
	}
	// someone else's counts beside it
	w.ReceiveReaction(whatsapp.Reaction{Chat: chat, Msg: "R1", Reactor: other, Emoji: "👍"})
	if summary() != "👍 2" {
		t.Fatalf("theirs: %q", summary())
	}
	// another emoji replaces yours: one each, as on the phone
	if added, err = a.ToggleReaction(ctx, acc, name, "🔥"); err != nil || !added || summary() != "👍 1 · 🔥 1" {
		t.Fatalf("replace: %v %v %q", added, err, summary())
	}
	// the message arriving again (an edit, history) doesn't wipe them
	w.Receive(whatsapp.Message{ID: "R1", Chat: chat, Sender: them, SenderName: "Courier", Text: "At your door!", Edit: true})
	if summary() != "👍 1 · 🔥 1" {
		t.Fatalf("after an edit: %q", summary())
	}
	// picking yours takes it back: WhatsApp gets an empty reaction
	if added, err = a.ToggleReaction(ctx, acc, name, "🔥"); err != nil || added || summary() != "👍 1" {
		t.Fatalf("take back: %v %v %q", added, err, summary())
	}
	if last := w.Reacted[len(w.Reacted)-1]; !strings.HasSuffix(last, " ") {
		t.Fatalf("not taken back: %q", last)
	}
	// they take theirs back, from their phone
	w.ReceiveReaction(whatsapp.Reaction{Chat: chat, Msg: "R1", Reactor: other})
	if summary() != "" {
		t.Fatalf("all gone: %q", summary())
	}
	if pend, _ := a.Store.PendingWhatsAppReactions(waReadsKept); len(pend) != 0 {
		t.Fatalf("still pending: %v", pend)
	}
}

// Without the connection (the daemon holds it) a reaction waits, and a failure retries.
func TestWhatsAppReactionWaits(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	chat := "15550001111@s.whatsapp.net"
	w.Receive(whatsapp.Message{ID: "R2", Chat: chat, Sender: chat, SenderName: "Courier", Text: "Hi"})
	w.SendErr = errors.New("offline")
	if _, err := a.ToggleReaction(ctx, store.WhatsAppAccount, waName(chat, "R2"), "❤️"); err != nil {
		t.Fatal(err)
	}
	if pend, _ := a.Store.PendingWhatsAppReactions(waReadsKept); len(pend) != 1 {
		t.Fatalf("not kept for later: %v", pend)
	}
	w.SendErr = nil
	a.flushWhatsAppReactions(ctx)
	if pend, _ := a.Store.PendingWhatsAppReactions(waReadsKept); len(pend) != 0 || len(w.Reacted) != 1 {
		t.Fatalf("not sent: %v %v", pend, w.Reacted)
	}
}

func waitReady(t *testing.T, a *App) {
	t.Helper()
	a.waMu.Lock()
	ready := a.waReady
	a.waMu.Unlock()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("WhatsApp didn't connect")
	}
}

// WhatsApp takes documents up to 2 GB: an app build of ~90 MB must go,
// while mail keeps its 25 MB limit.
func TestWhatsAppTakesFilesOverMailLimit(t *testing.T) {
	a, _ := setupWhatsApp(t)
	big := filepath.Join(t.TempDir(), "app.apk")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxAttach + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: "919800000001@s.whatsapp.net",
		Files: []store.File{{Source: big}}}, OriginYou); err != nil {
		t.Fatalf("WhatsApp refused a file over %d MB: %v", MaxAttach>>20, err)
	}
	if _, err := a.AttachFiles([]string{big}); err == nil || !strings.Contains(err.Error(), "Drive") {
		t.Fatalf("mail should still stop at %d MB, got %v", MaxAttach>>20, err)
	}
	if MaxAttachFor(store.WhatsAppAccount) != 2<<30 {
		t.Fatal("WhatsApp's limit is 2 GB")
	}
}

// A command that doesn't hold WhatsApp saves a received file: it asks the
// holder (the daemon), waits for it, and gets the bytes.
func TestWhatsAppSaveFileThroughTheHolder(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if ok, err := a.StartWhatsApp(ctx); !ok || err != nil {
		t.Fatal(ok, err)
	}
	waitReady(t, a)
	w.Files["doc-1"] = []byte("%PDF-1.4 the itinerary")
	w.Receive(whatsapp.Message{ID: "D1", Chat: "919800000002@s.whatsapp.net", Sender: "919800000002@s.whatsapp.net", Text: "📎 plan.pdf",
		Media: &whatsapp.Media{Kind: "document", Name: "plan.pdf", Mime: "application/pdf", Size: 22, Raw: []byte("doc-1")}})
	other, err := Open() // the command line, beside the daemon
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if ok, _ := other.StartWhatsApp(ctx); ok {
		t.Fatal("two processes hold WhatsApp")
	}
	dir := t.TempDir()
	done := make(chan error, 1)
	var saved []string
	go func() {
		var err error
		saved, err = other.SaveChatFiles(ctx, "919800000002@s.whatsapp.net/D1", 0, dir)
		done <- err
	}()
	deadline := time.After(10 * time.Second)
	for {
		a.DeliverDue(ctx) // the daemon's tick
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(saved[0]); len(saved) != 1 || string(b) != "%PDF-1.4 the itinerary" || filepath.Base(saved[0]) != "plan.pdf" {
				t.Fatalf("saved %v: %q", saved, b)
			}
			return
		case <-deadline:
			t.Fatal("the file never came")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// An audio file sent as a voice note goes as one (push to talk).
func TestWhatsAppVoiceNote(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(t.TempDir(), "note.ogg")
	_ = os.WriteFile(note, []byte("OggS\x00\x02 OpusHead fake"), 0o600)
	it, err := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: "919800000002@s.whatsapp.net",
		Files: []store.File{{Source: note, Voice: true}}}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	a.DeliverDue(ctx)
	if it, _ = a.Store.OutboxItem(it.ID); it.Status != store.StatusSent {
		t.Fatalf("status %s %s", it.Status, it.Error)
	}
	last := w.Sent[len(w.Sent)-1]
	if last.Media == nil || last.Media.Kind != "voice" {
		t.Fatalf("sent as %+v", last.Media)
	}
	m, _ := a.Store.ChatMessage(store.WhatsAppAccount, it.Result)
	if !strings.Contains(m.Text, "Voice message") {
		t.Fatalf("shows as %q", m.Text)
	}
}

// deliver runs the outbox once and returns the item as it ended.
func deliver(t *testing.T, a *App, ctx context.Context, it store.OutboxItem) store.OutboxItem {
	t.Helper()
	a.DeliverDue(ctx)
	got, err := a.Store.OutboxItem(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWhatsAppEditDeleteForward(t *testing.T) {
	a, w := setupWhatsApp(t)
	a.Config.ChatUndoSeconds = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	ravi := "919800000002@s.whatsapp.net"
	it, _ := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: ravi, Body: "see you at 5"}, OriginYou)
	sent := deliver(t, a, ctx, it).Result

	// edit: through the outbox, the new text here once it's gone
	ed, err := a.EditWhatsApp(sent, "see you at 6", OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, ed); got.Status != store.StatusSent {
		t.Fatalf("edit: %s %s", got.Status, got.Error)
	}
	if m, _ := a.Store.ChatMessage(store.WhatsAppAccount, sent); m.Text != "see you at 6" || !m.Edited {
		t.Fatalf("here it says %q (edited %v)", m.Text, m.Edited)
	}
	if len(w.Edited) != 1 || !strings.HasSuffix(w.Edited[0], " see you at 6") {
		t.Fatalf("WhatsApp got %v", w.Edited)
	}
	// abuse: someone else's message, a file, too late, empty text
	if _, err := a.EditWhatsApp(ravi+"/H4", "mine now", OriginYou); err == nil {
		t.Fatal("edited someone else's message")
	}
	old := waName("919800000001@s.whatsapp.net", "H1") // yours, five hours ago
	if _, err := a.EditWhatsApp(old, "late", OriginYou); err == nil || !strings.Contains(err.Error(), "15 minutes") {
		t.Fatalf("edit after 15 minutes: %v", err)
	}
	if _, err := a.EditWhatsApp(sent, "  ", OriginYou); err == nil {
		t.Fatal("edited to nothing")
	}

	// forward a photo to Ravi: by reference, marked forwarded
	fw, err := a.ForwardWhatsApp(waName("919800000001@s.whatsapp.net", "H3"), store.Space{Name: ravi, DisplayName: "Ravi"}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, fw); got.Status != store.StatusSent {
		t.Fatalf("forward: %s %s", got.Status, got.Error)
	}
	last := w.Sent[len(w.Sent)-1]
	if !last.Forwarded || last.Chat != ravi || last.Media == nil || string(last.Media.Raw) != "demo-photo" {
		t.Fatalf("forwarded as %+v", last)
	}

	// delete for everyone: yours only
	if _, err := a.DeleteWhatsApp(ravi+"/H4", true, OriginYou); err == nil {
		t.Fatal("deleted someone else's message for everyone")
	}
	del, err := a.DeleteWhatsApp(sent, true, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, del); got.Status != store.StatusSent {
		t.Fatalf("delete: %s %s", got.Status, got.Error)
	}
	if _, err := a.Store.ChatMessage(store.WhatsAppAccount, sent); err == nil || len(w.Revoked) != 1 {
		t.Fatalf("still here, or WhatsApp wasn't told: %v", w.Revoked)
	}
	// delete for me: someone else's, here only, nothing sent
	if _, err := a.DeleteWhatsApp(ravi+"/H4", false, OriginYou); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.ChatMessage(store.WhatsAppAccount, ravi+"/H4"); err == nil || len(w.Revoked) != 1 {
		t.Fatal("delete for me must remove it here and tell no one")
	}
	// an agent's edit waits for approval, like its sends
	it2, _ := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: ravi, Body: "typo"}, OriginYou)
	sent2 := deliver(t, a, ctx, it2).Result
	if ag, err := a.EditWhatsApp(sent2, "fixed", OriginAgent); err != nil || ag.Status != store.StatusApproval {
		t.Fatalf("an agent's edit: %v %s", err, ag.Status)
	}
}

// Replying privately to a group message: the DM quotes the group's message.
func TestWhatsAppReplyPrivately(t *testing.T) {
	a, w := setupWhatsApp(t)
	a.Config.ChatUndoSeconds = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	it, err := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: "919800000002@s.whatsapp.net", Body: "just between us",
		Quote: waName("120363000000000001@g.us", "H6")}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, it); got.Status != store.StatusSent {
		t.Fatal(got.Error)
	}
	if len(w.Sent) != 1 || w.Sent[0].Quoted != "H6" || w.Sent[0].Chat != "919800000002@s.whatsapp.net" {
		t.Fatalf("sent %+v", w.Sent)
	}
	if len(w.Quotes) != 1 || w.Quotes[0].Chat != "120363000000000001@g.us" {
		t.Fatalf("the quote must name the group (a private reply): %+v", w.Quotes)
	}
}

func TestWhatsAppForwardedShows(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	w.Receive(whatsapp.Message{ID: "F1", Chat: "919800000002@s.whatsapp.net", Sender: "919800000002@s.whatsapp.net", Text: "the venue", Forwarded: true})
	if m, _ := a.Store.ChatMessage(store.WhatsAppAccount, "919800000002@s.whatsapp.net/F1"); !strings.HasPrefix(m.Text, "↪ Forwarded") {
		t.Fatalf("shows %q", m.Text)
	}
}
