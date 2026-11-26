# 10. Agents pipe to the daemon

**Status:** Accepted (amends ADR 4: a second, small binary)  
**Date:** 2026-11-26

## Context

Every agent session starts its own `narada mcp`. Each was a whole narada: the store open, WhatsApp, SQLite and the screen's packages loaded, about 30 MB. Six Claude sessions meant six copies doing what the daemon already does.

## Decision

- `narada daemon` also serves the MCP tools on a unix socket, `$XDG_RUNTIME_DIR/narada/mcp.sock` (`$NARADA_HOME/run/mcp.sock` under `NARADA_HOME`): its folder 0700, the socket 0600, a stale one replaced at start (the daemon lock says no live daemon owns it), removed at exit. Each connection is its own MCP session over newline-delimited JSON-RPC, so `initialize` passes through and the agent's name still comes out right.
- `narada mcp` connects to it and only copies stdin and stdout. A first line, `{"narada_pipe":1,"env":{...}}`, hands over the agent's `TMUX`, `TMUX_PANE`, `DISPLAY` and `WAYLAND_DISPLAY`, so an approval pops up beside the agent and click-to-chat opens on its screen, not the daemon's. A client that sends no such line is served too.
- A Go program starts every package it links, so the pipe can't be small inside narada. `narada-pipe` (`cmd/narada-pipe`, standard library only, built static and stripped) is the pipe; `narada mcp` execs it when it sits beside the narada binary, and pipes from itself when it doesn't. Either way the agent's command stays `narada mcp`.
- **The pipe outlives the daemon.** An update restarts narada.service, which would otherwise end narada in every open agent session. The pipe remembers the agent's `initialize` and `notifications/initialized`; when the socket closes while stdin is open, every request the daemon never answered gets a JSON-RPC error (same id, code -32000, "narada daemon restarted; try again"), it reconnects (100 ms doubling to 2 s, for up to 60 s), replays `initialize` under its own id (`pipe-reinit-N`, its answer swallowed) and `initialized`, and goes on. Requests made meanwhile wait (up to 64) and go after; if the daemon isn't back in 60 s they get the same error and the next request tries again. Only stdin's end ends the pipe; it never switches to in-process mid-session.
- No daemon at the start (no socket, or connection refused): `narada mcp` serves the tools itself as before and says so in one line on stderr. `narada-pipe` with no daemon execs `narada mcp --in-process`. `--in-process` forces it.

## Consequences

- An agent session costs about 4 MB instead of about 30 MB, and tools answer from the daemon's warm store.
- Installs carry two files: release assets `narada-pipe-linux-ARCH` beside `narada-linux-ARCH`, `make install` installs both, install.sh fetches or builds both. Without `narada-pipe`, everything works, at about 20 MB a session.
- When the daemon restarts, open agent sessions keep going: a call that was running gets "try again", nothing else shows. Session state the daemon held (MCP subscriptions, if one is ever added) is not replayed, only `initialize`.
- The daemon runs every agent's tool calls. It already held WhatsApp and delivered the outbox, so sends behave the same.
