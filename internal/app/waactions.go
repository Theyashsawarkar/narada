package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

// What a WhatsApp outbox item does besides sending (store.Outgoing.Action).
// Anything that changes what others see in a chat goes through the outbox
// like a send: your undo window, an agent's approval, the hourly cap, and
// delivery by whichever process holds WhatsApp (ADR 7).
const (
	ActEdit    = "edit"
	ActDelete  = "delete"
	ActForward = "forward"
	ActStatus  = "status" // Body and Files are your new status, for your contacts
	ActPin     = "pin"    // pin Target in its chat for everyone, Secs long (0: unpin)
)

// How long WhatsApp lets a message stay pinned.
const (
	PinDay   = 86400
	PinWeek  = 7 * 86400
	PinMonth = 30 * 86400
)

// PinWhatsAppMessage pins a message in its chat for everyone for secs (a
// day, a week or 30 days), or unpins it (secs 0).
func (a *App) PinWhatsAppMessage(msg string, secs int, origin string) (store.OutboxItem, error) {
	m, _, err := a.waMsg(msg)
	if err != nil {
		return store.OutboxItem{}, err
	}
	switch secs {
	case 0, PinDay, PinWeek, PinMonth:
	default:
		return store.OutboxItem{}, errors.New("a message stays pinned for 24h, 7d or 30d")
	}
	return a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: m.Space, SpaceName: a.waChatName(m.Space, ""), Action: ActPin, Target: msg, PinSecs: secs, Body: m.Text}, origin)
}

// PostWhatsAppStatus posts a status your contacts see for a day, with a
// photo or video when given. Only you: agents can't, and nothing else
// sends to the status chat (Queue refuses).
func (a *App) PostWhatsAppStatus(text string, files []store.File) (store.OutboxItem, error) {
	return a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: whatsapp.StatusChat, SpaceName: "Status", Action: ActStatus, Body: text, Files: files}, OriginYou)
}

// waMsg finds a WhatsApp message here, with its id within its chat.
func (a *App) waMsg(name string) (store.ChatMessage, string, error) {
	if store.IsPending(name) {
		return store.ChatMessage{}, "", errors.New("wait until it has gone out")
	}
	m, err := a.Store.ChatMessage(store.WhatsAppAccount, name)
	if err != nil {
		return m, "", fmt.Errorf("no WhatsApp message %s here (whatsapp read shows the ids)", name)
	}
	_, id, _ := strings.Cut(strings.TrimPrefix(name, m.Space), "/")
	return m, id, nil
}

func (a *App) waMine(m store.ChatMessage) bool {
	wa, _ := a.Store.WhatsApp()
	return m.Sender == wa.ChatUser || m.SenderName == "You"
}

// EditWhatsApp changes the text of a message you sent, as WhatsApp allows
// within 15 minutes of sending it.
func (a *App) EditWhatsApp(msg, text, origin string) (store.OutboxItem, error) {
	m, _, err := a.waMsg(msg)
	if err != nil {
		return store.OutboxItem{}, err
	}
	switch {
	case !a.waMine(m):
		return store.OutboxItem{}, errors.New("only your own messages can be edited")
	case len(m.Files) > 0:
		return store.OutboxItem{}, errors.New("WhatsApp edits text messages only")
	case time.Since(time.UnixMilli(m.Created)) > whatsapp.EditWindow*time.Second:
		return store.OutboxItem{}, errors.New("too late: WhatsApp lets you edit a message for 15 minutes")
	case strings.TrimSpace(text) == "":
		return store.OutboxItem{}, errors.New("the new text is empty (delete it instead)")
	}
	return a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: m.Space, SpaceName: a.waChatName(m.Space, ""),
		Action: ActEdit, Target: msg, Body: text}, origin)
}

