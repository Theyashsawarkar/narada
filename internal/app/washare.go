package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

// Sharing a place or a contact card goes through the outbox like a send
// (ADR 7); a chat's disappearing-messages timer through the action queue.

const (
	ActLocation = "location"
	ActContact  = "contact"
)

// ParseLocation reads "18.52,73.85" (latitude, longitude).
func ParseLocation(s string) (store.Location, error) {
	lat, lon, ok := strings.Cut(strings.ReplaceAll(s, " ", ""), ",")
	if !ok {
		return store.Location{}, errors.New("a place is latitude,longitude: 18.52,73.85")
	}
	la, err1 := strconv.ParseFloat(lat, 64)
	lo, err2 := strconv.ParseFloat(lon, 64)
	if err1 != nil || err2 != nil || math.Abs(la) > 90 || math.Abs(lo) > 180 {
		return store.Location{}, fmt.Errorf("%q isn't latitude,longitude", s)
	}
	return store.Location{Lat: la, Lon: lo}, nil
}

// ShareLocationWhatsApp shares a place in a chat.
func (a *App) ShareLocationWhatsApp(to store.Space, l store.Location, origin string) (store.OutboxItem, error) {
	return a.Queue(store.WhatsAppAccount, "whatsapp", LocationOutgoing(to, l), origin)
}

// LocationOutgoing is the outbox message that shares a place.
func LocationOutgoing(to store.Space, l store.Location) store.Outgoing {
	return store.Outgoing{Space: to.Name, SpaceName: firstNonEmpty(to.Alias, to.DisplayName), Action: ActLocation, Location: &l, Body: locationText(l)}
}

// ShareContactWhatsApp shares a contact card: who is a contact's name or
// a number (name then given).
func (a *App) ShareContactWhatsApp(ctx context.Context, to store.Space, who, name string, origin string) (store.OutboxItem, error) {
	out, err := a.ContactOutgoing(ctx, to, who, name)
	if err != nil {
		return store.OutboxItem{}, err
	}
	return a.Queue(store.WhatsAppAccount, "whatsapp", out, origin)
}

// ContactOutgoing is the outbox message that shares a contact card.
func (a *App) ContactOutgoing(ctx context.Context, to store.Space, who, name string) (store.Outgoing, error) {
	card := store.Card{Name: strings.TrimSpace(name)}
	if d := whatsapp.Digits(who); len(d) >= 7 && len(d) >= len(strings.Join(strings.Fields(who), ""))-4 {
		card.Phone = "+" + d
	} else {
		sp, err := a.WhatsAppChat(ctx, who)
		if err != nil {
			return store.Outgoing{}, err
		}
		if whatsapp.IsGroup(sp.Name) {
			return store.Outgoing{}, errors.New(who + " is a group, not a person")
		}
		card.Phone = phoneOf(sp.Name)
		card.Name = firstNonEmpty(card.Name, strings.TrimPrefix(firstNonEmpty(sp.Alias, sp.DisplayName), "~"))
	}
	if card.Name == "" {
		card.Name = card.Phone
	}
	if card.Phone == "" {
		return store.Outgoing{}, errors.New("no phone number for " + who)
	}
	return store.Outgoing{Space: to.Name, SpaceName: firstNonEmpty(to.Alias, to.DisplayName),
		Action: ActContact, Card: &card, Body: "👤 Contact: " + card.Name + " (" + card.Phone + ")"}, nil
}

func locationText(l store.Location) string {
	t := fmt.Sprintf("📍 Location https://maps.google.com/?q=%f,%f", l.Lat, l.Lon)
	if l.Name != "" {
		t += ": " + l.Name
	}
	return t
}

func (a *App) deliverShare(ctx context.Context, c WhatsAppConn, it store.OutboxItem, keep func(id, text string, files []store.ChatFile)) (string, error) {
	out := it.Out
	var id string
	var err error
	switch {
	case out.Action == ActLocation && out.Location != nil:
		id, err = c.SendLocation(ctx, out.Space, whatsapp.Location{Lat: out.Location.Lat, Lon: out.Location.Lon, Name: out.Location.Name})
	case out.Action == ActContact && out.Card != nil:
		id, err = c.SendContact(ctx, out.Space, whatsapp.Contact{Name: out.Card.Name, Phone: out.Card.Phone})
	default:
		return "", errors.New("nothing to share")
	}
	if err != nil {
		return "", err
	}
	keep(id, out.Body, nil)
	return waName(out.Space, id), nil
}

// Disappearing-message timers WhatsApp offers.
var DisappearTimers = map[string]int{"off": 0, "24h": 86400, "1d": 86400, "7d": 7 * 86400, "1w": 7 * 86400, "90d": 90 * 86400}

// DisappearWhatsApp sets a chat's disappearing-messages timer: new
// messages go after secs (24 hours, 7 or 90 days; 0 turns it off).
func (a *App) DisappearWhatsApp(ctx context.Context, chat string, secs int) error {
	switch secs {
	case 0, 86400, 7 * 86400, 90 * 86400:
	default:
		return errors.New("WhatsApp's timers are 24h, 7d and 90d (or off)")
	}
	if _, err := a.WhatsAppDo(ctx, whatsapp.DoDisappear, chat, strconv.Itoa(secs), 45*time.Second); err != nil {
		return err
	}
	return a.Store.SetDisappear(store.WhatsAppAccount, chat, secs)
}
