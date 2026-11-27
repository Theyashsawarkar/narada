package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/fake"
	"github.com/Theyashsawarkar/narada/internal/google"
	"github.com/Theyashsawarkar/narada/internal/mcpserver"
	"github.com/Theyashsawarkar/narada/internal/sock"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// ------------------------------------------------------------------- auth

func auth(a *app.App, args []string) error {
	if len(args) == 0 {
		return usageError("auth what? setup <client.json>, add <name>, list, remove <name>")
	}
	switch args[0] {
	case "setup":
		if len(args) != 2 {
			return usageError("auth setup <client.json>: the Desktop app OAuth client from the Cloud console")
		}
		b, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		if err := a.InstallClient(b); err != nil {
			return fmt.Errorf("%s: %w", args[1], err)
		}
		fmt.Printf("OAuth client saved to %s.\nNext: narada auth add work\n", a.Paths.ClientFile())
		return nil
	case "add", "login":
		fs := flags("narada auth add", "narada auth add <name> [--email you@company.com] [--no-chat]\n\nSigns in a Google account in your browser. --no-chat for a personal Gmail\n(Google Chat's API needs a Workspace account).")
		email := fs.String("email", "", "prefill the account on Google's page")
		noChat := fs.Bool("no-chat", false, "mail only")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usageError("auth add needs a name, like work or personal")
		}
		name := pos[0]
		if name == store.WhatsAppAccount {
			return usageError("%q is WhatsApp's: narada whatsapp link links WhatsApp", name)
		}
		chat := !*noChat
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		err = a.SignIn(ctx, name, *email, chat, func(url string) {
			fmt.Printf("Opening Google's sign-in page. If it doesn't open, visit:\n\n  %s\n\nWaiting...\n", url)
		})
		if err != nil {
			return err
		}
		fmt.Printf("Signed in as %q. First sync (last %d days)...\n", name, a.Config.InitialDays)
		sctx, scancel := ctxSignal()
		defer scancel()
		if _, err := a.SyncMail(sctx, name); err != nil {
			return fmt.Errorf("signed in, but the first sync failed: %w", err)
		}
		if chat {
			if _, err := a.SyncChat(sctx, name); err != nil {
				fmt.Fprintf(os.Stderr, "narada: mail works; chat failed: %v\n  (a personal Gmail has no Chat API: narada auth add %s --no-chat)\n", err, name)
			}
		}
		acc, _ := a.Store.Account(name)
		fmt.Printf("Done: %s. Open it with `narada`.\n", acc.Email)
		return nil
	case "list", "ls":
		accs, err := a.Store.Accounts()
		if err != nil {
			return err
		}
		if len(accs) == 0 {
			fmt.Println("no accounts: narada auth add work")
		}
		for _, x := range accs {
			chat := "mail+chat"
			if !x.Chat {
				chat = "mail"
			}
			synced := "never synced"
			if x.MailSynced > 0 {
				synced = "synced " + When(x.MailSynced)
			}
			fmt.Printf("%-10s %-32s %-9s %s\n", x.Name, x.Email, chat, synced)
		}
		return nil
	case "remove", "rm", "logout":
		if len(args) != 2 {
			return usageError("auth remove <name>")
		}
		if _, err := a.Store.Account(args[1]); err != nil {
			return err
		}
		a.Tokens.Delete(args[1])
		if err := a.Store.RemoveAccount(args[1]); err != nil {
			return err
		}
		fmt.Printf("Removed %s and its local copy. (Revoke access at https://myaccount.google.com/permissions too.)\n", args[1])
		return nil
	}
	return usageError("unknown auth command %q", args[0])
}

// ----------------------------------------------------------------- daemon

