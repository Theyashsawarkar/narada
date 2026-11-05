package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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
)

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
		return out.Target, a.Store.EditChatText(store.WhatsAppAccount, out.Target, out.Body)
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
