// Package whatsapp links sandesh to a WhatsApp account as a companion
// device, the way WhatsApp Web is linked, through whatsmeow. It turns
// WhatsApp's events into plain chats and messages for the app layer and
// sends text; it keeps nothing itself except whatsmeow's device keys
// (whatsapp.db). See docs/adr/0005-whatsapp-as-a-linked-device.md.
package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite" // the keys' database, like the local copy's
)

// Message is one WhatsApp message, as sandesh keeps it. Chat and Sender
// are JIDs ("919876543210@s.whatsapp.net", "1203…@g.us").
type Message struct {
	ID         string
	Chat       string
	Sender     string
	SenderName string
	Text       string
	Time       time.Time
	FromMe     bool
	Quoted     string // the id of the message this one replies to
	Edit       bool   // Text replaces message ID's text
	Revoke     bool   // message ID was deleted for everyone
	Media      *Media // a photo, GIF, file... it carries
}

// Chat is a conversation: a person or a group.
type Chat struct {
	JID    string
	Name   string
	Group  bool
	Last   time.Time
	Unread int // from the phone's history: how many are unread
}

// Event is what the connection hears. Exactly one part is set.
type Event struct {
	Messages []Message // new ones, or a page of history
	History  bool      // Messages came from the phone's history
	Progress int       // with History: how far the phone's sending of it is, 0-100 (0: unknown)
	Chats    []Chat    // names and unread counts
	Read     string    // a chat read on another device ...
	ReadAt   time.Time // ... up to this time
	Contacts map[string]string
	State    string // connected, disconnected, logged-out, replaced, banned
	Err      error
}

// Client is a WhatsApp account linked to this machine.
type Client struct {
	c     *whatsmeow.Client
	store *sqlstore.Container

	mu      sync.Mutex
	handler func(Event)
}

func init() {
	// what the phone shows under Linked devices
	store.SetOSInfo("sandesh", [3]uint32{0, 3, 0})
}

// Open opens the device keys in path (made 0600), linked or not.
func Open(ctx context.Context, path string) (*Client, error) {
	sc, err := sqlstore.New(ctx, "sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)", waLog.Noop)
	if err != nil {
		return nil, fmt.Errorf("whatsapp keys: %w", err)
	}
	for _, x := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(path+x, 0o600)
	}
	dev, err := sc.GetFirstDevice(ctx)
	if err != nil {
		sc.Close()
		return nil, err
	}
	cl := &Client{store: sc}
	cl.c = whatsmeow.NewClient(dev, waLog.Noop)
	cl.c.AddEventHandler(cl.event)
	return cl, nil
}

// Linked: this machine has been linked (it may still be logged out by
// the phone later).
func (cl *Client) Linked() bool { return cl.c.Store.ID != nil }

// Me is your own JID ("" before linking).
func (cl *Client) Me() string {
	if cl.c.Store.ID == nil {
		return ""
	}
	return cl.c.Store.ID.ToNonAD().String()
}

// Link links this machine. Without a phone number, show gets each QR
// code to draw (they change every ~20 seconds); with one, show gets the
// eight-character code to type on the phone. Once the phone accepts,
// WhatsApp has the new device reconnect and log in with its new keys:
// Link waits for that (going offline before it makes the phone report
// the link failed) and returns online, handing events to handler like
// Connect.
func (cl *Client) Link(ctx context.Context, phone string, show func(qr, code string), handler func(Event)) error {
	if cl.Linked() {
		return errors.New("already linked")
	}
	loggedIn := make(chan error, 1)
	cl.mu.Lock()
	cl.handler = func(e Event) {
		switch e.State {
		case "connected":
			select {
			case loggedIn <- nil:
			default:
			}
		case "logged-out", "replaced", "banned":
			select {
			case loggedIn <- e.Err:
			default:
			}
		}
		handler(e)
	}
	cl.mu.Unlock()
	qrs, err := cl.c.GetQRChannel(ctx)
	if err != nil {
		return err
	}
	if err := cl.c.Connect(); err != nil {
		return err
	}
	if phone != "" {
		code, err := cl.c.PairPhone(ctx, Digits(phone), true, whatsmeow.PairClientChrome, "Chrome (Linux)")
		if err != nil {
			cl.c.Disconnect()
			return err
		}
		show("", code)
	}
	paired := false
	for it := range qrs {
		if it.Event == whatsmeow.QRChannelEventCode {
			if phone == "" {
				show(it.Code, "")
			}
			continue
		}
		if it == whatsmeow.QRChannelSuccess {
			paired = true
			break
		}
		cl.c.Disconnect()
		switch {
		case it.Error != nil:
			return it.Error
		case it.Event == "timeout":
			return errors.New("timed out: nobody scanned the code")
		}
		return fmt.Errorf("linking failed (%s)", it.Event)
	}
	if !paired {
		cl.c.Disconnect()
		return ctx.Err()
	}
	select {
	case err := <-loggedIn:
		if err != nil {
			cl.c.Disconnect()
			return fmt.Errorf("the phone accepted, but logging in failed: %w", err)
		}
		return nil
	case <-time.After(90 * time.Second):
		cl.c.Disconnect()
		return errors.New("the phone accepted, but WhatsApp never logged this device in; try again")
	case <-ctx.Done():
		cl.c.Disconnect()
		return ctx.Err()
	}
}

