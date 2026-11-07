package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
	"golang.org/x/term"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

func whatsApp(a *app.App, args []string) error {
	if len(args) == 0 {
		return usageError("whatsapp what? link, unlink, list, read, send, save, edit, delete, forward, archive, pin, mute, unread, star, starred, block, group, poll, vote, disappear, status, pin-message")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "link":
		fs := flags("narada whatsapp link", "narada whatsapp link [--phone NUMBER]\n\nLink this machine to your WhatsApp, like WhatsApp Web: scan the QR code with\nthe phone (Settings → Linked devices → Link a device). With --phone, type the\ncode it shows on the phone instead (\"Link with phone number\").\n\nWhatsApp doesn't allow programs other than its own to link: this works the\nway WhatsApp Web does, but an account that sends a lot from one can be banned.\nnarada caps sends (whatsapp_per_hour) and never sends in bulk.")
		phone := fs.String("phone", "", "your number with country code, e.g. +919876543210")
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		ctx, cancel := ctxSignal()
		defer cancel()
		ctx, tcancel := context.WithTimeout(ctx, 5*time.Minute)
		defer tcancel()
		err := a.LinkWhatsApp(ctx, *phone, func(qr, code string) {
			if code != "" {
				fmt.Printf("On the phone: WhatsApp → Settings → Linked devices → Link a device → Link with phone number, and type:\n\n    %s\n\n", code)
				return
			}
			fmt.Print("\033[H\033[2J")
			fmt.Println("Scan with the phone: WhatsApp → Settings → Linked devices → Link a device")
			fmt.Println()
			qrterminal.GenerateHalfBlock(qr, qrterminal.L, os.Stdout)
			fmt.Println("\nThe code changes every 20 seconds. Ctrl+c stops.")
		}, func(chats, msgs, pct int) {
			if chats == 0 && msgs == 0 {
				fmt.Print("\033[H\033[2J")
				fmt.Println("narada: linked. Keep this open while the phone sends your recent chats (up to two minutes)…")
				return
			}
			fmt.Printf("\r\033[Knarada: received %d chats, %d messages", chats, msgs)
			if pct > 0 {
				fmt.Printf(" (%d%%)", pct)
			}
		})
		fmt.Println()
		if errors.Is(err, context.Canceled) {
			return errors.New("not linked")
		}
		if err != nil {
			return err
		}
		wa, _ := a.Store.WhatsApp()
		fmt.Printf("narada: WhatsApp %s is linked. Your chats arrive over the next minutes", wa.Email)
		if a.DaemonRunning() {
			fmt.Println(" (the background service picks it up).")
		} else {
			fmt.Println(";\nthe background service keeps them in sync: narada service install.")
		}
		return nil
	case "unlink", "logout":
		fs := flags("narada whatsapp unlink", "narada whatsapp unlink\n\nLog this machine out of WhatsApp (it leaves the phone's Linked devices) and\nforget its WhatsApp chats. Stop the background service first if it runs.")
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		ctx, cancel := ctxSignal()
		defer cancel()
		if err := a.UnlinkWhatsApp(ctx); err != nil {
			return err
		}
		fmt.Println("narada: WhatsApp unlinked")
		return nil
	case "list", "ls":
		fs := flags("narada whatsapp list", "narada whatsapp list [--unread] [--json]\n\nWhatsApp chats, most recent first.")
		unread := fs.Bool("unread", false, "only chats with unread messages")
		archived := fs.Bool("archived", false, "the archived chats (left out otherwise)")
		asJSON := fs.Bool("json", false, "JSON output")
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		if _, ok := a.Store.WhatsApp(); !ok {
			return errors.New("WhatsApp is not linked: narada whatsapp link")
		}
		sps, err := a.Store.Spaces([]string{store.WhatsAppAccount}, nil)
		if err != nil {
			return err
		}
		out := []store.Space{}
		for _, s := range sps {
			if (!*unread || s.Unread > 0) && s.Archived == *archived && s.Type != store.TypeStatus {
				out = append(out, s)
			}
		}
		if *asJSON {
			return printJSON(out)
		}
		for _, s := range out {
			n := "  "
			if s.Unread > 0 {
				n = fmt.Sprintf("%-2d", s.Unread)
			}
			marks := ""
			if s.Pinned {
				marks += " 📌"
			}
			if s.Muted() {
				marks += " 🔕"
			}
			if s.Blocked {
				marks += " ⊘"
			}
			fmt.Printf("%s %-8s %-24.24s %s  [%s]%s\n", n, When(s.LastActive), SpaceTitle(s), oneLine(s.LastText, 60), s.Name, marks)
		}
		return nil
	case "read", "show":
		fs := flags("narada whatsapp read", "narada whatsapp read <chat> [--limit N] [--mark-read] [--json]\n\nA chat's recent messages, oldest first. <chat> is a name, a phone number or its id.")
		limit := fs.Int("limit", 50, "at most this many")
		mark := fs.Bool("mark-read", false, "also mark it read (and tell the sender)")
		asJSON := fs.Bool("json", false, "JSON output")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return usageError("whatsapp read needs a chat")
		}
		ctx, cancel := ctxSignal()
		defer cancel()
		sp, err := a.WhatsAppChat(ctx, strings.Join(pos, " "))
		if err != nil {
			return err
		}
		msgs, err := a.Store.ChatMessages(sp.Account, sp.Name, *limit)
		if err != nil {
			return err
		}
		if *mark {
			startWhatsApp(ctx, a)
			if err := a.MarkSpaceRead(ctx, sp.Account, sp.Name); err != nil {
				return err
			}
		}
		if *asJSON {
			if msgs == nil {
				msgs = []store.ChatMessage{}
			}
			return printJSON(map[string]any{"chat": sp, "messages": msgs})
		}
		fmt.Printf("%s\n\n", SpaceTitle(sp))
		for _, m := range msgs {
			who := m.SenderName
			if m.Mine {
				who = "you"
			}
			fmt.Printf("%s  %s  [%s]\n%s\n", who, When(m.Created), m.Name, m.Text)
			for i, f := range m.Files {
				fmt.Printf("  file %d: %s (%s): narada whatsapp save %s %d\n", i+1, f.Name, app.HumanSize(f.Size), m.Name, i+1)
			}
			fmt.Println()
		}
		return nil
	case "save", "download":
		fs := flags("narada whatsapp save", "narada whatsapp save <message-id> [N|all] [--dir DIR]\n\nDownload a WhatsApp message's photo, video, voice note or document (whatsapp read\nshows the ids) into DIR, default ~/Downloads/narada. When another narada holds\nWhatsApp (the daemon), it downloads the file and this waits for it.")
		dir := fs.String("dir", "", "where to save")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) < 1 {
			return usageError("whatsapp save needs a message id (whatsapp read shows them)")
		}
		want := 0
		if len(pos) > 1 && pos[1] != "all" {
			if want, err = strconv.Atoi(pos[1]); err != nil || want < 1 {
				return usageError("N is a file number, or all")
			}
		}
		ctx, cancel := ctxSignal()
		defer cancel()
		startWhatsApp(ctx, a)
		ctx, tcancel := context.WithTimeout(ctx, 30*time.Minute)
		defer tcancel()
		saved, err := a.SaveChatFiles(ctx, pos[0], want, *dir)
		for _, p := range saved {
			fmt.Println(p)
		}
		return err
	case "archive", "unarchive", "pin", "unpin", "unread", "block", "unblock", "mute", "unmute":
		help := map[string]string{
			"archive": "Move a chat into Archived (out of the list); your phone follows.", "unarchive": "Bring a chat back from Archived.",
			"pin": "Pin a chat on top, here and on your phone (three at most).", "unpin": "Unpin a chat.",
			"unread": "Mark a chat unread, here and on your phone.",
			"block":  "Block a person: they can't call or message you.", "unblock": "Unblock a person.",
			"mute": "Silence a chat's notifications: --for 8h, 1w (a week) or always (the default).", "unmute": "Turn a chat's notifications back on.",
		}[sub]
		fs := flags("narada whatsapp "+sub, "narada whatsapp "+sub+" <chat>\n\n"+help+"\n<chat> is a name, a phone number or its id.")
		dur := fs.String("for", "always", "mute: 8h, 1w, always, or any duration (90m, 3d)")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return usageError("whatsapp %s needs a chat", sub)
		}
		ctx, cancel := ctxSignal()
		defer cancel()
		startWhatsApp(ctx, a)
		sp, err := a.WhatsAppChat(ctx, strings.Join(pos, " "))
		if err != nil {
			return err
		}
		switch sub {
		case "archive", "unarchive":
			err = a.ArchiveWhatsApp(ctx, sp.Name, sub == "archive")
		case "pin", "unpin":
			err = a.PinWhatsApp(ctx, sp.Name, sub == "pin")
		case "unread":
			err = a.MarkSpaceUnread(ctx, sp.Account, sp.Name)
		case "block", "unblock":
			err = a.BlockWhatsApp(ctx, sp.Name, sub == "block")
		case "mute", "unmute":
			var d time.Duration
			if sub == "mute" && *dur != "always" {
				if d, err = app.ParseLength(*dur); err != nil {
					return usageError("--for is 8h, 1w, always or a duration like 90m")
				}
			}
			err = a.MuteWhatsApp(ctx, sp.Name, d, sub == "unmute")
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "narada: %s: %s\n", SpaceTitle(sp), map[string]string{"archive": "archived", "unarchive": "unarchived", "pin": "pinned",
			"unpin": "unpinned", "unread": "marked unread", "block": "blocked", "unblock": "unblocked", "mute": "muted", "unmute": "unmuted"}[sub])
		return nil
	case "star", "unstar":
		fs := flags("narada whatsapp "+sub, "narada whatsapp "+sub+" <message-id>\n\nStar a message (or unstar it), here and on your phone. whatsapp starred lists them.")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usageError("whatsapp %s needs one message id", sub)
		}
		ctx, cancel := ctxSignal()
		defer cancel()
		startWhatsApp(ctx, a)
		if err := a.StarWhatsApp(ctx, pos[0], sub == "star"); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "narada: "+sub+"red")
		return nil
	case "starred":
		fs := flags("narada whatsapp starred", "narada whatsapp starred [--json]\n\nYour starred WhatsApp messages, newest first.")
		asJSON := fs.Bool("json", false, "JSON output")
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		msgs, err := a.Store.Starred(store.WhatsAppAccount, 200)
		if err != nil {
			return err
		}
		if *asJSON {
			if msgs == nil {
				msgs = []store.ChatMessage{}
			}
			return printJSON(msgs)
		}
		for _, m := range msgs {
			chat := m.Space
			if sp, err := a.Store.Space(store.WhatsAppAccount, m.Space); err == nil {
				chat = SpaceTitle(sp)
			}
			fmt.Printf("★ %s · %s  %s  [%s]\n%s\n\n", chat, m.SenderName, When(m.Created), m.Name, m.Text)
		}
		return nil
	case "status":
		if len(rest) > 0 && rest[0] == "post" {
			fs := flags("narada whatsapp status post", "narada whatsapp status post [text...] [--attach FILE]\n\nPost a status: all your contacts (as your privacy settings say) see it for a\nday. With --attach, a photo or video with the text as its caption.")
			var attach listFlag
			fs.Var(&attach, "attach", "a photo or video")
			pos, err := parse(fs, rest[1:])
			if err != nil {
				return err
			}
			var files []store.File
			for _, p := range attach {
				files = append(files, store.File{Source: p})
			}
			return waAct(a, func() (store.OutboxItem, error) { return a.PostWhatsAppStatus(strings.Join(pos, " "), files) })
		}
		if len(rest) > 0 && (rest[0] == "reply" || rest[0] == "react") {
			what := rest[0]
			fs := flags("narada whatsapp status "+what, "narada whatsapp status reply <status-id> <text...>\nnarada whatsapp status react <status-id> <emoji>\n\nAnswer someone's status, as WhatsApp does: a message in your chat with them\nthat quotes the status (a reaction is the emoji alone). Only they see it.\nStatus ids are in brackets in narada whatsapp status.")
			pos, err := parse(fs, rest[1:])
			if err != nil {
				return err
			}
			if len(pos) < 2 {
				return usageError("whatsapp status %s needs a status id and %s", what, map[string]string{"reply": "the text", "react": "an emoji"}[what])
			}
			if what == "react" {
				return waAct(a, func() (store.OutboxItem, error) {
					return a.ReactToStatus(pos[0], strings.Join(pos[1:], " "), store.Outgoing{}, app.OriginYou)
				})
			}
			return waAct(a, func() (store.OutboxItem, error) {
				return a.ReplyToStatus(pos[0], store.Outgoing{Body: strings.Join(pos[1:], " ")}, app.OriginYou)
			})
		}
		fs := flags("narada whatsapp status", "narada whatsapp status [--json]\nnarada whatsapp status post [text...] [--attach FILE]\nnarada whatsapp status reply <status-id> <text...>\nnarada whatsapp status react <status-id> <emoji>\n\nPeople's status updates from the last day, by person (narada doesn't tell them\nyou looked); post your own, or answer someone's. Their photos and videos:\nnarada whatsapp save <id>.")
		asJSON := fs.Bool("json", false, "JSON output")
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		groups, err := a.Statuses()
		if err != nil {
			return err
		}
		if *asJSON {
			if groups == nil {
				groups = []app.StatusGroup{}
			}
			return printJSON(groups)
		}
		if len(groups) == 0 {
			fmt.Println("no status updates in the last day (they arrive while narada is online)")
		}
		for _, g := range groups {
			fmt.Printf("%s\n", g.Name)
			for _, m := range g.Items {
				fmt.Printf("  %s  [%s]\n  %s\n", When(m.Created), m.Name, strings.ReplaceAll(m.Text, "\n", "\n  "))
			}
			fmt.Println()
		}
		return nil
	case "pin-message", "unpin-message":
		fs := flags("narada whatsapp "+sub, "narada whatsapp pin-message <message-id> [--for 24h|7d|30d]\nnarada whatsapp unpin-message <message-id>\n\nPin a message in its chat for everyone (a week by default), or unpin it.")
		dur := fs.String("for", "7d", "24h, 7d or 30d")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usageError("whatsapp %s needs one message id", sub)
		}
		secs := map[string]int{"24h": app.PinDay, "1d": app.PinDay, "7d": app.PinWeek, "1w": app.PinWeek, "30d": app.PinMonth}[*dur]
		if secs == 0 {
			return usageError("--for is 24h, 7d or 30d")
		}
		if sub == "unpin-message" {
			secs = 0
		}
		return waAct(a, func() (store.OutboxItem, error) { return a.PinWhatsAppMessage(pos[0], secs, app.OriginYou) })
	case "group", "groups":
		return waGroup(a, rest)
	case "disappear", "disappearing":
		fs := flags("narada whatsapp disappear", "narada whatsapp disappear <chat> 24h|7d|90d|off\n\nTurn disappearing messages on for a chat (new messages go after that long, for\neveryone in it), or off. Messages you send from narada there disappear too.")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) < 2 {
			return usageError("whatsapp disappear needs a chat and 24h, 7d, 90d or off")
		}
		secs, ok := app.DisappearTimers[strings.ToLower(pos[len(pos)-1])]
		if !ok {
			return usageError("the timer is 24h, 7d, 90d or off")
		}
		ctx, cancel := ctxSignal()
		defer cancel()
		startWhatsApp(ctx, a)
		sp, err := a.WhatsAppChat(ctx, strings.Join(pos[:len(pos)-1], " "))
		if err != nil {
			return err
		}
		if err := a.DisappearWhatsApp(ctx, sp.Name, secs); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "narada: %s: %s\n", SpaceTitle(sp), strings.TrimPrefix(whatsapp.DisappearText(secs), "⏱ "))
		return nil
	case "poll":
		fs := flags("narada whatsapp poll", "narada whatsapp poll <to> <question> <option> <option>... [--multi] [--at WHEN]\n\nAsk a poll in a chat: 2 to 12 options. --multi lets people pick several.\nThe votes show in the poll's message as they come.")
		multi := fs.Bool("multi", false, "people may pick several options")
		at := fs.String("at", "", "send later")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) < 4 {
			return usageError("whatsapp poll needs who to ask, a question and at least two options")
		}
		ctx, cancel := ctxSignal()
		defer cancel()
		startWhatsApp(ctx, a)
		sp, err := a.WhatsAppChat(ctx, pos[0])
		if err != nil {
			return err
		}
		return waAct(a, func() (store.OutboxItem, error) {
			if *at != "" {
				return store.OutboxItem{}, usageError("--at: polls go now (schedule a message instead)")
			}
			return a.PollWhatsApp(sp, pos[1], pos[2:], *multi, app.OriginYou)
		})
	case "vote":
		fs := flags("narada whatsapp vote", "narada whatsapp vote <poll-message-id> [option...]\n\nVote in a poll: options by number (from 1) or by their text. No option takes\nyour vote back. whatsapp read shows the poll and its id.")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) < 1 {
			return usageError("whatsapp vote needs the poll's message id")
		}
		return waAct(a, func() (store.OutboxItem, error) { return a.VoteWhatsApp(pos[0], pos[1:], app.OriginYou) })
	case "edit":
		fs := flags("narada whatsapp edit", "narada whatsapp edit <message-id> <new text...>\n\nChange the text of a message you sent (whatsapp read shows the ids). WhatsApp\nallows it for 15 minutes after sending.")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) < 2 {
			return usageError("whatsapp edit needs a message id and the new text")
		}
		return waAct(a, func() (store.OutboxItem, error) {
			return a.EditWhatsApp(pos[0], strings.Join(pos[1:], " "), app.OriginYou)
		})
	case "delete", "rm":
		fs := flags("narada whatsapp delete", "narada whatsapp delete <message-id> [--for-me]\n\nDelete a message you sent, for everyone (WhatsApp allows it for about two and\na half days). --for-me removes any message from this machine only.")
		forMe := fs.Bool("for-me", false, "only here: nobody else is told")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usageError("whatsapp delete needs one message id")
		}
		if *forMe {
			if _, err := a.DeleteWhatsApp(pos[0], false, app.OriginYou); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "narada: deleted for you (here)")
			return nil
		}
		return waAct(a, func() (store.OutboxItem, error) { return a.DeleteWhatsApp(pos[0], true, app.OriginYou) })
	case "forward", "fwd":
		fs := flags("narada whatsapp forward", "narada whatsapp forward <message-id> <to>\n\nForward a message (its text, photo, video, voice note or document) to another\nchat: a contact, a group or a phone number. It shows as forwarded.")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) < 2 {
			return usageError("whatsapp forward needs a message id and who to send it to")
		}
		ctx, cancel := ctxSignal()
		defer cancel()
		startWhatsApp(ctx, a)
		sp, err := a.WhatsAppChat(ctx, strings.Join(pos[1:], " "))
		if err != nil {
			return err
		}
		return waAct(a, func() (store.OutboxItem, error) { return a.ForwardWhatsApp(pos[0], sp, app.OriginYou) })
	case "send":
		fs := flags("narada whatsapp send", "narada whatsapp send <to> [text...] [--attach FILE]... [--voice FILE]... [--reply-to MSG] [--location LAT,LON [--place NAME]] [--contact WHO] [--at WHEN] [--now] [--link]\n\nSend a WhatsApp message (text from stdin when not given, with files too). <to> is a contact,\na chat or a phone number with country code. Waits chat_undo_seconds first.\n--attach sends a file (repeat for more; up to 2 GB each): each is its own\nmessage, the text its first one's caption. --voice sends an audio file as a\nvoice note (made Ogg Opus with ffmpeg when it isn't).\n\n--link (or when WhatsApp isn't linked) opens WhatsApp's click-to-chat page\nwith the message typed in instead: you press Send there. <to> is then a phone\nnumber.")
		nowFlag := fs.Bool("now", false, "skip the undo window")
		link := fs.Bool("link", false, "open the click-to-chat page instead of sending")
		at := fs.String("at", "", "send later: \"tomorrow 9am\", \"in 2h\"...")
		replyTo := fs.String("reply-to", "", "a message id to reply to, quoting it (a group's message, sent to a person: a private reply)")
		location := fs.String("location", "", "share a place instead: latitude,longitude (18.52,73.85)")
		place := fs.String("place", "", "with --location: what the place is called")
		contact := fs.String("contact", "", "share a contact card instead: a contact's name or a phone number (the text is then its name)")
		var attach, voice listFlag
		fs.Var(&attach, "attach", "a file to send (repeat for more)")
		fs.Var(&voice, "voice", "an audio file to send as a voice note (repeat for more)")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) < 1 {
			return usageError("whatsapp send needs who to send to")
		}
		attach = append(attach, voice...)
		text := strings.Join(pos[1:], " ")
		switch {
		case text != "" || *location != "" || *contact != "":
		case len(attach) > 0:
			text = captionFromStdin(os.Stdin) // a file can go without text: don't wait for ever
		case !term.IsTerminal(int(os.Stdin.Fd())):
			b, _ := io.ReadAll(os.Stdin)
			text = strings.TrimSpace(string(b))
		}
		if _, linked := a.Store.WhatsApp(); (*link || !linked) && len(attach) > 0 {
			return usageError("files go only through a linked WhatsApp (narada whatsapp link), not the click-to-chat page")
		}
		if _, linked := a.Store.WhatsApp(); *link || !linked {
			u := app.WhatsAppLink(pos[0], text)
			if err := app.OpenURL(u); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "narada: opened %s: press Send in WhatsApp\n", u)
			return nil
		}
		ctx, cancel := ctxSignal()
		defer cancel()
		online := startWhatsApp(ctx, a)
		sp, err := a.WhatsAppChat(ctx, pos[0])
		if err != nil {
			return err
		}
		if *nowFlag {
			a.Config.ChatUndoSeconds = 0
		}
		if *location != "" {
			l, err := app.ParseLocation(*location)
			if err != nil {
				return usageError("%v", err)
			}
			l.Name = *place
			it, err := a.ShareLocationWhatsApp(sp, l, app.OriginYou)
			if err != nil {
				return err
			}
			return waitSend(a, it)
		}
		if *contact != "" {
			it, err := a.ShareContactWhatsApp(ctx, sp, *contact, text, app.OriginYou)
			if err != nil {
				return err
			}
			return waitSend(a, it)
		}
		out := store.Outgoing{Space: sp.Name, SpaceName: SpaceTitle(sp), Body: text, Quote: *replyTo}
		if *replyTo != "" {
			if _, err := a.Store.ChatMessage(store.WhatsAppAccount, *replyTo); err != nil {
				return fmt.Errorf("no message %s here (whatsapp read shows the ids)", *replyTo)
			}
		}
		for i, p := range attach {
			out.Files = append(out.Files, store.File{Source: p, Voice: i >= len(attach)-len(voice)})
		}
		if err := scheduleFlag(&out, *at); err != nil {
			return err
		}
		it, err := a.Queue(sp.Account, "whatsapp", out, app.OriginYou)
		if err != nil {
			return err
		}
		if !online && !a.DaemonRunning() {
			fmt.Fprintln(os.Stderr, "narada: WhatsApp is held by another narada (the screen?); it sends this.")
		}
		return waitSend(a, it)
	}
	return usageError("unknown whatsapp command %q", sub)
}

