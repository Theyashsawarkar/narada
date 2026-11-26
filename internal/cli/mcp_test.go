package cli

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/fake"
	"github.com/Theyashsawarkar/narada/internal/sock"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// With no daemon to pipe to, `narada mcp` serves the tools itself.
func TestMCPFallsBackInProcess(t *testing.T) {
	g := fake.New()
	g.Seed()
	srv := httptest.NewServer(g)
	defer srv.Close()
	t.Setenv("NARADA_GOOGLE_URL", srv.URL)
	t.Setenv("NARADA_HOME", t.TempDir()) // its socket: nothing listens there
	t.Setenv("TMUX", "")
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- mcpCmd(nil, inR, outW)
		outW.Close()
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "claude-code"}, nil).Connect(context.Background(), &mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 16 {
		t.Fatalf("tools: %v", err)
	}
	cs.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("narada mcp didn't end with its agent")
	}
}

// `narada mcp` becomes pranali; a test or demo daemon (NARADA_HOME) is
// found through PRANALI_SOCKET.
func TestPranaliArgs(t *testing.T) {
	t.Setenv("NARADA_HOME", "/srv/n")
	t.Setenv("PRANALI_SOCKET", "")
	os.Unsetenv("PRANALI_SOCKET")
	argv, env := pranaliArgs("/opt/narada", []string{"A=1"})
	if strings.Join(argv, " ") != "pranali narada --fallback /opt/narada mcp --in-process" {
		t.Errorf("argv: %q", argv)
	}
	if strings.Join(env, " ") != "A=1 PRANALI_SOCKET=/srv/n/run/mcp.sock" {
		t.Errorf("env: %q", env)
	}
	os.Unsetenv("NARADA_HOME")
	if _, env := pranaliArgs("/opt/narada", []string{"A=1"}); len(env) != 1 {
		t.Errorf("the real daemon needs no PRANALI_SOCKET: %q", env)
	}
}

// The unit files shipped in systemd/ are what `narada service install`
// writes.
func TestShippedUnits(t *testing.T) {
	for file, want := range map[string]string{
		"../../systemd/narada.service":    app.Unit("/usr/bin/narada"),
		"../../systemd/narada-mcp.socket": app.SocketUnit,
	} {
		b, err := os.ReadFile(file)
		if err != nil || string(b) != want {
			t.Errorf("%s differs from the one service install writes (%v)", file, err)
		}
	}
	for _, line := range []string{"Sockets=narada-mcp.socket", "TimeoutStopSec=30"} {
		if !strings.Contains(app.Unit("x"), line+"\n") {
			t.Errorf("narada.service lacks %s", line)
		}
	}
}

// SIGTERM to the daemon while an agent's send waits for approval: the
// call still finishes (approved during the drain, sent by the daemon's
// outbox), then the daemon exits.
func TestDaemonDrainsOnSIGTERM(t *testing.T) {
	g := fake.New()
	g.Seed()
	srv := httptest.NewServer(g)
	defer srv.Close()
	t.Setenv("NARADA_GOOGLE_URL", srv.URL)
	home, err := os.MkdirTemp("", "ndr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("NARADA_HOME", home)
	t.Setenv("TMUX", "")
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Config.UndoSeconds, a.Config.Notify = 0, false
	_ = a.Store.AddAccount(store.Account{Name: "work", Chat: true})
	done := make(chan error, 1)
	go func() { done <- daemon(a, nil) }()
	path := sock.Path()
	var c net.Conn
	for i := 0; ; i++ {
		if c, err = net.Dial("unix", path); err == nil {
			break
		}
		if i > 250 {
			t.Fatalf("no socket: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer c.Close()
	br := bufio.NewReader(c)
	for _, l := range []string{
		`{"pranali":1,"tool":"narada","agent":"agent","env":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"mail_send","arguments":{"reply_to_thread":"t-priya","body":"drain","why":"test"}}}`,
	} {
		if _, err := c.Write([]byte(l + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	var id int64
	for i := 0; id == 0; i++ {
		if items, _ := a.Store.Outbox(store.StatusApproval); len(items) > 0 {
			id = items[0].ID
		} else if i > 500 {
			t.Fatal("the send never waited for approval")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("the daemon exited with a call running: %v", err)
	default:
	}
	if err := a.Approve(id, nil); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("no answer: %v", err)
		}
		if strings.Contains(line, `"id":2`) {
			if !strings.Contains(line, "Sent.") {
				t.Fatalf("answer: %s", line)
			}
			break
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon didn't exit after the drain")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket left: %v", err)
	}
	if len(g.Sent) != 1 {
		t.Errorf("sent %d", len(g.Sent))
	}
}
