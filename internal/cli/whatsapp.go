package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
	"golang.org/x/term"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/store"
)

func whatsApp(a *app.App, args []string) error {
	if len(args) == 0 {
		return usageError("whatsapp what? link, unlink, list, read, send")
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
			if !*unread || s.Unread > 0 {
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
			fmt.Printf("%s %-8s %-24.24s %s  [%s]\n", n, When(s.LastActive), SpaceTitle(s), oneLine(s.LastText, 60), s.Name)
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
			fmt.Printf("%s  %s\n%s\n\n", who, When(m.Created), m.Text)
		}
		return nil
	case "send":
		fs := flags("narada whatsapp send", "narada whatsapp send <to> [text...] [--attach FILE]... [--at WHEN] [--now] [--link]\n\nSend a WhatsApp message (text from stdin when not given). <to> is a contact,\na chat or a phone number with country code. Waits chat_undo_seconds first.\n--attach sends a file (repeat for more): each is its own message, the text\nits first one's caption.\n\n--link (or when WhatsApp isn't linked) opens WhatsApp's click-to-chat page\nwith the message typed in instead: you press Send there. <to> is then a phone\nnumber.")
		nowFlag := fs.Bool("now", false, "skip the undo window")
		link := fs.Bool("link", false, "open the click-to-chat page instead of sending")
		at := fs.String("at", "", "send later: \"tomorrow 9am\", \"in 2h\"...")
		var attach listFlag
		fs.Var(&attach, "attach", "a file to send (repeat for more)")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) < 1 {
			return usageError("whatsapp send needs who to send to")
		}
		text := strings.Join(pos[1:], " ")
		if text == "" && len(attach) == 0 && !term.IsTerminal(int(os.Stdin.Fd())) {
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
		out := store.Outgoing{Space: sp.Name, SpaceName: SpaceTitle(sp), Body: text}
		for _, p := range attach {
			out.Files = append(out.Files, store.File{Source: p})
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
