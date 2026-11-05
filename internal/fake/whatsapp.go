package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

// WhatsApp is an in-memory WhatsApp account for the tests and `narada
// demo`: it links at once, answers sends, and lets a test play messages
// arriving.
type WhatsApp struct {
	mu      sync.Mutex
	Me      string // your JID once linked
	linked  bool
	handler func(whatsapp.Event)
	nextID  int

	Sent      []whatsapp.Message  // what narada sent, in order
	Read      []string            // chats narada sent read receipts for
	Reacted   []string            // reactions sent: "chat/id sender emoji"
	Edited    []string            // edits sent: "chat/id new text"
	Revoked   []string            // deleted for everyone: "chat/id"
	Quotes    []whatsapp.Message  // what each send that replied quoted
	Did       []string            // chat-state, presence and group actions: "action chat arg"
	Voted     []string            // votes sent: "chat/poll option,option"
	Disappear map[string]int      // the timer narada gave each chat before sending
	DoErr     error               // Do fails with this
	Members   map[string][]string // group JID → its people
	ReadErr   error               // MarkRead fails with this
	SendErr   error               // Send fails with this
	Groups    map[string]string   // group JID → name
	People    map[string]string   // JID → contact name
	// History is played when narada connects for the first time.
	History []whatsapp.Message
	Chats   []whatsapp.Chat
	played  bool
	// LinkDelay is how long the "phone" takes to scan (the demo shows
	// the QR code meanwhile).
	LinkDelay time.Duration
	// Receipts: the "other side" gets each message you send a second
	// later and reads it three seconds later (the demo's ticks)
	Receipts bool
	// Files are the media on WhatsApp's servers, by Media.Raw.
	Files map[string][]byte
}

