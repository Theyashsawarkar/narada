package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// Actions Do does: chat state, presence and groups (ADR 7). Arg and the
// result are per action, as below.
const (
	DoArchive    = "archive"    // chat; arg "1" archives, "" unarchives
	DoPin        = "pin"        // chat; arg "1" pins, "" unpins
	DoMute       = "mute"       // chat; arg: until (ms; -1 for ever), "" unmutes
	DoUnread     = "unread"     // chat; arg "1" marks it unread, "" read
	DoStar       = "star"       // chat; arg "id sender fromMe(0/1) on(0/1)"
	DoBlock      = "block"      // chat (a person); arg "1" blocks, "" unblocks
	DoTyping     = "typing"     // chat; arg "composing", "recording" or "paused"
	DoOnline     = "online"     // arg "1": you're here (presence available), "" away
	DoWatch      = "watch"      // chat (a person): hear when they're online
	DoDisappear  = "disappear"  // chat; arg: seconds (0 turns it off)
	DoGroupNew   = "group-new"  // arg JSON {"name","people"}; result: the group's id
	DoGroupName  = "group-name" // chat; arg: the new name
	DoGroupAbout = "group-about"
	DoGroupAdd   = "group-add" // chat; arg: people's ids, comma-separated
	DoGroupKick  = "group-remove"
	DoGroupAdmin = "group-promote"
	DoGroupUser  = "group-demote"
	DoGroupLink  = "group-link"  // chat; arg "reset" makes a new one; result: the link
	DoGroupJoin  = "group-join"  // arg: an invite link or code; result: the group's id
	DoGroupLeave = "group-leave" // chat
	DoGroupInfo  = "group-info"  // chat; result: GroupInfo as JSON
)

// GroupInfo is what group-info returns.
type GroupInfo struct {
	JID     string        `json:"id"`
	Name    string        `json:"name"`
	About   string        `json:"about,omitempty"`
	Created time.Time     `json:"created"`
	People  []GroupPerson `json:"people"`
}

type GroupPerson struct {
	JID   string `json:"id"`
	Admin bool   `json:"admin,omitempty"`
	Owner bool   `json:"owner,omitempty"`
}

// NewGroup is group-new's arg.
type NewGroup struct {
	Name   string   `json:"name"`
	People []string `json:"people"`
}

