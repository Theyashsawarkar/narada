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

func TestWhatsAppChatState(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	const amma, ravi, trek = "919800000001@s.whatsapp.net", "919800000002@s.whatsapp.net", "120363000000000001@g.us"
	sp := func(chat string) store.Space {
		s, err := a.Store.Space(store.WhatsAppAccount, chat)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if err := a.ArchiveWhatsApp(ctx, ravi, true); err != nil {
		t.Fatal(err)
	}
	if err := a.PinWhatsApp(ctx, amma, true); err != nil {
		t.Fatal(err)
	}
	if err := a.MuteWhatsApp(ctx, trek, 8*time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if err := a.StarWhatsApp(ctx, waName(amma, "H2"), true); err != nil {
		t.Fatal(err)
	}
	if err := a.BlockWhatsApp(ctx, ravi, true); err != nil {
		t.Fatal(err)
	}
	if !sp(ravi).Archived || !sp(amma).Pinned || !sp(trek).Muted() || !sp(ravi).Blocked {
		t.Fatalf("not shown here: %+v %+v %+v", sp(ravi), sp(amma), sp(trek))
	}
	if m, _ := a.Store.ChatMessage(store.WhatsAppAccount, waName(amma, "H2")); !m.Starred {
		t.Fatal("not starred here")
	}
	want := []string{"archive " + ravi + " 1", "pin " + amma + " 1", "mute " + trek, "star " + amma + " H2 " + amma + " 0 1", "block " + ravi + " 1"}
	if len(w.Did) != len(want) {
		t.Fatalf("WhatsApp got %q", w.Did)
	}
	for i, x := range want {
		if !strings.HasPrefix(w.Did[i], x) {
			t.Errorf("action %d: %q, want %q…", i, w.Did[i], x)
		}
	}
	// a group can't be blocked; a fourth pin is refused, as WhatsApp does
	if err := a.BlockWhatsApp(ctx, trek, true); err == nil {
		t.Fatal("blocked a group")
	}
	_ = a.Store.PutSpace(store.Space{Account: store.WhatsAppAccount, Name: "919800000009@s.whatsapp.net", Type: "DIRECT_MESSAGE"})
	_ = a.PinWhatsApp(ctx, trek, true)
	_ = a.PinWhatsApp(ctx, ravi, true)
	if err := a.PinWhatsApp(ctx, "919800000009@s.whatsapp.net", true); err == nil {
		t.Fatal("a fourth pin")
	}
	// archiving unpins; unarchive and unmute come back
	_ = a.ArchiveWhatsApp(ctx, amma, true)
	if sp(amma).Pinned {
		t.Fatal("archiving should unpin")
	}
	_ = a.ArchiveWhatsApp(ctx, amma, false)
	_ = a.MuteWhatsApp(ctx, trek, 0, true)
	if sp(amma).Archived || sp(trek).Muted() {
		t.Fatal("unarchive / unmute")
	}
	_ = a.MuteWhatsApp(ctx, trek, 0, false)
	if sp(trek).MutedUntil != -1 {
		t.Fatal("muted for ever is -1")
	}
}

// What other devices change arrives: archived, pinned, muted, unread,
// starred; and who's online or typing.
func TestWhatsAppStateFromThePhone(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	const amma, ravi = "919800000001@s.whatsapp.net", "919800000002@s.whatsapp.net"
	yes, until := true, int64(-1)
	w.Emit(whatsapp.Event{ChatState: &whatsapp.ChatState{Chat: ravi, Archived: &yes, MutedUntil: &until}})
	w.Emit(whatsapp.Event{ChatState: &whatsapp.ChatState{Chat: amma, Pinned: &yes, Unread: &yes}})
	w.Emit(whatsapp.Event{Star: &whatsapp.Star{Chat: amma, ID: "H2", On: true}})
	_ = a.Store.SetSpaceRead(store.WhatsAppAccount, amma, time.Now().UnixMilli())
	w.Emit(whatsapp.Event{ChatState: &whatsapp.ChatState{Chat: amma, Unread: &yes}})
	r, _ := a.Store.Space(store.WhatsAppAccount, ravi)
	m, _ := a.Store.Space(store.WhatsAppAccount, amma)
	if !r.Archived || !r.Muted() || !m.Pinned || m.Unread == 0 {
		t.Fatalf("ravi %+v amma %+v", r, m)
	}
	if msg, _ := a.Store.ChatMessage(store.WhatsAppAccount, waName(amma, "H2")); !msg.Starred {
		t.Fatal("star from the phone")
	}
	w.Emit(whatsapp.Event{Presence: &whatsapp.Presence{Who: ravi, Chat: ravi, Online: true, Typing: "composing"}})
	if ty := a.Store.TypingIn(ravi); len(ty) != 1 || ty[0].Typing != "composing" {
		t.Fatalf("typing: %+v", ty)
	}
	w.Receive(whatsapp.Message{Chat: ravi, Sender: ravi, Text: "done"})
	if ty := a.Store.TypingIn(ravi); len(ty) != 0 {
		t.Fatal("a message came: they've stopped typing")
	}
	seen := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	w.Emit(whatsapp.Event{Presence: &whatsapp.Presence{Who: amma, LastSeen: seen}})
	if p := a.Store.PresenceOf(amma); p.Online || p.LastSeen != seen.UnixMilli() {
		t.Fatalf("presence %+v", p)
	}
}

// A command beside the daemon: the action waits for the holder, which does it.
func TestWhatsAppActionThroughTheHolder(t *testing.T) {
	a, w := setupWhatsApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	other, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	done := make(chan error, 1)
	go func() { done <- other.BlockWhatsApp(ctx, "919800000002@s.whatsapp.net", true) }()
	for {
		a.DeliverDue(ctx)
		select {
		case err := <-done:
			if err != nil || len(w.Did) != 1 {
				t.Fatalf("%v %q", err, w.Did)
			}
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func TestWhatsAppPolls(t *testing.T) {
	a, w := setupWhatsApp(t)
	a.Config.ChatUndoSeconds = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	trek := store.Space{Name: "120363000000000001@g.us", DisplayName: "Weekend trek"}
	if _, err := a.PollWhatsApp(trek, "When?", []string{"Sat"}, false, OriginYou); err == nil {
		t.Fatal("a poll with one option")
	}
	if _, err := a.PollWhatsApp(trek, " ", []string{"Sat", "Sun"}, false, OriginYou); err == nil {
		t.Fatal("a poll with no question")
	}
	it, err := a.PollWhatsApp(trek, "When?", []string{"Sat", "Sun", "Sat"}, false, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	got := deliver(t, a, ctx, it)
	if got.Status != store.StatusSent || len(w.Sent) != 1 || w.Sent[0].Poll == nil || len(w.Sent[0].Poll.Options) != 2 {
		t.Fatalf("poll: %s %s %+v", got.Status, got.Error, w.Sent)
	}
	poll := got.Result
	id := strings.TrimPrefix(poll, trek.Name+"/")
	w.ReceiveVote(trek.Name, id, "919800000002@s.whatsapp.net", "Sat")
	w.ReceiveVote(trek.Name, id, "919800000003@s.whatsapp.net", "Sun")
	w.ReceiveVote(trek.Name, id, "919800000003@s.whatsapp.net", "Sat") // changed their mind
	m, _ := a.Store.ChatMessage(store.WhatsAppAccount, poll)
	if !strings.Contains(m.Text, "○ 1. Sat · 2") || strings.Contains(m.Text, "Sun ·") {
		t.Fatalf("tally:\n%s", m.Text)
	}
	if _, err := a.VoteWhatsApp(poll, []string{"1", "2"}, OriginYou); err == nil {
		t.Fatal("two votes in a single-choice poll")
	}
	if _, err := a.VoteWhatsApp(poll, []string{"Mon"}, OriginYou); err == nil {
		t.Fatal("a vote for no option")
	}
	v, err := a.VoteWhatsApp(poll, []string{"2"}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, v); got.Status != store.StatusSent || len(w.Voted) != 1 || !strings.HasSuffix(w.Voted[0], " Sun") {
		t.Fatalf("vote: %s %s %v", got.Status, got.Error, w.Voted)
	}
	m, _ = a.Store.ChatMessage(store.WhatsAppAccount, poll)
	if !strings.Contains(m.Text, "● 2. Sun · 1") {
		t.Fatalf("your vote:\n%s", m.Text)
	}
	w.Receive(whatsapp.Message{ID: "P1", Chat: trek.Name, Sender: "919800000002@s.whatsapp.net", Text: "📊 Poll: Bus or train?",
		Poll: &whatsapp.Poll{Question: "Bus or train?", Options: []string{"Bus", "Train"}, Multi: true}})
	m, _ = a.Store.ChatMessage(store.WhatsAppAccount, trek.Name+"/P1")
	if !strings.Contains(m.Text, "(pick several)") || !strings.Contains(m.Text, "○ 2. Train") {
		t.Fatalf("received poll:\n%s", m.Text)
	}
	if _, err := a.VoteWhatsApp(trek.Name+"/H6", []string{"1"}, OriginYou); err == nil {
		t.Fatal("voted in a message that isn't a poll")
	}
}

func TestWhatsAppShareAndDisappear(t *testing.T) {
	a, w := setupWhatsApp(t)
	a.Config.ChatUndoSeconds = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	ravi, _ := a.Store.Space(store.WhatsAppAccount, "919800000002@s.whatsapp.net")
	if _, err := ParseLocation("118.5,73"); err == nil {
		t.Fatal("latitude 118")
	}
	l, err := ParseLocation("18.52, 73.85")
	if err != nil {
		t.Fatal(err)
	}
	l.Name = "Shaniwar Wada"
	it, err := a.ShareLocationWhatsApp(ravi, l, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, it); got.Status != store.StatusSent || !strings.Contains(w.Sent[0].Text, "18.52") {
		t.Fatalf("location: %s %s %+v", got.Status, got.Error, w.Sent)
	}
	ct, err := a.ShareContactWhatsApp(ctx, ravi, "Amma", "", OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, ct); got.Status != store.StatusSent || !strings.Contains(w.Sent[1].Text, "FN:Amma") || !strings.Contains(w.Sent[1].Text, "waid=919800000001") {
		t.Fatalf("contact: %s %s %q", got.Status, got.Error, w.Sent[1].Text)
	}
	if _, err := a.ShareContactWhatsApp(ctx, ravi, "Weekend trek", "", OriginYou); err == nil {
		t.Fatal("shared a group as a contact")
	}
	// disappearing: WhatsApp's timers only; what you send then carries it
	if err := a.DisappearWhatsApp(ctx, ravi.Name, 3600); err == nil {
		t.Fatal("a one-hour timer")
	}
	if err := a.DisappearWhatsApp(ctx, ravi.Name, 7*86400); err != nil {
		t.Fatal(err)
	}
	if sp, _ := a.Store.Space(store.WhatsAppAccount, ravi.Name); sp.Disappear != 7*86400 {
		t.Fatalf("timer here: %d", sp.Disappear)
	}
	it, _ = a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: ravi.Name, Body: "gone in a week"}, OriginYou)
	deliver(t, a, ctx, it)
	if w.Disappear[ravi.Name] != 7*86400 {
		t.Fatalf("the send didn't carry the timer: %v", w.Disappear)
	}
	// the other side turns it off: the chat says so, and the timer goes
	w.Receive(whatsapp.Message{ID: "E1", Chat: ravi.Name, Sender: ravi.Name, Text: whatsapp.DisappearText(0), Disappear: -1})
	if sp, _ := a.Store.Space(store.WhatsAppAccount, ravi.Name); sp.Disappear != 0 {
		t.Fatalf("timer after off: %d", sp.Disappear)
	}
}

func TestWhatsAppStatus(t *testing.T) {
	a, w := setupWhatsApp(t)
	a.Config.ChatUndoSeconds = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	const ravi = "919800000002@s.whatsapp.net"
	w.Receive(whatsapp.Message{ID: "S1", Chat: whatsapp.StatusChat, Sender: ravi, SenderName: "Ravi", Text: "At the top!"})
	sp, err := a.Store.Space(store.WhatsAppAccount, whatsapp.StatusChat)
	if err != nil || sp.DisplayName != "Status" || sp.Type != store.TypeStatus || sp.Unread != 0 {
		t.Fatalf("status chat: %+v %v", sp, err)
	}
	// only status post posts: not a plain send, not an agent
	for _, origin := range []string{OriginYou, OriginAgent} {
		if _, err := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: whatsapp.StatusChat, Body: "hi all"}, origin); err == nil {
			t.Fatalf("a %s send reached the status chat", origin)
		}
	}
	if _, err := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: whatsapp.StatusChat, Action: ActStatus, Body: "hi all"}, OriginAgent); err == nil {
		t.Fatal("an agent posted a status")
	}
	it, err := a.PostWhatsAppStatus("On the trail", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, it); got.Status != store.StatusSent || w.Sent[0].Chat != whatsapp.StatusChat || w.Sent[0].Text != "On the trail" {
		t.Fatalf("post: %s %s %+v", got.Status, got.Error, w.Sent)
	}
	if _, err := a.PostWhatsAppStatus("  ", nil); err == nil {
		t.Fatal("an empty status")
	}
	// looking tells nobody
	if err := a.MarkSpaceRead(ctx, store.WhatsAppAccount, whatsapp.StatusChat); err != nil {
		t.Fatal(err)
	}
	a.DeliverDue(ctx)
	if len(w.Read) != 0 {
		t.Fatalf("a read receipt for a status: %v", w.Read)
	}
	// a day on, they're gone
	_ = a.Store.DeleteChatOlderThan(store.WhatsAppAccount, whatsapp.StatusChat, time.Now().Add(time.Hour).UnixMilli())
	if msgs, _ := a.Store.ChatMessages(store.WhatsAppAccount, whatsapp.StatusChat, 0); len(msgs) != 0 {
		t.Fatalf("old statuses: %+v", msgs)
	}
}

