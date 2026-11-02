package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

// WhatsAppConn is a WhatsApp account linked as a device: whatsapp.Client,
// or the fake one in tests and the demo.
type WhatsAppConn interface {
	Linked() bool
	Me() string
	Link(ctx context.Context, phone string, show func(qr, code string), handler func(whatsapp.Event)) error
	Connect(handler func(whatsapp.Event)) error
	Send(ctx context.Context, chat, text string, quote *whatsapp.Message) (string, error)
	SendFile(ctx context.Context, chat, path, caption string, quote *whatsapp.Message) (string, *whatsapp.Media, error)
	Download(ctx context.Context, raw []byte) ([]byte, error)
	MarkRead(ctx context.Context, chat string, msgs []whatsapp.Message) error
	Resolve(ctx context.Context, phone string) (string, error)
	GroupName(ctx context.Context, chat string) (string, error)
	Logout(ctx context.Context) error
	Close()
}

// WhatsAppKeys is where whatsmeow keeps this device's keys.
func (a *App) WhatsAppKeys() string { return filepath.Join(a.Paths.Data, "whatsapp.db") }

func (a *App) dialWhatsApp(ctx context.Context) (WhatsAppConn, error) {
	if a.DialWhatsApp != nil {
		return a.DialWhatsApp(ctx)
	}
	return whatsapp.Open(ctx, a.WhatsAppKeys())
}

// WhatsAppOnline: this process holds the WhatsApp connection (and so
// delivers WhatsApp sends).
func (a *App) WhatsAppOnline() bool {
	a.waMu.Lock()
	defer a.waMu.Unlock()
	return a.wa != nil
}

func (a *App) whatsApp() WhatsAppConn {
	a.waMu.Lock()
	defer a.waMu.Unlock()
	return a.wa
}

// linkQuiet: linking is done once the phone's history stops this long.
var linkQuiet = 15 * time.Second

// LinkWhatsApp links this machine to a WhatsApp account (QR code, or a
// pairing code with phone) and records it. Only one process may hold the
// connection: linking fails while another one is online. After the phone
// accepts, it stays online while the phone sends your recent chats
// (progress hears how many so far) and returns once they stop coming,
// or after at most two minutes; whoever goes online next gets the rest.
func (a *App) LinkWhatsApp(ctx context.Context, phone string, show func(qr, code string), progress func(chats, messages, percent int)) error {
	unlock, ok := a.lock("whatsapp")
	if !ok {
		return errors.New("WhatsApp is in use by another narada (the daemon?); stop it, or unlink first")
	}
	defer unlock()
	c, err := a.dialWhatsApp(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if c.Linked() {
		if _, ok := a.Store.WhatsApp(); !ok {
			_ = a.Store.AddWhatsApp(phone, c.Me())
		}
		return errors.New("already linked (narada whatsapp unlink first)")
	}
	var (
		mu               sync.Mutex
		chats, msgs, pct int
		lastHistory      time.Time
		recorded         bool
		historyArrived   = make(chan struct{}, 1)
		wasPhone         = phone
	)
	record := func() {
		if recorded || c.Me() == "" {
			return
		}
		p := wasPhone
		if p == "" {
			p = "+" + strings.SplitN(c.Me(), "@", 2)[0]
		}
		if a.Store.AddWhatsApp(p, c.Me()) == nil {
			recorded = true
		}
	}
	handler := func(e whatsapp.Event) {
		mu.Lock()
		record() // the account first: messages need your own id
		if e.History {
			chats += len(e.Chats)
			msgs += len(e.Messages)
			pct = max(pct, e.Progress)
			lastHistory = time.Now()
			select {
			case historyArrived <- struct{}{}:
			default:
			}
		}
		n, m, p := chats, msgs, pct
		mu.Unlock()
		a.onWhatsApp(ctx, e)
		if e.History && progress != nil {
			progress(n, m, p)
		}
	}
	if err := c.Link(ctx, phone, show, handler); err != nil {
		return err
	}
	mu.Lock()
	record()
	ok = recorded
	mu.Unlock()
	if !ok {
		return errors.New("linked, but WhatsApp didn't say who you are; try again")
	}
	if progress != nil {
		progress(0, 0, 0)
	}
	// the phone sends history in pieces, starting a few seconds in
	deadline := time.After(2 * time.Minute)
	select {
	case <-historyArrived:
	case <-time.After(45 * time.Second):
		return nil // nothing came: it will arrive at the next connect
	case <-deadline:
		return nil
	case <-ctx.Done():
		return nil
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			return nil
		case <-ctx.Done():
			return nil
		case <-tick.C:
			mu.Lock()
			quiet := time.Since(lastHistory) > linkQuiet || pct >= 100 && time.Since(lastHistory) > linkQuiet/5
			mu.Unlock()
			if quiet {
				return nil
			}
		}
	}
}