func daemon(a *app.App, args []string) error {
	fs := flags("narada daemon", "narada daemon\n\nThe background sync: mail every mail_interval, chat every chat_interval,\nsends the outbox, notifies about new mail and DMs. Logs to stderr.")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	// SIGTERM ends ctx: no new syncs or agent sessions. life goes on
	// through the drain, so calls still running (a send in its undo
	// window, WhatsApp delivering it) can finish.
	ctx, cancel := ctxSignal()
	defer cancel()
	life, endLife := context.WithCancel(context.Background())
	defer endLife()
	log.SetFlags(0)
	// a soft cap: the GC works harder instead of growing (a first sync
	// of hundreds of HTML mails once peaked near 400 MB)
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(128 << 20)
	}
	release, ok := a.HoldDaemonLock()
	if !ok {
		return errors.New("another narada daemon is already running")
	}
	defer release()
	a.SetDaemon()
	// the bar watches this small file, not the database
	a.PublishCounts(func() any { return GetCounts(a) })
	log.Printf("narada %s daemon: mail every %s, chat every %s", app.Version, a.Config.MailInterval.Duration, a.Config.ChatInterval.Duration)
	// agents: each `narada mcp` is pranali, a pipe to this socket
	served := make(chan struct{})
	if l, err := sock.Listen(sock.Path()); err != nil {
		close(served)
		log.Printf("mcp socket: %v (agents run narada in-process)", err)
	} else {
		how := "bound here"
		if l.Activated {
			how = "from systemd"
		}
		log.Printf("agents: %s (%s)", l.Path, how)
		go func() {
			defer close(served)
			if err := mcpserver.Serve(ctx, l, a, MCPHelpers(), mcpserver.ServeOptions{Life: life, Kick: a.Wake}); err != nil {
				log.Printf("mcp socket %s: %v", l.Path, err)
			}
		}()
	}
	// on the way out: the drain first, then what it needed
	defer func() { cancel(); <-served; endLife() }()
	type backoff struct {
		next  time.Time
		delay time.Duration
	}
	mailNext, chatNext := map[string]*backoff{}, map[string]*backoff{}
	step := func(m map[string]*backoff, acc string, every time.Duration, f func(context.Context, string) ([]app.News, error)) {
		b := m[acc]
		if b == nil {
			b = &backoff{}
			m[acc] = b
		}
		if time.Now().Before(b.next) {
			return
		}
		sctx, scancel := context.WithTimeout(ctx, 5*time.Minute)
		news, err := f(sctx, acc)
		scancel()
		if err != nil {
			b.delay = min(max(b.delay*2, every), 15*time.Minute)
			b.next = time.Now().Add(b.delay)
			log.Printf("%s: %v (retry in %s)", acc, err, b.delay)
		} else {
			b.delay = 0
			b.next = time.Now().Add(every)
		}
		if a.Config.Notify {
			for _, n := range news {
				app.Notify(n.Title, n.Body)
			}
		}
	}
	// sends go out on their own clock (a slow sync never delays a
	// scheduled message or the end of an undo window), woken by whatever
	// queues one, here or in another narada, and by the next send_at:
	// no ticking
	go a.RunDeliverer(life, func(sent []store.OutboxItem) {
		for _, it := range sent {
			if it.Status == store.StatusFailed {
				log.Printf("send #%d failed: %s", it.ID, it.Error)
				if a.Config.Notify {
					app.Notify("narada: send failed", it.Error)
				}
			}
		}
	})
	// the syncs sleep until the next one is due (at most 30 s, so an
	// account added meanwhile and WhatsApp's takeover aren't missed)
	wake := time.NewTimer(0)
	defer wake.Stop()
	<-wake.C
	var waNext time.Time
	for {
		// WhatsApp is a connection, not a sync: go online when linked
		// (and take it over when the screen that held it quits)
		if !time.Now().Before(waNext) {
			waNext = time.Now().Add(30 * time.Second)
			if _, linked := a.Store.WhatsApp(); linked && !a.WhatsAppOnline() {
				if ok, err := a.StartWhatsApp(life); err != nil {
					log.Printf("whatsapp: %v", err)
				} else if ok {
					log.Print("whatsapp: online")
				}
			}
		}
		next := waNext
		for _, acc := range a.AccountNames() {
			step(mailNext, acc, a.Config.MailInterval.Duration, a.SyncMail)
			step(chatNext, acc, a.Config.ChatInterval.Duration, a.SyncChat)
			for _, b := range []*backoff{mailNext[acc], chatNext[acc]} {
				if b != nil && b.next.Before(next) {
					next = b.next
				}
			}
		}
		wake.Reset(max(time.Until(next), 100*time.Millisecond))
		select {
		case <-ctx.Done():
			log.Print("stopping")
			return nil
		case <-wake.C:
		}
	}
}

func service(a *app.App, args []string) error {
	if len(args) == 0 {
		return usageError("service install, remove or status")
	}
	sc := func(args ...string) error {
		cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	switch args[0] {
	case "install":
		if err := app.InstallService(); err != nil {
			return err
		}
		fmt.Printf("Installed %s and %s, and started them.\nLogs: journalctl --user -u narada -f\n", app.UnitPath(), app.SocketPath())
		return nil
	case "remove", "uninstall":
		_ = sc("disable", "--now", "narada-mcp.socket", "narada.service")
		for _, p := range []string{app.SocketPath(), app.UnitPath()} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		_ = sc("daemon-reload")
		fmt.Println("Removed the background sync.")
		return nil
	case "status":
		return sc("status", "--no-pager", "narada-mcp.socket", "narada.service")
	}
	return usageError("unknown service command %q", args[0])
}

// -------------------------------------------------------------------- mcp

func mcpCmd(args []string, in io.ReadCloser, out io.Writer) error {
	fs := flags("narada mcp", "narada mcp [--in-process]\n\nThe MCP server (stdio) for Claude Code and other agents:\n  claude mcp add --scope user narada -- narada mcp\nWith pranali on PATH this becomes `pranali narada`, a pipe to the daemon's\nsocket (it serves the tools in-process when no daemon answers); without\npranali it serves the tools itself.")
	inProcess := fs.Bool("in-process", false, "serve the tools in this process even when the daemon runs")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if !*inProcess && in == os.Stdin {
		if p, err := exec.LookPath("pranali"); err == nil {
			argv, env := pranaliArgs(app.Executable(), os.Environ())
			err := syscall.Exec(p, argv, env)
			fmt.Fprintf(os.Stderr, "narada: can't start %s (%v); serving the tools in-process\n", p, err)
		}
	}
	a, err := app.Open()
	if err != nil {
		return err
	}
	defer a.Close()
	a.PublishCounts(func() any { return GetCounts(a) })
	ctx, cancel := ctxSignal()
	defer cancel()
	err = mcpserver.Run(ctx, a, MCPHelpers(), in, nopWriteCloser{out})
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return nil // the client closed us: a normal end
	}
	return err
}

