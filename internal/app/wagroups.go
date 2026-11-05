package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

// Groups: made, named and run through the process holding WhatsApp
// (ADR 7); each waits for WhatsApp's answer.

const groupWait = 45 * time.Second

func (a *App) groupDo(ctx context.Context, action, chat, arg string) (string, error) {
	if chat != "" && !whatsapp.IsGroup(chat) {
		return "", errors.New("that's not a group")
	}
	return a.WhatsAppDo(ctx, action, chat, arg, groupWait)
}

// NewWhatsAppGroup makes a group with these people (chat ids) and
// returns it.
func (a *App) NewWhatsAppGroup(ctx context.Context, name string, people []string) (store.Space, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(people) == 0 {
		return store.Space{}, errors.New("a group needs a name and at least one person")
	}
	b, _ := json.Marshal(whatsapp.NewGroup{Name: name, People: people})
	jid, err := a.groupDo(ctx, whatsapp.DoGroupNew, "", string(b))
	if err != nil {
		return store.Space{}, err
	}
	return a.Store.Space(store.WhatsAppAccount, jid)
}

// RenameWhatsAppGroup names a group for everyone.
func (a *App) RenameWhatsAppGroup(ctx context.Context, group, name string) error {
	_, err := a.groupDo(ctx, whatsapp.DoGroupName, group, strings.TrimSpace(name))
	return err
}

// DescribeWhatsAppGroup sets a group's description.
func (a *App) DescribeWhatsAppGroup(ctx context.Context, group, about string) error {
	_, err := a.groupDo(ctx, whatsapp.DoGroupAbout, group, about)
	return err
}

// ChangeWhatsAppGroup adds, removes, promotes (to admin) or demotes people.
func (a *App) ChangeWhatsAppGroup(ctx context.Context, group, change string, people []string) error {
	action := map[string]string{"add": whatsapp.DoGroupAdd, "remove": whatsapp.DoGroupKick, "promote": whatsapp.DoGroupAdmin, "demote": whatsapp.DoGroupUser}[change]
	if action == "" {
		return errors.New("add, remove, promote or demote")
	}
	if len(people) == 0 {
		return errors.New("who?")
	}
	if _, err := a.groupDo(ctx, action, group, strings.Join(people, ",")); err != nil {
		return err
	}
	if info, err := a.WhatsAppGroupInfo(ctx, group); err == nil { // who's in it now
		var ids []string
		for _, p := range info.People {
			ids = append(ids, p.JID)
		}
		_ = a.Store.SetWhatsAppMembers(group, ids)
	}
	return nil
}

// WhatsAppGroupLink is the group's invite link; reset makes a new one
// (the old one stops working).
func (a *App) WhatsAppGroupLink(ctx context.Context, group string, reset bool) (string, error) {
	arg := ""
	if reset {
		arg = "reset"
	}
	return a.groupDo(ctx, whatsapp.DoGroupLink, group, arg)
}

// JoinWhatsAppGroup joins a group by its invite link.
func (a *App) JoinWhatsAppGroup(ctx context.Context, link string) (store.Space, error) {
	if !strings.Contains(link, "chat.whatsapp.com/") && strings.Contains(link, "/") {
		return store.Space{}, errors.New("that's not a WhatsApp invite link (https://chat.whatsapp.com/…)")
	}
	jid, err := a.groupDo(ctx, whatsapp.DoGroupJoin, "", strings.TrimSpace(link))
	if err != nil {
		return store.Space{}, err
	}
	return a.Store.Space(store.WhatsAppAccount, jid)
}

// LeaveWhatsAppGroup leaves a group (it moves to Archived).
func (a *App) LeaveWhatsAppGroup(ctx context.Context, group string) error {
	_, err := a.groupDo(ctx, whatsapp.DoGroupLeave, group, "")
	return err
}

// WhatsAppGroupInfo asks WhatsApp who's in a group, its admins and description.
func (a *App) WhatsAppGroupInfo(ctx context.Context, group string) (whatsapp.GroupInfo, error) {
	var g whatsapp.GroupInfo
	res, err := a.groupDo(ctx, whatsapp.DoGroupInfo, group, "")
	if err != nil {
		return g, err
	}
	return g, json.Unmarshal([]byte(res), &g)
}

// WhatsAppIDs turns names and numbers into the chat ids of people.
func (a *App) WhatsAppIDs(ctx context.Context, who []string) ([]string, error) {
	var out []string
	for _, w := range who {
		sp, err := a.WhatsAppChat(ctx, w)
		if err != nil {
			return nil, err
		}
		if whatsapp.IsGroup(sp.Name) {
			return nil, errors.New(w + " is a group, not a person")
		}
		out = append(out, sp.Name)
	}
	return out, nil
}