// Connect goes online and hands every event to handler, until Close.
func (cl *Client) Connect(handler func(Event)) error {
	if !cl.Linked() {
		return errors.New("WhatsApp is not linked: run `sandesh whatsapp link`")
	}
	cl.mu.Lock()
	cl.handler = handler
	cl.mu.Unlock()
	if cl.c.IsConnected() {
		return nil // still online from Link
	}
	return cl.c.Connect()
}

func (cl *Client) emit(e Event) {
	cl.mu.Lock()
	h := cl.handler
	cl.mu.Unlock()
	if h != nil {
		h(e)
	}
}

// Close goes offline and closes the keys.
func (cl *Client) Close() {
	cl.c.Disconnect()
	_ = cl.store.Close()
}

// Logout unlinks this machine (it disappears from the phone's Linked
// devices) and forgets the keys.
func (cl *Client) Logout(ctx context.Context) error {
	if !cl.c.IsConnected() {
		if err := cl.c.Connect(); err != nil {
			return err
		}
	}
	return cl.c.Logout(ctx)
}

// Send sends a text message to chat; quote (optional) is the message it
// replies to. Returns the new message's id.
func (cl *Client) Send(ctx context.Context, chat, text string, quote *Message) (string, error) {
	to, err := types.ParseJID(chat)
	if err != nil {
		return "", fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
	}
	msg := &waE2E.Message{Conversation: proto.String(text)}
	if quote != nil {
		msg = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID:      proto.String(quote.ID),
				Participant:   proto.String(quote.Sender),
				QuotedMessage: &waE2E.Message{Conversation: proto.String(quote.Text)},
			},
		}}
	}
	resp, err := cl.c.SendMessage(ctx, to, msg)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// MarkRead tells the sender (and your other devices) that these
// messages of one chat were read.
func (cl *Client) MarkRead(ctx context.Context, chat string, msgs []Message) error {
	jid, err := types.ParseJID(chat)
	if err != nil {
		return err
	}
	// receipts go per sender (a group has many)
	bySender := map[string][]types.MessageID{}
	for _, m := range msgs {
		if !m.FromMe {
			bySender[m.Sender] = append(bySender[m.Sender], m.ID)
		}
	}
	for sender, ids := range bySender {
		var s types.JID
		if jid.Server == types.GroupServer {
			if s, err = types.ParseJID(sender); err != nil {
				continue
			}
		}
		if err := cl.c.MarkRead(ctx, ids, time.Now(), jid, s); err != nil {
			return err
		}
	}
	return nil
}

// Resolve turns a phone number into the person's chat JID, checking
// they are on WhatsApp.
func (cl *Client) Resolve(ctx context.Context, phone string) (string, error) {
	d := Digits(phone)
	res, err := cl.c.IsOnWhatsApp(ctx, []string{"+" + d})
	if err != nil {
		return "", err
	}
	for _, r := range res {
		if r.IsIn {
			return r.JID.ToNonAD().String(), nil
		}
	}
	return "", fmt.Errorf("+%s is not on WhatsApp", d)
}

// GroupName asks WhatsApp for a group's subject.
func (cl *Client) GroupName(ctx context.Context, chat string) (string, error) {
	jid, err := types.ParseJID(chat)
	if err != nil {
		return "", err
	}
	info, err := cl.c.GetGroupInfo(ctx, jid)
	if err != nil {
		return "", err
	}
	return info.Name, nil
}