const groupUsage = `narada whatsapp group <what> ...

  new <name> <person>...          make a group (people: names, numbers or ids)
  info <group> [--json]           who's in it, its admins and description
  rename <group> <name...>        rename it for everyone
  about <group> <text...>         set its description
  add|remove <group> <person>...  add or remove people (you must be an admin)
  promote|demote <group> <person>...  make people admins, or not
  link <group> [--reset]          its invite link (--reset: a new one; the old stops)
  join <link>                     join a group by its invite link
  leave <group>                   leave it`

func waGroup(a *app.App, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(groupUsage)
		if len(args) == 0 {
			return usageError("whatsapp group what?")
		}
		return nil
	}
	sub, rest := args[0], args[1:]
	fs := flags("narada whatsapp group "+sub, groupUsage)
	reset := fs.Bool("reset", false, "link: make a new link")
	asJSON := fs.Bool("json", false, "info: JSON output")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	need := map[string]int{"new": 2, "info": 1, "rename": 2, "about": 2, "add": 2, "remove": 2, "promote": 2, "demote": 2, "link": 1, "join": 1, "leave": 1}[sub]
	if need == 0 {
		return usageError("unknown whatsapp group command %q\n\n%s", sub, groupUsage)
	}
	if len(pos) < need {
		return usageError("whatsapp group %s needs more\n\n%s", sub, groupUsage)
	}
	ctx, cancel := ctxSignal()
	defer cancel()
	startWhatsApp(ctx, a)
	if sub == "new" {
		people, err := a.WhatsAppIDs(ctx, pos[1:])
		if err != nil {
			return err
		}
		sp, err := a.NewWhatsAppGroup(ctx, pos[0], people)
		if err != nil {
			return err
		}
		fmt.Printf("narada: made %s [%s]\n", SpaceTitle(sp), sp.Name)
		return nil
	}
	if sub == "join" {
		sp, err := a.JoinWhatsAppGroup(ctx, pos[0])
		if err != nil {
			return err
		}
		fmt.Printf("narada: joined %s [%s]\n", SpaceTitle(sp), sp.Name)
		return nil
	}
	g, err := a.WhatsAppChat(ctx, pos[0])
	if err != nil {
		return err
	}
	rest = pos[1:]
	switch sub {
	case "info":
		info, err := a.WhatsAppGroupInfo(ctx, g.Name)
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(info)
		}
		fmt.Printf("%s  [%s]\n", info.Name, info.JID)
		if info.About != "" {
			fmt.Printf("\n%s\n", info.About)
		}
		fmt.Printf("\n%d people:\n", len(info.People))
		for _, p := range info.People {
			name, _, _ := a.Store.User(store.WhatsAppAccount, p.JID)
			if wa, _ := a.Store.WhatsApp(); p.JID == wa.ChatUser {
				name = "You"
			}
			role := ""
			switch {
			case p.Owner:
				role = "  (owner)"
			case p.Admin:
				role = "  (admin)"
			}
			fmt.Printf("  %-28s +%s%s\n", strings.TrimPrefix(name, "~"), strings.TrimSuffix(p.JID, "@s.whatsapp.net"), role)
		}
		return nil
	case "rename":
		err = a.RenameWhatsAppGroup(ctx, g.Name, strings.Join(rest, " "))
	case "about":
		err = a.DescribeWhatsAppGroup(ctx, g.Name, strings.Join(rest, " "))
	case "add", "remove", "promote", "demote":
		var people []string
		if people, err = a.WhatsAppIDs(ctx, rest); err == nil {
			err = a.ChangeWhatsAppGroup(ctx, g.Name, sub, people)
		}
	case "link":
		link, err := a.WhatsAppGroupLink(ctx, g.Name, *reset)
		if err != nil {
			return err
		}
		fmt.Println(link)
		return nil
	case "leave":
		err = a.LeaveWhatsAppGroup(ctx, g.Name)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "narada: %s: done\n", SpaceTitle(g))
	return nil
}