// NewWhatsApp is a fake account with the phone number +91 98765 00000.
func NewWhatsApp() *WhatsApp {
	return &WhatsApp{Me: "919876500000@s.whatsapp.net", Groups: map[string]string{}, People: map[string]string{}, Files: map[string][]byte{},
		Members: map[string][]string{}}
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

// Receipt: the other side got (level 2) or read (3) messages of yours.
func (w *WhatsApp) Receipt(chat string, level int, ids ...string) {
	w.Emit(whatsapp.Event{Ticks: &whatsapp.Ticks{Chat: chat, IDs: ids, Level: level}})
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
		show("2@narada-demo,not-a-real-code,"+strings.Repeat("x", 120), "")
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
	if w.SendErr != nil {
		return "", w.SendErr
	}
	w.nextID++
	m := whatsapp.Message{ID: fmt.Sprintf("OUT%04d", w.nextID), Chat: chat, Sender: w.Me, Text: text, Time: time.Now(), FromMe: true}
	if quote != nil {
		m.Quoted = quote.ID
		w.Quotes = append(w.Quotes, *quote)
	}
	w.Sent = append(w.Sent, m)
	w.receipts(chat, m.ID)
	return m.ID, nil
}

func (c *WhatsAppConn) SendFile(ctx context.Context, chat string, f whatsapp.File, quote *whatsapp.Message) (string, *whatsapp.Media, error) {
	name, caption := f.Name, f.Caption
	if name == "" {
		name = filepath.Base(f.Path)
	}
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return "", nil, err
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if c.closed || w.handler == nil {
		return "", nil, errors.New("offline")
	}
	w.nextID++
	kind := whatsapp.KindOf(name, http.DetectContentType(data))
	if f.Voice {
		kind = "voice"
	}
	md := &whatsapp.Media{Kind: kind, Name: name, Mime: http.DetectContentType(data), Size: int64(len(data)),
		Raw: []byte(fmt.Sprintf("sent-%d", w.nextID))}
	w.Files[string(md.Raw)] = data
	m := whatsapp.Message{ID: fmt.Sprintf("OUT%04d", w.nextID), Chat: chat, Sender: w.Me, Text: caption, Time: time.Now(), FromMe: true, Media: md}
	if quote != nil {
		m.Quoted = quote.ID
	}
	w.Sent = append(w.Sent, m)
	w.receipts(chat, m.ID)
	return m.ID, md, nil
}

// receipts plays delivered and read for a sent message (w.mu held).
func (w *WhatsApp) receipts(chat, id string) {
	if !w.Receipts {
		return
	}
	go func() {
		time.Sleep(time.Second)
		w.Receipt(chat, whatsapp.TickDelivered, id)
		time.Sleep(3 * time.Second)
		w.Receipt(chat, whatsapp.TickRead, id)
	}()
}

func (c *WhatsAppConn) Download(ctx context.Context, raw []byte) ([]byte, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	if b, ok := c.w.Files[string(raw)]; ok {
		return b, nil
	}
	return nil, errors.New("no such file on the fake WhatsApp")
}

func (c *WhatsAppConn) React(ctx context.Context, chat, sender, id, emoji string) error {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	if c.w.SendErr != nil {
		return c.w.SendErr
	}
	c.w.Reacted = append(c.w.Reacted, chat+"/"+id+" "+sender+" "+emoji)
	return nil
}

func (c *WhatsAppConn) Edit(ctx context.Context, chat, id, text string) error {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	if c.w.SendErr != nil {
		return c.w.SendErr
	}
	c.w.Edited = append(c.w.Edited, chat+"/"+id+" "+text)
	return nil
}

func (c *WhatsAppConn) Revoke(ctx context.Context, chat, sender, id string) error {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	if c.w.SendErr != nil {
		return c.w.SendErr
	}
	c.w.Revoked = append(c.w.Revoked, chat+"/"+id)
	return nil
}

// Forward records the forward as a sent message (Forwarded, with the
// original's media).
func (c *WhatsAppConn) Forward(ctx context.Context, chat string, m whatsapp.Message) (string, error) {
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if c.closed || w.handler == nil {
		return "", errors.New("offline")
	}
	if w.SendErr != nil {
		return "", w.SendErr
	}
	w.nextID++
	fw := whatsapp.Message{ID: fmt.Sprintf("OUT%04d", w.nextID), Chat: chat, Sender: w.Me, Text: m.Text, Time: time.Now(), FromMe: true,
		Media: m.Media, Forwarded: true}
	w.Sent = append(w.Sent, fw)
	return fw.ID, nil
}

func (c *WhatsAppConn) SendPoll(ctx context.Context, chat string, p whatsapp.Poll) (string, error) {
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if c.closed || w.handler == nil {
		return "", errors.New("offline")
	}
	w.nextID++
	m := whatsapp.Message{ID: fmt.Sprintf("OUT%04d", w.nextID), Chat: chat, Sender: w.Me, Text: "📊 Poll: " + p.Question, Time: time.Now(), FromMe: true, Poll: &p}
	w.Sent = append(w.Sent, m)
	return m.ID, nil
}

func (c *WhatsAppConn) SendVote(ctx context.Context, chat, sender, pollID string, fromMe bool, options []string) error {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	if c.w.SendErr != nil {
		return c.w.SendErr
	}
	c.w.Voted = append(c.w.Voted, chat+"/"+pollID+" "+strings.Join(options, ","))
	return nil
}

func (c *WhatsAppConn) SendLocation(ctx context.Context, chat string, l whatsapp.Location) (string, error) {
	return c.Send(ctx, chat, fmt.Sprintf("📍 Location https://maps.google.com/?q=%f,%f", l.Lat, l.Lon), nil)
}

func (c *WhatsAppConn) SendContact(ctx context.Context, chat string, ct whatsapp.Contact) (string, error) {
	return c.Send(ctx, chat, "👤 Contact: "+ct.Name+" "+whatsapp.VCard(ct), nil)
}

// SetDisappearing records the timer narada says a chat has.
func (c *WhatsAppConn) SetDisappearing(chat string, secs int) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	if c.w.Disappear == nil {
		c.w.Disappear = map[string]int{}
	}
	c.w.Disappear[chat] = secs
}