// UnlinkWhatsApp logs this device out of WhatsApp and forgets its chats.
func (a *App) UnlinkWhatsApp(ctx context.Context) error {
	a.StopWhatsApp()
	unlock, ok := a.lock("whatsapp")
	if !ok {
		return errors.New("WhatsApp is in use by another narada (the daemon?); stop it first")
	}
	defer unlock()
	if c, err := a.dialWhatsApp(ctx); err == nil {
		if c.Linked() {
			if err := c.Logout(ctx); err != nil {
				c.Close()
				return fmt.Errorf("logging out: %w (remove this device on the phone under Linked devices)", err)
			}
		}
		c.Close()
	}
	return a.Store.RemoveAccount(store.WhatsAppAccount)
}

// StartWhatsApp goes online when WhatsApp is linked and no other narada
// process is: the daemon calls it, and the screen and MCP server when no
// daemon runs. It stays online until ctx ends. ok tells whether this
// process now holds the connection.
func (a *App) StartWhatsApp(ctx context.Context) (ok bool, err error) {
	if a.WhatsAppOnline() {
		return true, nil
	}
	if _, linked := a.Store.WhatsApp(); !linked {
		return false, nil
	}
	unlock, got := a.lock("whatsapp")
	if !got {
		return false, nil
	}
	c, err := a.dialWhatsApp(ctx)
	if err != nil {
		unlock()
		return false, err
	}
	if !c.Linked() {
		c.Close()
		unlock()
		_ = a.Store.SetError(store.WhatsAppAccount, "not linked: run `narada whatsapp link`")
		return false, nil
	}
	a.waMu.Lock()
	a.wa, a.waUnlock = c, unlock
	a.waReady, a.waReadyOnce = make(chan struct{}), &sync.Once{}
	a.waMu.Unlock()
	if err := c.Connect(func(e whatsapp.Event) { a.onWhatsApp(ctx, e) }); err != nil {
		a.StopWhatsApp()
		_ = a.Store.SetError(store.WhatsAppAccount, err.Error())
		return false, err
	}
	go func() {
		<-ctx.Done()
		a.StopWhatsApp()
	}()
	return true, nil
}

// StopWhatsApp goes offline and lets another process (or a later
// StartWhatsApp) take WhatsApp.
func (a *App) StopWhatsApp() {
	a.waMu.Lock()
	c, unlock := a.wa, a.waUnlock
	a.wa, a.waUnlock = nil, nil
	a.waMu.Unlock()
	if c != nil {
		c.Close()
	}
	if unlock != nil {
		unlock()
	}
}

