package pipe

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDaemon answers every request with {"result":{"echo":method}} and
// records the lines it got (the hello line too).
type fakeDaemon struct {
	t     *testing.T
	l     net.Listener
	mu    sync.Mutex
	got   []string
	conns []net.Conn
}

func startFake(t *testing.T, path string) *fakeDaemon {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDaemon{t: t, l: l}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns = append(f.conns, c)
			f.mu.Unlock()
			go func() {
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					line := sc.Text()
					f.mu.Lock()
					f.got = append(f.got, line)
					f.mu.Unlock()
					var m struct {
						ID     json.RawMessage `json:"id"`
						Method string          `json:"method"`
					}
					if json.Unmarshal([]byte(line), &m) != nil || len(m.ID) == 0 || m.Method == "" || m.Method == "hang" {
						continue
					}
					b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]string{"echo": m.Method}})
					_, _ = c.Write(append(b, '\n'))
				}
				c.Close() // the agent is done, as the daemon does
			}()
		}
	}()
	return f
}

func (f *fakeDaemon) stop() {
	f.l.Close()
	f.mu.Lock()
	for _, c := range f.conns {
		c.Close()
	}
	f.mu.Unlock()
}

func (f *fakeDaemon) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

func socketPath(t *testing.T) string {
	d, err := os.MkdirTemp("", "npipe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return filepath.Join(d, "mcp.sock")
}

func TestRunWithoutDaemon(t *testing.T) {
	ok, err := Run(socketPath(t), strings.NewReader("{}\n"), io.Discard, nil)
	if ok || err == nil {
		t.Fatalf("no daemon: ok=%v err=%v", ok, err)
	}
}

func TestReconnectGiveUpAndRetry(t *testing.T) {
	old := [3]time.Duration{FirstWait, MaxWait, RetryFor}
	FirstWait, MaxWait, RetryFor = 10*time.Millisecond, 20*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { FirstWait, MaxWait, RetryFor = old[0], old[1], old[2] })

	path := socketPath(t)
	f := startFake(t, path)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := Run(path, inR, outW, map[string]string{"TMUX": "/tmp/t,1,0"})
		outW.Close()
		done <- err
	}()
	out := bufio.NewScanner(outR)
	send := func(s string) { _, _ = inW.Write([]byte(s + "\n")) }
	next := func() string {
		t.Helper()
		if !out.Scan() {
			t.Fatal("stdout ended")
		}
		return out.Text()
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if got := next(); !strings.Contains(got, `"echo":"initialize"`) {
		t.Fatal(got)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":"a","method":"hang"}`) // never answered
	time.Sleep(100 * time.Millisecond)
	f.stop()
	if got := next(); !strings.Contains(got, `"id":"a"`) || !strings.Contains(got, `-32000`) || !strings.Contains(got, RestartMessage) {
		t.Fatalf("dropped request: %s", got)
	}
	// nobody comes back: after RetryFor the waiting request is answered
	time.Sleep(50 * time.Millisecond)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if got := next(); !strings.Contains(got, `"id":2`) || !strings.Contains(got, RestartMessage) {
		t.Fatalf("given up: %s", got)
	}
	// the daemon is back: the next request reconnects and goes through
	f = startFake(t, path)
	defer f.stop()
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/call"}`)
	if got := next(); got != `{"id":3,"jsonrpc":"2.0","result":{"echo":"tools/call"}}` {
		t.Fatalf("after reconnecting: %s", got) // the replayed initialize's answer was swallowed
	}
	got := f.lines()
	if len(got) != 4 || !strings.Contains(got[0], `"narada_pipe":1`) || !strings.Contains(got[0], "/tmp/t,1,0") ||
		!strings.Contains(got[1], `"id":"pipe-reinit-1"`) || !strings.Contains(got[1], `"method":"initialize"`) ||
		!strings.Contains(got[2], "notifications/initialized") || !strings.Contains(got[3], `"id":3`) {
		t.Fatalf("the daemon got: %q", got)
	}
	inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pipe didn't end with stdin")
	}
}
