# 6. A trust policy: agents send unless told to ask

**Status:** Accepted, amends [ADR 3](0003-one-outbox-agents-ask.md)  
**Date:** 2026-11-02

## Context

ADR 3 made every agent send wait for approval, with `send_policy` relaxing it only for people the user has written to. In daily use, the user asks an agent to send a message and is then asked again to approve it: the second question adds nothing when the request was theirs.

## Decision

Add `send_policy = "trust"`. Under it an agent's send skips approval, including to unknown recipients, with local files, and on Chat and WhatsApp. A send still goes through the outbox and waits out an undo window, at least a few seconds even in chat, with a desktop notification that can cancel it. A new `ask_first` argument on the send tools brings approval back for one message; agents pass it when the user asks to check a message, or when they are unsure. `confirm` stays the default.

## Consequences

- One outbox and one send path remain, as ADR 3 requires.
- Under `trust` a prompt injection in mail or chat that convinces an agent to send is stopped only by the undo window and the agent's own judgment. The user opts in, and the README says so.
- The per-hour WhatsApp cap and the agent log still apply.