// onWhatsApp keeps the local copy up to date with what WhatsApp says.
func (a *App) onWhatsApp(ctx context.Context, e whatsapp.Event) {
	const acc = store.WhatsAppAccount
	wa, _ := a.Store.WhatsApp()
	switch {
	case e.State != "":
		switch e.State {
		case "connected":
			_ = a.Store.RepairWhatsAppNames()
			a.waMu.Lock()
			if a.waReadyOnce != nil {
				a.waReadyOnce.Do(func() { close(a.waReady) })
			}
			a.waMu.Unlock()
			_ = a.Store.SetChatSynced(acc, "")
			if wa.LastError == "" || !strings.HasPrefix(wa.LastError, "WhatsApp: ") {
				_ = a.Store.SetError(acc, "")
			}
		case "logged-out", "replaced", "banned":
			_ = a.Store.SetError(acc, "WhatsApp: "+e.Err.Error())
			Notify("narada: WhatsApp went offline", e.Err.Error())
			go a.StopWhatsApp()
		}
	case e.Contacts != nil:
		sps, _ := a.Store.Spaces([]string{acc}, nil)
		have := map[string]store.Space{}
		for _, sp := range sps {
			have[sp.Name] = sp
		}
		for jid, name := range e.Contacts {
			if name == "" || whatsapp.IsGroup(jid) {
				continue
			}
			_ = a.Store.PutUser(acc, jid, name, phoneOf(jid))
			if sp, ok := have[jid]; ok && (sp.DisplayName == "" || sp.DisplayName == phoneOf(jid)) {
				_ = a.Store.RenameSpace(acc, jid, name)
			}
		}
	case len(e.Chats) > 0:
		for _, ch := range e.Chats {
			_ = a.Store.PutSpace(store.Space{Account: acc, Name: ch.JID, DisplayName: a.waChatName(ch.JID, ch.Name), Type: waType(ch.JID),
				LastActive: ch.Last.UnixMilli()})
			if e.History {
				a.waUnreadMu.Lock()
				if a.waUnread == nil {
					a.waUnread = map[string]int{}
				}
				a.waUnread[ch.JID] = ch.Unread
				a.waUnreadMu.Unlock()
			}
		}
	case e.Read != "":
		_ = a.Store.SetSpaceRead(acc, e.Read, e.ReadAt.UnixMilli())
	case e.Ticks != nil:
		names := make([]string, len(e.Ticks.IDs))
		for i, id := range e.Ticks.IDs {
			names[i] = waName(e.Ticks.Chat, id)
		}
		_ = a.Store.RaiseTicks(acc, names, e.Ticks.Level)
	case len(e.Messages) > 0:
		var put []store.ChatMessage
		touched := map[string]int64{}
		self := map[string]string{} // the name a stranger gave themselves, for their chat's title
		for _, m := range e.Messages {
			name := waName(m.Chat, m.ID)
			switch {
			case m.Revoke:
				_ = a.Store.DeleteChatMessage(acc, name)
				continue
			case m.Edit && m.Text != "":
				if _, err := a.Store.ChatMessage(acc, name); err == nil {
					_ = a.Store.EditChatText(acc, name, m.Text)
					continue
				}
			}
			cm := store.ChatMessage{Account: acc, Name: name, Space: m.Chat, Sender: m.Sender, SenderName: a.waPerson(m.Chat, m.Sender, m.SenderName),
				Text: m.Text, Created: m.Time.UnixMilli(), Edited: m.Edit}
			if m.FromMe {
				cm.Sender, cm.SenderName = wa.ChatUser, "You"
				// sent from the phone: the server has it at least
				cm.Ticks = max(m.Ticks, whatsapp.TickSent)
			}
			if m.Quoted != "" {
				cm.Quoted = waName(m.Chat, m.Quoted)
			}
			if f, ok := waFile(m.Media); ok {
				cm.Files = []store.ChatFile{f}
				if !e.History {
					defer func(name string, f store.ChatFile) { go a.autoFetch(ctx, name, f) }(name, f)
				}
			}
			put = append(put, cm)
			touched[m.Chat] = max(touched[m.Chat], cm.Created)
			if !m.FromMe && m.Sender == m.Chat && m.SenderName != "" {
				self[m.Chat] = "~" + m.SenderName
			}
			if !e.History && !m.FromMe && a.Config.Notify && a.Store.FirstNotify(acc, name) {
				title := a.waChatName(m.Chat, "")
				if whatsapp.IsGroup(m.Chat) {
					title = cm.SenderName + " in " + title
				}
				Notify("WhatsApp: "+title, m.Text)
			}
		}
		for chat, last := range touched {
			if _, err := a.Store.Space(acc, chat); errors.Is(err, store.ErrNotFound) {
				_ = a.Store.PutSpace(store.Space{Account: acc, Name: chat, DisplayName: a.waChatName(chat, self[chat]), Type: waType(chat), LastActive: last})
				if whatsapp.IsGroup(chat) {
					go a.waGroupName(ctx, chat)
				}
				if e.History {
					continue
				}
				// a chat that starts now: what came before isn't news
				_ = a.Store.SetSpaceRead(acc, chat, last-1)
			}
			_ = a.Store.PutSpace(store.Space{Account: acc, Name: chat, Type: waType(chat), LastActive: last})
		}
		_ = a.Store.PutChatMessages(put)
		if e.History {
			a.waUnreadMu.Lock()
			for chat, n := range a.waUnread {
				if _, ok := touched[chat]; ok {
					_ = a.Store.SetUnreadCount(acc, chat, n)
				}
			}
			a.waUnreadMu.Unlock()
		}
	}
}

