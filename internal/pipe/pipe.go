// Package pipe is the agent's end of `narada mcp` when the daemon runs:
// stdin and stdout carried to and from the daemon's socket, nothing else.
// It imports only the standard library, so the narada-pipe helper built
// from it stays a few MB (narada itself carries WhatsApp, SQLite and the
// screen, and every one of their packages starts up with it).
//
// The pipe outlives the daemon: an update restarts narada.service, and
// every agent's MCP server would die with it. When the socket closes while
// the agent is still there, the requests the daemon never answered get an
// error, the pipe reconnects, replays the agent's initialize (answering it
// itself) and goes on; the agent only sees those errors.
package pipe

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Socket is where `narada daemon` serves agents:
// $XDG_RUNTIME_DIR/narada/mcp.sock (under NARADA_HOME when that is set,
// so a test or demo daemon never meets the real one).
func Socket() string {
	if h := os.Getenv("NARADA_HOME"); h != "" {
		return filepath.Join(h, "run", "mcp.sock")
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "narada", "mcp.sock")
	}
	return filepath.Join(os.TempDir(), "narada-"+strconv.Itoa(os.Getuid()), "mcp.sock")
}

// EnvKeys are the agent's surroundings the daemon needs: its tmux (the
// approval pops up beside the agent) and its display (links open there).
var EnvKeys = []string{"TMUX", "TMUX_PANE", "DISPLAY", "WAYLAND_DISPLAY"}

// Env is this process's EnvKeys.
func Env() map[string]string {
	env := map[string]string{}
	for _, k := range EnvKeys {
		env[k] = os.Getenv(k)
	}
	return env
}

// Hello is the line a pipe sends first on every connection:
// {"narada_pipe":1,"env":{...}}. The MCP stream follows it unchanged.
type Hello struct {
	Pipe int               `json:"narada_pipe"`
	Env  map[string]string `json:"env"`
}

// Reconnecting: the first wait, the longest, and how long to keep trying
// before answering what waits with an error (the next request tries again).
var (
	FirstWait = 100 * time.Millisecond
	MaxWait   = 2 * time.Second
	RetryFor  = 60 * time.Second
)

// MaxQueued requests wait for the daemon to come back; more are answered
// with an error at once.
const MaxQueued = 64

// RestartCode and RestartMessage answer a request the daemon dropped.
const (
	RestartCode    = -32000
	RestartMessage = "narada daemon restarted; try again"
)

// msg is what the pipe needs to know about a JSON-RPC line.
type msg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func parse(line []byte) (id string, method string) {
	var m msg
	if json.Unmarshal(line, &m) != nil {
		return "", "" // not an object: passed along, not followed
	}
	if len(m.ID) > 0 && !bytes.Equal(m.ID, []byte("null")) {
		var b bytes.Buffer
		if json.Compact(&b, m.ID) == nil {
			id = b.String()
		}
	}
	return id, m.Method
}

func errorLine(id string) []byte {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": json.RawMessage(id),
		"error": map[string]any{"code": RestartCode, "message": RestartMessage},
	})
	return append(b, '\n')
}

// withID is line with its id replaced.
func withID(line []byte, id string) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(line, &m) != nil {
		return line
	}
	m["id"] = json.RawMessage(id)
	b, _ := json.Marshal(m)
	return append(b, '\n')
}

func dial(path string, env map[string]string) (net.Conn, error) {
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(Hello{Pipe: 1, Env: env})
	if _, err := c.Write(append(b, '\n')); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// readLines sends each line of r (with its newline) to ch, then nil.
func readLines(r io.Reader, ch chan<- []byte) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if line[len(line)-1] != '\n' {
				line = append(line, '\n')
			}
			ch <- line
		}
		if err != nil {
			ch <- nil
			return
		}
	}
}

type daemonLine struct {
	conn net.Conn
	line []byte // nil: the connection closed
}

// Run connects to the socket at path, says hello with env, and carries
// in to it and its answers to out until in ends (then the daemon still
// gets to finish what it was asked). connected is false, and nothing was
// read from in, when there is no daemon at the start, so the caller can
// serve the tools itself; after that the pipe never gives up on its own:
// a daemon that goes away is waited for (see the package comment).
func Run(path string, in io.Reader, out io.Writer, env map[string]string) (connected bool, err error) {
	c, err := dial(path, env)
	if err != nil {
		return false, err
	}
	p := &pipeState{path: path, env: env, out: out, pending: map[string]bool{}, swallow: map[string]bool{}}
	return true, p.run(c, in)
}