func TestWhatsAppPinMessage(t *testing.T) {
	a, w := setupWhatsApp(t)
	a.Config.ChatUndoSeconds = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	const trek = "120363000000000001@g.us"
	msg := waName(trek, "H9")
	if _, err := a.PinWhatsAppMessage(msg, 3600, OriginYou); err == nil {
		t.Fatal("pinned for an hour")
	}
	it, err := a.PinWhatsAppMessage(msg, PinWeek, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, it); got.Status != store.StatusSent {
		t.Fatal(got.Error)
	}
	if m, _ := a.Store.ChatMessage(store.WhatsAppAccount, msg); !m.Pinned || !strings.Contains(strings.Join(w.Did, " "), "pin-message "+msg+" true 604800") {
		t.Fatalf("pinned %v, did %q", m.Pinned, w.Did)
	}
	// someone unpins it
	w.Emit(whatsapp.Event{Pin: &whatsapp.MsgPin{Chat: trek, Msg: "H9"}})
	if m, _ := a.Store.ChatMessage(store.WhatsAppAccount, msg); m.Pinned {
		t.Fatal("still pinned")
	}
	// an agent's waits for approval, and what an action does can't be edited there
	ag, err := a.PinWhatsAppMessage(msg, PinDay, OriginAgent)
	if err != nil || ag.Status != store.StatusApproval {
		t.Fatalf("%v %s", err, ag.Status)
	}
	changed := ag.Out
	changed.Body = "something else"
	if err := a.Approve(ag.ID, &changed); err == nil || !strings.Contains(err.Error(), "can't be edited") {
		t.Fatalf("edited a pin's approval: %v", err)
	}
}