// pranaliArgs is how `narada mcp` becomes pranali: `pranali narada
// --fallback <this narada> mcp --in-process`. Under NARADA_HOME (tests,
// demos) the daemon's socket isn't where pranali looks, so PRANALI_SOCKET
// says where it is (unless already set).
func pranaliArgs(self string, environ []string) (argv, env []string) {
	argv = []string{"pranali", "narada", "--fallback", self, "mcp", "--in-process"}
	env = environ
	if os.Getenv("NARADA_HOME") != "" && os.Getenv("PRANALI_SOCKET") == "" {
		env = append(append([]string{}, environ...), "PRANALI_SOCKET="+sock.Path())
	}
	return argv, env
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// MCPHelpers hands the CLI's lookups and formatting to the MCP server.
func MCPHelpers() mcpserver.Helpers {
	return mcpserver.Helpers{
		ListThreads: listThreads, Search: search, FormatThread: FormatThread, ReplyTo: ReplyTo, FindSpace: FindSpace,
		SpaceTitle: SpaceTitle, When: When, Counts: func(a *app.App) any { return GetCounts(a) },
	}
}

// ------------------------------------------------------------------- demo

func demo(args []string, tui func(*app.App, string) error) error {
	fs := flags("narada demo", "narada demo [--server ADDR]\n\nThe screen on made-up mail and chat, served by a fake Google inside this\nprocess. Nothing touches your accounts; everything is gone when you quit.\n--server only runs the fake Google (for development): point other narada\ncommands at it with the NARADA_HOME and NARADA_GOOGLE_URL it prints.")
	serve := fs.String("server", "", "run only the fake Google on this address, e.g. 127.0.0.1:8765")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if *serve != "" {
		g := fake.New()
		g.Seed()
		home, err := os.MkdirTemp("", "narada-dev-")
		if err != nil {
			return err
		}
		fmt.Printf("export NARADA_HOME=%s NARADA_GOOGLE_URL=http://%s\nthen: narada auth add demo && narada\n", home, *serve)
		return (&http.Server{Addr: *serve, Handler: g, ReadHeaderTimeout: 10 * time.Second}).ListenAndServe()
	}
	home, err := os.MkdirTemp("", "narada-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	// a closed terminal or tmux kill: still leave nothing behind
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGTERM)
	go func() { <-sig; os.RemoveAll(home); os.Exit(1) }()
	g := fake.New()
	g.Seed()
	srv := httptest.NewServer(g)
	defer srv.Close()
	os.Setenv("NARADA_HOME", home)
	os.Setenv("NARADA_GOOGLE_URL", srv.URL)
	a, err := app.Open()
	if err != nil {
		return err
	}
	defer a.Close()
	if err := a.Store.AddAccount(store.Account{Name: "demo", Chat: true}); err != nil {
		return err
	}
	w := fake.NewWhatsApp()
	w.SeedWhatsApp()
	w.SeedStatuses()
	w.Receipts = true
	a.Config.ChatUndoSeconds = max(a.Config.ChatUndoSeconds, 5) // to show the countdown
	a.DialWhatsApp = func(ctx context.Context) (app.WhatsAppConn, error) { return w.Conn(ctx) }
	if os.Getenv("NARADA_DEMO_UNLINKED") != "" {
		// for trying the WhatsApp tab's linking: the "phone" scans after 6 s
		c, _ := w.Conn(context.Background())
		_ = c.Logout(context.Background())
		w.LinkDelay = 6 * time.Second
	} else if err := a.Store.AddWhatsApp("+919876500000", w.Me); err != nil {
		return err
	}
	if _, errs := a.SyncAll(context.Background()); len(errs) > 0 {
		for _, e := range errs {
			return e
		}
	}
	// who's online: Amma is, Ravi was three hours ago
	_ = a.Store.SetPresence("919800000001@s.whatsapp.net", true, 0)
	_ = a.Store.SetPresence("919800000002@s.whatsapp.net", false, time.Now().Add(-3*time.Hour).UnixMilli())
	return tui(a, "demo")
}

// ----------------------------------------------------------------- doctor

func doctor(a *app.App, args []string) error {
	fs := flags("narada doctor", "narada doctor\n\nChecks the OAuth client, each account's sign-in and sync, the background\nservice, the tmux plugin and the Claude Code MCP server.")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	bad := 0
	okf := func(f string, x ...any) { fmt.Printf("  \033[32m✓\033[0m "+f+"\n", x...) }
	warn := func(f string, x ...any) { fmt.Printf("  \033[33m!\033[0m "+f+"\n", x...) }
	fail := func(f string, x ...any) { bad++; fmt.Printf("  \033[31m✗\033[0m "+f+"\n", x...) }
	look := func(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

	fmt.Printf("narada %s\n", app.Version)
	fmt.Println("Setup")
	if _, err := os.Stat(a.Paths.ConfigFile()); err == nil {
		okf("config: %s", a.Paths.ConfigFile())
	} else {
		okf("config: defaults (narada config > %s to change them)", a.Paths.ConfigFile())
	}
	if google.FakeURL() != "" {
		warn("NARADA_GOOGLE_URL is set: talking to a fake Google at %s", google.FakeURL())
	} else if _, err := google.LoadClient(a.Paths.ClientFile(), nil); err != nil {
		fail("%v", err)
	} else {
		okf("OAuth client: %s", a.Paths.ClientFile())
	}
	if look("secret-tool") {
		okf("keyring: secret-tool")
	} else {
		warn("no secret-tool: tokens are kept in private files under %s (install libsecret)", a.Paths.Config)
	}
	accs, _ := a.Store.Accounts()
	fmt.Println("Accounts")
	if len(accs) == 0 {
		fail("none: narada auth add work")
	}
	for _, x := range accs {
		where := a.Tokens.Where(x.Name)
		switch {
		case google.FakeURL() != "":
		case where == "":
			fail("%s: not signed in (narada auth add %s)", x.Name, x.Name)
			continue
		case where == "file":
			warn("%s: token in a private file, not the keyring", x.Name)
		}
		if x.LastError != "" {
			fail("%s: last sync failed: %s", x.Name, x.LastError)
		} else if x.MailSynced == 0 {
			warn("%s: never synced (narada sync)", x.Name)
		} else {
			age := time.Since(time.UnixMilli(x.MailSynced)).Round(time.Second)
			if age > 10*time.Minute {
				warn("%s (%s): last synced %s ago; is the background sync running?", x.Name, x.Email, age)
			} else {
				okf("%s (%s): synced %s ago", x.Name, x.Email, age)
			}
		}
	}
	fmt.Println("Integrations")
	if look("systemctl") {
		out, _ := exec.Command("systemctl", "--user", "is-active", "narada.service").Output()
		if strings.TrimSpace(string(out)) == "active" {
			okf("background sync: running (narada.service)")
		} else {
			warn("background sync: not running (narada service install)")
		}
		out, _ = exec.Command("systemctl", "--user", "is-active", "narada-mcp.socket").Output()
		if strings.TrimSpace(string(out)) == "active" {
			okf("agents' socket: listening (narada-mcp.socket)")
		} else {
			warn("agents' socket: not set up (narada service install adds narada-mcp.socket)")
		}
	}
	if look("pranali") {
		okf("pranali: agents pipe to the daemon")
	} else {
		warn("no pranali on PATH: each agent's narada mcp runs a whole narada (about 30 MB)")
	}
	if os.Getenv("TMUX") != "" {
		out, _ := exec.Command("tmux", "list-keys", "-T", "prefix").Output()
		if strings.Contains(string(out), "narada") {
			okf("tmux: popup key bound")
		} else {
			warn("tmux: no narada key (add set -g @plugin 'Theyashsawarkar/narada')")
		}
	}
	if look("claude") {
		home, _ := os.UserHomeDir()
		b, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
		if strings.Contains(string(b), `"narada"`) {
			okf("Claude Code: narada MCP server added")
		} else {
			warn("Claude Code: run `claude mcp add --scope user narada -- narada mcp`")
		}
	} else {
		warn("no claude on PATH: @, S, D and T in the screen need it (or set ai_command)")
	}
	if look("notify-send") {
		okf("notifications: notify-send")
	} else {
		warn("no notify-send: no desktop notifications")
	}
	if items, _ := a.Store.Outbox(store.StatusApproval); len(items) > 0 {
		warn("%d send(s) waiting for your approval: narada approve", len(items))
	}
	if bad > 0 {
		return exitError{1, fmt.Sprintf("%d problem(s)", bad)}
	}
	return nil
}