type pipeState struct {
	path string
	env  map[string]string
	out  io.Writer

	conn        net.Conn // nil while the daemon is away
	ready       bool     // conn is set up (initialize replayed); else lines queue
	pending     map[string]bool
	swallow     map[string]bool // the pipe's own replayed initialize
	queue       [][]byte
	initialize  []byte // the agent's, to replay
	initialized []byte
	reinits     int
	retrying    bool // a reconnect is under way
	writeErr    error
}

func (p *pipeState) write(line []byte) {
	if p.writeErr == nil {
		_, p.writeErr = p.out.Write(line)
	}
}

// send gives a line from the agent to the daemon.
func (p *pipeState) send(line []byte) {
	// a failed write shows as the connection closing, which answers it
	if id, method := parse(line); id != "" && method != "" {
		p.pending[id] = true
	}
	_, _ = p.conn.Write(line)
}

func (p *pipeState) run(c net.Conn, in io.Reader) error {
	agent := make(chan []byte)
	go readLines(in, agent)
	daemon := make(chan daemonLine)
	reconnected := make(chan net.Conn)
	gaveUp := make(chan struct{})
	attach := func(c net.Conn) {
		p.conn = c
		go func() {
			lines := make(chan []byte)
			go readLines(c, lines)
			for l := range lines {
				daemon <- daemonLine{c, l}
				if l == nil {
					return
				}
			}
		}()
	}
	reconnect := func() {
		p.retrying = true
		go func() {
			wait, deadline := FirstWait, time.Now().Add(RetryFor)
			for {
				if c, err := dial(p.path, p.env); err == nil {
					reconnected <- c
					return
				}
				if time.Now().After(deadline) {
					gaveUp <- struct{}{}
					return
				}
				time.Sleep(wait)
				wait = min(wait*2, MaxWait)
			}
		}()
	}
	flush := func() {
		q := p.queue
		p.queue = nil
		for _, l := range q {
			p.send(l)
		}
	}
	attach(c)
	p.ready = true
	agentDone := false
	for {
		if p.writeErr != nil {
			if p.conn != nil {
				p.conn.Close()
			}
			return p.writeErr
		}
		select {
		case line := <-agent:
			if line == nil {
				agentDone = true
				agent = nil
				if p.conn == nil {
					return nil
				}
				if uc, ok := p.conn.(*net.UnixConn); ok {
					_ = uc.CloseWrite()
				} else {
					p.conn.Close()
				}
				continue
			}
			id, method := parse(line)
			switch method {
			case "initialize":
				p.initialize = line
			case "notifications/initialized":
				p.initialized = line
			}
			if p.ready {
				p.send(line)
				continue
			}
			if len(p.queue) >= MaxQueued {
				if id != "" && method != "" {
					p.write(errorLine(id))
				}
				continue
			}
			p.queue = append(p.queue, line)
			if !p.retrying && p.conn == nil {
				reconnect() // it gave up before: this request tries again
			}

		case d := <-daemon:
			if d.conn != p.conn {
				continue // an old connection's last words
			}
			if d.line == nil {
				p.conn.Close()
				p.conn, p.ready = nil, false
				if agentDone {
					return nil
				}
				for id := range p.pending {
					p.write(errorLine(id))
				}
				p.pending = map[string]bool{}
				p.swallow = map[string]bool{}
				reconnect()
				continue
			}
			id, method := parse(d.line)
			if id != "" && method == "" {
				if p.swallow[id] {
					// the replayed initialize is answered: carry on
					delete(p.swallow, id)
					if p.initialized != nil {
						p.send(p.initialized)
					}
					p.ready = true
					flush()
					continue
				}
				delete(p.pending, id)
			}
			p.write(d.line)

		case c := <-reconnected:
			p.retrying = false
			attach(c)
			if p.initialize == nil {
				p.ready = true
				flush()
				continue
			}
			p.reinits++
			id := strconv.Quote("pipe-reinit-" + strconv.Itoa(p.reinits))
			p.swallow[id] = true
			if _, err := c.Write(withID(p.initialize, id)); err != nil {
				continue // the reader sees it closed and we try again
			}

		case <-gaveUp:
			p.retrying = false
			for _, l := range p.queue {
				if id, method := parse(l); id != "" && method != "" {
					p.write(errorLine(id))
				}
			}
			p.queue = nil
		}
	}
}
