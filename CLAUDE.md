# narada -- notes for agents

Gmail, Google Chat and WhatsApp in the terminal: one Go binary
(`cmd/narada`) with a Bubble Tea screen, a CLI, an MCP server and a sync
daemon, all over one local SQLite copy. Usable as the `narada` command
(install.sh, make install, packaging/arch, kosha) and as a TPM plugin
(`narada.tmux` binds a popup key and `#{narada_status}`). It is pulled
into Vayu (github.com/Theyashsawarkar/vayu) as a tmux plugin, but must work
on any Linux setup without Vayu: no hardcoded paths, Vayu-specific
behaviour only through the `@narada-*` options and config.toml.

- Layout: `internal/app` is the core every front end shares (sync, mail
  actions, the outbox and send policy); `internal/store` the SQLite schema;
  `internal/google` OAuth and the REST clients; `internal/cli` the commands
  (and the editor template, reply/forward helpers the screen reuses);
  `internal/tui` the screen; `internal/mcpserver` the agent tools;
  `internal/whatsapp` WhatsApp as a linked device (whatsmeow, ADR 5);
  `internal/fake` an in-memory Google and WhatsApp used by every test and
  `narada demo`. WhatsApp is the account `whatsapp` of kind `whatsapp`:
  `Store.Accounts()` is Google-only, so never hand it to Google code.
- WhatsApp is a connection one process holds (lock `whatsapp`); only that
  process delivers `whatsapp` outbox rows. Test with `fake.WhatsApp`
  (`App.DialWhatsApp`), never a real phone or account.
- Every send goes through `app.Queue` → outbox → `app.DeliverDue`. Never add
  a path that calls the Gmail or Chat send API directly. An agent's send
  must stay behind `needsApproval`; mail and chat text returned to agents
  stays inside `<untrusted>` blocks.
- **Every corner is rounded.** Boxes use `╭╮╰╯`. Anything drawn on a filled
  background (key caps, the mode pill, places, badges, chips, avatars) goes
  through `styles.round`, which gives it round ends; never `.Render` a style
  with a background straight to the screen. Only what can't be rounded
  (tmux's own pane borders, a full-screen window) is exempt.
- **A message you send shows once, at once, and never flickers.** From
  Enter it is in the chat as its pending copy (`showPending`, ◷); nothing
  else draws a second copy of it (the outbox lines skip sends already in the
  chat). When it goes out, `Store.ReplacePending` swaps the copy for the
  real message in one transaction, at the copy's time, so it never
  disappears, doubles or moves; after that only its ticks change (◷ → ✓ →
  ✓✓ → blue, or red ✗ when it fails). Never delete and re-insert a row the
  screen shows in separate steps. `TestSentMessageShowsOnceAndStaysPut`
  guards it.
- Keys live in `internal/tui/keys.go`. After adding, changing or removing
  one: `go test ./internal/tui -update` (rewrites `scripts/keybindings`,
  which Vayu's keybinding search reads), and update the README's key
  tables.
- The README documents every feature, key, option, command and dependency:
  a change to any of them updates it. Every change gets a CHANGELOG.md
  entry; releases are tags (`vX.Y.Z`) with `Version` in internal/app/app.go
  and `pkgver` in packaging/arch/PKGBUILD bumped to match.
- `make check` must pass (vet, gofmt, ShellCheck, tests, site check).
- Test against the fake Google (`NARADA_HOME`, `NARADA_GOOGLE_URL`), never
  a real account. Drive the screen in a throwaway tmux server:
  `tmux -L t -f /dev/null new -d -s o -x 150 -y 40 "env -u TMUX narada demo"`,
  then `tmux -L t send-keys -t o ...` / `capture-pane -p -t o`, and
  `tmux -L t kill-server` after. Never touch the user's live tmux or mail.
- The website (docs/, GitHub Pages) is generated: after changing README.md,
  CHANGELOG.md or docs/adr/, run `python3 scripts/build-site.py` and commit
  docs/ (CI checks it). A significant design decision gets a new ADR in
  docs/adr/ (next number; supersede, never rewrite).