// waGroupName asks WhatsApp for a group's name, once a day at most.
func (a *App) waGroupName(ctx context.Context, chat string) {
	c := a.whatsApp()
	if c == nil || !a.Store.TryAgain(store.WhatsAppAccount, "group:"+chat, 24*time.Hour) {
		return
	}
	_ = a.Store.Tried(store.WhatsAppAccount, "group:"+chat)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if name, err := c.GroupName(ctx, chat); err == nil && name != "" {
		_ = a.Store.RenameSpace(store.WhatsAppAccount, chat, name)
	}
}

// waName is a WhatsApp message's name in the local copy: WhatsApp's ids
// are only unique within a chat.
func waName(chat, id string) string { return chat + "/" + id }

func waType(jid string) string {
	if whatsapp.IsGroup(jid) {
		return "GROUP_CHAT"
	}
	return "DIRECT_MESSAGE"
}

// phoneOf: "+919876543210" for a person's JID ("" for others).
func phoneOf(jid string) string {
	user, server, _ := strings.Cut(jid, "@")
	if server != "s.whatsapp.net" {
		return ""
	}
	return "+" + user
}

// waChatName: the name to show for a chat: your contact's, else
// WhatsApp's, else the phone number.
// A name starting with ~ is the one people gave themselves (shown when
// they aren't in your contacts, as WhatsApp does).
func (a *App) waChatName(jid, given string) string {
	if n, ok := whatsapp.Known[jid]; ok {
		return n
	}
	if !whatsapp.IsGroup(jid) {
		if name, _, ok := a.Store.User(store.WhatsAppAccount, jid); ok && name != "" && (given == "" || !strings.HasPrefix(name, "~")) {
			return name
		}
	}
	return firstNonEmpty(given, phoneOf(jid))
}

// waPerson names a message's sender: your contact name; in a one-to-one
// chat the chat's own name (it is them); else the name they gave
// themselves, else their number.
func (a *App) waPerson(chat, jid, pushName string) string {
	name, _, _ := a.Store.User(store.WhatsAppAccount, jid)
	if name != "" && !strings.HasPrefix(name, "~") {
		return name
	}
	if jid == chat {
		if sp, err := a.Store.Space(store.WhatsAppAccount, chat); err == nil && sp.DisplayName != "" && !strings.HasPrefix(sp.DisplayName, "+") {
			return sp.DisplayName
		}
	}
	if pushName != "" {
		return "~" + pushName
	}
	return firstNonEmpty(name, phoneOf(jid), jid)
}

