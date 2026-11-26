package mcpserver_test

import (
	"bufio"
	"context"
	"encoding/json"
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
	"github.com/Theyashsawarkar/narada/internal/sock"
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

// daemon is the daemon's socket server: stop is its SIGTERM (it returns
// when the drain is over); life is what the drain runs under.
type daemon struct {
	stop func()
	life context.Context
	done chan error
}

func serve(t *testing.T, a *app.App, path string) *daemon {
	t.Helper()
	l, err := sock.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	life, endLife := context.WithCancel(context.Background())
	d := &daemon{life: life, done: make(chan error, 1)}
	go func() { d.done <- mcpserver.Serve(ctx, l, a, cli.MCPHelpers(), mcpserver.ServeOptions{Life: life}) }()
	var once sync.Once
	d.stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-d.done:
				if err != nil {
					t.Errorf("Serve: %v", err)
				}
			case <-time.After(mcpserver.DrainFor + 10*time.Second):
				t.Error("Serve didn't stop")
			}
			endLife()
		})
	}
	t.Cleanup(d.stop)
	return d
}

// deliverer is the daemon's outbox and "the user", approving every send
// (each agent's name noted) until ctx ends.
func deliverer(ctx context.Context, a *app.App, approve bool) (agents func() []string) {
	var mu sync.Mutex
	var names []string
	go func() {
		seen := map[int64]bool{}
		for ctx.Err() == nil {
			a.DeliverDue(ctx)
			items, _ := a.Store.Outbox(store.StatusApproval)
			for _, it := range items {
				if !seen[it.ID] {
					seen[it.ID] = true
					mu.Lock()
					names = append(names, it.Out.Agent)
					mu.Unlock()
					if approve {
						_ = a.Approve(it.ID, nil)
					}
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), names...)
	}
}

// dialHello connects and says hello (pranali's, or any first line);
// reply is the daemon's answer line ("" when none is expected).
func dialHello(t *testing.T, path, hello string, wantReply bool) (net.Conn, *bufio.Reader, string) {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Write([]byte(hello + "\n")); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	reply := ""
	if wantReply {
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("hello reply: %v", err)
		}
		_ = c.SetReadDeadline(time.Time{})
		reply = strings.TrimSpace(line)
	}
	return c, br, reply
}

func pranaliHello(agent, cwd string) string {
	b, _ := json.Marshal(map[string]any{"pranali": 1, "tool": "narada", "agent": agent, "pid": os.Getpid(), "cwd": cwd,
		"env": map[string]string{"TMUX": ""}, "future_field": true})
	return string(b)
}

