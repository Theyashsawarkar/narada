# 11. Agents reach the daemon through pranali

**Status:** Accepted (amends ADR 10: its pipe, `narada-pipe`, and its preamble are replaced; ADR 4's single binary is back)  
**Date:** 2026-11-26

## Context

ADR 10 gave narada its own pipe, `narada-pipe`. darshan, sutradhar and ekatva each grew one too: four pipes with four hellos and four reconnect state machines. pranali (प्रणाली, "channel") is one small stdio pipe for all of them, with one protocol (`docs/PROTOCOL.md` in pranali's repository). Its other half is the daemon's, and narada's daemon had gaps the protocol closes: the socket died with the daemon, SIGTERM cut every running call, an agent that half-closed lost its last answers, and without `XDG_RUNTIME_DIR` the socket went to `/tmp`.

## Decision

- **pranali is the pipe.** `narada mcp` execs `pranali narada --fallback <this narada> mcp --in-process` when `pranali` is on PATH; without it, it serves the tools in process. `internal/pipe` and `cmd/narada-pipe` are gone. Under `NARADA_HOME` (tests, demos) it sets `PRANALI_SOCKET` to that daemon's socket.
- **The hello.** The daemon reads one line first (5 s): pranali's `{"pranali":1,"tool":"narada","agent":…,"pid":…,"cwd":…,"env":{…}}`, answered `{"pranali":1,"ok":true}` (or `ok:false` with the reason: another protocol version, another tool's name). `env` (TMUX, TMUX_PANE, DISPLAY, WAYLAND_DISPLAY, XAUTHORITY, PATH) is where approval popups and click-to-chat go and what commands for that agent run with; `cwd` is what a relative attachment path is relative to; the agent is named by the MCP client's name, else pranali's `agent` label. The old `{"narada_pipe":1,…}` preamble (no answer) is still accepted for one release, for pipes already running in open sessions; a line that is neither is the MCP stream itself.
- **systemd owns the socket.** `narada-mcp.socket` (`ListenStream=%t/narada/mcp.sock`, 0600 in a 0700 folder, `Service=narada.service`) ships in `systemd/` and is written and enabled by `narada service install`; narada.service gets `Sockets=narada-mcp.socket` and `TimeoutStopSec=30`. The daemon takes fd 3 (`LISTEN_FDS`/`LISTEN_PID`, by name when there are several) and binds the path itself only when not socket-activated. While it restarts, connections wait in systemd's backlog. It never unlinks a socket it didn't bind: a leftover socket is removed only when connecting to it is refused, and its own is removed at exit only if the file is still the one it bound.
- **The path** is `$XDG_RUNTIME_DIR/narada/mcp.sock`, else `${XDG_STATE_HOME:-~/.local/state}/narada/run/mcp.sock`, never `/tmp`; the folder must be a real directory owned by the user with no group or other permissions.
- **Drain on SIGTERM.** The daemon stops accepting, closes idle sessions, lets running calls finish for up to 20 s (the outbox and WhatsApp keep running for them), answers a call that arrives meanwhile with -32002 "narada is restarting; this call was not run, retry it", then exits.
- **Half-open.** When the agent's side closes its writing half, answers still owed go out for up to 10 s before the session ends.
- **Per call.** Calls already run side by side (one goroutine per request, one session per connection, SQLite's 10 s busy timeout); every tool call now also has a limit (sends: the 10-minute approval plus the undo window and 2.5 min; attachment_save: an hour; the rest: 2 minutes) and answers when it's reached.

## Consequences

- One pipe to install for every tool; narada ships one binary again. Without pranali, an agent's `narada mcp` is a whole narada again (about 30 MB), as before ADR 10.
- A daemon restart (an update) no longer fails calls that were about to finish, and new connections during it wait instead of failing.
- Going live needs `narada service install` once (or the manual steps in the README), with the old daemon stopped before the socket unit starts: an older narada removes its socket file on the way out.
- The `narada_pipe` preamble goes in the release after the one that ships this.