// DeleteWhatsApp deletes a message: for everyone (yours, within about two
// and a half days, as WhatsApp allows), or for you only, which just
// removes it here.
func (a *App) DeleteWhatsApp(msg string, forEveryone bool, origin string) (store.OutboxItem, error) {
	if id, ok := store.PendingOutboxID(msg); ok { // not sent yet: stop it
		_ = a.Cancel(id)
		return store.OutboxItem{}, a.Store.DeleteChatMessage(store.WhatsAppAccount, msg)
	}
	m, _, err := a.waMsg(msg)
	if err != nil {
		return store.OutboxItem{}, err
	}
	if !forEveryone {
		return store.OutboxItem{}, a.Store.DeleteChatMessage(store.WhatsAppAccount, msg)
	}
	switch {
	case !a.waMine(m):
		return store.OutboxItem{}, errors.New("only your own messages can be deleted for everyone (others' only for you)")
	case time.Since(time.UnixMilli(m.Created)) > whatsapp.RevokeWindow*time.Second:
		return store.OutboxItem{}, errors.New("too late to delete it for everyone (WhatsApp allows about two and a half days); delete it for you instead")
	}
	return a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: m.Space, SpaceName: a.waChatName(m.Space, ""),
		Action: ActDelete, Target: msg, Body: m.Text}, origin)
}

// ForwardWhatsApp forwards a message (its text, or its photo, video, voice
// note or document) to another chat.
func (a *App) ForwardWhatsApp(msg string, to store.Space, origin string) (store.OutboxItem, error) {
	m, _, err := a.waMsg(msg)
	if err != nil {
		return store.OutboxItem{}, err
	}
	if len(m.Files) == 0 && strings.TrimSpace(m.Text) == "" {
		return store.OutboxItem{}, errors.New("nothing to forward")
	}
	return a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: to.Name, SpaceName: firstNonEmpty(to.Alias, to.DisplayName),
		Action: ActForward, Target: msg, Body: m.Text}, origin)
}

// deliverWhatsAppAction does an outbox item's Action; the result is the
// message it acted on, or the new one.
func (a *App) deliverWhatsAppAction(ctx context.Context, c WhatsAppConn, it store.OutboxItem, keep func(id, text string, files []store.ChatFile)) (string, error) {
	out := it.Out
	switch out.Action {
	case ActPoll, ActVote:
		return a.deliverPoll(ctx, c, it, keep)
	case ActLocation, ActContact:
		return a.deliverShare(ctx, c, it, keep)
	}
	m, id, err := a.waMsg(out.Target)
	if err != nil && out.Action != ActDelete {
		return "", err
	}
	switch out.Action {
	case ActEdit:
		if err := c.Edit(ctx, m.Space, id, out.Body); err != nil {
			return "", err
		}
		return out.Target, a.Store.EditChatText(store.WhatsAppAccount, out.Target, out.Body, "")
	case ActPin:
		if err := c.PinMessage(ctx, m.Space, m.Sender, id, out.PinSecs > 0, out.PinSecs); err != nil {
			return "", err
		}
		return out.Target, a.Store.SetMessagePinned(store.WhatsAppAccount, out.Target, out.PinSecs > 0)
	case ActDelete:
		if err != nil {
			return out.Target, nil // gone already
		}
		if err := c.Revoke(ctx, m.Space, "", id); err != nil {
			return "", err
		}
		return out.Target, a.Store.DeleteChatMessage(store.WhatsAppAccount, out.Target)
	case ActForward:
		fw := whatsapp.Message{ID: id, Chat: m.Space, Sender: m.Sender, Text: m.Text}
		var files []store.ChatFile
		if len(m.Files) > 0 && strings.HasPrefix(m.Files[0].Ref, waRef) {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(m.Files[0].Ref, waRef))
			if err != nil {
				return "", err
			}
			fw.Media = &whatsapp.Media{Raw: raw}
			files = m.Files[:1]
		}
		nid, err := c.Forward(ctx, out.Space, fw)
		if err != nil {
			return "", err
		}
		keep(nid, m.Text, files)
		return waName(out.Space, nid), nil
	}
	return "", fmt.Errorf("unknown WhatsApp action %q", out.Action)
}

// StatusGroup is one person's statuses that are still up, oldest first.
// Mine marks your own.
type StatusGroup struct {
	Sender, Name string
	Mine         bool
	Items        []store.ChatMessage
}

