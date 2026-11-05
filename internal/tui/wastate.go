package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/cli"
	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

// presenceLine says who is typing in a WhatsApp chat, else whether the
// person is online or when they were last seen ("" when unknown).
func (m *Model) presenceLine(sp store.Space) string {
	s := m.styles
	if typing := m.a.Store.TypingIn(sp.Name); len(typing) > 0 {
		var who []string
		rec := false
		for _, p := range typing {
			rec = rec || p.Typing == "recording"
			if whatsapp.IsGroup(sp.Name) {
				name, _, _ := m.a.Store.User(store.WhatsAppAccount, p.JID)
				who = append(who, strings.TrimPrefix(firstNonEmpty(name, "+"+strings.TrimSuffix(p.JID, "@s.whatsapp.net")), "~"))
			}
		}
		what := "typing…"
		if rec {
			what = "recording audio…"
		}
		if len(who) > 0 {
			what = strings.Join(who, ", ") + " " + what
		}
		return s.green.Render(" · " + what)
	}
	timer := ""
	if sp.Disappear > 0 {
		timer = s.muted.Render(" · ⏱ " + whatsapp.DisappearName(sp.Disappear))
	}
	if whatsapp.IsGroup(sp.Name) {
		return timer
	}
	p := m.a.Store.PresenceOf(sp.Name)
	return m.onlineLine(p) + timer
}

func (m *Model) onlineLine(p store.Presence) string {
	s := m.styles
	switch {
	case p.JID == "" || time.Since(time.UnixMilli(p.At)) > 10*time.Minute && p.Online:
		return ""
	case p.Online:
		return s.green.Render(" · online")
	case p.LastSeen > 0:
		return s.muted.Render(" · last seen " + strings.ToLower(cli.When(p.LastSeen)))
	}
	return ""
}

// waTyping tells the chat you're typing (at most every 8 seconds), or
// that you stopped.
func (m *Model) waTyping(stopped bool) {
	sp, ok := m.curSpace()
	if !ok || !m.wa || store.IsPending(sp.Name) {
		return
	}
	if stopped {
		if m.typedAt.IsZero() {
			return
		}
		m.typedAt = time.Time{}
		go m.a.WhatsAppDo(context.Background(), whatsapp.DoTyping, sp.Name, "paused", 0)
		return
	}
	if time.Since(m.typedAt) < 8*time.Second {
		return
	}
	m.typedAt = time.Now()
	state := "composing"
	if m.rec != nil {
		state = "recording"
	}
	go m.a.WhatsAppDo(context.Background(), whatsapp.DoTyping, sp.Name, state, 0)
}

// waHere says you're on WhatsApp now (and asks to hear whether the person
// in this chat is online), once per chat every few minutes, as WhatsApp
// Web does while it's open.
func (m *Model) waHere() {
	sp, ok := m.curSpace()
	if !ok || !m.wa || sp.Account != store.WhatsAppAccount {
		return
	}
	if m.watched == nil {
		m.watched = map[string]time.Time{}
	}
	if time.Since(m.watched[sp.Name]) < 5*time.Minute {
		return
	}
	m.watched[sp.Name] = time.Now()
	a := m.a
	go func() {
		ctx := context.Background()
		_, _ = a.WhatsAppDo(ctx, whatsapp.DoOnline, "", "1", 0)
		if !whatsapp.IsGroup(sp.Name) {
			_, _ = a.WhatsAppDo(ctx, whatsapp.DoWatch, sp.Name, "", 0)
		}
	}()
}

