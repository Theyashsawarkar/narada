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
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Theyashsawarkar/narada/internal/app"
)

// The daemon serves agents on a unix socket (internal/sock), so each
// agent's `narada mcp` is only pranali, a small pipe to it, instead of
// its own copy of narada with the store open. Each connection is its own
// MCP session: initialize goes through unchanged, so the agent's name
// still comes out right. The protocol is pranali's (docs/PROTOCOL.md in
// pranali's repo, ADR 11): one hello line each way, then MCP.

// HelloTimeout is how long a new connection has to say its first line.
var HelloTimeout = 5 * time.Second

// HalfOpenFor is how long answers still owed go out after the agent's
// side closed its writing half.
var HalfOpenFor = 10 * time.Second

// DrainFor is how long calls still running at SIGTERM get to finish
// (the unit's TimeoutStopSec=30 leaves room for the rest of the stop).
var DrainFor = 20 * time.Second

// hello is pranali's first line, {"pranali":1,"tool":"narada",
// "agent":"claude","pid":1234,"cwd":"/home/u/x","env":{...}}. Unknown
// fields are ignored.
//
// LegacyPipe is the narada_pipe preamble of the narada-pipe that came
// before pranali ({"narada_pipe":1,"env":{...}}, no reply expected).
// Pipes started by that version keep running in open agent sessions and
// reconnect after the daemon restarts, so it is still accepted for one
// release; drop it in the release after the one that ships pranali.
type hello struct {
	Pranali    int               `json:"pranali"`
	Tool       string            `json:"tool"`
	Agent      string            `json:"agent"`
	PID        int               `json:"pid"`
	Cwd        string            `json:"cwd"`
	Env        map[string]string `json:"env"`
	LegacyPipe int               `json:"narada_pipe"`
	Kick       int               `json:"narada_kick"`
}

// ServeOptions are the daemon's hooks into the socket server.
type ServeOptions struct {
	// Life is what sessions and their calls run under. It must outlive
	// ctx (Serve's) by the drain: SIGTERM ends ctx, the calls still
	// running finish under Life.
	Life context.Context
	// Kick is called for a {"narada_kick":1} line: another narada
	// process put work in the outbox for the daemon.
	Kick func()
}

// Serve runs MCP sessions on l until ctx ends, then drains: it stops
// accepting (with socket activation, systemd keeps the socket and new
// connections wait in its backlog for the next daemon), closes the
// sessions that have nothing running, lets calls still running finish
// for up to DrainFor (a request arriving meanwhile is answered "not run,
// retry"), then closes the rest. It closes l; a self-bound socket's file
// goes with it, systemd's stays.
func Serve(ctx context.Context, l net.Listener, a *app.App, h Helpers, opt ServeOptions) error {
	if opt.Life == nil {
		opt.Life = context.Background()
	}
	life, endLife := context.WithCancel(opt.Life)
	defer endLife()
	var (
		mu       sync.Mutex
		sessions = map[*session]bool{}
		wg       sync.WaitGroup
		draining bool
	)
	accepted := make(chan error, 1)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				accepted <- err
				return
			}
			s := newSession(c)
			mu.Lock()
			if draining {
				mu.Unlock()
				c.Close()
				continue
			}
			sessions[s] = true
			wg.Add(1)
			mu.Unlock()
			go func() {
				defer wg.Done()
				defer func() {
					mu.Lock()
					delete(sessions, s)
					mu.Unlock()
					s.close()
				}()
				if err := s.serve(life, a, h, opt); err != nil && life.Err() == nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					log.Printf("mcp session: %v", err)
				}
			}()
		}
	}()
	var err error
	select {
	case <-ctx.Done():
	case err = <-accepted:
	}
	l.Close()
	mu.Lock()
	draining = true
	for s := range sessions {
		s.drain()
	}
	mu.Unlock()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(DrainFor):
		mu.Lock()
		n := len(sessions)
		for s := range sessions {
			s.close()
		}
		mu.Unlock()
		log.Printf("mcp: %d session(s) still busy after %s: closed", n, DrainFor)
		endLife()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// session is one connection: it tracks the requests the agent sent that
// aren't answered yet, so the read side's end (pranali half-closes when
// its agent leaves) and the drain both wait for them.
type session struct {
	c  net.Conn
	br *bufio.Reader

	mu       sync.Mutex
	inflight map[string]bool
	idle     *sync.Cond
	draining bool
	closed   bool

	wmu sync.Mutex // one line at a time on the wire
	buf []byte     // what the server wrote of a line so far
}

func newSession(c net.Conn) *session {
	s := &session{c: c, br: bufio.NewReader(c), inflight: map[string]bool{}}
	s.idle = sync.NewCond(&s.mu)
	return s
}

// drain: no new calls; close as soon as nothing runs.
func (s *session) drain() {
	s.mu.Lock()
	s.draining = true
	idle := len(s.inflight) == 0
	s.mu.Unlock()
	if idle {
		s.close()
	}
}

func (s *session) close() {
	s.mu.Lock()
	s.closed = true
	s.idle.Broadcast()
	s.mu.Unlock()
	s.c.Close()
}

