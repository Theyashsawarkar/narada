# WhatsApp Web parity

What WhatsApp Web does, what narada does, and what is left. narada is a
linked device through whatsmeow (ADR 5), so it can do what a linked device
can, and nothing a linked device can't.

Everything that changes what other people see in a chat goes through the
outbox (ADR 3, ADR 7); chat state, presence and groups go through the queue
the process holding WhatsApp works off (ADR 7).

**How far this is tested:** only against `fake.WhatsApp`, never a real phone
or account, and not every front end of every row has its own test. The whatsmeow calls themselves
(`internal/whatsapp`) have not run against WhatsApp's servers. See
[Not yet tried on a real account](#not-yet-tried-on-a-real-account).

## What narada does

"—" means that front end doesn't have it; the reason is under
[What's left](#whats-left).

### Media

| Feature | CLI | Screen | MCP |
|---|---|---|---|
| Send photos, videos, GIFs (ffmpeg), documents, up to 2 GB | `send --attach` | `U`, `Ctrl+o`, `Ctrl+v` | `attachments` |
| Send stickers (a `.webp`) | `send --attach` | `U`, `Ctrl+o` | `attachments` |
| Send voice notes | `send --voice` | `Ctrl+r` records | — |
| Receive all of them; photos and GIFs show inline | `read` lists them | yes | `whatsapp_read` lists them |
| Download any of them, streamed to disk | `save` | `v`, `V` | `attachment_save` |

### Messages

| Feature | CLI | Screen | MCP |
|---|---|---|---|
| Send and receive text; read receipts; ticks | `send`, `read` | yes | `whatsapp_send`, `whatsapp_read` |
| Quote-reply | `send --reply-to` | `r` | `reply_to` |
| Reply privately to a group message | `send --reply-to` | `R` | `reply_to` |
| Reactions | — | `e` (picking) | — |
| Tag people | `@<number>` | `@` picker | `@<number>` |
| Scheduled sends | `send --at` | `Ctrl+t` | `send_at` |
| Edit (15 minutes) | `edit` | `E` | — |
| Delete for everyone | `delete` | `d d` | — |
| Delete for me (this machine only) | `delete --for-me` | `d d` on someone else's | — |
| Forward | `forward` | `f` | `forward` |
| Star; list starred | `star`, `starred` | `*`; no list | — |
| Pin a message for everyone | `pin-message` | `p` (picking) | — |
| Polls: ask, vote, live tally | `poll`, `vote` | `Ctrl+p`, `P` | `poll_options` (ask only) |
| Share a contact | `send --contact` | — | `contact` |
| Share a place | `send --location` | — | `location` |
| Disappearing messages | `disappear` | `X` | — |
| Search messages | `search` (mail only) | `/`, `Ctrl+k` | — |

### Chats and people

| Feature | CLI | Screen | MCP |
|---|---|---|---|
| Archive, with an Archived section | `archive`, `list --archived` | `e`, section | shown in `whatsapp_chats` |
| Pin a chat (three at most) | `pin` | `p` | shown in `whatsapp_chats` |
| Mute | `mute --for`, `unmute` | `Z` | shown in `whatsapp_chats` |
| Mark unread | `unread` | `M` | — |
| Block, unblock | `block`, `unblock` | `B B` | shown in `whatsapp_chats` |
| Typing, both ways; online and last seen | — | in an open chat | — |

Archive, pin, mute, unread and star are meant to sync with the phone both ways.

### Groups

| Feature | CLI | Screen | MCP |
|---|---|---|---|
| Rename | `group rename` | `n` | — |
| People, admins, description | `group info` | `I` | — |
| Make, describe, add, remove, promote, demote | `group ...` | — | — |
| Invite link, reset it, join by link, leave | `group ...` | — | — |

### Status

| Feature | CLI | Screen | MCP |
|---|---|---|---|
| See the last day's updates, as they arrive while narada is online | `status` | Status section | — |
| Post text, a photo or a video | `status post` | — | — (on purpose) |

Agents can't post a status, and a plain send can't reach the status chat.

## What's left

Could be done, not done yet:

- **In the screen:** make a group and run its members (the shell has it);
  share a contact or a place; a list of starred messages; post a status.
- **Voice notes from agents.**
- **Agents voting in polls**, and editing or deleting. Left out on purpose
  for now: an agent acting on your messages should be something you ask for.
- **Delete for me on your other devices.** It stays on this machine: whatsmeow
  has no builder for that app-state change.
- **Status:** older updates from before narada was online; telling people you
  looked (narada doesn't send that receipt).
- **Votes from before narada saw the poll**, and votes in the phone's history,
  don't count in the tally.
- **Profile:** your name, photo and about; other people's profile pictures;
  group photos; group settings (who can send, who can add).
- **Message info** (who read it, per person, in a group), **link previews**,
  **labels**, **mentions of everyone**, **sticker packs**.
- **Older messages** beyond what the phone sends at linking.

## Can't, or not planned

- **Voice and video calls**: whatsmeow has no call media.
- **Channels, communities' admin, broadcast lists**: narada hides these on
  purpose; broadcast also goes against its one-chat-at-a-time rule.
- **Payments, catalogs, business tools.**

## Not yet tried on a real account

These are tested only through the fake, which accepts whatever narada sends.
The riskiest, which would fail only against the real servers:

- **Archive and mark unread** send no last-message key (whatsmeow fills in the
  current time). The phone may ignore or reject that.
- **Stickers**: a `.webp` that isn't 512×512 may show oddly.
- **Status posts**, **pinning a message**, **voting** and **voice notes**: built
  from whatsmeow's types and its docs, never seen on a phone.
- **Big files**: sending and saving now stream through `UploadReader` and
  `DownloadToFile`. The fake only has the older `Download`.
- **Presence**: opening a chat in the screen marks you available, as WhatsApp
  Web does, and your phone may hold back notifications meanwhile.
