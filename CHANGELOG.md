# Changelog

Newest first.

## Unreleased

- **WhatsApp takes big files**: up to 2 GB a file, as WhatsApp itself does (an app
  build of 90 MB goes), from the screen, `whatsapp send --attach` and agents. Mail
  and Google Chat keep their 25 MB limit, and the file picker's warning says which
  limit applies.
- `whatsapp_send` tells agents plainly that it takes files (`attachments`, up to 2 GB
  each) and that they attach only files you asked for. An agent that still sees a
  text-only `whatsapp_send` is talking to an old `narada mcp` (or `sandesh mcp`) left
  running: restart that agent session.
- **Voice notes on WhatsApp**: `Ctrl+r` while typing records one from the microphone
  (`Ctrl+r` again adds it, `Enter` sends it, `Esc` drops it); `whatsapp send --voice
  FILE` sends an audio file as one. Needs `ffmpeg`, which also turns other audio into
  the Ogg Opus WhatsApp plays.
- **Download WhatsApp files from the command line and agents**: `narada whatsapp save
  MESSAGE [N|all]`, and `attachment_save` takes WhatsApp message ids. `whatsapp read`
  (and the `whatsapp_read` tool) list each message's files and ids. When the daemon
  holds WhatsApp, it downloads the file and the command waits for it, instead of
  failing with "try again". Big files stream to disk both ways (sending and saving),
  and a long download no longer holds up sends.
- **Edit, delete, forward and reply privately on WhatsApp**: `E` on your message edits
  it (within 15 minutes), `d d` deletes it for everyone, or someone else's for you
  (here only); `f` forwards the picked message, photo or file and all, marked
  forwarded; `R` on a group message answers its sender privately, quoting it. From
  the shell: `whatsapp edit|delete [--for-me]|forward`, and `whatsapp send
  --reply-to`. Agents can forward (`whatsapp_send` with `forward`), behind your
  approval. Edits and deletions go through the outbox like sends (ADR 7), so an
  agent's wait for you and each counts against `whatsapp_per_hour`. Forwarded messages
  you receive say so.