// Statuses are the statuses up now (a day), one group per person: yours
// first, then whoever posted last.
func (a *App) Statuses() ([]StatusGroup, error) {
	msgs, err := a.Store.ChatMessages(store.WhatsAppAccount, whatsapp.StatusChat, 2000)
	if err != nil {
		return nil, err
	}
	since := time.Now().Add(-whatsapp.StatusLife).UnixMilli()
	var groups []StatusGroup
	at := map[string]int{}
	for _, m := range msgs { // oldest first
		if m.Created < since {
			continue
		}
		i, ok := at[m.Sender]
		if !ok {
			i = len(groups)
			at[m.Sender] = i
			name := m.SenderName
			if m.Mine {
				name = "You"
			}
			groups = append(groups, StatusGroup{Sender: m.Sender, Name: firstNonEmpty(name, phoneOf(m.Sender), m.Sender), Mine: m.Mine})
		}
		groups[i].Items = append(groups[i].Items, m)
	}
	last := func(g StatusGroup) int64 { return g.Items[len(g.Items)-1].Created }
	slices.SortStableFunc(groups, func(x, y StatusGroup) int {
		switch {
		case x.Mine != y.Mine:
			if x.Mine {
				return -1
			}
			return 1
		case last(x) > last(y):
			return -1
		case last(x) < last(y):
			return 1
		}
		return 0
	})
	return groups, nil
}

// statusTarget checks status is someone's status still up and gives the
// chat a reply to it goes in: yours with whoever posted it.
func (a *App) statusTarget(status string) (store.ChatMessage, store.Space, error) {
	m, err := a.Store.ChatMessage(store.WhatsAppAccount, status)
	if err != nil || m.Space != whatsapp.StatusChat {
		return m, store.Space{}, fmt.Errorf("%q isn't a status (see `narada whatsapp status`)", status)
	}
	if m.Mine {
		return m, store.Space{}, errors.New("that's your own status")
	}
	if m.Sender == "" || whatsapp.IsGroup(m.Sender) {
		return m, store.Space{}, errors.New("this status has no sender to answer")
	}
	sp, err := a.Store.Space(store.WhatsAppAccount, m.Sender)
	if err != nil {
		sp = store.Space{Account: store.WhatsAppAccount, Name: m.Sender, DisplayName: a.waChatName(m.Sender, m.SenderName), Type: "DIRECT_MESSAGE"}
	}
	return m, sp, nil
}

// ReplyToStatus answers someone's status, as WhatsApp does: a message in
// your chat with them that quotes the status. Nothing goes to the status
// chat, so nobody else sees it. out carries the text (and an agent's Agent,
// Why, AskFirst); it goes through the outbox like any send.
func (a *App) ReplyToStatus(status string, out store.Outgoing, origin string) (store.OutboxItem, error) {
	_, sp, err := a.statusTarget(status)
	if err != nil {
		return store.OutboxItem{}, err
	}
	if strings.TrimSpace(out.Body) == "" && len(out.Files) == 0 {
		return store.OutboxItem{}, errors.New("the reply is empty")
	}
	out.Space, out.SpaceName, out.Quote = sp.Name, firstNonEmpty(sp.Alias, sp.DisplayName, phoneOf(sp.Name)), status
	return a.Queue(store.WhatsAppAccount, "whatsapp", out, origin)
}

// ReactToStatus is WhatsApp's quick reaction to a status: the emoji alone,
// as a reply quoting it, in your chat with whoever posted it.
func (a *App) ReactToStatus(status, emoji string, out store.Outgoing, origin string) (store.OutboxItem, error) {
	emoji = strings.TrimSpace(emoji)
	if emoji == "" || strings.ContainsAny(emoji, " \n") || len(emoji) > 32 {
		return store.OutboxItem{}, fmt.Errorf("%q isn't one emoji", emoji)
	}
	out.Body, out.Files = emoji, nil
	return a.ReplyToStatus(status, out, origin)
}
