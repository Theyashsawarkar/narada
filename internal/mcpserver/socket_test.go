package mcpserver_test

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/cli"
	"github.com/Theyashsawarkar/narada/internal/mcpserver"
	"github.com/Theyashsawarkar/narada/internal/pipe"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// shortSocket is a socket path short enough for a unix socket (108 bytes).
func shortSocket(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "nmcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return filepath.Join(d, "run", "mcp.sock")
}

// listen runs the daemon's socket server; stop ends it and waits.
func listen(t *testing.T, a *app.App, sock string) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mcpserver.Listen(ctx, a, cli.MCPHelpers(), sock) }()
	for i := 0; ; i++ {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			break
		}
		if i > 100 {
			t.Fatal("the socket never came up")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Listen: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Listen didn't stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// pipeSession is an agent talking through `narada mcp`'s pipe.
func pipeSession(t *testing.T, sock, name string) (*mcp.ClientSession, <-chan error) {
	t.Helper()
	inR, inW := io.Pipe()   // agent -> pipe's stdin
	outR, outW := io.Pipe() // pipe's stdout -> agent
	ended := make(chan error, 1)
	go func() {
		ok, err := pipe.Run(sock, inR, outW, map[string]string{"TMUX": ""})
		if !ok && err == nil {
			err = errors.New("not connected")
		}
		outW.Close()
		ended <- err
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: name}, nil).Connect(context.Background(), &mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs, ended
}

func TestDaemonSocket(t *testing.T) {
	a, g := testApp(t)
	sock := shortSocket(t)
	// a socket file left by a daemon that died
	_ = os.MkdirAll(filepath.Dir(sock), 0o755)
	if err := os.WriteFile(sock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stop := listen(t, a, sock)
	if fi, err := os.Stat(filepath.Dir(sock)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("socket dir: %v %v", fi.Mode(), err)
	}
	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o600 || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket: %v %v", fi.Mode(), err)
	}

	// what the daemon does: deliver, and "the user" approves each send
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var agents []string
	go func() {
		seen := map[int64]bool{}
		for ctx.Err() == nil {
			a.DeliverDue(ctx)
			items, _ := a.Store.Outbox(store.StatusApproval)
			for _, it := range items {
				if !seen[it.ID] {
					seen[it.ID] = true
					mu.Lock()
					agents = append(agents, it.Out.Agent)
					mu.Unlock()
					_ = a.Approve(it.ID, nil)
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	// two agents at once, each its own session
	var wg sync.WaitGroup
	for _, name := range []string{"claude-code", "codex-mcp-client"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cs, ended := pipeSession(t, sock, name)
			if r := cs.InitializeResult(); r == nil || r.ServerInfo.Name != "narada" {
				t.Errorf("%s: initialize: %+v", name, r)
			}
			tools, err := cs.ListTools(context.Background(), nil)
			if err != nil || len(tools.Tools) != 16 {
				t.Errorf("%s: tools: %v", name, err)
			}
			if out := call(t, cs, "mail_list", map[string]any{"today": true}); !strings.Contains(out, "id=t-priya") {
				t.Errorf("%s: mail_list: %s", name, out)
			}
			if out := call(t, cs, "mail_send", map[string]any{"reply_to_thread": "t-priya", "body": "From " + name, "why": "test"}); out != "Sent." {
				t.Errorf("%s: mail_send: %q", name, out)
			}
			cs.Close()
			select {
			case err := <-ended:
				if err != nil {
					t.Errorf("%s: pipe: %v", name, err)
				}
			case <-time.After(5 * time.Second):
				t.Errorf("%s: the pipe didn't end with its agent", name)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	sort.Strings(agents)
	if strings.Join(agents, ",") != "Claude Code,Codex" {
		t.Errorf("agents: %q", agents)
	}
	mu.Unlock()
	if len(g.Sent) != 2 {
		t.Errorf("sent %d", len(g.Sent))
	}

	// a plain MCP client on the socket (no hello line) works too
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "socat"}, nil).Connect(context.Background(), &mcp.IOTransport{Reader: c, Writer: c}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tools, err := cs.ListTools(context.Background(), nil); err != nil || len(tools.Tools) != 16 {
		t.Fatalf("raw client: %v", err)
	}

	// stopping closes open sessions and removes the socket
	stop()
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind: %v", err)
	}
	cs.Close()
}

func TestPipeWithoutDaemon(t *testing.T) {
	sock := shortSocket(t)
	ok, err := pipe.Run(sock, strings.NewReader("{}\n"), io.Discard, nil)
	if ok || err == nil {
		t.Fatalf("no daemon: ok=%v err=%v", ok, err)
	}
}

// An update restarts the daemon: the agent's session goes on. The call
// the daemon dropped gets an error, a call made while it was away waits
// for it, and later calls work without the agent initializing again.
func TestPipeSurvivesDaemonRestart(t *testing.T) {
	a, _ := testApp(t)
	sock := shortSocket(t)
	stop := listen(t, a, sock)
	cs, ended := pipeSession(t, sock, "claude-code")
	if out := call(t, cs, "mail_list", nil); !strings.Contains(out, "id=t-priya") {
		t.Fatalf("mail_list: %s", out)
	}
	// a send waiting for approval is in flight when the daemon goes
	inflight := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "mail_send", Arguments: map[string]any{"reply_to_thread": "t-priya", "body": "hi", "why": "test"}})
		inflight <- err
	}()
	for i := 0; ; i++ {
		if items, _ := a.Store.Outbox(store.StatusApproval); len(items) > 0 {
			break
		}
		if i > 200 {
			t.Fatal("the send never waited for approval")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	select {
	case err := <-inflight:
		if err == nil || !strings.Contains(err.Error(), "narada daemon restarted; try again") {
			t.Fatalf("in-flight call: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the dropped call was never answered")
	}
	// asked while the daemon is away: waits for it
	waiting := make(chan string, 1)
	go func() { waiting <- call(t, cs, "contacts_find", map[string]any{"query": "priya"}) }()
	time.Sleep(300 * time.Millisecond)
	listen(t, a, sock)
	select {
	case out := <-waiting:
		if strings.HasPrefix(out, "ERROR") {
			t.Fatalf("queued call: %s", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the queued call never went through")
	}
	if out := call(t, cs, "mail_list", nil); !strings.Contains(out, "id=t-priya") {
		t.Fatalf("mail_list after the restart: %s", out)
	}
	cs.Close()
	select {
	case err := <-ended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pipe didn't end with its agent")
	}
}
