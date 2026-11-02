package fake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Theyashsawarkar/sandesh/internal/whatsapp"
)

// WhatsApp is an in-memory WhatsApp account for the tests and `sandesh
// demo`: it links at once, answers sends, and lets a test play messages
// arriving.
type WhatsApp struct {
	mu      sync.Mutex
	Me      string // your JID once linked
	linked  bool
	handler func(whatsapp.Event)
	nextID  int

	Sent   []whatsapp.Message // what sandesh sent, in order
	Read   []string           // chats sandesh sent read receipts for
	Groups map[string]string  // group JID → name
	People map[string]string  // JID → contact name
	// History is played when sandesh connects for the first time.
	History []whatsapp.Message
	Chats   []whatsapp.Chat
	played  bool
	// LinkDelay is how long the "phone" takes to scan (the demo shows
	// the QR code meanwhile).
	LinkDelay time.Duration
}

// NewWhatsApp is a fake account with the phone number +91 98765 00000.
func NewWhatsApp() *WhatsApp {
	return &WhatsApp{Me: "919876500000@s.whatsapp.net", Groups: map[string]string{}, People: map[string]string{}}
}

// Conn is what app.DialWhatsApp returns: one linked device of this
// account (the account outlives it, as on a real phone).
func (w *WhatsApp) Conn(context.Context) (*WhatsAppConn, error) { return &WhatsAppConn{w: w}, nil }

// SetLinked links the account without the QR code.
func (w *WhatsApp) SetLinked() { w.mu.Lock(); w.linked = true; w.mu.Unlock() }

// Receive plays a message arriving from someone.
func (w *WhatsApp) Receive(m whatsapp.Message) {
	w.mu.Lock()
	h := w.handler
	if m.ID == "" {
		w.nextID++
		m.ID = fmt.Sprintf("IN%04d", w.nextID)
	}
	if m.Time.IsZero() {
		m.Time = time.Now()
	}
	w.mu.Unlock()
	if h != nil {
		h(whatsapp.Event{Messages: []whatsapp.Message{m}})
	}
}

// Emit plays any event.
func (w *WhatsApp) Emit(e whatsapp.Event) {
	w.mu.Lock()
	h := w.handler
	w.mu.Unlock()
	if h != nil {
		h(e)
	}
}

// WhatsAppConn is one connection to the fake account.
type WhatsAppConn struct {
	w      *WhatsApp
	closed bool
}

func (c *WhatsAppConn) Linked() bool { c.w.mu.Lock(); defer c.w.mu.Unlock(); return c.w.linked }

func (c *WhatsAppConn) Me() string {
	if !c.Linked() {
		return ""
	}
	return c.w.Me
}

// Link "scans" at once and stays online, like the real one.
func (c *WhatsAppConn) Link(ctx context.Context, phone string, show func(qr, code string), handler func(whatsapp.Event)) error {
	if phone != "" {
		show("", "K7QD9WXA")
	} else {
		show("2@sandesh-demo,not-a-real-code,"+strings.Repeat("x", 120), "")
	}
	select {
	case <-time.After(c.w.LinkDelay):
	case <-ctx.Done():
		return ctx.Err()
	}
	c.w.SetLinked()
	return c.Connect(handler)
}

func (c *WhatsAppConn) Connect(handler func(whatsapp.Event)) error {
	w := c.w
	w.mu.Lock()
	if !w.linked {
		w.mu.Unlock()
		return errors.New("not linked")
	}
	w.handler = handler
	history, chats, first := w.History, w.Chats, !w.played
	w.played = true
	people := map[string]string{}
	for k, v := range w.People {
		people[k] = v
	}
	w.mu.Unlock()
	handler(whatsapp.Event{State: "connected"})
	handler(whatsapp.Event{Contacts: people})
	if first {
		if len(chats) > 0 {
			handler(whatsapp.Event{Chats: chats, History: true, Progress: 40})
		}
		if len(history) > 0 {
			handler(whatsapp.Event{Messages: history, History: true, Progress: 100})
		}
	}
	return nil
}