func (c *WhatsAppConn) PinMessage(ctx context.Context, chat, sender, id string, on bool, secs int) error {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	if c.w.SendErr != nil {
		return c.w.SendErr
	}
	c.w.Did = append(c.w.Did, fmt.Sprintf("pin-message %s/%s %v %d", chat, id, on, secs))
	return nil
}

// ReceiveVote is someone voting in a poll.
func (w *WhatsApp) ReceiveVote(chat, poll, voter string, options ...string) {
	v := whatsapp.Vote{Chat: chat, Poll: poll, Voter: voter}
	for _, o := range options {
		v.Hashes = append(v.Hashes, whatsapp.OptionHash(o))
	}
	w.Emit(whatsapp.Event{Votes: []whatsapp.Vote{v}})
}

// Do records the action and answers as WhatsApp would.
func (c *WhatsAppConn) Do(ctx context.Context, action, chat, arg string) (string, error) {
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.DoErr != nil {
		return "", w.DoErr
	}
	w.Did = append(w.Did, strings.TrimSpace(action+" "+chat+" "+arg))
	if w.Members == nil {
		w.Members = map[string][]string{}
	}
	switch action {
	case whatsapp.DoGroupNew:
		var g whatsapp.NewGroup
		_ = json.Unmarshal([]byte(arg), &g)
		w.nextID++
		jid := fmt.Sprintf("1203639000000%05d@g.us", w.nextID)
		w.Groups[jid] = g.Name
		w.Members[jid] = append([]string{w.Me}, g.People...)
		return jid, nil
	case whatsapp.DoGroupName:
		w.Groups[chat] = arg
	case whatsapp.DoGroupAdd:
		w.Members[chat] = append(w.Members[chat], strings.Split(arg, ",")...)
	case whatsapp.DoGroupKick:
		var keep []string
		for _, p := range w.Members[chat] {
			if !strings.Contains(","+arg+",", ","+p+",") {
				keep = append(keep, p)
			}
		}
		w.Members[chat] = keep
	case whatsapp.DoGroupLink:
		return "https://chat.whatsapp.com/FAKEINVITE" + map[bool]string{true: "2", false: "1"}[arg == "reset"], nil
	case whatsapp.DoGroupJoin:
		jid := "120363000000000777@g.us"
		w.Groups[jid] = "Joined by link"
		return jid, nil
	case whatsapp.DoGroupInfo:
		name, ok := w.Groups[chat]
		if !ok {
			return "", errors.New("no such group")
		}
		g := whatsapp.GroupInfo{JID: chat, Name: name}
		for i, p := range w.Members[chat] {
			g.People = append(g.People, whatsapp.GroupPerson{JID: p, Admin: i == 0, Owner: i == 0})
		}
		b, _ := json.Marshal(g)
		return string(b), nil
	}
	return "", nil
}

