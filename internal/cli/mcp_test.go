package cli

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Theyashsawarkar/narada/internal/fake"
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