func (c *WhatsAppConn) Send(ctx context.Context, chat, text string, quote *whatsapp.Message) (string, error) {
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if c.closed || w.handler == nil {
		return "", errors.New("offline")
	}
	w.nextID++
	m := whatsapp.Message{ID: fmt.Sprintf("OUT%04d", w.nextID), Chat: chat, Sender: w.Me, Text: text, Time: time.Now(), FromMe: true}
	if quote != nil {
		m.Quoted = quote.ID
	}
	w.Sent = append(w.Sent, m)
	return m.ID, nil
}

func (c *WhatsAppConn) MarkRead(ctx context.Context, chat string, msgs []whatsapp.Message) error {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	c.w.Read = append(c.w.Read, chat)
	return nil
}

func (c *WhatsAppConn) Resolve(ctx context.Context, phone string) (string, error) {
	return whatsapp.PhoneJID(phone), nil
}

func (c *WhatsAppConn) GroupName(ctx context.Context, chat string) (string, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	if n, ok := c.w.Groups[chat]; ok {
		return n, nil
	}
	return "", errors.New("no such group")
}

func (c *WhatsAppConn) Logout(ctx context.Context) error {
	c.w.mu.Lock()
	c.w.linked, c.w.played = false, false
	c.w.mu.Unlock()
	return nil
}

func (c *WhatsAppConn) Close() {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	c.closed = true
	c.w.handler = nil
}

// SeedWhatsApp fills the account with a few chats for the demo.
func (w *WhatsApp) SeedWhatsApp() {
	now := time.Now()
	amma, ravi, group := "919800000001@s.whatsapp.net", "919800000002@s.whatsapp.net", "120363000000000001@g.us"
	w.People[amma] = "Amma"
	w.People[ravi] = "Ravi Kulkarni"
	w.Groups[group] = "Weekend trek"
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	w.Chats = []whatsapp.Chat{
		{JID: amma, Last: at(20 * time.Minute), Unread: 2},
		{JID: ravi, Last: at(3 * time.Hour)},
		{JID: group, Name: "Weekend trek", Group: true, Last: at(40 * time.Minute), Unread: 3},
	}
	w.History = []whatsapp.Message{
		{ID: "H1", Chat: amma, Sender: w.Me, FromMe: true, Text: "Reached office, will call at lunch", Time: at(5 * time.Hour)},
		{ID: "H2", Chat: amma, Sender: amma, Text: "Did you eat?", Time: at(25 * time.Minute)},
		{ID: "H3", Chat: amma, Sender: amma, Text: "📷 Photo: the new curtains", Time: at(20 * time.Minute)},
		{ID: "H4", Chat: ravi, Sender: ravi, SenderName: "Ravi", Text: "PR is up, can you look today?", Time: at(4 * time.Hour)},
		{ID: "H5", Chat: ravi, Sender: w.Me, FromMe: true, Text: "Yes, after standup", Time: at(3 * time.Hour), Quoted: "H4"},
		{ID: "H6", Chat: group, Sender: ravi, SenderName: "Ravi", Text: "Rajmachi this Saturday?", Time: at(2 * time.Hour)},
		{ID: "H7", Chat: group, Sender: "919800000003@s.whatsapp.net", SenderName: "Sneha", Text: "I'm in! Leaving 5am?", Time: at(70 * time.Minute)},
		{ID: "H8", Chat: group, Sender: "919800000004@s.whatsapp.net", SenderName: "Kunal", Text: "📍 Location https://maps.google.com/?q=18.828,73.395: base village", Time: at(50 * time.Minute)},
		{ID: "H9", Chat: group, Sender: ravi, SenderName: "Ravi", Text: "Done, 5am at Lonavala station", Time: at(40 * time.Minute)},
	}
	w.linked = true
}
