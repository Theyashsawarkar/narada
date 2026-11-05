package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// How long WhatsApp lets you change your messages after sending them.
const (
	EditWindow   = 15 * 60 // seconds: edits
	RevokeWindow = 60 * 60 * 60
)

// Edit changes the text of a message of yours.
func (cl *Client) Edit(ctx context.Context, chat, id, text string) error {
	to, err := types.ParseJID(chat)
	if err != nil {
		return fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
	}
	_, err = cl.c.SendMessage(ctx, to, cl.c.BuildEdit(to, id, &waE2E.Message{Conversation: proto.String(text)}))
	return err
}

// Revoke deletes a message for everyone: yours, or (sender set, as a
// group's admin) someone else's.
func (cl *Client) Revoke(ctx context.Context, chat, sender, id string) error {
	to, err := types.ParseJID(chat)
	if err != nil {
		return fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
	}
	var from types.JID
	if sender != "" {
		if from, err = types.ParseJID(sender); err != nil {
			return err
		}
	}
	_, err = cl.c.SendMessage(ctx, to, cl.c.BuildRevoke(to, from, id))
	return err
}

// PinMessage pins a message in its chat for everyone, for secs (WhatsApp
// offers a day, a week or 30 days), or unpins it (on false).
func (cl *Client) PinMessage(ctx context.Context, chat, sender, id string, on bool, secs int) error {
	to, err := types.ParseJID(chat)
	if err != nil {
		return fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
	}
	var from types.JID
	if sender != "" {
		if from, err = types.ParseJID(sender); err != nil {
			return err
		}
	}
	kind := waE2E.PinInChatMessage_UNPIN_FOR_ALL
	if on {
		kind = waE2E.PinInChatMessage_PIN_FOR_ALL
	}
	msg := &waE2E.Message{PinInChatMessage: &waE2E.PinInChatMessage{Key: cl.c.BuildMessageKey(to, from, id), Type: kind.Enum(),
		SenderTimestampMS: proto.Int64(time.Now().UnixMilli())}}
	if on {
		msg.MessageContextInfo = &waE2E.MessageContextInfo{MessageAddOnDurationInSecs: proto.Uint32(uint32(secs))}
	}
	_, err = cl.c.SendMessage(ctx, to, msg)
	return err
}

// Forward sends a message on to chat, marked "Forwarded" as the phone
// does: its text, or its media (by reference: nothing is uploaded again).
func (cl *Client) Forward(ctx context.Context, chat string, m Message) (string, error) {
	to, err := types.ParseJID(chat)
	if err != nil {
		return "", fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
	}
	msg, err := forwarded(m)
	if err != nil {
		return "", err
	}
	resp, err := cl.c.SendMessage(ctx, to, msg)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// forwarded is the message that forwards m.
func forwarded(m Message) (*waE2E.Message, error) {
	ci := &waE2E.ContextInfo{IsForwarded: proto.Bool(true), ForwardingScore: proto.Uint32(1)}
	if m.Media == nil || len(m.Media.Raw) == 0 {
		if m.Text == "" {
			return nil, errors.New("nothing to forward")
		}
		return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(m.Text), ContextInfo: ci}}, nil
	}
	var msg waE2E.Message
	if err := proto.Unmarshal(m.Media.Raw, &msg); err != nil {
		return nil, err
	}
	switch {
	case msg.ImageMessage != nil:
		msg.ImageMessage.ContextInfo = ci
	case msg.VideoMessage != nil:
		msg.VideoMessage.ContextInfo = ci
	case msg.AudioMessage != nil:
		msg.AudioMessage.ContextInfo = ci
	case msg.DocumentMessage != nil:
		msg.DocumentMessage.ContextInfo = ci
	case msg.StickerMessage != nil:
		msg.StickerMessage.ContextInfo = ci
	default:
		return nil, errors.New("this message can't be forwarded")
	}
	return &msg, nil
}
