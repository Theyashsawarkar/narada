# WhatsApp Web parity

What WhatsApp Web does, what narada does, and what is left. narada is a
linked device through whatsmeow (ADR 5), so it can do what a linked
device can, and nothing a linked device can't.

Status: **done** (CLI, screen, and MCP where an agent would use it),
**partial** (says which part), **gap** (not yet), **can't** (a linked
device or whatsmeow can't do it).

Every change that puts something in a chat (a message, an edit, a
deletion, a forward, a poll, a vote, a location, a contact) goes through
the outbox (ADR 3, ADR 7); everything else WhatsApp-side (chat state,
groups, presence) goes through the queue the connection holder works off
(ADR 7).

## Already there before this round

| Feature | CLI | Screen | MCP |
|---|---|---|---|
| Send and receive text | `whatsapp send` / `read` | yes | `whatsapp_send` / `whatsapp_read` |
| Send files: photos, videos, GIFs (via ffmpeg), documents | `--attach` | `U`, `Ctrl+o`, `Ctrl+v` | `attachments` |
| Receive photos, stickers, GIFs, videos, voice notes, audio, documents | label in text | inline photos and GIFs, `v` / `V` | label in text |
| Quote-reply | — | `r` on a picked message | `reply_to` |
| Reactions | — | `e` on a picked message | — |
| Tag people (`@name`) | `@<number>` in text | `@` picker | `@<number>` in text |
| Read receipts, both ways; ticks on yours | `read --mark-read` | yes | `mark_read` |
| Your message shows at once (◷, ✗) | — | yes | — |
| Scheduled sends | `--at` | `Ctrl+t` | `send_at` |
| Search messages and chats | `search` | `/`, `Ctrl+k` | — |
| Pin a chat on top (this machine only) | — | `p` | — |
| Mark a chat unread (this machine only) | — | `M` | — |

## This round

See the table at the end for what shipped, what's partial and what's left.

| # | Feature | Notes |
|---|---|---|
| 1 | Files to 2 GB; `whatsapp_send` attachments | WhatsApp's own document limit; mail stays at 25 MB |
| 2 | Download received media from the CLI and MCP | `whatsapp save`, `attachment_save` on WhatsApp message ids; waits for the process holding the connection; big files stream to disk |
| 3 | Voice notes | send an audio file as a voice note (`--voice`); record one in the screen |
| 4 | Edit and delete for everyone | through the outbox; WhatsApp allows edits for 15 minutes |
| 5 | Delete for me | hides it here only |
| 6 | Forward | text and media, marked "Forwarded" as on the phone |
| 7 | Archive, mute, pin, mark unread, star | synced with the phone (app state) both ways |
| 8 | Block / unblock | |
| 9 | Typing and online / last seen | both ways |
| 10 | Groups: create, rename, description, add, remove, promote, demote, invite link, join, leave | |
| 11 | Polls: create, vote, see the votes | |
| 12 | Share a location, a contact | |
| 13 | Disappearing messages | per chat |
| 14 | Reply privately to a group message | quote the group message in a DM |
| 15 | Status: view; post text | |

## Can't, and why

- **Voice and video calls**: whatsmeow can only reject a call; it has no
  call media. WhatsApp Web's calls are its own desktop app's.
- **Communities and channels admin**: channels can be read by whatsmeow
  but narada hides them, as it hides broadcast lists; not planned.
- **Payments, catalogs, business tools**: not available to linked devices
  outside WhatsApp's own apps.
- **View-once media you receive**: WhatsApp doesn't deliver it to linked
  companion devices (the phone shows it only).
- **Stickers you make**: sending a .webp sends it as a photo; WhatsApp's
  sticker packs and sticker store aren't reachable from a linked device.