func (c *WhatsAppConn) MarkRead(ctx context.Context, chat string, msgs []whatsapp.Message) error {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	if c.w.ReadErr != nil {
		return c.w.ReadErr
	}
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
	w.Members[group] = []string{w.Me, ravi, "919800000003@s.whatsapp.net", "919800000004@s.whatsapp.net"}
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	w.Chats = []whatsapp.Chat{
		{JID: amma, Last: at(20 * time.Minute), Unread: 2},
		{JID: ravi, Last: at(3 * time.Hour)},
		{JID: group, Name: "Weekend trek", Group: true, Last: at(40 * time.Minute), Unread: 3},
	}
	w.History = []whatsapp.Message{
		{ID: "H1", Chat: amma, Sender: w.Me, FromMe: true, Text: "Reached office, will call at lunch", Time: at(5 * time.Hour), Ticks: whatsapp.TickRead},
		{ID: "H2", Chat: amma, Sender: amma, Text: "Did you eat?", Time: at(25 * time.Minute)},
		{ID: "H3", Chat: amma, Sender: amma, Text: "📷 Photo: the new curtains", Time: at(20 * time.Minute),
			Media: &whatsapp.Media{Kind: "image", Name: "photo.png", Mime: "image/png", Raw: []byte("demo-photo")}},
		{ID: "H4", Chat: ravi, Sender: ravi, SenderName: "Ravi", Text: "PR is up, can you look today?", Time: at(4 * time.Hour)},
		{ID: "H5", Chat: ravi, Sender: w.Me, FromMe: true, Text: "Yes, after standup", Time: at(3 * time.Hour), Quoted: "H4", Ticks: whatsapp.TickDelivered},
		{ID: "H6", Chat: group, Sender: ravi, SenderName: "Ravi", Text: "Rajmachi this Saturday?", Time: at(2 * time.Hour)},
		{ID: "H7", Chat: group, Sender: "919800000003@s.whatsapp.net", SenderName: "Sneha", Text: "I'm in! Leaving 5am?", Time: at(70 * time.Minute)},
		{ID: "H8", Chat: group, Sender: "919800000004@s.whatsapp.net", SenderName: "Kunal", Text: "📍 Location https://maps.google.com/?q=18.828,73.395: base village", Time: at(50 * time.Minute)},
		{ID: "H9", Chat: group, Sender: ravi, SenderName: "Ravi", Text: "Done, 5am at Lonavala station", Time: at(40 * time.Minute)},
		{ID: "H10", Chat: group, Sender: "919800000003@s.whatsapp.net", SenderName: "Sneha", Text: "GIF", Time: at(35 * time.Minute),
			Media: &whatsapp.Media{Kind: "gif", Name: "gif.gif", Mime: "image/gif", Raw: []byte("demo-gif")}},
	}
	w.Files["demo-photo"] = demoPhoto()
	w.Files["demo-gif"] = demoGIF()
	for i := range w.History {
		if md := w.History[i].Media; md != nil {
			md.Size = int64(len(w.Files[string(md.Raw)]))
		}
	}
	w.linked = true
}

// demoPhoto is a made-up photo: a sunset gradient with a sun.
func demoPhoto() []byte {
	const W, H = 480, 320
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			t := float64(y) / H
			c := color.RGBA{uint8(250 - 120*t), uint8(140 - 90*t), uint8(90 + 100*t), 255}
			dx, dy := x-W/2, y-H*2/3
			if dx*dx+dy*dy < 60*60 {
				c = color.RGBA{255, 214, 120, 255}
			}
			if y > H*3/4 {
				c = color.RGBA{uint8(40 + 30*t), uint8(50 + 30*t), 90, 255}
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// demoGIF is a made-up GIF: a ball bouncing across, 12 frames.
func demoGIF() []byte {
	const W, H = 160, 100
	pal := color.Palette{color.RGBA{30, 30, 46, 255}, color.RGBA{166, 227, 161, 255}, color.RGBA{137, 180, 250, 255}}
	var g gif.GIF
	for f := 0; f < 12; f++ {
		img := image.NewPaletted(image.Rect(0, 0, W, H), pal)
		cx := 20 + f*10
		cy := 70 - int(40*math.Abs(math.Sin(float64(f)*math.Pi/6)))
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				switch {
				case (x-cx)*(x-cx)+(y-cy)*(y-cy) < 12*12:
					img.SetColorIndex(x, y, 1)
				case y > 84:
					img.SetColorIndex(x, y, 2)
				}
			}
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 8)
	}
	var b bytes.Buffer
	_ = gif.EncodeAll(&b, &g)
	return b.Bytes()
}

// GroupChanged is WhatsApp saying who's in a group now.
func (w *WhatsApp) GroupChanged(ch whatsapp.Chat) {
	w.mu.Lock()
	h := w.handler
	w.mu.Unlock()
	if h != nil {
		h(whatsapp.Event{Chats: []whatsapp.Chat{ch}})
	}
}

// ReceiveReaction is someone reacting to a message (or taking a reaction back).
func (w *WhatsApp) ReceiveReaction(r whatsapp.Reaction) {
	w.mu.Lock()
	h := w.handler
	w.mu.Unlock()
	if h != nil {
		h(whatsapp.Event{Reactions: []whatsapp.Reaction{r}})
	}
}