// Digits keeps a phone number's digits: "+91 98765-43210" → "919876543210".
func Digits(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PhoneJID is a phone number's chat JID, without asking WhatsApp.
func PhoneJID(phone string) string { return Digits(phone) + "@" + types.DefaultUserServer }

// IsGroup: the JID is a group chat.
func IsGroup(jid string) bool { return strings.HasSuffix(jid, "@"+types.GroupServer) }

// ------------------------------------------------------------------ events

func (cl *Client) event(evt any) {
	ctx := context.Background()
	switch e := evt.(type) {
	case *events.Connected:
		cl.emit(Event{State: "connected"})
		cl.emit(Event{Contacts: cl.contacts(ctx)})
	case *events.Disconnected:
		cl.emit(Event{State: "disconnected"})
	case *events.LoggedOut:
		cl.emit(Event{State: "logged-out", Err: errors.New("the phone unlinked this device: run `sandesh whatsapp link` again")})
	case *events.StreamReplaced:
		cl.emit(Event{State: "replaced", Err: errors.New("another program connected with these keys")})
	case *events.TemporaryBan:
		cl.emit(Event{State: "banned", Err: errors.New(e.String())})
	case *events.Message:
		if m, ok := cl.convert(ctx, e); ok {
			cl.emit(Event{Messages: []Message{m}})
		}
	case *events.HistorySync:
		cl.history(ctx, e)
	case *events.Receipt:
		// you read it on your phone (or another linked device)
		if e.IsFromMe && (e.Type == types.ReceiptTypeRead || e.Type == types.ReceiptTypeReadSelf) {
			cl.emit(Event{Read: cl.chatJID(ctx, e.Chat), ReadAt: e.Timestamp})
		}
	case *events.MarkChatAsRead:
		if e.Action.GetRead() {
			cl.emit(Event{Read: cl.chatJID(ctx, e.JID), ReadAt: e.Timestamp})
		}
	case *events.PushName:
		cl.emit(Event{Contacts: map[string]string{cl.chatJID(ctx, e.JID): e.NewPushName}})
	case *events.GroupInfo:
		if e.Name != nil {
			cl.emit(Event{Chats: []Chat{{JID: e.JID.String(), Name: e.Name.Name, Group: true}}})
		}
	}
}

// chatJID: a person's phone-number JID when WhatsApp addressed them by
// their hidden id (LID), so one person is one chat.
func (cl *Client) chatJID(ctx context.Context, j types.JID) string {
	j = j.ToNonAD()
	if j.Server == types.HiddenUserServer {
		if pn, err := cl.c.Store.LIDs.GetPNForLID(ctx, j); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD().String()
		}
	}
	return j.String()
}

func (cl *Client) contacts(ctx context.Context) map[string]string {
	all, err := cl.c.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for jid, c := range all {
		n := firstNonEmpty(c.FullName, c.FirstName, c.BusinessName)
		if n == "" && c.PushName != "" {
			n = "~" + c.PushName // their own name: not in your contacts
		}
		if n != "" {
			out[cl.chatJID(ctx, jid)] = n
		}
	}
	return out
}

func (cl *Client) history(ctx context.Context, e *events.HistorySync) {
	var chats []Chat
	var msgs []Message
	for _, conv := range e.Data.GetConversations() {
		jid, err := types.ParseJID(conv.GetID())
		if err != nil || Hidden(jid.String()) || jid.Server == types.BroadcastServer || jid.Server == types.NewsletterServer {
			continue
		}
		ch := Chat{JID: cl.chatJID(ctx, jid), Name: conv.GetName(), Group: jid.Server == types.GroupServer,
			Last: time.Unix(int64(conv.GetConversationTimestamp()), 0), Unread: int(conv.GetUnreadCount())}
		chats = append(chats, ch)
		for _, hm := range conv.GetMessages() {
			evt, err := cl.c.ParseWebMessage(jid, hm.GetMessage())
			if err != nil {
				continue
			}
			if m, ok := cl.convert(ctx, evt); ok {
				msgs = append(msgs, m)
			}
		}
	}
	pct := int(e.Data.GetProgress())
	if len(chats) > 0 {
		cl.emit(Event{Chats: chats, History: true, Progress: pct})
	}
	if len(msgs) > 0 || len(chats) == 0 {
		cl.emit(Event{Messages: msgs, History: true, Progress: pct})
	}
	cl.emit(Event{Contacts: cl.contacts(ctx)})
}

// convert turns a whatsmeow message into ours; ok is false for what
// sandesh doesn't show (status updates, key exchanges, reactions).
func (cl *Client) convert(ctx context.Context, e *events.Message) (Message, bool) {
	info := e.Info
	if Hidden(info.Chat.String()) || info.Chat.Server == types.BroadcastServer || info.Chat.Server == types.NewsletterServer {
		return Message{}, false
	}
	sender := info.Sender
	if sender.Server == types.HiddenUserServer && info.SenderAlt.Server == types.DefaultUserServer {
		sender = info.SenderAlt // the phone number WhatsApp sent beside the hidden id
	}
	chat := info.Chat
	if chat.Server == types.HiddenUserServer {
		if alt := firstPN(info.SenderAlt, info.RecipientAlt); !alt.IsEmpty() && !info.IsGroup {
			chat = alt
		}
	}
	m := Message{ID: info.ID, Chat: cl.chatJID(ctx, chat), Sender: cl.chatJID(ctx, sender), SenderName: info.PushName,
		Time: info.Timestamp, FromMe: info.IsFromMe}
	msg := e.Message
	if pm := msg.GetProtocolMessage(); pm != nil {
		switch pm.GetType() {
		case waE2E.ProtocolMessage_REVOKE:
			m.ID, m.Revoke = pm.GetKey().GetID(), true
			return m, true
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			m.ID, m.Edit = pm.GetKey().GetID(), true
			m.Text = Text(pm.GetEditedMessage())
			return m, m.Text != ""
		}
		return Message{}, false
	}
	if e.IsEdit {
		m.Edit = true
	}
	m.Text = Text(msg)
	if m.Text == "" {
		return Message{}, false
	}
	m.Media = media(msg)
	if ci := contextInfo(msg); ci != nil {
		m.Quoted = ci.GetStanzaID()
		m.Text = cl.mentions(ctx, m.Text, ci.GetMentionedJID())
	}
	return m, true
}