// A .webp goes as a sticker.
func TestWhatsAppSticker(t *testing.T) {
	a, w := setupWhatsApp(t)
	a.Config.ChatUndoSeconds = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "wave.webp")
	_ = os.WriteFile(p, []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), 0o600)
	it, err := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: "919800000002@s.whatsapp.net", Files: []store.File{{Source: p}}}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, it); got.Status != store.StatusSent || w.Sent[0].Media.Kind != "sticker" {
		t.Fatalf("%s %s %+v", got.Status, got.Error, w.Sent[0].Media)
	}
}

// With nobody holding WhatsApp, saving a file says so instead of hanging.
func TestWhatsAppSaveFileWithNobodyOnline(t *testing.T) {
	a, _ := setupWhatsApp(t)
	done := make(chan error, 1)
	go func() {
		_, err := a.SaveChatFiles(context.Background(), "919800000001@s.whatsapp.net/H3", 0, t.TempDir())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "no narada is online") {
			t.Fatalf("got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("it hung with nobody to download the file")
	}
}

// A connection ending doesn't take a later one of the same process with it.
func TestWhatsAppOldContextLeavesNewConnection(t *testing.T) {
	a, _ := setupWhatsApp(t)
	first, cancelFirst := context.WithCancel(context.Background())
	if ok, err := a.StartWhatsApp(first); !ok || err != nil {
		t.Fatal(ok, err)
	}
	a.StopWhatsApp()
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	if ok, err := a.StartWhatsApp(second); !ok || err != nil {
		t.Fatal(ok, err)
	}
	cancelFirst()
	time.Sleep(100 * time.Millisecond)
	if !a.WhatsAppOnline() {
		t.Fatal("the first context's end stopped the second connection")
	}
}

// Answering a status goes to whoever posted it, in your chat with them,
// quoting the status, and never to the status chat; an agent's answer
// waits for approval like any send. A reaction is the emoji alone.
func TestWhatsAppStatusReplyAndReact(t *testing.T) {
	a, w := setupWhatsApp(t)
	a.Config.ChatUndoSeconds = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	const ravi, meena = "919800000002@s.whatsapp.net", "919800000003@s.whatsapp.net"
	w.Receive(whatsapp.Message{ID: "S1", Chat: whatsapp.StatusChat, Sender: ravi, SenderName: "Ravi", Text: "At the top!", Time: time.Now().Add(-time.Hour)})
	w.Receive(whatsapp.Message{ID: "S2", Chat: whatsapp.StatusChat, Sender: meena, SenderName: "Meena", Text: "📷 Photo", Time: time.Now().Add(-time.Minute)})
	w.Receive(whatsapp.Message{ID: "S3", Chat: whatsapp.StatusChat, Sender: ravi, SenderName: "Ravi", Text: "Down again", Time: time.Now().Add(-30 * time.Second)})
	w.Receive(whatsapp.Message{ID: "S0", Chat: whatsapp.StatusChat, Sender: meena, SenderName: "Meena", Text: "old", Time: time.Now().Add(-25 * time.Hour)})
	// they outlive a reconnect (which once dropped every status)
	a.StopWhatsApp()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	groups, err := a.Statuses()
	if err != nil || len(groups) != 2 || groups[0].Sender != ravi || len(groups[0].Items) != 2 || groups[1].Sender != meena || !strings.Contains(groups[1].Name, "Meena") || len(groups[1].Items) != 1 {
		t.Fatalf("statuses: %+v %v", groups, err)
	}
	status := waName(whatsapp.StatusChat, "S1")
	it, err := a.ReplyToStatus(status, store.Outgoing{Body: "Wow, where is this?"}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, it); got.Status != store.StatusSent {
		t.Fatalf("reply: %s %s", got.Status, got.Error)
	}
	if s := w.Sent[len(w.Sent)-1]; s.Chat != ravi || s.Text != "Wow, where is this?" {
		t.Fatalf("sent: %+v", s)
	}
	if q := w.Quotes[len(w.Quotes)-1]; q.ID != "S1" || q.Chat != whatsapp.StatusChat || q.Sender != ravi {
		t.Fatalf("quote: %+v", q)
	}
	it, err = a.ReactToStatus(waName(whatsapp.StatusChat, "S2"), "😍", store.Outgoing{}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliver(t, a, ctx, it); got.Status != store.StatusSent || w.Sent[len(w.Sent)-1].Chat != meena || w.Sent[len(w.Sent)-1].Text != "😍" {
		t.Fatalf("react: %s %+v", got.Status, w.Sent[len(w.Sent)-1])
	}
	for _, s := range w.Sent {
		if s.Chat == whatsapp.StatusChat {
			t.Fatalf("something went to the status chat: %+v", s)
		}
	}
	// an agent's waits for you
	it, err = a.ReplyToStatus(status, store.Outgoing{Body: "Nice", Agent: "Claude Code", Why: "say nice to Ravi's status"}, OriginAgent)
	if err != nil || it.Status != store.StatusApproval {
		t.Fatalf("agent: %+v %v", it, err)
	}
	for _, bad := range []struct{ status, emoji string }{{waName(ravi, "H1"), "👍"}, {status, ""}, {status, "two words"}} {
		if _, err := a.ReactToStatus(bad.status, bad.emoji, store.Outgoing{}, OriginYou); err == nil {
			t.Fatalf("reacted: %+v", bad)
		}
	}
}

// You're shown by name: your WhatsApp profile name once WhatsApp says it,
// your number before that; on Google your name, else your address's name.
func TestMyName(t *testing.T) {
	a, w := setupWhatsApp(t)
	acc, _ := a.Store.WhatsApp()
	if got := a.MyName(store.WhatsAppAccount); got != acc.Email {
		t.Fatalf("before WhatsApp names you: %q (want the number %q)", got, acc.Email)
	}
	w.MyName = "Sam Lee"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.StartWhatsApp(ctx); err != nil {
		t.Fatal(err)
	}
	waitReady(t, a)
	deadline := time.Now().Add(2 * time.Second)
	for a.MyName(store.WhatsAppAccount) != "Sam Lee" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := a.MyName(store.WhatsAppAccount); got != "Sam Lee" {
		t.Fatalf("WhatsApp: %q", got)
	}
	if err := a.Store.AddAccount(store.Account{Name: "work", Email: "sam.lee@acme.example", Chat: true}); err != nil {
		t.Fatal(err)
	}
	if got := a.MyName("work"); got != "sam.lee" {
		t.Fatalf("Google without a name: %q", got)
	}
	_ = a.Store.SetChatSynced("work", "users/7")
	_ = a.Store.PutUser("work", "users/7", "Sam Lee", "sam.lee@acme.example")
	if got := a.MyName("work"); got != "Sam Lee" {
		t.Fatalf("Google: %q", got)
	}
}
