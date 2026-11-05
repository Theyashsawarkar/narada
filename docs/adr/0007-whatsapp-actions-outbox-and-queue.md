# 7. WhatsApp actions: the outbox for what others see, a queue for the rest

**Status:** Accepted, extends [ADR 3](0003-one-outbox-agents-ask.md) and [ADR 5](0005-whatsapp-as-a-linked-device.md)  
**Date:** 2026-11-05

## Context

WhatsApp Web does far more than send: it edits and deletes messages, forwards them, runs polls, shares locations and contacts, archives, mutes and pins chats, stars messages, blocks people, shows who is typing, and administers groups. narada is a linked device (ADR 5), and only the one process holding the connection (usually the daemon) can talk to WhatsApp. The screen, the command line and the MCP server must ask it.

Two mechanisms already existed: the outbox (ADR 3), which every send goes through, with the undo window, an agent's approval, the hourly WhatsApp cap and delivery by whichever process may deliver; and small store queues for read receipts and reactions, which the holder works off and retries.

## Decision

- **What changes what other people see in a chat goes through the outbox**, as a WhatsApp item with an `action` beside the message: `edit` and `delete` (for everyone) name the message they change, `forward` the message it forwards; later polls, votes, locations and contacts do the same. So an agent's edit, deletion or forward waits for the user's approval exactly as its sends do, every one counts against `whatsapp_per_hour`, a failed one shows as failed in the outbox, and the approval card says what the item does ("wants to forward a message to Ravi").
- **What changes only your own view, or the state of a chat rather than its messages, goes through a queue** the holder works off, like reactions and read receipts: archive, mute, pin, mark unread, star, block, your typing, asking for someone's presence, and group administration. A command that queues one waits for the holder to do it and reports the result (an invite link, an error).
- **Delete for me** stays on this machine. WhatsApp syncs it between devices through app state that whatsmeow doesn't build; deleting here is what a person expects of "for me".
- Agents get forwarding (as a `forward` argument of `whatsapp_send`, under approval) and polls, locations and contacts as they come; editing, deleting, chat state and group administration stay with the user, in the screen and the command line.

## Consequences

- One send path remains: nothing new talks to WhatsApp outside the process holding it, and nothing an agent does reaches other people without passing the send policy.
- Edits and deletions take the outbox's round trip (a second or so with the daemon running) before they show; narada shows them when WhatsApp has them, not before.
- A queued chat-state change made while no narada is online waits until one is, as read receipts already do.