func (s *session) serve(life context.Context, a *app.App, h Helpers, opt ServeOptions) error {
	_ = s.c.SetReadDeadline(time.Now().Add(HelloTimeout))
	first, err := s.br.ReadBytes('\n')
	_ = s.c.SetReadDeadline(time.Time{})
	if err != nil && len(first) == 0 {
		return err
	}
	env := app.Env{} // the agent's, never the daemon's own tmux
	var agent, cwd string
	var hi hello
	isHello := json.Unmarshal(first, &hi) == nil
	switch {
	case isHello && hi.Kick == 1:
		if opt.Kick != nil {
			opt.Kick()
		}
		return nil
	case isHello && hi.Pranali != 0:
		reply := map[string]any{"pranali": 1, "ok": true}
		switch {
		case hi.Pranali != 1:
			reply = map[string]any{"pranali": 1, "ok": false, "error": "narada speaks pranali protocol 1, not " + jsonInt(hi.Pranali) + ": update narada or pranali"}
		case hi.Tool != "" && hi.Tool != "narada":
			reply = map[string]any{"pranali": 1, "ok": false, "error": "this is narada's socket, not " + hi.Tool + "'s"}
		}
		b, _ := json.Marshal(reply)
		if _, err := s.c.Write(append(b, '\n')); err != nil || reply["ok"] != true {
			return err
		}
		for _, k := range app.EnvKeys {
			if v, ok := hi.Env[k]; ok && v != "" {
				env[k] = v
			}
		}
		if hi.Agent != "agent" { // pranali's default says nothing
			agent = hi.Agent // when the MCP client gives no name
		}
		cwd = hi.Cwd
	case isHello && hi.LegacyPipe == 1:
		// narada-pipe (before pranali): no reply, MCP follows
		for _, k := range app.EnvKeys {
			if v := hi.Env[k]; v != "" {
				env[k] = v
			}
		}
	default:
		// no hello: the line is the MCP stream itself (socat works too)
		s.br = bufio.NewReader(io.MultiReader(bytes.NewReader(first), s.br))
	}
	srv := newServerFor(life, a, h, env, agent, cwd)
	return srv.Run(life, &mcp.IOTransport{Reader: (*sessionReader)(s), Writer: (*sessionWriter)(s)})
}

func jsonInt(n int) string { b, _ := json.Marshal(n); return string(b) }

// lineID is a JSON-RPC line's id (compact JSON, so 1 and "1" differ; ""
// for none or null) and method; batches and other values give "", "".
func lineID(line []byte) (id, method string) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(line, &m) != nil {
		return "", ""
	}
	if len(m.ID) > 0 && !bytes.Equal(m.ID, []byte("null")) {
		var b bytes.Buffer
		if json.Compact(&b, m.ID) == nil {
			id = b.String()
		}
	}
	return id, m.Method
}

// NotRunCode and notRun answer a request that came in while the daemon
// was stopping: it never ran, so retrying is safe (pranali's own -32001
// is for calls that were running: those may have happened).
const NotRunCode = -32002

func notRun(id string) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id),
		"error": map[string]any{"code": NotRunCode, "message": "narada is restarting; this call was not run, retry it"}})
	return append(b, '\n')
}

// sessionReader is what the MCP server reads: the agent's lines, each
// request counted as running until its answer goes out. When the agent's
// side ends it waits for those answers before saying EOF, so a
// half-closed connection still gets them (the SDK cancels every running
// call at EOF).
type sessionReader session

func (r *sessionReader) Read(p []byte) (int, error) {
	s := (*session)(r)
	for {
		line, err := s.br.ReadBytes('\n')
		if len(line) > 0 {
			id, method := lineID(line)
			if id != "" && method != "" {
				s.mu.Lock()
				if s.draining {
					s.mu.Unlock()
					s.writeLine(notRun(id))
					if err == nil {
						continue
					}
				} else {
					s.inflight[id] = true
					s.mu.Unlock()
					return r.give(p, line)
				}
			} else {
				return r.give(p, line)
			}
		}
		if err != nil {
			// the agent's side ended (pranali half-closes): answers it is
			// owed still go out, for up to HalfOpenFor
			s.mu.Lock()
			if len(s.inflight) > 0 && !s.closed {
				t := time.AfterFunc(HalfOpenFor, func() {
					s.mu.Lock()
					s.closed = true // stop waiting; the SDK cancels the rest
					s.idle.Broadcast()
					s.mu.Unlock()
				})
				for len(s.inflight) > 0 && !s.closed {
					s.idle.Wait()
				}
				t.Stop()
			}
			s.mu.Unlock()
			return 0, err
		}
	}
}

// give hands a whole line to the decoder (lines longer than p carry on
// in the next Read through br, which can't happen with the SDK's 4 KB+
// reads of short lines, so they are pushed back instead).
func (r *sessionReader) give(p []byte, line []byte) (int, error) {
	s := (*session)(r)
	n := copy(p, line)
	if n < len(line) {
		s.br = bufio.NewReader(io.MultiReader(bytes.NewReader(line[n:]), s.br))
	}
	return n, nil
}

func (r *sessionReader) Close() error { (*session)(r).close(); return nil }

// sessionWriter is what the MCP server writes: each answer retires its
// request; when the last one goes during a drain, the session closes.
type sessionWriter session

func (w *sessionWriter) Write(p []byte) (int, error) {
	s := (*session)(w)
	s.wmu.Lock()
	s.buf = append(s.buf, p...)
	var lines [][]byte
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, s.buf[:i+1])
		s.buf = s.buf[i+1:]
	}
	var err error
	for _, l := range lines {
		if _, e := s.c.Write(l); e != nil && err == nil {
			err = e
		}
	}
	if len(s.buf) == 0 {
		s.buf = nil
	}
	s.wmu.Unlock()
	closeNow := false
	for _, l := range lines {
		if id, method := lineID(l); id != "" && method == "" {
			s.mu.Lock()
			delete(s.inflight, id)
			if len(s.inflight) == 0 {
				s.idle.Broadcast()
				closeNow = s.draining
			}
			s.mu.Unlock()
		}
	}
	if closeNow {
		s.close()
	}
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *sessionWriter) Close() error { (*session)(w).close(); return nil }

// writeLine puts a line of the daemon's own between the server's.
func (s *session) writeLine(l []byte) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, _ = s.c.Write(l) // a part-line of the server's waits in buf, unsent

}
