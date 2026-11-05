package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

// Polls: asking one and voting go through the outbox like sends (ADR 7);
// the votes that arrive keep its tally in the message's text.

const (
	ActPoll = "poll"
	ActVote = "vote"
)

// PollWhatsApp asks a poll in a chat: 2 to 12 options; multi lets people
// pick several.
func (a *App) PollWhatsApp(to store.Space, question string, options []string, multi bool, origin string) (store.OutboxItem, error) {
	p, err := cleanPoll(question, options)
	if err != nil {
		return store.OutboxItem{}, err
	}
	p.Multi = multi
	return a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: to.Name, SpaceName: firstNonEmpty(to.Alias, to.DisplayName),
		Action: ActPoll, Poll: &p, Body: pollText(p.Question, p.Options, p.Multi, nil, "")}, origin)
}

func cleanPoll(question string, options []string) (store.Poll, error) {
	p := store.Poll{Question: strings.TrimSpace(question)}
	for _, o := range options {
		if o = strings.TrimSpace(o); o != "" && !slices.Contains(p.Options, o) {
			p.Options = append(p.Options, o)
		}
	}
	switch {
	case p.Question == "":
		return p, errors.New("a poll needs a question")
	case len(p.Options) < 2 || len(p.Options) > 12:
		return p, errors.New("a poll has 2 to 12 different options")
	}
	return p, nil
}

// VoteWhatsApp votes in a poll: picks are option numbers (from 1) or
// their text; none takes your vote back.
func (a *App) VoteWhatsApp(msg string, picks []string, origin string) (store.OutboxItem, error) {
	m, _, err := a.waMsg(msg)
	if err != nil {
		return store.OutboxItem{}, err
	}
	p, err := a.Store.WhatsAppPollOf(msg)
	if err != nil {
		return store.OutboxItem{}, errors.New("that message isn't a poll")
	}
	var chosen []string
	for _, pick := range picks {
		pick = strings.TrimSpace(pick)
		if pick == "" {
			continue
		}
		o := ""
		if n, err := strconv.Atoi(pick); err == nil && n >= 1 && n <= len(p.Options) {
			o = p.Options[n-1]
		} else {
			for _, opt := range p.Options {
				if strings.EqualFold(opt, pick) {
					o = opt
				}
			}
		}
		if o == "" {
			return store.OutboxItem{}, fmt.Errorf("%q isn't one of the poll's options", pick)
		}
		if !slices.Contains(chosen, o) {
			chosen = append(chosen, o)
		}
	}
	if len(chosen) > 1 && !p.Multi {
		return store.OutboxItem{}, errors.New("this poll takes one option")
	}
	body := "🗳 " + strings.Join(chosen, ", ")
	if len(chosen) == 0 {
		body = "🗳 vote taken back"
	}
	return a.Queue(store.WhatsAppAccount, "whatsapp", store.Outgoing{Space: m.Space, SpaceName: a.waChatName(m.Space, ""), Action: ActVote, Target: msg,
		Poll: &store.Poll{Options: chosen}, Body: body + " · " + p.Question}, origin)
}

// pollText is how a poll shows: its question, then each option with its
// votes, yours marked ●.
func pollText(question string, options []string, multi bool, votes map[string][]string, me string) string {
	var b strings.Builder
	b.WriteString("📊 Poll: " + question)
	if multi {
		b.WriteString(" (pick several)")
	}
	for i, o := range options {
		n, mine := 0, false
		for voter, picked := range votes {
			if slices.Contains(picked, o) {
				n++
				mine = mine || voter == me
			}
		}
		mark := "○"
		if mine {
			mark = "●"
		}
		fmt.Fprintf(&b, "\n%s %d. %s", mark, i+1, o)
		if n > 0 {
			fmt.Fprintf(&b, " · %d", n)
		}
	}
	return b.String()
}

// showPoll writes a poll's tally into its message.
func (a *App) showPoll(msg string) {
	p, err := a.Store.WhatsAppPollOf(msg)
	if err != nil {
		return
	}
	wa, _ := a.Store.WhatsApp()
	_ = a.Store.SetChatText(store.WhatsAppAccount, msg, pollText(p.Question, p.Options, p.Multi, p.Votes, wa.ChatUser))
}

// onWhatsAppVotes keeps the votes that arrive.
func (a *App) onWhatsAppVotes(votes []whatsapp.Vote) {
	wa, _ := a.Store.WhatsApp()
	for _, v := range votes {
		msg := waName(v.Chat, v.Poll)
		p, err := a.Store.WhatsAppPollOf(msg)
		if err != nil {
			continue // a poll from before narada saw it
		}
		var picked []string
		for _, o := range p.Options {
			for _, h := range v.Hashes {
				if bytes.Equal(h, whatsapp.OptionHash(o)) {
					picked = append(picked, o)
				}
			}
		}
		voter := v.Voter
		if v.FromMe {
			voter = wa.ChatUser
		}
		_ = a.Store.SetWhatsAppVote(msg, voter, picked)
		a.showPoll(msg)
	}
}

// deliverPoll asks the poll or casts the vote of an outbox item.
func (a *App) deliverPoll(ctx context.Context, c WhatsAppConn, it store.OutboxItem, keep func(id, text string, files []store.ChatFile)) (string, error) {
	out := it.Out
	if out.Poll == nil {
		return "", errors.New("no poll")
	}
	if out.Action == ActPoll {
		id, err := c.SendPoll(ctx, out.Space, whatsapp.Poll{Question: out.Poll.Question, Options: out.Poll.Options, Multi: out.Poll.Multi})
		if err != nil {
			return "", err
		}
		keep(id, pollText(out.Poll.Question, out.Poll.Options, out.Poll.Multi, nil, ""), nil)
		name := waName(out.Space, id)
		_ = a.Store.PutWhatsAppPoll(store.WhatsAppPoll{Msg: name, Question: out.Poll.Question, Options: out.Poll.Options, Multi: out.Poll.Multi})
		return name, nil
	}
	m, id, err := a.waMsg(out.Target)
	if err != nil {
		return "", err
	}
	if err := c.SendVote(ctx, m.Space, m.Sender, id, a.waMine(m), out.Poll.Options); err != nil {
		return "", err
	}
	wa, _ := a.Store.WhatsApp()
	_ = a.Store.SetWhatsAppVote(out.Target, wa.ChatUser, out.Poll.Options)
	a.showPoll(out.Target)
	return out.Target, nil
}
