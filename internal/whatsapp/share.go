package whatsapp

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Location is a place to share.
type Location struct {
	Lat, Lon float64
	Name     string // optional: what the place is called
}

// Contact is a contact card to share.
type Contact struct {
	Name, Phone string // phone with its country code
}

// SendLocation shares a place.
func (cl *Client) SendLocation(ctx context.Context, chat string, l Location) (string, error) {
	to, err := types.ParseJID(chat)
	if err != nil {
		return "", fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
	}
	lm := &waE2E.LocationMessage{DegreesLatitude: proto.Float64(l.Lat), DegreesLongitude: proto.Float64(l.Lon)}
	if l.Name != "" {
		lm.Name = proto.String(l.Name)
	}
	if exp := cl.expiration(chat); exp > 0 {
		lm.ContextInfo = &waE2E.ContextInfo{Expiration: proto.Uint32(uint32(exp))}
	}
	resp, err := cl.c.SendMessage(ctx, to, &waE2E.Message{LocationMessage: lm})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// SendContact shares a contact card.
func (cl *Client) SendContact(ctx context.Context, chat string, c Contact) (string, error) {
	to, err := types.ParseJID(chat)
	if err != nil {
		return "", fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
	}
	cm := &waE2E.ContactMessage{DisplayName: proto.String(c.Name), Vcard: proto.String(VCard(c))}
	if exp := cl.expiration(chat); exp > 0 {
		cm.ContextInfo = &waE2E.ContextInfo{Expiration: proto.Uint32(uint32(exp))}
	}
	resp, err := cl.c.SendMessage(ctx, to, &waE2E.Message{ContactMessage: cm})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// VCard is the card WhatsApp sends for a contact (waid: their number, so
// "Message" on it opens their chat).
func VCard(c Contact) string {
	d := Digits(c.Phone)
	esc := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", " ").Replace(c.Name)
	return "BEGIN:VCARD\nVERSION:3.0\nN:;" + esc + ";;;\nFN:" + esc + "\nTEL;type=CELL;type=VOICE;waid=" + d + ":+" + d + "\nEND:VCARD"
}

var reTel = regexp.MustCompile(`(?m)^TEL[^:]*:(.+)$`)

// vcardPhones: " (+91 98…)" for the numbers on a card.
func vcardPhones(card string) string {
	var out []string
	for _, m := range reTel.FindAllStringSubmatch(card, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	if len(out) == 0 {
		return ""
	}
	return " (" + strings.Join(out, ", ") + ")"
}

// SetDisappearing records a chat's disappearing-messages timer (seconds,
// 0 off), so what you send there disappears with the rest.
func (cl *Client) SetDisappearing(chat string, secs int) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.disappear == nil {
		cl.disappear = map[string]int{}
	}
	if secs <= 0 {
		delete(cl.disappear, chat)
		return
	}
	cl.disappear[chat] = secs
}

func (cl *Client) expiration(chat string) int {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.disappear[chat]
}

// DisappearText is the line a chat shows when its timer changes.
func DisappearText(secs int) string {
	if secs <= 0 {
		return "⏱ Disappearing messages are off"
	}
	return "⏱ Disappearing messages: new messages go after " + DisappearName(secs)
}

// DisappearName is a timer as people say it: 24 hours, 7 days, 90 days.
func DisappearName(secs int) string {
	switch {
	case secs%86400 == 0 && secs/86400 == 1:
		return "24 hours"
	case secs%86400 == 0:
		return fmt.Sprintf("%d days", secs/86400)
	case secs%3600 == 0:
		return fmt.Sprintf("%d hours", secs/3600)
	}
	return fmt.Sprintf("%d seconds", secs)
}
