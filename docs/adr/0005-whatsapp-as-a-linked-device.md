# 5. WhatsApp as a linked device

**Status:** Accepted  
**Date:** 2026-11-02

## Context

The user wants WhatsApp next to mail and chat: read it, send from the terminal, and let an agent send. WhatsApp has no free API for a personal account. The ways in:

- **Meta's Cloud API**: official, but only for a separate business number (not your own chats), paid per conversation, and a message to someone who hasn't written in 24 hours needs a pre-approved template.
- **Click-to-chat** (`wa.me/<number>?text=…`): official and free; it opens WhatsApp with the text typed in and you press Send. It can't read anything.
- **Through the phone** (ekatva): the real app via intents and notifications; needs the phone link up and a tap per message.
- **A linked device** (whatsmeow, the multi-device protocol WhatsApp Web uses): reads and sends everything, free, a pure-Go library. Not official: it is against WhatsApp's terms, and accounts that send a lot (bulk, many strangers, the same text to many) get banned.

## Decision

Link sandesh as a companion device with whatsmeow, with click-to-chat as the fallback when nothing is linked.

- WhatsApp is an account of kind `whatsapp` (name `whatsapp`) in the same store: its chats are spaces and its messages chat messages, so the chat screen, unread counts and the outbox work unchanged. `Store.Accounts()` stays Google-only, so no Google code path ever sees it.
- WhatsApp is a connection, not a sync: one process holds it (a `whatsapp` lock): the daemon, else the screen, the MCP server or a CLI command while they run. Only that process claims `whatsapp` outbox rows; the others leave them queued for it.
- Every send still goes through `app.Queue` → outbox → `DeliverDue`. An agent's WhatsApp send always asks, whatever `send_policy`. Sends are capped at `whatsapp_per_hour` (30), yours included; there is no way to send one text to several chats.
- whatsmeow's device keys live in their own file (`whatsapp.db`, mode 600) beside the local copy.
- Text only at first; media shows as a label ("📷 Photo: caption").

## Consequences

- The user gets a real WhatsApp tab and agent tools, at the cost of a ban risk the README and `sandesh whatsapp link --help` state plainly. The cap and the always-ask rule keep usage looking like a person's.
- whatsmeow follows WhatsApp's protocol changes; sandesh has to update it when WhatsApp breaks old clients.
- The phone must be online now and then (every ~14 days) or WhatsApp unlinks the device; sandesh then shows the error and asks to link again.
- Reactions, edits, deletes, files and new groups aren't sent yet; the screen says so instead of failing.