// mentions writes "@Priya" for the "@1234…" WhatsApp puts in the text
// (a hidden id, or a phone number) where it knows the name.
func (cl *Client) mentions(ctx context.Context, text string, jids []string) string {
	for _, s := range jids {
		j, err := types.ParseJID(s)
		if err != nil || !strings.Contains(text, "@"+j.User) {
			continue
		}
		if name := cl.name(ctx, j); name != "" {
			text = strings.ReplaceAll(text, "@"+j.User, "@"+name)
		}
	}
	return text
}

// name is what you call someone: your contact name, else theirs.
func (cl *Client) name(ctx context.Context, j types.JID) string {
	for _, try := range []types.JID{j, mustJID(cl.chatJID(ctx, j))} {
		if c, err := cl.c.Store.Contacts.GetContact(ctx, try); err == nil && c.Found {
			if n := firstNonEmpty(c.FullName, c.FirstName, c.PushName, c.BusinessName); n != "" {
				return n
			}
		}
	}
	return ""
}

func mustJID(s string) types.JID { j, _ := types.ParseJID(s); return j }

func firstPN(js ...types.JID) types.JID {
	for _, j := range js {
		if j.Server == types.DefaultUserServer {
			return j
		}
	}
	return types.JID{}
}

// Hidden: WhatsApp's own service chats (its announcements), not people.
func Hidden(jid string) bool { return jid == "0@s.whatsapp.net" || strings.HasPrefix(jid, "status@") }

// Known names for WhatsApp's own accounts.
var Known = map[string]string{"13135550002@s.whatsapp.net": "Meta AI"}

func contextInfo(msg *waE2E.Message) *waE2E.ContextInfo {
	switch {
	case msg.GetExtendedTextMessage() != nil:
		return msg.GetExtendedTextMessage().GetContextInfo()
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage().GetContextInfo()
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage().GetContextInfo()
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage().GetContextInfo()
	case msg.GetAudioMessage() != nil:
		return msg.GetAudioMessage().GetContextInfo()
	}
	return nil
}

// Text is what a message says, with a word for what it carries ("📷
// Photo: caption"); "" for messages with nothing to show.
func Text(msg *waE2E.Message) string {
	with := func(label, caption string) string {
		if caption = strings.TrimSpace(caption); caption != "" {
			return label + ": " + caption
		}
		return label
	}
	switch {
	case msg == nil:
		return ""
	case msg.GetConversation() != "":
		return msg.GetConversation()
	case msg.GetExtendedTextMessage() != nil:
		return msg.GetExtendedTextMessage().GetText()
	case msg.GetImageMessage() != nil:
		return with("📷 Photo", msg.GetImageMessage().GetCaption())
	case msg.GetVideoMessage() != nil:
		if msg.GetVideoMessage().GetGifPlayback() {
			return with("GIF", msg.GetVideoMessage().GetCaption())
		}
		return with("🎬 Video", msg.GetVideoMessage().GetCaption())
	case msg.GetAudioMessage() != nil:
		if msg.GetAudioMessage().GetPTT() {
			return "🎤 Voice message"
		}
		return "🎵 Audio"
	case msg.GetDocumentMessage() != nil:
		d := msg.GetDocumentMessage()
		return with("📎 "+firstNonEmpty(d.GetFileName(), d.GetTitle(), "Document"), d.GetCaption())
	case msg.GetStickerMessage() != nil:
		return "Sticker"
	case msg.GetLocationMessage() != nil:
		l := msg.GetLocationMessage()
		return with(fmt.Sprintf("📍 Location https://maps.google.com/?q=%f,%f", l.GetDegreesLatitude(), l.GetDegreesLongitude()), l.GetName())
	case msg.GetContactMessage() != nil:
		return "👤 Contact: " + msg.GetContactMessage().GetDisplayName()
	case msg.GetPollCreationMessage() != nil:
		return "📊 Poll: " + msg.GetPollCreationMessage().GetName()
	case msg.GetPollCreationMessageV3() != nil:
		return "📊 Poll: " + msg.GetPollCreationMessageV3().GetName()
	}
	return ""
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
