package whatsapp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// OptionHash is how a vote names an option: SHA-256 of its text.
func OptionHash(option string) []byte {
	h := sha256.Sum256([]byte(option))
	return h[:]
}

// poll is the poll a message asks, if it does.
func poll(msg *waE2E.Message) *Poll {
	var pc *waE2E.PollCreationMessage
	switch {
	case msg.GetPollCreationMessage() != nil:
		pc = msg.GetPollCreationMessage()
	case msg.GetPollCreationMessageV2() != nil:
		pc = msg.GetPollCreationMessageV2()
	case msg.GetPollCreationMessageV3() != nil:
		pc = msg.GetPollCreationMessageV3()
	default:
		return nil
	}
	p := &Poll{Question: pc.GetName(), Multi: pc.GetSelectableOptionsCount() != 1}
	for _, o := range pc.GetOptions() {
		p.Options = append(p.Options, o.GetOptionName())
	}
	return p
}

// vote decrypts someone's vote (only the poll's people can).
func (cl *Client) vote(ctx context.Context, e *events.Message) (Vote, bool) {
	pv, err := cl.c.DecryptPollVote(ctx, e)
	if err != nil {
		return Vote{}, false
	}
	key := e.Message.GetPollUpdateMessage().GetPollCreationMessageKey()
	chat := e.Info.Chat
	if chat.Server == types.HiddenUserServer {
		if alt := firstPN(e.Info.SenderAlt, e.Info.RecipientAlt); !alt.IsEmpty() && !e.Info.IsGroup {
			chat = alt
		}
	}
	sender := e.Info.Sender
	if sender.Server == types.HiddenUserServer && e.Info.SenderAlt.Server == types.DefaultUserServer {
		sender = e.Info.SenderAlt
	}
	return Vote{Chat: cl.chatJID(ctx, chat), Poll: key.GetID(), Voter: cl.chatJID(ctx, sender), Hashes: pv.GetSelectedOptions(), FromMe: e.Info.IsFromMe}, true
}

// SendPoll asks a poll in chat; returns its id.
func (cl *Client) SendPoll(ctx context.Context, chat string, p Poll) (string, error) {
	to, err := types.ParseJID(chat)
	if err != nil {
		return "", fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
	}
	if len(p.Options) < 2 || len(p.Options) > 12 {
		return "", errors.New("a poll has 2 to 12 options")
	}
	n := 1
	if p.Multi {
		n = 0 // any number
	}
	resp, err := cl.c.SendMessage(ctx, to, cl.c.BuildPollCreation(p.Question, p.Options, n))
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// SendVote votes in a poll (sender wrote it; fromMe: you did) for these
// options; none takes your vote back.
func (cl *Client) SendVote(ctx context.Context, chat, sender, pollID string, fromMe bool, options []string) error {
	to, err := types.ParseJID(chat)
	if err != nil {
		return fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
	}
	from, err := types.ParseJID(sender)
	if err != nil {
		return err
	}
	info := &types.MessageInfo{MessageSource: types.MessageSource{Chat: to, Sender: from, IsFromMe: fromMe, IsGroup: to.Server == types.GroupServer}, ID: pollID}
	msg, err := cl.c.BuildPollVote(ctx, info, options)
	if err != nil {
		return fmt.Errorf("voting: %w (narada can vote only in polls it has seen arrive)", err)
	}
	_, err = cl.c.SendMessage(ctx, to, msg)
	return err
}