- **Archive, pin, mute, mark unread, star and block on WhatsApp**, synced with your
  phone both ways: `e` archives a chat (into a new Archived section), `p` pins it on
  your phone too, `M` marks it unread there too, `Z` mutes it (8 hours, a week,
  always; muted chats don't notify), `B B` blocks, `*` stars a message. From the
  shell: `whatsapp archive|unarchive|pin|unpin|unread|mute [--for]|unmute|block|
  unblock|star|unstar|starred`, and `whatsapp list --archived`.
- **Typing and online on WhatsApp**: an open chat says when they're typing or
  recording, online, or last seen; they see you typing. Only while you have a chat
  open, as WhatsApp Web, so your phone keeps its notifications otherwise.
- **WhatsApp groups**: `narada whatsapp group new|info|rename|about|add|remove|promote|
  demote|link [--reset]|join|leave`. In the screen `n` renames a group for everyone
  and `I` shows its people, admins and description. A group you leave moves to
  Archived.
- **WhatsApp polls**: polls show their options and a live tally (yours marked ●);
  `P` on a poll votes; `Ctrl+p` while typing sends the question and the options
  under it as a poll. `whatsapp poll <to> <question> <options...> [--multi]` and
  `whatsapp vote <poll> <option>...`; agents can ask one (`whatsapp_send` with
  `poll_options`), behind your approval.
- **Share a place or a contact card on WhatsApp**: `whatsapp send --location LAT,LON
  [--place NAME]` and `--contact WHO`, and `whatsapp_send` with `location` or `contact`.
  Contact cards you receive show their numbers.
- **Disappearing messages on WhatsApp**: `X` in a chat, `whatsapp disappear <chat>
  24h|7d|90d|off`. An open chat shows the timer, what you send from narada carries
  it, and a change made by anyone shows as a line in the chat.
- **WhatsApp status**: a Status section shows people's updates from the last day, and
  `whatsapp status` lists them, without telling them you looked. `whatsapp status post
  [text] [--attach FILE]` posts yours; agents can't, and a plain send can't reach the
  status chat.
- **Pin a WhatsApp message** in its chat for everyone: `p` on a picked message (a
  week), `whatsapp pin-message <message> [--for 24h|7d|30d]`, `unpin-message`. Pinned
  messages show 📌. A `.webp` now goes as a sticker, not a photo.
- An agent's forward, poll, vote, place, contact or pin can't be changed in the
  approval: approve it as it is, or decline it. Edit still works on its text messages.
- **A big WhatsApp file no longer shows as failed while it uploads.** Every narada
  process marked a send stuck after a minute, so a long upload read `failed`
  ("interrupted") until it finished, and retrying then would send it twice. A WhatsApp
  file now gets two hours.
- **`whatsapp send --attach` no longer drops text piped on stdin**: `narada whatsapp
  send X --attach f < msg.txt` sent the file with no caption, silently. The text now
  becomes the first file's caption, as the help says. An empty stdin sends the file
  alone, and a pipe that stays silent for 2 seconds does too, with a warning.
- `whatsapp save` and `attachment_save` say so at once when no narada is online to
  download a file, rather than waiting.
- After upgrading, restart the background service (`systemctl --user restart narada`) and any
  agent running `narada mcp`: edits, deletions, forwards, polls and the like wait for
  a narada that understands them.
- These go through a queue that the narada holding WhatsApp works off (ADR 7), so they
  work beside the daemon.

## v0.10.0 -- 2026-11-05

- **Tag people on WhatsApp by name**: type `@` and pick from a group's members and
  whoever has written there; typing narrows the list (name or number), `Enter` or
  `Tab` tags them, `Esc` leaves the `@` as you typed it. WhatsApp still gets their
  number underneath, so the tag notifies them.
- **Tags show as names**, in chats, the chat list, replies and the outbox: the name
  you saved, else the name they gave themselves, and their number only when neither
  is known. Group members are now kept, so names can be found for people who haven't
  written yet.

## v0.9.0 -- 2026-11-05

- **React on WhatsApp**: `e` on a picked message. One reaction each, as on the phone:
  another replaces yours, and picking yours again takes it back. Other people's
  reactions now show under their messages too. Yours shows at once and waits for the
  process holding the connection, retrying if it fails.
- **You can see which message you're on**: the picked message in a chat or on WhatsApp has a heavy rounded frame in the accent colour and a `▶` beside it, and the picked item in a list has the same frame.
- Every corner is rounded, on the website's callouts too.
- **Scheduled and queued mail shows as it will arrive**: its tables and formatting,
  not flattened text.

## v0.8.0 -- 2026-11-03

- **Your chat message shows the moment you send it**, in Google Chat and WhatsApp:
  `◷` while it's on its way, `✗ not sent` if it fails. Deleting one that hasn't
  gone yet stops it.
- **WhatsApp read receipts always arrive.** Only the process holding the connection
  can send them, so marking a chat read from the screen or a command could lose
  them. They now wait in a queue for that process (a week at most) and retry if
  they fail. Answering a chat marks it read, as on the phone.
- A logo and a shloka at the top of the README and the website: every Vayu tool has them now.

## v0.7.0 -- 2026-11-03

- **The approval screen says more about where a send goes**, as the approved
  design shows it:
  - a WhatsApp group's size ("a group of 14 people"), who's tagged by name
    ("tags Dana Reyes", and `@Dana Reyes` in the message, as WhatsApp shows
    it), your number, and the last two messages in the chat;
  - a Google Chat space's size, and the thread a reply goes into, with its
    replies;
  - after you edit, a "Your edit" summary (lines added and removed), and the
    next send in the queue ("3 of 3 · Mail to Priya Nair").
- Clearer answers: "Edit first" (after an edit, "Edit again"), "Decide
  later", and a `j/k scroll` hint. After you answer, the screen says what
  happened to which send ("The mail to Priya Nair goes in 10s."), how to stop
  it, and whether the next one opens or the popup closes.
- Group sizes come from WhatsApp when narada connects (one call for all your
  groups, again when people join or leave), space sizes from Google Chat's
  space list; until then the screen says what it knows.

## v0.6.2 -- 2026-11-03

- The approval screen's labels are only what's at stake: why narada asks (your
  send_policy) moved to the details. Labels and answers sit evenly apart.

## v0.6.1 -- 2026-11-03

- **Files on WhatsApp from agents and the command line.** `whatsapp_send`
  takes `attachments` (photos, videos, PDFs and other documents; each its own
  message, the text the first one's caption; text may be empty), and
  `narada whatsapp send` takes `--attach FILE` (repeatable). Only the screen
  could send them before, so agents fell back to Google Chat.
- Fixed: a document sent on WhatsApp arrived named after narada's private copy
  (`0-report.pdf`). It now keeps your file's name.

## v0.6.0 -- 2026-11-02

- **A new approval screen.** One sentence says who wants to send what to
  whom, then your own words, then labels for what you mustn't miss: people
  outside your company (from your account's domain), a WhatsApp group, who's
  tagged, attachments. Details on the left, the message on the right exactly
  as it will arrive (an HTML mail's tables included). The border turns orange
  when it reaches past your company. Small terminals stack the panes.
- **After you edit**: your changes are marked line by line (`d` hides them),
  `u` undoes the edit, and the action says "Send my version".
- **Harder to answer by accident**: `Enter` no longer approves, and `y` and
  `n` wait a moment after the popup opens, so what you were typing elsewhere
  can't answer it.
- Long recipient lists can be read in full (`J`/`K`), and the screen says so.
- **AI requests**: an agent's sends now queue in their own section of Mail,
  Chat and WhatsApp, each place counting its own ("Mail · 2 AI"). Pick one and
  `y`, `e` or `n` answer it right there, on the same card; `A` goes to them.
  This replaces the banner across the top of the screen.
- **No notifications unless you ask**: `notify = false` (the default) now means
  none at all. Before, it only silenced new mail: an agent's send, its cancel
  button and failed sends still notified. Stop a send in its undo window with
  `z` or `narada outbox cancel`.
- Fixed: the mail list stopped at a few rows with space left below; it counted
  rows for the old card style. It now fills the height.
- Fixed: editing an agent's mail that had an HTML version sent the old HTML
  beside your new text. An edited body now drops the HTML.

## v0.5.1 -- 2026-11-02

- **Tag people on WhatsApp**: an `@` followed by a phone number with its
  country code (`@919820147460`) in a WhatsApp message you or an agent
  send is now a real tag, so that person gets a mention notification in
  the group. Before, it went as plain text.
- **Tables with long cells show as tables**: a table with a header row
  (like a daily status report's Time | Tasks) is drawn as a grid even when
  its cells hold whole sentences; they wrap inside their column, with a rule
  between rows. Before, such a table was taken for page layout and came out
  as loose lines, and a long cell in a drawn table was cut off with "…".

## v0.5.0 -- 2026-11-02

- **Renamed from sandesh to narada** (नारद, the divine messenger who carries word
  between worlds). The command is `narada`, the repository
  github.com/Theyashsawarkar/narada (the old address redirects), the MCP server
  `narada`, the service `narada.service`. The first run moves your folders
  (mail cache, WhatsApp link, settings) and keyring sign-ins to the new name, so
  nothing needs linking or signing in again; `kosha update` moves the install.
- **Quiet by default**: no desktop notification for new mail, chat or WhatsApp
  (`notify = true` brings them back). The tmux capsule still shows what came; a
  send an agent asks for still shows its undo notification.

## v0.4.3 -- 2026-11-02

- **Quieter**: bubbles and the picked list row lose their colour fill: just
  a rounded outline, plain text, and the time dim inside the bubble.

## v0.4.2 -- 2026-11-02

- **Really rounded**: bubbles and the picked list row get rounded borders
  (╭ ╮ ╰ ╯) in their colour; the half-block corners of v0.4.1 were too
  small to read as round.

## v0.4.1 -- 2026-11-02

- **Fixed: sending files on WhatsApp**: `U` said "files isn't on WhatsApp
  yet" (a leftover guard); it sends now, and `v` / `V` open and save
  WhatsApp files. The key bar shows `U send a file`.
- **Rounded**: one-line messages are pills, longer ones rounded blocks; the
  picked list row is rounded too, and keeps its colours (name capsules
  showed as dark blobs on it).
- **Switching places**: `Shift+Tab` goes back (Mail ← Chat ← WhatsApp),
  `Alt+m` / `Alt+c` / `Alt+w` go straight to one.
- **Picking several, marking all read**: `x` picks chats too, `Ctrl+x`
  picks the whole list (again clears), `m` / `M` act on what's picked, and
  `Alt+r` marks everything in the list read.

## v0.4.0 -- 2026-11-02

- **Filled bubbles**: chat and WhatsApp messages are filled blocks, yours
  in the accent colour, with the time and ticks inside; one name per run.
- **One panel for the lists**: mail, chat and WhatsApp rows divided by
  hairlines, the picked one filled in the tab's colour (`list_style`,
  replacing `chat_list`; `chat_list = "cards"` still works).
- **Mail threads fold**: read messages to one line, a long run to one line
  in all.
- **One frame for the message box**: the reply and the files go inside it.
- **A clearer approval**: "Claude Code wants to send a mail to …", the
  reason as a badge, the agent's own `why` (new on `mail_send`, `chat_send`
  and `whatsapp_send`) and `y send in 10s`.

## v0.3.0 -- 2026-11-02

- **Screenshots** in the README and on the website (from `sandesh demo`).
- **Ticks**: ✓ sent, ✓✓ delivered and blue ✓✓ read beside your WhatsApp
  messages, from the phone's receipts and its history.
- **Paste a screenshot**: `Ctrl+v` while typing attaches a copied image;
  files about to go show as small previews above the box.
- **Pictures full size**: `Space` (or `Enter`) on a picked photo or GIF
  fills the screen; `h`/`l` go through the chat's pictures.
- **Compact chat list**: two rows per chat instead of a box, so about twice
  as many fit; `chat_list = "cards"` brings the boxes back.
- **The undo window where you look**: a message counting down to send, or
  waiting for approval, shows under the conversation; `z` cancels the open
  chat's own first.
- **Clearer**: the unread count sits on Unread, not All; the list title says
  "chats"; a one-to-one chat shows the number, not "You, Amma · 2 people";
  the hint under the message box, which repeated the key bar, is gone.

- **Photos, GIFs and files on WhatsApp**: they show in the conversation (on
  kitty as real pictures, in tmux popups too, and GIFs play; elsewhere as
  coloured blocks; the `images` option chooses), and `Ctrl+o` while typing
  or `U` sends them, with the text as the first file's caption.
- **Link WhatsApp from its tab**: `L` shows the QR code, `P` a phone code,
  and a bar follows the sync, with no separate terminal.
- **`send_policy = "trust"` and `ask_first`**: agents send without asking
  unless you told them to ask, after an undo window with a notification
  that can cancel it (ADR 6). The approval screen is redesigned.
- **The chat box grows**: long messages wrap and the box gets taller, and
  Enter sends and keeps you typing. The active tab is a filled pill in its
  own colour.
- **Fixed**: WhatsApp names and mentions showing as numbers; `@` and the AI
  keys not finding `claude` or `sandesh` from a tmux popup.

- **WhatsApp**: `sandesh whatsapp link` links your WhatsApp as a device
  (QR code, or `--phone` for a code to type on the phone), and its chats
  become a third tab (`Tab`: Mail → Chat → WhatsApp) with unread counts in
  `sandesh status` and the tmux segment, notifications, read receipts and
  quoted replies. `sandesh whatsapp list|read|send|unlink` on the command
  line, `whatsapp_chats`, `whatsapp_read` and `whatsapp_send` for agents.
  Every send goes through the outbox; an agent's always waits for you, and
  WhatsApp sends are capped at `whatsapp_per_hour` (30). Without linking,
  `whatsapp send` opens WhatsApp's click-to-chat page with the text typed
  in instead. Linking isn't allowed by WhatsApp's terms
  (see the README's WhatsApp section and ADR 5).

- **HTML mail, for tables and the like**: `mail_send` and `mail_draft` take
  an optional `html` (and `sandesh mail send` an `--html FILE`). It goes
  out beside the plain text as `multipart/alternative`, so mail apps show
  the HTML and the plain text is still there for those that don't; a reply
  quotes the thread in the HTML too. Plain text alone can't make a table
  Gmail shows as one.

## v0.2.0 -- 2026-10-28

- **`--tab chat`**: `sandesh --tab chat` (and `sandesh --popup --tab chat`)
  opens the screen on the chat tab, so a status bar's chat count can open
  chat and its mail count mail.

- **`v` opens files**: an attachment or chat file opens in the app your
  desktop uses for it (kept in ~/Downloads/sandesh); `V` only saves.

- **Take a reaction back**: in the reaction window (`e`), the ones you
  added are marked ✓, and picking one removes it.

- **Jump to the original**: Enter on a picked reply jumps to the message it
  quotes, loading earlier messages until it's there; Enter again follows the
  chain further back, Backspace retraces it.

- **Chat, redone as bubbles** (option A from the mockup): theirs left,
  yours right; quotes, files and numbered links inside the bubble;
  reactions underneath; "(edited)"; a red "new" line where unread starts
  (the chat opens there); who's in the chat on top.
- **Chat list in groups**: pinned (`p`), today, earlier; `@N` badges for
  unread mentions of you.
- **More on a picked message**: `e` reacts (an emoji window), `E` edits
  yours, `d d` deletes yours; `k` past the first message loads 50 earlier
  ones.
- `[` / `]` move between chats without leaving the conversation; `Ctrl+k`
  opens the chat switcher; `Ctrl+o` adds files to the message you're typing.

- **Reply to a specific chat message**: Enter (or 2) on a chat lets j/k pick
  messages; `r` replies to the picked one, quoting it like Google Chat
  (quotedMessageMetadata), with "↩ replying to …" over the message box;
  from the list `r` answers the latest message from someone else. `y`
  copies and `v` saves the picked message.
- **Quoted replies show**: a message that quotes another shows it above
  (`❝ You: …`); recent chats are fetched again once to fill this in.
- No more "reply in a thread" labels: Google reports every space as
  threaded, and replies show inline as quotes, as in the browser.

- **Name a group**: starting a chat with two or more people asks for a name
  (named: a space; unnamed: a group chat), and `n` names an existing group
  chat on Google Chat (it becomes a space, same people and messages).
- **Sign in again without leaving**: when Google wants the newer permission
  (starting chats, naming groups), the window says so and Enter signs in
  again in the browser, then carries on with what you were doing.

- **Start a conversation (`N` in chat)**: pick people (from chat, mail
  contacts and the Workspace directory); sandesh opens the DM you already
  have with them (found on Google, earlier messages fetched) or starts one,
  a group chat for several, and puts you in the message box.
- **Chat search finds people and conversations**: CONVERSATIONS, PEOPLE and
  MESSAGES groups; Enter on a person opens (or starts) your chat with them.
- MCP `chat_send` takes `to` (email addresses) instead of a space.

- **Scheduled tabs**: scheduled mail has its own **Scheduled** section next
  to Sent, and scheduled chat messages a **Scheduled** tab in chat, soonest
  first with their time and counts on the tabs (`t` moves, Enter edits, `z`
  cancels). Sent goes back to mail that went (and what's sending or failed).

- **Send later**: `Ctrl+t` in a mail, `t` on review, `Ctrl+t` while typing a
  chat message: pick a time or type it ("tomorrow 9am", "fri 6pm", "in 2h",
  "3 nov 10:30"). Scheduled sends wait in the outbox and the background
  service sends them on time (on its own loop now, so a slow sync never
  delays one); a send that goes out late because the computer was off says
  so. They're listed on top of Sent (`t` moves, Enter edits, `z` cancels).
  CLI `--at` and `outbox reschedule`; MCP `send_at`.

- **Search no longer freezes the screen**: a one-letter search took ~5 s on a
  real mailbox (the full-text index was queried once per thread); it is now
  one lookup (~20-50 ms), and it runs in the background after a short pause
  in typing, with stale answers dropped and Gmail's search cancelled when
  you type on. Previews are drawn in the background, once.
- **Search where you are**: a mail section searches that section (Today,
  Sent, Inbox...), a chat space searches that space; Tab widens to all mail
  or every chat. Opening a chat result scrolls to the message and marks it.

- **`?` is a window**: every key, grouped in columns in the middle of the
  screen, with a filter as you type.
- **A file picker of its own** (replacing fzf): a window over the screen
  with fuzzy matching and a preview of the highlighted file (text, image
  size or the picture itself with chafa, a PDF's first page with
  pdftotext); Tab picks several; Ctrl+r switches folder, Ctrl+h hidden.
- **Rename a chat (`n`)**: spaces on Google Chat (new `chat.spaces`
  permission; sign in again once), DMs and unnamed groups by a name kept on
  this machine.
- The popup is bigger by default (96% × 94%).

- **Round ends on every filled label**: @mentions in chat, the active tab,
  the mode label, key hints, the send button and the approval banner use
  Nerd Font half circles like the avatars (`nerd_font = false` keeps square).

- **A search window**: `/` (or `s`) opens a floating search over the
  screen. Mail and chat from this machine show as you type, matches
  highlighted, with a preview of the highlighted result; when typing pauses,
  Gmail's own search adds what isn't here yet (☁). Gmail search syntax
  works. Replaces the old local filter and separate server search.
- **Older mail on demand**: no background download by default any more
  (`keep = "none"`; it competed with searches for Gmail's rate limit). `j`
  at the end of a list loads the next 10 older threads of that section;
  `+` / `-` change the batch (10, 25, 50, 100), `older_page` the default.
- **Search results survive a refresh**: they're kept like a section.
- **Rounded everywhere, round avatars**: the selected card keeps rounded
  corners (heavier sides mark it), mail tables have rounded corners, and
  initials sit in round pills (Nerd Font half circles; `nerd_font = false`
  for plain ones).

- **Compose, redone** (from the approved mockup): a new mail gets the whole
  screen; a reply keeps the original on the left (PgUp/PgDn). Recipients are
  pills with initials (←/→ selects, Backspace removes); someone you've never
  written to is marked *first time*. Suggestions highlight the typed letters
  and say why they're there (`wrote 4× · 2d ago`, `wrote you 9×`,
  `directory`). Cc/Bcc hidden until `Ctrl+b`. Your Gmail signature is
  fetched and added (`Ctrl+g` toggles). Drafts are saved on disk every few
  seconds and reopened by the same key. A status line with recipients,
  files, words and buttons; `Ctrl+f` switches the sending account. Tab now
  adds a typed contact and moves on.
- **A Sent section**: who you wrote to, and whether they answered (`↩ Priya
  replied 09:58`, `no reply yet · 2d`); mail in the outbox (sending, waiting
  for approval, failed) on top, `z` cancels, `R` retries a failed send.
- **All your mail, not just 30 days**: after the first sync, older mail is
  downloaded in the background (~150 threads a minute, newest first) until
  the mailbox is complete, with a progress bar in the top bar. `keep`
  limits it (`"1y"`, `"6m"`, `"none"`).

- **A compose screen**: `c`, `r`, `a`, `f` and `D` open a form in the right
  panel instead of a bare file in the editor: From, To, Cc, Bcc as chips,
  Subject, Files and the body, with a reply's quote folded underneath.
  Recipients autocomplete from your contacts (ranked by how often you write
  to them) and from your Google Workspace directory (People API). `Ctrl+s`
  sends, `Ctrl+d` saves a draft, `Ctrl+o` attaches, `Ctrl+e` still takes
  the whole mail to your editor and back; `compose = "editor"` keeps the
  old way. An agent's message is edited here too.

- **Syncs ~10× cheaper**: a sync with nothing new went from 23 Google calls
  (12.6 s) to 2 (1.1 s) on a real account. Lookups that fail (a DM's title, a
  person's name) are remembered and retried every 6 hours instead of on
  every sync; changed spaces are fetched four at a time; cached spaces are
  read in one query. The background service has a soft 128 MB memory cap.
- **Chat read state follows the browser**: a space read on the web or phone
  turns read here too, without waiting for a new message. `M` on a space is
  Chat's own "mark as unread" (it stays unread after a sync, and marks the
  latest message from someone else, even when you wrote last).
- **Forwarding keeps attachments**: they're listed as `Attach: original: …`
  in the editor (delete to drop) and fetched from Gmail when sent.
- File picker (`+`, `U`) starts in the folder you're in; the review screen
  shows each file's name, size and folder, and says when one is missing.
- Fixes: `W` no longer glues the name onto the search (an empty name keeps
  the search); `mail archive` and friends say what they did; the outbox
  names files; `sandesh mcp` exits quietly when its client closes it; the
  setup screen's border no longer shifts on the title line.
- `SANDESH_DEBUG_HTTP=1` logs every Google request with its time.

- **Compose works without $EDITOR**: with no editor set, sandesh used `vi`,
  which many systems don't have, so `c`, `r` and friends failed at once. It
  now picks the first installed of nvim, vim, vi, helix, micro, nano, kak
  and emacs, and says which editor failed and how to set one.

- **Chat sends instantly**: chat messages skip the 10-second undo window
  (`chat_undo_seconds`, default 0; mail keeps `undo_seconds`), appear in the
  conversation as soon as Chat accepts them, and sending always jumps to the
  end of the chat.

- **The tmux popup runs the current sandesh**: tmux's environment often lacks
  `~/.local/bin`, so the plugin fell back to a copy built once in its own
  folder and kept running it after updates. It now looks in `~/.local/bin`
  and `~/go/bin` too, and rebuilds its own copy whenever the code is newer.
- **New chat messages show up**: a conversation scrolled to the bottom stays
  at the bottom when messages arrive, like a chat app; before, they landed
  below the visible part. The space list says "You:" for your own last
  message.

- **Files, both ways**: `v` saves a mail's attachments or a chat's files to
  `~/Downloads/sandesh` (an fzf picker when there are several), `V` saves
  and opens them. Send files with `Attach:` in the mail editor, `+` on the
  review screen, or `U` in a chat space; `--attach` on `mail send` and
  `chat send`; `mail save` / `chat save` download from the shell. Big mails
  (over 4 MB) go through Gmail's upload endpoint. Files are copied privately
  when queued, so what you reviewed is what is sent.
- **Agents and files**: `mail_send`, `mail_draft` and `chat_send` take
  `attachments`; such sends always ask for approval, which lists each file.
  New `attachment_save` tool, confined to the downloads folder.
- Other people's chat messages get a solid bar in their colour.

- **Follows the desktop theme live**: colours come from varna's
  `palette.json` (sandesh's own pick under `tools.sandesh` if the theme has
  one) and change on the running screen when the theme changes; `[colors]`
  in the config still win. `SANDESH_NO_VARNA=1` turns it off.
- **Cleaner borders**: panels and frames use the `border` colour instead of
  one almost the same as the background; the selected card is a heavy
  accent frame instead of a background fill, which looked muddy on
  translucent terminals.
- **No red error when Chat has no app configured**: marking a space read
  works on this machine, and a quiet line says what to set up; messages that
  were cached empty are fetched again.

- **Syncs in the background, not when you open it**: the screen shows the
  local copy at once and leaves syncing to the background service (setup
  now installs it); it syncs by itself only when the service isn't
  running. After a send, an archive or another change it syncs again
  shortly so the copy matches Google. Only one process syncs an account at
  a time.
- **Never hits Gmail's rate limit**: requests are paced (40 quota units a
  second), Google's "slow down" answers are retried with backoff, and a
  first sync cut short resumes instead of starting over. The first sync
  takes the newest 150 threads (`max_threads`); older mail comes through
  search.
- **Real names in chat**: names come from Chat's own messages, sandesh
  finds which member is you from your DMs (no People API needed), DMs are
  named after the other person and groups "Naeem, Rahul, Rugved +2";
  placeholder "user 1234" names are no longer stored.
- **Chat is compact**: one name line per group, a coloured bar per
  message, no boxes; messages that are only an attachment or a card say so.
- Errors get their own line (Esc hides it) instead of covering the keys,
  and the common Google errors say what to do. Card borders are brighter
  (new `border` colour).

- **A new look**: the mail list is one bordered card per thread with gaps
  between them; the thread view puts each message in its own card with a
  From / To / Date header and renders HTML mail as terminal formatting
  (headings, bold, lists, tables, buttons, numbered links), folding older
  messages and quoted replies (`E`, `Q`, `o` opens a link). Chat reads like
  a conversation: grouped by sender, yours on the right, day separators,
  thread replies and mentions marked; `i` types a message in place.
- **`m` marks read and `M` marks unread** (no more toggle), for the thread
  or every selected one, in mail and chat.
- **Setup asks for the client file's path** (with completion) instead of
  looking in Downloads, copies it into its config, and shows the three
  steps with a progress bar for the first sync.
- **Chat works without the People API**: names fall back to "user 1234"
  instead of the whole chat sync failing.
- The local copy keeps each message's HTML; existing copies refetch once.

- **Setup screen**: opening sandesh (or `prefix e`) with no credentials no
  longer stops with an error. It says what's missing and takes it right
  there: the client file found in `~/Downloads`, a path or pasted JSON, or
  a typed client ID and secret; then the Google sign-in in your browser
  (work account or personal Gmail) and the first sync, straight into the
  inbox. `d` tries the demo instead.

## v0.1.0 -- 2026-10-26

First release.

- **The screen**: Gmail and Google Chat in one keyboard-driven terminal UI,
  laid out like sangam: sections on top (Inbox, Today, Unread, Starred,
  saved Gmail searches; All, Unread, DMs, Spaces for chat), the list on the
  left, the thread or space on the right. Vim keys, `?` for all of them.
- **Write in your editor**: compose, reply, reply all, forward and chat
  messages open `$EDITOR`; a review step shows exactly what goes out, then
  `y` sends, `d` saves a Gmail draft.
- **Undo**: `z` undoes archive, trash, read and star, and cancels a send in
  its 10-second undo window.
- **Background sync** (`sandesh service install`): Gmail's change feed and
  Chat's active spaces into a local SQLite copy with full-text search;
  desktop notifications for new inbox mail, DMs and mentions.
- **MCP server** (`sandesh mcp`) with 11 tools for Claude Code and other
  agents. Agents' sends wait for your approval in a tmux popup, `A` in the
  screen or `sandesh approve`; mail content reaches them marked untrusted;
  `send_policy` sets how much they may send alone.
- **Claude keys in the screen**: `@` ask, `S` summarise, `D` draft a reply,
  `T` today's digest, through `claude -p`.
- **Command line** for everything, with `--json`: `mail`, `chat`,
  `status`, `sync`, `outbox`, `approve`, `auth`, `doctor`, `demo`.
- **tmux plugin**: `prefix e` popup and `#{sandesh_status}` for the status
  line.
