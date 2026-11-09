# Plan: narada, fully Vim

Status: **draft, for the user's review** · 2026-11-08 · becomes ADR 9 once agreed
(it supersedes parts of ADR 8: digits, `J`/`K`, the single-letter actions).

**This plan follows the family grammar** that every Vayu tool will share
(`~/Documents/mockups/vim-keys-roadmap.html`, §1–2):

- Reserved Space letters mean the same thing in every tool.
- narada's own groups use only the free letters `b c h i j l n p v w x z`.
- Destructive actions are `dd` or `Space d …` behind one confirm (`y` = yes).
- No single key is irreversible.
- Digits are counts.
- `?` is help, `H`/`L` are places, and `r` = reply is narada's documented exception.

The family plan is itself waiting on the user's decisions (its §9). Where it changes,
this plan follows.

## 1. What we want

- Keys that work like Vim's, so a Neovim hand never has to think: motions, counts,
  operators, `.`, `u` / `Ctrl+r`, marks, the jump list, `/ ? n N`, `:`.
- **Space is the leader, and everything that isn't Vim lives under it**, in
  mnemonic groups (`Space m` this message, `Space c` this chat, `Space g` go…), shown
  by a which-key popup, the way LazyVim, Doom and Spacemacs do it.
- **`Ctrl+j` / `Ctrl+k` scroll what's open**, down / up.
- **`:` opens a floating command bar in the middle of the screen**, with completion
  for commands and their arguments.
- **One source of truth**: an action registry and a keymap feed the keys, `?`, the
  which-key menu, `:` completion, `/`, the README and Vayu's keybinding search.

## 2. Where we are today (what the code does)

- `vim.go` sits **on top of** the old single-letter actions: `vimKey` translates
  (`dd` → old `e`, `u` → old `z`, `#` → old `d`) and `actKey` (model.go, ~50 `case`s)
  does the work. Every new key means another translation, and a key's meaning depends
  on which layer saw it first. This is the main thing to fix.
- Prefix keys exist (`g d y z` and Space with a 350 ms menu), VISUAL exists (`v`, `V`),
  `:` exists (a bottom line, prefix completion, 20 commands, in-memory history).
- No counts, no `.`, no redo, no marks, no jump list, no operator + motion
  (`dj`), no search inside a conversation.