// Do does one action that changes state rather than sending a message.
func (cl *Client) Do(ctx context.Context, action, chat, arg string) (string, error) {
	var jid types.JID
	if chat != "" {
		var err error
		if jid, err = types.ParseJID(chat); err != nil {
			return "", fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
		}
	}
	on := arg != ""
	switch action {
	case DoArchive:
		return "", cl.c.SendAppState(ctx, appstate.BuildArchive(jid, on, time.Time{}, nil))
	case DoPin:
		return "", cl.c.SendAppState(ctx, appstate.BuildPin(jid, on))
	case DoMute:
		if !on {
			return "", cl.c.SendAppState(ctx, appstate.BuildMuteAbs(jid, false, nil))
		}
		until, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return "", err
		}
		return "", cl.c.SendAppState(ctx, appstate.BuildMuteAbs(jid, true, &until))
	case DoUnread:
		return "", cl.c.SendAppState(ctx, appstate.BuildMarkChatAsRead(jid, !on, time.Time{}, nil))
	case DoStar:
		f := strings.Fields(arg)
		if len(f) != 4 {
			return "", errors.New("star needs id, sender, fromMe and on")
		}
		sender, _ := types.ParseJID(f[1])
		return "", cl.c.SendAppState(ctx, appstate.BuildStar(jid, sender, f[0], f[2] == "1", f[3] == "1"))
	case DoBlock:
		act := events.BlocklistChangeActionUnblock
		if on {
			act = events.BlocklistChangeActionBlock
		}
		_, err := cl.c.UpdateBlocklist(ctx, jid, act, "")
		return "", err
	case DoTyping:
		state, media := types.ChatPresenceComposing, types.ChatPresenceMediaText
		switch arg {
		case "paused":
			state = types.ChatPresencePaused
		case "recording":
			media = types.ChatPresenceMediaAudio
		}
		return "", cl.c.SendChatPresence(ctx, jid, state, media)
	case DoOnline:
		p := types.PresenceUnavailable
		if on {
			p = types.PresenceAvailable
		}
		return "", cl.c.SendPresence(ctx, p)
	case DoWatch:
		return "", cl.c.SubscribePresence(ctx, jid)
	case DoDisappear:
		secs, err := strconv.Atoi(arg)
		if err != nil {
			return "", err
		}
		return "", cl.c.SetDisappearingTimer(ctx, jid, time.Duration(secs)*time.Second, time.Now())
	case DoGroupNew:
		var g NewGroup
		if err := json.Unmarshal([]byte(arg), &g); err != nil {
			return "", err
		}
		ps, err := jids(strings.Join(g.People, ","))
		if err != nil {
			return "", err
		}
		info, err := cl.c.CreateGroup(ctx, whatsmeow.ReqCreateGroup{Name: g.Name, Participants: ps})
		if err != nil {
			return "", err
		}
		return info.JID.String(), nil
	case DoGroupName:
		return "", cl.c.SetGroupName(ctx, jid, arg)
	case DoGroupAbout:
		return "", cl.c.SetGroupDescription(ctx, jid, arg)
	case DoGroupAdd, DoGroupKick, DoGroupAdmin, DoGroupUser:
		ps, err := jids(arg)
		if err != nil {
			return "", err
		}
		change := map[string]whatsmeow.ParticipantChange{DoGroupAdd: whatsmeow.ParticipantChangeAdd, DoGroupKick: whatsmeow.ParticipantChangeRemove,
			DoGroupAdmin: whatsmeow.ParticipantChangePromote, DoGroupUser: whatsmeow.ParticipantChangeDemote}[action]
		res, err := cl.c.UpdateGroupParticipants(ctx, jid, ps, change)
		if err != nil {
			return "", err
		}
		var failed []string
		for _, p := range res {
			if p.Error != 0 {
				failed = append(failed, fmt.Sprintf("%s (%d)", p.JID.User, p.Error))
			}
		}
		if len(failed) > 0 {
			return "", fmt.Errorf("WhatsApp refused: %s (403: their privacy settings; 409: already in)", strings.Join(failed, ", "))
		}
		return "", nil
	case DoGroupLink:
		return cl.c.GetGroupInviteLink(ctx, jid, arg == "reset")
	case DoGroupJoin:
		code := arg[strings.LastIndex(arg, "/")+1:]
		j, err := cl.c.JoinGroupWithLink(ctx, code)
		if err != nil {
			return "", err
		}
		return j.String(), nil
	case DoGroupLeave:
		return "", cl.c.LeaveGroup(ctx, jid)
	case DoGroupInfo:
		info, err := cl.c.GetGroupInfo(ctx, jid)
		if err != nil {
			return "", err
		}
		g := GroupInfo{JID: info.JID.String(), Name: info.Name, About: info.Topic, Created: info.GroupCreated}
		for _, p := range info.Participants {
			j := p.JID
			if !p.PhoneNumber.IsEmpty() {
				j = p.PhoneNumber
			}
			g.People = append(g.People, GroupPerson{JID: cl.chatJID(ctx, j), Admin: p.IsAdmin || p.IsSuperAdmin, Owner: p.IsSuperAdmin})
		}
		b, _ := json.Marshal(g)
		return string(b), nil
	}
	return "", fmt.Errorf("unknown WhatsApp action %q", action)
}

func jids(list string) ([]types.JID, error) {
	var out []types.JID
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		j, err := types.ParseJID(s)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	if len(out) == 0 {
		return nil, errors.New("nobody given")
	}
	return out, nil
}