// agentSession is an agent behind pranali: the hello, then MCP.
func agentSession(t *testing.T, path, hello, name string, wantReply bool) *mcp.ClientSession {
	t.Helper()
	c, br, reply := dialHello(t, path, hello, wantReply)
	if wantReply && reply != `{"ok":true,"pranali":1}` && reply != `{"pranali":1,"ok":true}` {
		t.Fatalf("hello reply: %s", reply)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: name}, nil).Connect(context.Background(), &mcp.IOTransport{Reader: readCloser{br, c}, Writer: c}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

type readCloser struct {
	io.Reader
	io.Closer
}

func TestDaemonSocket(t *testing.T) {
	a, g := testApp(t)
	path := shortSocket(t)
	d := serve(t, a, path)
	if fi, err := os.Stat(filepath.Dir(path)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("socket dir: %v %v", fi.Mode(), err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket: %v %v", fi.Mode(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agents := deliverer(ctx, a, true)

	// three agents at once, each its own session: two through pranali
	// (one with --agent), one through the narada-pipe of before
	var wg sync.WaitGroup
	for _, x := range []struct{ client, hello string }{
		{"claude-code", pranaliHello("agent", "/")},
		{"codex-mcp-client", pranaliHello("someone", "/")},
		{"gemini-cli", `{"narada_pipe":1,"env":{"TMUX":""}}`},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cs := agentSession(t, path, x.hello, x.client, strings.Contains(x.hello, "pranali"))
			if r := cs.InitializeResult(); r == nil || r.ServerInfo.Name != "narada" {
				t.Errorf("%s: initialize: %+v", x.client, r)
			}
			tools, err := cs.ListTools(context.Background(), nil)
			if err != nil || len(tools.Tools) != 16 {
				t.Errorf("%s: tools: %v", x.client, err)
			}
			if out := call(t, cs, "mail_list", map[string]any{"today": true}); !strings.Contains(out, "id=t-priya") {
				t.Errorf("%s: mail_list: %s", x.client, out)
			}
			if out := call(t, cs, "mail_send", map[string]any{"reply_to_thread": "t-priya", "body": "From " + x.client, "why": "test"}); out != "Sent." {
				t.Errorf("%s: mail_send: %q", x.client, out)
			}
			cs.Close()
		}()
	}
	wg.Wait()
	names := agents()
	sort.Strings(names)
	// the client's name wins over pranali's --agent label
	if strings.Join(names, ",") != "Claude Code,Codex,Gemini" {
		t.Errorf("agents: %q", names)
	}
	if len(g.Sent) != 3 {
		t.Errorf("sent %d", len(g.Sent))
	}

	// a plain MCP client on the socket (no hello line) works too
	raw, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "socat"}, nil).Connect(context.Background(), &mcp.IOTransport{Reader: raw, Writer: raw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tools, err := cs.ListTools(context.Background(), nil); err != nil || len(tools.Tools) != 16 {
		t.Fatalf("raw client: %v", err)
	}
	cs.Close()

	// stopping removes the socket this daemon bound
	d.stop()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind: %v", err)
	}
}

func TestHelloRefused(t *testing.T) {
	a, _ := testApp(t)
	path := shortSocket(t)
	serve(t, a, path)
	for hello, want := range map[string]string{
		`{"pranali":2,"tool":"narada"}`: "protocol 1",
		`{"pranali":1,"tool":"ekatva"}`: "not ekatva's",
	} {
		c, br, reply := dialHello(t, path, hello, true)
		var r struct {
			Pranali int    `json:"pranali"`
			OK      bool   `json:"ok"`
			Error   string `json:"error"`
		}
		if json.Unmarshal([]byte(reply), &r) != nil || r.Pranali != 1 || r.OK || !strings.Contains(r.Error, want) {
			t.Errorf("%s: %s", hello, reply)
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := br.ReadByte(); !errors.Is(err, io.EOF) {
			t.Errorf("%s: the connection should close after a refusal: %v", hello, err)
		}
	}
}

// A relative attachment is the agent's file (pranali's cwd), not the
// daemon's.
func TestHelloCwd(t *testing.T) {
	a, _ := testApp(t)
	path := shortSocket(t)
	serve(t, a, path)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	cs := agentSession(t, path, pranaliHello("claude", dir), "claude-code", true)
	go func() {
		_, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "mail_send",
			Arguments: map[string]any{"reply_to_thread": "t-priya", "body": "see attached", "attachments": []string{"notes.txt"}, "why": "test"}})
	}()
	it := waitApproval(t, a)
	if len(it.Out.Files) != 1 || it.Out.Files[0].Name != "notes.txt" || it.Out.Files[0].Size != 5 {
		t.Fatalf("files: %+v", it.Out.Files)
	}
	_ = a.Decline(it.ID)
}

