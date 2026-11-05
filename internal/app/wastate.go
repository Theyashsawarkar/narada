package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

// Chat state, presence and groups: only the process holding WhatsApp can
// do them, so they wait in a queue for it, as reactions do (ADR 7). Your
// own view (archived, pinned, muted, starred, blocked) changes here at
// once; WhatsApp and your other devices follow.

// fireAndForget: actions nobody waits on, and that aren't worth doing late.
var fireAndForget = map[string]bool{whatsapp.DoTyping: true, whatsapp.DoOnline: true, whatsapp.DoWatch: true}

// WhatsAppDo queues an action for the process holding WhatsApp and, with
// wait > 0, waits that long for it to be done, returning its result.
func (a *App) WhatsAppDo(ctx context.Context, action, chat, arg string, wait time.Duration) (string, error) {
	if _, ok := a.Store.WhatsApp(); !ok {
		return "", errors.New("WhatsApp is not linked: run `narada whatsapp link`")
	}
	if err := a.waLocal(action, chat, arg); err != nil {
		return "", err
	}
	id, err := a.Store.AddWhatsAppAction(action, chat, arg)
	if err != nil {
		return "", err
	}
	if a.whatsApp() != nil {
		a.waMu.Lock()
		ready := a.waReady
		a.waMu.Unlock()
		select { // a command that just went online is still connecting
		case <-ready:
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
		a.flushWhatsAppActions(ctx)
	}
	if wait <= 0 {
		return "", nil
	}
	deadline := time.After(wait)
	for {
		a.flushWhatsAppActions(ctx) // when this process holds WhatsApp (once it's connected)
		if it, err := a.Store.WhatsAppActionByID(id); err == nil && it.Done {
			if it.Error != "" {
				return "", errors.New(it.Error)
			}
			return it.Result, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", errors.New("queued, but the narada holding WhatsApp (the daemon?) hasn't done it yet; it will when it's online")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// waLocal shows the change here at once.
func (a *App) waLocal(action, chat, arg string) error {
	const acc = store.WhatsAppAccount
	on := arg != ""
	switch action {
	case whatsapp.DoArchive:
		if on {
			_ = a.Store.SetPinned(acc, chat, false) // archiving unpins, as on the phone
		}
		return a.Store.SetArchived(acc, chat, on)
	case whatsapp.DoPin:
		if on {
			_ = a.Store.SetArchived(acc, chat, false)
		}
		return a.Store.SetPinned(acc, chat, on)
	case whatsapp.DoMute:
		var until int64
		if on {
			if _, err := fmt.Sscan(arg, &until); err != nil {
				return fmt.Errorf("mute until %q?", arg)
			}
		}
		return a.Store.SetMuted(acc, chat, until)
	case whatsapp.DoBlock:
		return a.Store.SetBlocked(acc, chat, on)
	case whatsapp.DoStar:
		f := strings.Fields(arg)
		if len(f) == 4 {
			return a.Store.SetStarred(acc, waName(chat, f[0]), f[3] == "1")
		}
	}
	return nil
}

// flushWhatsAppActions does the queued actions, when this process holds
// WhatsApp. Each is done once: a failure is reported to whoever waits.
func (a *App) flushWhatsAppActions(ctx context.Context) {
	c := a.whatsApp()
	if c == nil {
		return
	}
	a.waMu.Lock()
	ready := a.waReady
	a.waMu.Unlock()
	select {
	case <-ready:
	default:
		return
	}
	acts, err := a.Store.PendingWhatsAppActions(waReadsKept)
	if err != nil {
		return
	}
	for _, act := range acts {
		if !a.Store.ClaimWhatsAppAction(act.ID) {
			continue
		}
		if fireAndForget[act.Action] && time.Since(time.UnixMilli(act.Created)) > 30*time.Second {
			_ = a.Store.FinishWhatsAppAction(act.ID, "", "too late")
			continue
		}
		actx, cancel := context.WithTimeout(ctx, 30*time.Second)
		res, err := c.Do(actx, act.Action, act.Chat, act.Arg)
		cancel()
		msg := ""
		if err != nil {
			msg = err.Error()
			if !fireAndForget[act.Action] {
				log.Printf("whatsapp: %s %s: %v", act.Action, act.Chat, err)
			}
		}
		_ = a.Store.FinishWhatsAppAction(act.ID, res, msg)
		a.afterWhatsAppAction(act, res, err)
	}
}

// afterWhatsAppAction keeps the local copy in step with what was done.
func (a *App) afterWhatsAppAction(act store.WhatsAppAction, res string, err error) {
	if err != nil {
		return
	}
	const acc = store.WhatsAppAccount
	switch act.Action {
	case whatsapp.DoGroupName:
		_ = a.Store.RenameSpace(acc, act.Chat, act.Arg)
	case whatsapp.DoGroupNew, whatsapp.DoGroupJoin:
		if res != "" {
			name := ""
			if act.Action == whatsapp.DoGroupNew {
				var g whatsapp.NewGroup
				if json.Unmarshal([]byte(act.Arg), &g) == nil {
					name = g.Name
				}
			}
			_ = a.Store.PutSpace(store.Space{Account: acc, Name: res, DisplayName: name, Type: "GROUP_CHAT", LastActive: time.Now().UnixMilli()})
			if name == "" {
				go a.waGroupName(context.Background(), res)
			}
		}
	case whatsapp.DoGroupLeave:
		_ = a.Store.SetArchived(acc, act.Chat, true) // you left: out of the list, as the phone does
	}
}

// onWhatsAppState keeps chat state and presence from other devices.
func (a *App) onWhatsAppState(e whatsapp.Event) {
	const acc = store.WhatsAppAccount
	if cs := e.ChatState; cs != nil {
		if cs.Archived != nil {
			_ = a.Store.SetArchived(acc, cs.Chat, *cs.Archived)
		}
		if cs.Pinned != nil {
			_ = a.Store.SetPinned(acc, cs.Chat, *cs.Pinned)
		}
		if cs.MutedUntil != nil {
			_ = a.Store.SetMuted(acc, cs.Chat, *cs.MutedUntil)
		}
		if cs.Unread != nil && *cs.Unread {
			if last := a.Store.LastOtherMessage(acc, cs.Chat); last > 0 {
				_ = a.Store.ForceSpaceRead(acc, cs.Chat, last-1)
			}
		}
	}
	if st := e.Star; st != nil {
		_ = a.Store.SetStarred(acc, waName(st.Chat, st.ID), st.On)
	}
	if p := e.Presence; p != nil {
		if p.Chat != "" {
			_ = a.Store.SetTyping(p.Who, p.Chat, p.Typing)
		} else {
			var seen int64
			if !p.LastSeen.IsZero() {
				seen = p.LastSeen.UnixMilli()
			}
			_ = a.Store.SetPresence(p.Who, p.Online, seen)
		}
	}
}

// ---------------------------------------------------- the actions, named

// ArchiveWhatsApp archives a chat (out of the list, into Archived), or brings it back.
func (a *App) ArchiveWhatsApp(ctx context.Context, chat string, on bool) error {
	_, err := a.WhatsAppDo(ctx, whatsapp.DoArchive, chat, flag(on), 0)
	return err
}

// PinWhatsApp pins a chat on top of the list, everywhere (WhatsApp allows three).
func (a *App) PinWhatsApp(ctx context.Context, chat string, on bool) error {
	if on {
		sps, _ := a.Store.Spaces([]string{store.WhatsAppAccount}, nil)
		n := 0
		for _, sp := range sps {
			if sp.Pinned && sp.Name != chat {
				n++
			}
		}
		if n >= 3 {
			return errors.New("WhatsApp pins three chats at most: unpin one first")
		}
	}
	_, err := a.WhatsAppDo(ctx, whatsapp.DoPin, chat, flag(on), 0)
	return err
}

// MuteWhatsApp silences a chat's notifications for d (0: for ever), or
// turns them back on (unmute).
func (a *App) MuteWhatsApp(ctx context.Context, chat string, d time.Duration, unmute bool) error {
	arg := ""
	if !unmute {
		arg = "-1"
		if d > 0 {
			arg = fmt.Sprint(time.Now().Add(d).UnixMilli())
		}
	}
	_, err := a.WhatsAppDo(ctx, whatsapp.DoMute, chat, arg, 0)
	return err
}

// StarWhatsApp stars a message (or unstars it).
func (a *App) StarWhatsApp(ctx context.Context, msg string, on bool) error {
	m, id, err := a.waMsg(msg)
	if err != nil {
		return err
	}
	_, err = a.WhatsAppDo(ctx, whatsapp.DoStar, m.Space, fmt.Sprintf("%s %s %s %s", id, firstNonEmpty(m.Sender, "0"), flag01(a.waMine(m)), flag01(on)), 0)
	return err
}

// BlockWhatsApp blocks a person (or unblocks them); it waits for WhatsApp.
func (a *App) BlockWhatsApp(ctx context.Context, chat string, on bool) error {
	if whatsapp.IsGroup(chat) {
		return errors.New("block a person, not a group (leave the group instead)")
	}
	_, err := a.WhatsAppDo(ctx, whatsapp.DoBlock, chat, flag(on), 30*time.Second)
	if err != nil {
		_ = a.Store.SetBlocked(store.WhatsAppAccount, chat, !on)
	}
	return err
}

func flag(on bool) string {
	if on {
		return "1"
	}
	return ""
}

func flag01(on bool) string {
	if on {
		return "1"
	}
	return "0"
}
