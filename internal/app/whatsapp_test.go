package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Theyashsawarkar/sandesh/internal/fake"
	"github.com/Theyashsawarkar/sandesh/internal/store"
	"github.com/Theyashsawarkar/sandesh/internal/whatsapp"
)

func setupWhatsApp(t *testing.T) (*App, *fake.WhatsApp) {
	t.Helper()
	linkQuiet = 50 * time.Millisecond
	a, _ := setup(t)
	w := fake.NewWhatsApp()
	w.SeedWhatsApp()
	c, _ := w.Conn(context.Background())
	_ = c.Logout(context.Background()) // seeded linked; link it through sandesh
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
	if err != nil || sp.DisplayName != "Courier" || sp.Unread != 1 {
		t.Fatalf("new chat: %+v %v", sp, err)
	}
	if name, _, _ := a.Store.User(store.WhatsAppAccount, stranger); name != "Courier" {
		t.Fatalf("push name kept: %q", name)
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
	// files aren't supported yet
	if _, err := a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: sp.Name, Body: "x", Files: []store.File{{Source: "/etc/hostname"}}}, OriginYou); err == nil {
		t.Fatal("a file went into a WhatsApp send")
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
	// reading a chat sends receipts
	if err := a.MarkSpaceRead(ctx, store.WhatsAppAccount, "120363000000000001@g.us"); err != nil {
		t.Fatal(err)
	}
	if len(w.Read) != 1 {
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