func waitApproval(t *testing.T, a *app.App) store.OutboxItem {
	t.Helper()
	for i := 0; i < 250; i++ {
		if items, _ := a.Store.Outbox(store.StatusApproval); len(items) > 0 {
			return items[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the send never waited for approval")
	return store.OutboxItem{}
}

// SIGTERM while a send waits for approval: the daemon stops taking
// calls, idle sessions close, and the running call still gets its
// answer when the user approves during the drain. A request arriving
// meanwhile is answered "not run".
func TestDrainLetsRunningCallsFinish(t *testing.T) {
	a, g := testApp(t)
	path := shortSocket(t)
	d := serve(t, a, path)
	busy := agentSession(t, path, pranaliHello("agent", "/"), "claude-code", true)
	idle := agentSession(t, path, pranaliHello("agent", "/"), "claude-code", true)
	answer := make(chan string, 1)
	go func() {
		answer <- call(t, busy, "mail_send", map[string]any{"reply_to_thread": "t-priya", "body": "drain", "why": "test"})
	}()
	it := waitApproval(t, a)
	stopped := make(chan struct{})
	go func() { d.stop(); close(stopped) }()
	time.Sleep(200 * time.Millisecond)
	// the idle session is closed at once
	if _, err := idle.ListTools(context.Background(), nil); err == nil {
		t.Error("an idle session should close when the daemon stops")
	}
	// the socket takes no one new
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		t.Error("still accepting while draining")
	}
	// a new call on the busy session: not run
	if res, err := busy.CallTool(context.Background(), &mcp.CallToolParams{Name: "contacts_find", Arguments: map[string]any{"query": "priya"}}); err == nil || !strings.Contains(err.Error(), "this call was not run, retry it") {
		t.Errorf("a call during the drain: %v %v", res, err)
	}
	select {
	case <-stopped:
		t.Fatal("stopped before the running call finished")
	default:
	}
	// the user approves; the daemon's outbox (still running) sends it
	ctx, cancel := context.WithCancel(d.life)
	defer cancel()
	deliverer(ctx, a, false)
	if err := a.Approve(it.ID, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-answer:
		if out != "Sent." {
			t.Fatalf("running call: %q", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the running call never answered")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon didn't stop once its calls were done")
	}
	if len(g.Sent) != 1 {
		t.Errorf("sent %d", len(g.Sent))
	}
}

// pranali half-closes when its agent leaves: answers written after that
// still arrive.
func TestHalfOpenGetsLateAnswers(t *testing.T) {
	a, _ := testApp(t)
	path := shortSocket(t)
	serve(t, a, path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deliverer(ctx, a, false)
	c, br, reply := dialHello(t, path, pranaliHello("agent", "/"), true)
	if !strings.Contains(reply, `"ok":true`) {
		t.Fatal(reply)
	}
	for _, l := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"late","method":"tools/call","params":{"name":"mail_send","arguments":{"reply_to_thread":"t-priya","body":"late","why":"test"}}}`,
	} {
		if _, err := c.Write([]byte(l + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	it := waitApproval(t, a)
	if err := c.(*net.UnixConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := a.Approve(it.ID, nil); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("the late answer never came: %v", err)
		}
		if strings.Contains(line, `"id":"late"`) {
			if !strings.Contains(line, "Sent.") {
				t.Fatalf("late answer: %s", line)
			}
			break
		}
	}
	// then the daemon closes its side
	if _, err := br.ReadString('\n'); !errors.Is(err, io.EOF) {
		t.Fatalf("after the last answer: %v", err)
	}
}

// One session's call stuck waiting doesn't hold up another's, and a call
// that never ends still gets an answer at its limit.
func TestCallsDontBlockEachOther(t *testing.T) {
	a, _ := testApp(t)
	path := shortSocket(t)
	serve(t, a, path)
	old := mcpserver.CallTimeout
	mcpserver.CallTimeout = 2 * time.Second
	t.Cleanup(func() { mcpserver.CallTimeout = old })
	stuck := agentSession(t, path, pranaliHello("agent", "/"), "claude-code", true)
	other := agentSession(t, path, pranaliHello("agent", "/"), "codex", true)
	answer := make(chan string, 1)
	go func() {
		answer <- call(t, stuck, "mail_send", map[string]any{"reply_to_thread": "t-priya", "body": "never approved", "why": "test"})
	}()
	waitApproval(t, a)
	start := time.Now()
	for _, cs := range []*mcp.ClientSession{other, stuck} {
		if out := call(t, cs, "contacts_find", map[string]any{"query": "priya"}); strings.HasPrefix(out, "ERROR") {
			t.Fatal(out)
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("other calls took %s while one waited", d)
	}
	select {
	case out := <-answer:
		if !strings.Contains(out, "Still waiting for the user's approval") && !strings.Contains(out, "given up on") {
			t.Fatalf("stuck call: %q", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stuck call never answered")
	}
}
