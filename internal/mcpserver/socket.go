package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/pipe"
)

// The daemon serves agents on a unix socket, so each agent's `narada mcp`
// is only a pipe to it (a few MB) instead of its own copy of narada with
// the store open. Each connection is its own MCP session: initialize goes
// through unchanged, so the agent's name still comes out right.
//
// A pipe (internal/pipe) first sends a hello line with its tmux and
// display, so approvals pop up beside the agent. Any other first line is
// the MCP stream itself (socat works too).

// Listen serves MCP sessions on the unix socket at path until ctx ends,
// then closes them and removes the socket. The directory is made 0700 and
// the socket 0600; a socket left by a daemon that died is replaced (the
// caller holds the daemon lock, so no live daemon owns it).
func Listen(ctx context.Context, a *app.App, h Helpers, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	l.(*net.UnixListener).SetUnlinkOnClose(true)
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return err
	}
	var (
		mu    sync.Mutex
		conns = map[net.Conn]bool{}
		wg    sync.WaitGroup
	)
	go func() {
		<-ctx.Done()
		l.Close()
		mu.Lock()
		for c := range conns {
			c.Close()
		}
		mu.Unlock()
	}()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			l.Close()
			wg.Wait()
			return err
		}
		mu.Lock()
		conns[c] = true
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				mu.Lock()
				delete(conns, c)
				mu.Unlock()
				c.Close()
			}()
			if err := serveConn(ctx, a, h, c); err != nil && ctx.Err() == nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("mcp session: %v", err)
			}
		}()
	}
}

func serveConn(ctx context.Context, a *app.App, h Helpers, c net.Conn) error {
	br := bufio.NewReader(c)
	first, err := br.ReadBytes('\n')
	if err != nil && len(first) == 0 {
		return err
	}
	env := app.Env{} // the agent's, never the daemon's own tmux
	var r io.Reader = br
	var hi pipe.Hello
	if json.Unmarshal(first, &hi) == nil && hi.Pipe == 1 {
		for k, v := range hi.Env {
			env[k] = v
		}
	} else {
		r = io.MultiReader(bytes.NewReader(first), br)
	}
	srv := newServer(ctx, a, h, env)
	return srv.Run(ctx, &mcp.IOTransport{Reader: readCloser{r, c}, Writer: c})
}

type readCloser struct {
	io.Reader
	io.Closer
}
