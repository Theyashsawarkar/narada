package mcpserver_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Theyashsawarkar/sandesh/internal/app"
	"github.com/Theyashsawarkar/sandesh/internal/cli"
	"github.com/Theyashsawarkar/sandesh/internal/fake"
	"github.com/Theyashsawarkar/sandesh/internal/mcpserver"
	"github.com/Theyashsawarkar/sandesh/internal/store"
)

func session(t *testing.T) (*app.App, *fake.Server, *mcp.ClientSession) {
	t.Helper()
	g := fake.New()
	g.Seed()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	t.Setenv("SANDESH_GOOGLE_URL", srv.URL)
	t.Setenv("SANDESH_HOME", t.TempDir())
	t.Setenv("TMUX", "") // no popups from tests
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.Config.UndoSeconds = 0
	_ = a.Store.AddAccount(store.Account{Name: "work", Chat: true})
	if _, errs := a.SyncAll(context.Background()); len(errs) > 0 {
		t.Fatal(errs)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	if _, err := mcpserver.New(a, cli.MCPHelpers()).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return a, g, cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if res.IsError {
		return "ERROR " + b.String()
	}
	return b.String()
}

func TestTools(t *testing.T) {
	a, g, cs := session(t)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 15 {
		t.Fatalf("tools: %v %v", len(tools.Tools), err)
	}
	out := call(t, cs, "mail_list", map[string]any{"today": true})
	if !strings.Contains(out, "id=t-priya") || !strings.Contains(out, "<untrusted") || strings.Contains(out, "t-rohan") {
		t.Fatalf("mail_list today: %s", out)
	}
	out = call(t, cs, "mail_read", map[string]any{"thread_id": "t-priya"})
	if !strings.Contains(out, "QA signed off") {
		t.Fatalf("mail_read: %s", out)
	}
	out = call(t, cs, "mail_search", map[string]any{"query": "from:github"})
	if !strings.Contains(out, "t-gh") {
		t.Fatalf("mail_search: %s", out)
	}
	out = call(t, cs, "contacts_find", map[string]any{"query": "rohan"})
	if !strings.Contains(out, "rohan.d@example.com") || !strings.Contains(out, "times_you_wrote=1") {
		t.Fatalf("contacts_find: %s", out)
	}
	out = call(t, cs, "digest", nil)
	if !strings.Contains(out, "Release cut") || !strings.Contains(out, "portal-dev") {
		t.Fatalf("digest: %s", out)
	}
	out = call(t, cs, "chat_read", map[string]any{"space": "portal"})
	if !strings.Contains(out, "smoke tests green") {
		t.Fatalf("chat_read: %s", out)
	}
	// downloads land only inside the downloads folder, whatever is asked
	dl := t.TempDir()
	t.Setenv("SANDESH_DOWNLOADS", dl)
	out = call(t, cs, "attachment_save", map[string]any{"thread_id": "t-hr", "subfolder": "../../etc"})
	if !strings.Contains(out, filepath.Join(dl, "etc", "holidays-2027.csv")) || !strings.Contains(out, "untrusted") {
		t.Fatalf("attachment_save: %s", out)
	}
	if !strings.Contains(call(t, cs, "chat_read", map[string]any{"space": "portal"}), "file 1: smoke-report.txt") {
		t.Fatal("chat_read doesn't list files")
	}
	out = call(t, cs, "mail_modify", map[string]any{"thread_ids": []string{"t-ci"}, "action": "archive"})
	if !strings.Contains(out, "archive: t-ci") {
		t.Fatalf("mail_modify: %s", out)
	}
	out = call(t, cs, "mail_draft", map[string]any{"reply_to_thread": "t-priya", "body": "Draft: yes"})
	if !strings.Contains(out, "Nothing was sent") || len(g.Drafts) != 1 || len(g.Sent) != 0 {
		t.Fatalf("mail_draft: %s", out)
	}

	// mail_send waits for approval: approve from "the user" meanwhile
	go func() {
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			items, _ := a.Store.Outbox(store.StatusApproval)
			if len(items) > 0 {
				_ = a.Approve(items[0].ID, nil)
				return
			}
		}
	}()
	out = call(t, cs, "mail_send", map[string]any{"reply_to_thread": "t-priya", "body": "Yes, Thursday works."})
	if out != "Sent." || len(g.Sent) != 1 || !strings.Contains(g.Sent[0], "Re: Release cut on Thursday?") {
		t.Fatalf("mail_send: %q sent=%d", out, len(g.Sent))
	}

	// declined
	go func() {
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			items, _ := a.Store.Outbox(store.StatusApproval)
			if len(items) > 0 {
				_ = a.Decline(items[0].ID)
				return
			}
		}
	}()
	out = call(t, cs, "chat_send", map[string]any{"space": "portal-dev", "text": "looking now"})
	if !strings.Contains(out, "declined") {
		t.Fatalf("chat_send declined: %q", out)
	}
	if call(t, cs, "mail_read", map[string]any{"thread_id": "nope"})[:5] != "ERROR" {
		t.Fatal("missing thread should be a tool error")
	}
}

func TestWhatsAppTools(t *testing.T) {
	a, _, cs := session(t)
	if out := call(t, cs, "whatsapp_chats", nil); !strings.Contains(out, "not linked") {
		t.Fatalf("unlinked: %s", out)
	}
	w := fake.NewWhatsApp()
	w.SeedWhatsApp()
	a.DialWhatsApp = func(ctx context.Context) (app.WhatsAppConn, error) { return w.Conn(ctx) }
	if err := a.Store.AddWhatsApp("+919876500000", w.Me); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if ok, err := a.StartWhatsApp(ctx); !ok || err != nil {
		t.Fatal(ok, err)
	}
	out := call(t, cs, "whatsapp_chats", map[string]any{"unread_only": true})
	if !strings.Contains(out, `group "Weekend trek"  unread=3`) || !strings.Contains(out, "<untrusted") || strings.Contains(out, `person "Ravi Kulkarni"`) {
		t.Fatalf("whatsapp_chats: %s", out)
	}
	out = call(t, cs, "whatsapp_read", map[string]any{"chat": "trek"})
	if !strings.Contains(out, "Sneha (message 120363000000000001@g.us/H7): I'm in!") {
		t.Fatalf("whatsapp_read: %s", out)
	}
	// Google Chat's tools don't see WhatsApp
	if out := call(t, cs, "chat_spaces", nil); strings.Contains(out, "trek") {
		t.Fatalf("chat_spaces shows WhatsApp: %s", out)
	}
	go func() {
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			items, _ := a.Store.Outbox(store.StatusApproval)
			if len(items) > 0 {
				_ = a.Approve(items[0].ID, nil)
				return
			}
		}
	}()
	out = call(t, cs, "whatsapp_send", map[string]any{"to": "Amma", "text": "Yes, ate. Curtains look nice!", "reply_to": "919800000001@s.whatsapp.net/H3"})
	if out != "Sent." || len(w.Sent) != 1 || w.Sent[0].Quoted != "H3" || w.Sent[0].Chat != "919800000001@s.whatsapp.net" {
		t.Fatalf("whatsapp_send: %q %+v", out, w.Sent)
	}
}