// markWhatsAppRead sends read receipts for a chat's unread messages
// (when this process is online; the local copy is marked either way).
func (a *App) markWhatsAppRead(ctx context.Context, space string) error {
	c := a.whatsApp()
	if c == nil {
		return nil
	}
	unread, err := a.Store.UnreadChatMessages(store.WhatsAppAccount, space)
	if err != nil || len(unread) == 0 {
		return err
	}
	var msgs []whatsapp.Message
	for _, m := range unread {
		_, id, _ := strings.Cut(strings.TrimPrefix(m.Name, space), "/")
		msgs = append(msgs, whatsapp.Message{ID: id, Sender: m.Sender})
	}
	return c.MarkRead(ctx, space, msgs)
}

// deliverWhatsApp sends one queued WhatsApp message.
func (a *App) deliverWhatsApp(ctx context.Context, it store.OutboxItem) (string, error) {
	c := a.whatsApp()
	if c == nil {
		return "", errors.New("WhatsApp is offline")
	}
	a.waMu.Lock()
	ready := a.waReady
	a.waMu.Unlock()
	select {
	case <-ready:
	case <-time.After(30 * time.Second):
		return "", errors.New("WhatsApp didn't connect (is the phone online?)")
	case <-ctx.Done():
		return "", ctx.Err()
	}
	out := it.Out
	var quote *whatsapp.Message
	if out.Quote != "" {
		if q, err := a.Store.ChatMessage(store.WhatsAppAccount, out.Quote); err == nil {
			_, id, _ := strings.Cut(strings.TrimPrefix(q.Name, q.Space), "/")
			quote = &whatsapp.Message{ID: id, Sender: q.Sender, Text: q.Text}
		}
	}
	wa, _ := a.Store.WhatsApp()
	var name string
	keep := func(id, text string, files []store.ChatFile) {
		now := time.Now().UnixMilli()
		name = waName(out.Space, id)
		_ = a.Store.PutSpace(store.Space{Account: store.WhatsAppAccount, Name: out.Space, DisplayName: a.waChatName(out.Space, out.SpaceName),
			Type: waType(out.Space), LastActive: now})
		_ = a.Store.PutChatMessages([]store.ChatMessage{{Account: store.WhatsAppAccount, Name: name, Space: out.Space, Sender: wa.ChatUser,
			SenderName: "You", Text: text, Created: now, Quoted: out.Quote, Files: files, Ticks: whatsapp.TickSent}})
		_ = a.Store.SetSpaceRead(store.WhatsAppAccount, out.Space, time.Now().UnixMilli())
	}
	if len(out.Files) == 0 {
		id, err := c.Send(ctx, out.Space, out.Body, quote)
		if err != nil {
			return "", err
		}
		keep(id, out.Body, nil)
		return name, nil
	}
	// each file is its own WhatsApp message, and is kept as one
	for i, f := range out.Files {
		// the text goes as the first file's caption, like WhatsApp does
		caption := ""
		if i == 0 {
			caption = out.Body
		}
		id, sent, err := c.SendFile(ctx, out.Space, f.Path, caption, quote)
		if err != nil {
			if i > 0 {
				return name, fmt.Errorf("%d of %d files went out, then: %w", i, len(out.Files), err)
			}
			return "", err
		}
		cf, ok := waFile(sent)
		if !ok {
			keep(id, caption, nil)
			continue
		}
		cf.Name = firstNonEmpty(f.Name, cf.Name)
		// what you sent shows at once: it is its own cached copy
		if data, rerr := os.ReadFile(f.Path); rerr == nil && sent.Kind != "gif" {
			_ = writeCache(a.mediaPath(store.WhatsAppAccount+"/"+waName(out.Space, id)+"0", cf.Name), data)
		}
		keep(id, whatsapp.MediaLabel(cf.Kind, cf.Name, caption), []store.ChatFile{cf})
		quote = nil
	}
	return name, nil
}