// captionWait is how long a file's send waits for text on stdin to start.
var captionWait = 2 * time.Second

// captionFromStdin is text piped in for a file's caption: "" from a
// terminal, an empty stdin, or a pipe that stays silent (a parent process
// that left one open must not hang the send). Once text starts, it's read
// to the end.
func captionFromStdin(in *os.File) string {
	if term.IsTerminal(int(in.Fd())) {
		return ""
	}
	type got struct {
		b []byte
	}
	first := make(chan got, 1)
	go func() {
		b := make([]byte, 4096)
		n, _ := in.Read(b)
		first <- got{b[:n]}
	}()
	select {
	case g := <-first:
		if len(g.b) == 0 {
			return ""
		}
		rest, _ := io.ReadAll(in)
		return strings.TrimSpace(string(g.b) + string(rest))
	case <-time.After(captionWait):
		fmt.Fprintln(os.Stderr, "narada: nothing came on stdin: sending the file without text")
		return ""
	}
}

// waAct queues a change to a message and waits for it to go out, as a
// send does.
func waAct(a *app.App, queue func() (store.OutboxItem, error)) error {
	ctx, cancel := ctxSignal()
	defer cancel()
	startWhatsApp(ctx, a)
	it, err := queue()
	if err != nil {
		return err
	}
	return waitSend(a, it)
}

// startWhatsApp goes online for this command when no other narada holds
// WhatsApp (they deliver its sends otherwise); it waits briefly for the
// connection.
func startWhatsApp(ctx context.Context, a *app.App) bool {
	ok, err := a.StartWhatsApp(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "narada: WhatsApp:", err)
	}
	return ok
}