// waChatKey is a key on a WhatsApp chat in the list (or the chat open):
// e archives, p pins, M marks unread, Z mutes (8 hours, a week, always,
// then unmuted, as WhatsApp offers), B blocks; * stars the
// picked message. ok is false for other keys.
func (m Model) waChatKey(s string) (Model, tea.Cmd, bool) {
	if !m.wa || m.tab != tabChat {
		return m, nil, false
	}
	sp, ok := m.curSpace()
	if !ok {
		return m, nil, false
	}
	a := m.a
	if sp.Type == store.TypeStatus {
		switch s {
		case "i", "r", "U", "c", "e", "p", "M", "Z", "B", "X", "I", "n", "f", "R", "P":
			if m.pickingMsg() && s == "e" {
				break
			}
			m.say("people's statuses are to look at: narada whatsapp status post posts yours")
			return m, nil, true
		}
		return m, nil, false
	}
	switch s {
	case "*":
		if !m.pickingMsg() {
			return m, nil, false
		}
		c := m.chatMsgs[m.chatSel]
		on := !c.Starred
		return m, m.act(map[bool]string{true: "starred", false: "unstarred"}[on], func(ctx context.Context) error { return a.StarWhatsApp(ctx, c.Name, on) }), true
	case "e":
		if m.pickingMsg() {
			return m, nil, false // e reacts to the picked message
		}
		on := !sp.Archived
		return m, m.act(map[bool]string{true: "archived (Archived section has it)", false: "back from the archive"}[on],
			func(ctx context.Context) error { return a.ArchiveWhatsApp(ctx, sp.Name, on) }), true
	case "p":
		on := !sp.Pinned
		return m, m.act(map[bool]string{true: "pinned on top, everywhere", false: "unpinned"}[on],
			func(ctx context.Context) error { return a.PinWhatsApp(ctx, sp.Name, on) }), true
	case "M":
		if len(m.selected) > 0 {
			return m, nil, false
		}
		return m, m.act("marked unread", func(ctx context.Context) error { return a.MarkSpaceUnread(ctx, sp.Account, sp.Name) }), true
	case "Z":
		left := time.Until(time.UnixMilli(sp.MutedUntil))
		switch {
		case sp.MutedUntil < 0: // always → unmuted
			return m, m.act("unmuted", func(ctx context.Context) error { return a.MuteWhatsApp(ctx, sp.Name, 0, true) }), true
		case !sp.Muted():
			return m, m.act("muted for 8 hours (Z again: a week, always, then unmuted)",
				func(ctx context.Context) error { return a.MuteWhatsApp(ctx, sp.Name, 8*time.Hour, false) }), true
		case left <= 8*time.Hour+time.Minute:
			return m, m.act("muted for a week", func(ctx context.Context) error { return a.MuteWhatsApp(ctx, sp.Name, 7*24*time.Hour, false) }), true
		}
		return m, m.act("muted always", func(ctx context.Context) error { return a.MuteWhatsApp(ctx, sp.Name, 0, false) }), true
	case "I":
		if !whatsapp.IsGroup(sp.Name) {
			m.say("I shows a group's people and description")
			return m, nil, true
		}
		m.aiBusy = true
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			info, err := a.WhatsAppGroupInfo(ctx, sp.Name)
			if err != nil {
				return aiMsg{err: err}
			}
			var b strings.Builder
			if info.About != "" {
				b.WriteString(info.About + "\n\n")
			}
			fmt.Fprintf(&b, "%d people:\n", len(info.People))
			for _, p := range info.People {
				name, _, _ := a.Store.User(store.WhatsAppAccount, p.JID)
				role := ""
				if p.Owner {
					role = " · owner"
				} else if p.Admin {
					role = " · admin"
				}
				fmt.Fprintf(&b, "  %s +%s%s\n", strings.TrimPrefix(firstNonEmpty(name, "?"), "~"), strings.TrimSuffix(p.JID, "@s.whatsapp.net"), role)
			}
			b.WriteString("\nnarada whatsapp group add|remove|promote|link … changes it")
			return aiMsg{title: info.Name, text: b.String()}
		}, true
	case "X":
		next := map[int]int{0: 86400, 86400: 7 * 86400, 7 * 86400: 90 * 86400}[sp.Disappear] // 90 days → off
		what := "disappearing messages off"
		if next > 0 {
			what = "new messages disappear after " + whatsapp.DisappearName(next) + " (X again: longer, then off)"
		}
		return m, m.act(what, func(ctx context.Context) error { return a.DisappearWhatsApp(ctx, sp.Name, next) }), true
	case "B":
		if whatsapp.IsGroup(sp.Name) {
			m.say("B blocks a person; leave a group with narada whatsapp group leave")
			return m, nil, true
		}
		on := !sp.Blocked
		if on && m.armDelete != "block:"+sp.Name {
			m.armDelete = "block:" + sp.Name
			m.say(fmt.Sprintf("B again blocks %s: they can't call or message you", cli.SpaceTitle(sp)))
			return m, nil, true
		}
		m.armDelete = ""
		return m, m.act(map[bool]string{true: "blocked", false: "unblocked"}[on], func(ctx context.Context) error { return a.BlockWhatsApp(ctx, sp.Name, on) }), true
	}
	return m, nil, false
}

// sendPoll sends what's typed as a poll: the first line asks, each other
// line is an option (Ctrl+p while typing).
func (m Model) sendPoll() (tea.Model, tea.Cmd) {
	sp, ok := m.curSpace()
	if !ok {
		return m, nil
	}
	lines := strings.Split(strings.TrimSpace(string(m.input)), "\n")
	if len(lines) < 3 {
		m.say("a poll: the question on the first line, then one option per line (Alt+Enter), then Ctrl+p")
		return m, nil
	}
	multi := strings.HasSuffix(strings.TrimSpace(lines[0]), "(several)")
	q := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(lines[0]), "(several)"))
	it, err := m.a.PollWhatsApp(sp, q, lines[1:], multi, app.OriginYou)
	if err != nil {
		m.setErr(err.Error())
		return m, nil
	}
	m.input, m.back = nil, 0
	m.waTyping(true)
	return m, m.sent(it)
}