// WhatsAppChat finds or starts the chat for to: a chat id, a phone
// number, or (part of) a contact's or chat's name.
func (a *App) WhatsAppChat(ctx context.Context, to string) (store.Space, error) {
	const acc = store.WhatsAppAccount
	if _, ok := a.Store.WhatsApp(); !ok {
		return store.Space{}, errors.New("WhatsApp is not linked: run `narada whatsapp link`")
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return store.Space{}, errors.New("who to? a phone number, a contact or a chat")
	}
	if strings.Contains(to, "@") {
		if sp, err := a.Store.Space(acc, to); err == nil {
			return sp, nil
		}
	}
	if d := whatsapp.Digits(to); len(d) >= 7 && len(d) >= len(strings.Join(strings.Fields(to), ""))-4 {
		jid := whatsapp.PhoneJID(d)
		if c := a.whatsApp(); c != nil {
			rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			j, err := c.Resolve(rctx, d)
			cancel()
			if err != nil {
				return store.Space{}, err
			}
			jid = j
		}
		if sp, err := a.Store.Space(acc, jid); err == nil {
			return sp, nil
		}
		sp := store.Space{Account: acc, Name: jid, DisplayName: a.waChatName(jid, ""), Type: "DIRECT_MESSAGE", LastActive: time.Now().UnixMilli()}
		if err := a.Store.PutSpace(sp); err != nil {
			return sp, err
		}
		return a.Store.Space(acc, jid)
	}
	// a name: a chat's title first, then a contact
	sps, _ := a.Store.Spaces([]string{acc}, nil)
	low := strings.ToLower(to)
	var found []store.Space
	for _, sp := range sps {
		if t := strings.ToLower(firstNonEmpty(sp.Alias, sp.DisplayName)); t == low {
			return sp, nil
		} else if strings.Contains(t, low) {
			found = append(found, sp)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	if len(found) > 1 {
		var names []string
		for _, sp := range found[:min(len(found), 6)] {
			names = append(names, firstNonEmpty(sp.Alias, sp.DisplayName))
		}
		return store.Space{}, fmt.Errorf("%q matches several chats: %s", to, strings.Join(names, ", "))
	}
	people, _ := a.Store.ChatUsers(acc, to, 6)
	switch len(people) {
	case 0:
		return store.Space{}, fmt.Errorf("no WhatsApp chat or contact matches %q (try a phone number)", to)
	case 1:
		sp := store.Space{Account: acc, Name: people[0].ID, DisplayName: people[0].Name, Type: "DIRECT_MESSAGE", LastActive: time.Now().UnixMilli()}
		if err := a.Store.PutSpace(sp); err != nil {
			return sp, err
		}
		return a.Store.Space(acc, sp.Name)
	}
	var names []string
	for _, p := range people {
		names = append(names, p.Name+" ("+p.Email+")")
	}
	return store.Space{}, fmt.Errorf("%q matches several contacts: %s", to, strings.Join(names, ", "))
}

// WhatsAppLink is WhatsApp's click-to-chat link: it opens the chat with
// the text typed in, for you to send. It works without linking.
func WhatsAppLink(phone, text string) string {
	u := "https://wa.me/" + whatsapp.Digits(phone)
	if text != "" {
		u += "?text=" + url.QueryEscape(text)
	}
	return u
}

// OpenURL opens a link in the default browser (or app).
func OpenURL(u string) error {
	for _, c := range []string{"xdg-open", "open"} {
		if _, err := exec.LookPath(c); err == nil {
			return exec.Command(c, u).Start()
		}
	}
	return errors.New("no xdg-open to open " + u)
}

// waFile is a message's media as a chat file: its kind, size, preview,
// and Ref to download it with.
func waFile(m *whatsapp.Media) (store.ChatFile, bool) {
	if m == nil || len(m.Raw) == 0 {
		return store.ChatFile{}, false
	}
	f := store.ChatFile{Name: m.Name, Type: m.Mime, Kind: m.Kind, Size: m.Size, Ref: waRef + base64.StdEncoding.EncodeToString(m.Raw)}
	if len(m.Thumb) > 0 {
		f.Thumb = base64.StdEncoding.EncodeToString(m.Thumb)
	}
	return f, true
}
