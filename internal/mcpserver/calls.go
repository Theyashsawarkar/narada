package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Theyashsawarkar/narada/internal/app"
)

// One daemon serves every agent. Calls already run side by side (the SDK
// handles each request on its own goroutine, each connection is its own
// session, and SQLite waits at most busy_timeout, 10 s, for a write
// lock), so a slow call holds up only itself. What callTimeouts adds is
// an end: every call answers within its kind's limit, even when what it
// waits on never comes back.

// callLimit is how long a tool call may take.
func callLimit(a *app.App, tool string) time.Duration {
	switch tool {
	case "mail_send", "chat_send", "whatsapp_send":
		// the approval (10 min), then the undo window and the send
		undo := max(a.Config.UndoSeconds, a.Config.ChatUndoSeconds, a.Config.AgentUndoSeconds)
		return 10*time.Minute + time.Duration(undo+90)*time.Second + time.Minute
	case "attachment_save":
		return time.Hour // WhatsApp files up to 2 GB on a slow line
	}
	return 2 * time.Minute
}

// CallTimeout overrides callLimit (tests).
var CallTimeout time.Duration

func callTimeouts(a *app.App) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctr, ok := req.(*mcp.CallToolRequest)
			if method != "tools/call" || !ok || ctr.Params == nil {
				return next(ctx, method, req)
			}
			limit := CallTimeout
			if limit == 0 {
				limit = callLimit(a, ctr.Params.Name)
			}
			cctx, cancel := context.WithTimeout(ctx, limit)
			type answer struct {
				r   mcp.Result
				err error
			}
			done := make(chan answer, 1)
			go func() {
				defer cancel()
				r, err := next(cctx, method, req)
				done <- answer{r, err}
			}()
			select {
			case x := <-done:
				return x.r, x.err
			case <-cctx.Done():
				if ctx.Err() != nil {
					return nil, ctx.Err() // the agent cancelled it
				}
				r := text(fmt.Sprintf("error: %s took longer than %s and was given up on; it may still finish (for a send, check narada's outbox before retrying)", ctr.Params.Name, limit))
				r.IsError = true
				return r, nil
			}
		}
	}
}