- **Clashes with Vim today:** `1`–`4` are places (in Vim they're counts), `?` is the
  key sheet (in Vim it's search backward), `n`/`N` jump to unread (in Vim it's the next
  match), `J`/`K` scroll by a line, `*` stars, `p` pins, `x` is a leftover, and about 25
  bare letters (`L U p Z X I B b t W + - A S D T @ Alt+r`…) aren't Vim at all.
- Done today: **`Ctrl+k` no longer opens the search**, only `/` does.

## 3. What other tools taught us (research summary)

- **Leader letters people already know** (LazyVim, Doom, Spacemacs): `f` find,
  `s` search, `b` buffers, `w` windows, `q` quit, `h` help, `t` toggles, `SPC SPC` the
  main picker, `SPC /` search, `SPC ,` switch, `SPC TAB` last one, `SPC :` commands,
  `SPC m` "actions for what you're in" (Doom's local leader), `SPC o` open. Two keys
  after Space is the norm; three only for sub-groups.
- **Which-key**: popup after ~200–400 ms; the prefix itself **waits for ever**
  (Spacemacs, Doom, Helix), so a slow hand never loses half a sequence; groups show as
  `+mail`; `Backspace` goes up a level, `Esc` cancels.
- **Vim mail and chat clients**: aerc uses `J`/`K` for next / previous message while
  reading, `zz zt zb`, `.` to repeat, and `rr` / `Rr` for reply variants; neomutt's
  vim-keys use `dd` and `gt`/`gT`; iamb (Matrix, built on Vim) puts message actions
  on `:` (`:reply :react :edit :download`) and `Enter` sends.
- **Terminal facts**:
  - `Ctrl+j` (0x0A) is distinct from `Enter` (0x0D). Bubble Tea v1.3.10 reports
    `ctrl+j`, and tmux passes it through, so it can be bound today.
  - `Ctrl+k` (0x0B) is distinct too.
  - **`Ctrl+i` is `Tab`, `Ctrl+m` is `Enter` and `Ctrl+[` is `Esc`.** They can't be
    told apart without the kitty protocol (Bubble Tea v2). So Vim's `Ctrl+i` (jump
    forward) needs another key while `Tab` switches sections.
- **Pitfalls**:
  - Space means page-down or mark in many TUIs. We move those jobs to `Ctrl+f`/`Ctrl+d`
    and `v`.
  - The leader must work only in NORMAL. In the message box, Space types a space and
    `Ctrl+k` deletes to the end of the line.
  - A key you might press by accident never destroys anything. Deleting for everyone
    keeps its second press.

## 4. The keymap

### 4.1 Bare keys in NORMAL: Vim's vocabulary only

**Moving**

| Key | Does | Vim's meaning it borrows |
|---|---|---|
| `j` / `k` (`{n}j`) | Next / previous thread, chat or (picking) message | down / up, with a count |
| `h` / `l` | To the list / to what's open | left / right |
| `gg` / `G` / `{n}G` | First / last / the n-th item | same |
| `H` / `L` | Previous / next place (Mail, Chat, WhatsApp, AI requests) | family grammar: tabs everywhere |
| `Ctrl+d` / `Ctrl+u` | What's open, half a page down / up | same |
| `Ctrl+f` / `Ctrl+b` | What's open, a page down / up | same |
| **`Ctrl+j` / `Ctrl+k`** | **What's open, scrolled 3 lines down / up** (`{n}Ctrl+j`; `scroll_lines` sets 3) | the user's choice; like telescope and fzf's next / previous |
| `Ctrl+e` / `Ctrl+y` | What's open, one line down / up | same |
| `zz` / `zt` / `zb` | The picked message to the middle / top / bottom | same |
| `J` / `K` | Next / previous message in the open thread or chat (opening the next thread at the end) | aerc's and tig's reading keys |
| `]]` / `[[` (and `Tab` / `Shift+Tab`) | Next / previous section | Vim's "sections" |
| `n` / `N` | Next / previous unread (while a search inside a conversation is on: its next / previous match, until `Esc` clears it) | family grammar; Vim's `n` after a search |
| `gt` / `gT` / `{n}gt` | Next / previous place (as `L` / `H`); `1gt` Mail, `2gt` Chat, `3gt` WhatsApp, `4gt` AI requests | tabs |
| `Ctrl+n` / `Ctrl+p` | Next / previous chat while staying in the conversation | kept |
| `gd` (or `%`) | Jump to the message a reply quotes | go to definition / matching pair |
| `Ctrl+o` / `]j` (`[j` = `Ctrl+o`) | Back / forward in the jump list (threads, chats, quoted jumps). `Ctrl+i` too, once the terminal can tell it from `Tab` | jump list |
| `m{a-z}` / `'{a-z}` | Mark this thread or chat / go back to it (kept across runs) | marks |
| `Enter` | Open | kept |
| `Esc` / `q` | Back a step (`q` at the top asks nothing and quits; `:q` too) | close the window |

**Searching**

| Key | Does |
|---|---|
| `/` | Find anything: actions, chats, people, messages, mail (as today; `Space f f` too) |
| `?` | Every key of this view (family grammar: help is `?`; `Space ?` too) |
| `Space f h` | Search **inside** the open thread or conversation; `n` / `N` step through the matches |
| `*` / `#` | Every message from this sender, forward / backward |

**Acting (Vim's verbs)**

| Key | Does | Borrows |
|---|---|---|
| `i` / `a` | Write: in a chat, a message; in mail, reply / reply to everyone | insert / append |
| `r` / `R` | Reply (in chat: to the picked message, quoting it) / reply to everyone (WhatsApp group: privately) | **the family's documented exception**: mail's `r` |
| `o` / `O` | A new mail or message here / a new conversation with someone | open a new line |
| `dd`, `d{motion}`, `{n}dd`, VISUAL `d` | Delete, behind **one confirm (`y` = yes, any other key = no)**: mail to the trash, a picked WhatsApp message for everyone (`Space d m` for you only). `dj` this and the next, `dG` to the end: one confirm for all | delete |
| `e` | Archive the thread or WhatsApp chat. **`u` undoes it, so one key is allowed** (the family rule is "no single key does something you can't undo"); also `Space m a` | — |
| `yy` / `y{motion}` / VISUAL `y` | Copy the message(s) / subjects | yank |
| `p` | Paste the clipboard into the message box (text, or an image as a file) | put |
| `~` | Toggle read / unread (also `Space m r` / `Space m u`) | toggle case |
| `u` / `Ctrl+r` | Undo / redo (archive, trash, read, star, label, mute, pin) | same |
| `.` | Repeat the last change on this item (`j.` archives the next, as in Vim) | dot repeat |
| `v` / `V` | VISUAL by item / the whole list; `gv` picks the last range again | same |
| `za` `zo` `zc` `zM` `zR` | Fold or unfold one message / all of them | folds |
| `zq` | Toggle the quoted text | narada's own, in the fold family |
| `gf` / `gF` / `gx` | Open / save files, open a link by its number | go to file / URL |
| `gi` | Back into the message box where you left your draft | insert where you stopped |
| `:` | The command bar (§5) | same |
| `Space` | The leader (§4.2) | |

**Safety exception** (as ADR 8): on an AI request only `y`, `e` and `n` answer it, and
`n` needs to be pressed twice. A send is never one key that might mean something else.

### 4.2 Everything else lives under Space

The which-key popup shows `+group` labels at the root, then the group's keys. Each
group shows only what works where you are. Others are dimmed with the reason.

**Reserved groups** (the same meaning in every tool):

| Group | narada's keys in it |
|---|---|
| `Space Space` | **The palette**: fuzzy over every action and every `:` command. `n` opens the next unread. |
| `Space ?` | Every key of this view (as `?`) |
| `Space q` | `q` close (quit at the top) |
| `Space t` +tabs | `tm` Mail · `tc` Chat · `tw` WhatsApp · `ta` AI requests |
| `Space u` +ui | `uf` fullscreen (zoom what's open) · `up` the preview pane · `uq` quoted text · `uz` fold all · `uc` compact list · `ui` pictures inline · `uw` wrap |
| `Space y` +copy | `yy` the message · `yl` the link · `ys` subject(s) · `yn` name / number |
| `Space f` +find | `ff` anything (`/`) · `fh` inside this conversation · `fp` a person · `fc` a chat · `fm` mail (Gmail's search words) · `fl` a link in what's open · `fa` an attachment · `fu` unread only (filter) |
| `Space s` +sort | `sd` by date · `ss` senders · `su` unread first (chat list) |
| `Space r` +refresh | `rr` sync now · `ro` load older mail (`r+` / `r-` how many) · `rw` reconnect WhatsApp · `rl` link WhatsApp (QR) · `rL` link with a code |
| `Space a` +add | `an` new mail or message · `ac` new conversation · `af` send a file · `ag` a section from this search |
| `Space d` +delete (always confirms) | `dd` this (as `dd`) · `dm` a picked message for you · `ds` the saved-search section · `dl` a label from the thread |
| `Space e` +edit | `ee` your picked message (WhatsApp: 15 min) · `er` rename the chat · `et` move a scheduled send · `ed` disappearing messages |
| `Space o` +open (outside narada) | `of` files · `oF` save files · `ol` a link · `ob` in the browser · `oi` the picture full size |
| `Space g` +go to | `gu` Unread · `gi` Inbox · `gs` Starred · `gS` Sent · `gd` Drafts · `gl` Scheduled · `gt` Status · `gr` Archived · `ga` account (next; `gA` all) · `g,` the last thread or chat |
| `Space m` +mark | `ma` archived · `ms` star · `ml` label · `mp` pin · `mr` read · `mu` unread · `mz` mute (8 h → week → always → off) · `mb` block |
| `Space ,` +settings | `,t` theme · `,s` send policy · `,n` older-mail count · `,k` the `[keys]` file |
| `Space k` +ask the AI | `kk` ask Claude · `ks` summary · `kd` draft a reply · `kt` today's digest |

**narada's own groups** (free letters):

| Group | Keys |
|---|---|
| `Space c` +chat (this chat) | `ci` group info · `cm` members (add, remove, promote) · `cl` invite link · `cv` vote in a poll · `cx` leave the group |
| `Space i` +item (this message or thread) | `if` forward · `ie` react · `ip` reply privately (WA group) · `iR` send a failed one again · `iy` copy its link |
| `Space w` +window | `wh` / `wl` focus list / open · `w=` reset · `ww` the other pane |
| `Space x` +AI requests | `xx` go to them · `xy` send the picked one · `xe` edit it first · `xn` decline (confirms) |

In the which-key popup, `Backspace` goes up a level and `Esc` cancels. A prefix waits
for ever, and the popup shows after 300 ms.

### 4.3 INSERT (the message box)

- Unchanged: `Enter` sends, `Alt+Enter` and **`Ctrl+j` start a new line**, and these
  keep their meaning: `Ctrl+w`, **`Ctrl+k` deletes to the end of the line**, `Ctrl+u`,
  `Ctrl+o` attaches, `Ctrl+v` pastes an image, `Ctrl+t` sends later, `Ctrl+r` records.
  The scroll keys are NORMAL only, so typing never scrolls.
- New (phase 6, `message_box_vim = true` by default): **`Esc` in the box goes to the
  box's own NORMAL**, keeping the cursor in your text:
  - Moving: `h l w b e 0 ^ $`, `gg G` across lines.
  - Editing: `x dw dd cw ciw C D`, `u Ctrl+r`, `p`.
  - Back to typing: `i a I A o`.
  - `Enter` still sends.
  - A second `Esc` (or `q`) leaves the box. Your text stays, as today.
- `Ctrl+x Ctrl+e` (or `Space e e` from the box's NORMAL) edits the message in `$EDITOR`
  (nvim) and brings it back, as the compose screen's `Ctrl+e` already does.

### 4.4 Lists inside popups (search, file picker, people, when)

`Ctrl+j` / `Ctrl+k` and `Ctrl+n` / `Ctrl+p` move. They already do. `Tab` keeps its
job, `Enter` picks, and `Esc` closes. The same rule applies everywhere.

## 5. The floating command bar (`:`)

- **Where it opens:**
  - In the middle of the screen: about 60% wide (at least 50 columns), a third of the
    way down.
  - A rounded frame in sky (the command colour) with `:` as its title. The screen
    behind stays drawn.
  - Opened from VISUAL, it starts with `'<,'>` (the picked range), as Vim does.
- **What it holds:**
  - The input line, with the rest of the best match in grey (ghost text).
  - Under it, up to 8 suggestions, one per line: the command, its arguments, what it
    does, and the key that does the same. Suggestions are fuzzy on names and aliases;
    exact prefixes come first.
- **Argument completion** (the command's own list):

  | Command | Completes |
  |---|---|
  | `:label` | Your Gmail labels |
  | `:account` | Your accounts |
  | `:go` | Places and sections |
  | `:chat` / `:to` | Chats / people |
  | `:later` | `tomorrow 9am`, `fri 6pm`, `in 2h`… |
  | `:react` | Emoji |
  | `:mute` | `8h 1w always off` |
  | `:set` | Options and their values |
  | `:help` | Keys and commands |
  | `:read` / `:unread` | — |

- **Keys:**
  - `Tab` / `Ctrl+n` / `Ctrl+j`: next suggestion. `Shift+Tab` / `Ctrl+p` / `Ctrl+k`:
    previous.
  - `→` or `Ctrl+e`: take the ghost text.
  - `Enter` runs the suggestion if one is picked, else what you typed.
  - `↑` / `↓` on an empty line: history, kept in `~/.local/state/narada/commands`.
  - `Ctrl+r`: search the history. `Esc` closes.
- **Commands:** every action gets one, named from its ID: `:archive :trash :read
  :unread :star :label :reply :replyall :forward :react :edit :pin :mute :rename
  :block :later :go :chat :account :sync :older :summary :draft :digest :ask :open
  :save :copy :fold :quoted :set :map :help :keys :q :qa`. Counts and ranges also
  work: `:3archive`, and `:'<,'>read` from VISUAL.
- **`:set` and `:map`** last for the session. `config.toml`'s `[keys]` makes them
  permanent:
  ```toml
  [keys.normal]
  "gh" = "go.mail"
  "<C-j>" = "scroll.down"
  ```

## 6. How it's built

1. **An action registry** (`internal/tui/actions.go`):
   ```go
   type Action struct {
       ID, Desc, Group string           // "thread.archive", "Archive the thread", "message"
       When  func(*Model) (bool, string) // works here? else why not (dimmed in which-key)
       Run   func(m *Model, a Args) (tea.Model, tea.Cmd) // a.Count, a.Range, a.Arg
       Change bool                       // repeatable with . ; recorded for u / Ctrl+r
       Args  func(*Model) []string       // : argument completion
   }
   ```
   Each `actKey` case moves into an action, one group at a time. `actKey` stays as a
   shim until it's empty, then goes.
2. **A keymap per mode** (`keymap.go`):
   - A trie of key sequences → action IDs, with `<count>` and operator-pending
     (`d` + motion) handled in one place.
   - Prefixes wait for ever. `showcmd` on the bar shows the pending keys (`3d…`).
   - Built from defaults, then `[keys]` on top. A test fails on any binding that
     collides or never fires.
3. **Generated surfaces:**
   - `keys.go`'s table becomes a view of the registry plus the keymap.
   - `?`/`Space ?`, which-key, `:` completion, `/`'s actions, the README tables
     (test-checked) and `scripts/keybindings` (Vayu's search, `keybind-search.py
     --check`) all come from it.
4. **Undo and redo:** a stack of changes, each with its inverse. Today's undo becomes
   its first entry kind, and `Ctrl+r` replays.
5. **Marks and the jump list** are stored in the SQLite store (marks survive restarts)
   and in memory (the jump list).

## 7. Phases (each its own release, `make check` green, README + CHANGELOG + site)

| # | Phase | Contents | Size |
|---|---|---|---|
| 0 | Safety, now | `/` is the only search key (**done**). **Remove the hidden passthrough in `vim.go`**: today any key `vimKey` doesn't know falls through to the old `actKey`, so undocumented keys act. Plain `e` archives at once; `x`, `D`, `S`, `T`, `@` and `Ctrl+x` do things the key sheet never mentions. Only keys in the table should do anything. Archive stays on `e` (undoable) or moves to `Space m a`, depending on decision 1. | S |
| 1 | Foundations | Action registry, keymap trie, counts, `showcmd`, generated key table. Existing keys move over **with no change in behaviour**, and the tests prove it. | L |
| 2 | Moving like Vim | `Ctrl+j/k`, `Ctrl+e/y`, `Ctrl+f/b`, `H`/`L` places, `zz zt zb`, `J`/`K` by message, `]]`/`[[`, `n`/`N` unread, `{n}gt` (digits become counts), `gd`/`%` | M |
| 3 | Space layering | The groups in §4.2, a nested which-key with `+labels`, Backspace up a level, dimmed reasons. The bare non-Vim letters are removed. | M |
| 4 | Command bar | Floating and centred, fuzzy and argument completion, ghost text, saved history, ranges and counts, `:set`, `:map`, `[keys]` in config | M |
| 5 | Vim verbs | `d{motion}`, `.`, `Ctrl+r`, `~`, `x`, `p`, `gi`, `gv`, marks, the jump list, `?`/`n`/`N`/`*`/`#` search inside a conversation | L |
| 6 | Vim in the message box | Box NORMAL (§4.3) in chat and in the compose screen's body, `$EDITOR` handoff | M |
| 7 | Release | ADR 9, a "what moved" table in the CHANGELOG and behind `:news`, README, keybindings file, site, screenshots, v0.12.0 | S |

Phases 1–3 make narada "Vim with Space as the leader". Phases 4–6 make it feel like
Neovim. A later option is to move to Bubble Tea v2 for the kitty keyboard protocol,
which would free `Ctrl+i` (jump forward), `Shift+Enter` (new line) and `Ctrl+h`.

## 8. Testing

- **Keymap tests:**
  - Every action is reachable from at least one key or command.
  - No two bindings collide in a mode, and no prefix hides a key.
  - Every which-key group has a label.
- **Vim conformance tests** (table-driven, through `press`): `3j`, `5G`, `dj`, `d3j`,
  `.` after `dd`, `u` then `Ctrl+r`, `ma` … `'a`, `Ctrl+o` after `gd`, `?word` `n` `N`,
  `{n}gt`, `Ctrl+j`/`Ctrl+k` scroll by `scroll_lines` and never move the selection.
- **INSERT:** `Ctrl+j` is a new line, `Ctrl+k` deletes to the end, and Space types a
  space, never the leader.
- **Command bar:** completion order, argument completion, history, ranges, and
  centring at 80, 100 and 150 columns.
- **On screen:** headless only, in the throwaway tmux server with `narada demo`
  (never the live tmux), with captures checked at each phase.

## 9. Risks

- **Muscle memory.** About 40 keys move. Mitigations:
  - A "what moved" table, shown once after the update (`:news` shows it again).
  - For a release, the old bare letters say where their action went instead of doing
    nothing ("L moved: Space m l").
- **Digits become counts**, so `3` no longer opens WhatsApp (`L`, `3gt` and
  `Space t w` do).
- **`Ctrl+i` is `Tab`** in most terminals, so jumping forward is `]j` until Bubble
  Tea v2.
- **A big refactor of `actKey`**: phase 1 changes nothing you can see, and the
  existing ~250 tests guard it.

## 10. Decisions for the user

The family grammar settles the old questions about `?` (it stays help), digits
(counts) and `r` (a bare reply). What's left:

1. **Archive.** The family grammar allows a single key for anything `u` can undo.
   - (a) `e` archives, and `Space m a` too. My pick.
   - (b) Only `Space m a` archives. (Not `Space d a`: in the grammar that means "delete all".)
2. **How far `Ctrl+j` / `Ctrl+k` scroll:** 3 lines (configurable), 1 line, or half a
   page?
3. **`f` (forward) and `e` (react)** go under `Space i` (item) in this plan. Should any
   of them stay bare?
4. **The family plan's own open questions** (its §9) apply here too. Answer them once
   and both plans follow.
