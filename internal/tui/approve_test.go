package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/store"
)

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// queued is an agent's send waiting for approval, on an approver at w×h.
func queued(t *testing.T, kind string, out store.Outgoing, w, h int) (Model, store.OutboxItem, approver) {
	t.Helper()
	m := model(t)
	m.a.Config.SendPolicy = "confirm"
	it, err := m.a.Queue("demo", kind, out, app.OriginAgent)
	if err != nil || it.Status != store.StatusApproval {
		t.Fatal(it, err)
	}
	ap := approver{a: m.a, items: []store.OutboxItem{it}, styles: m.styles, diff: true}
	nm, _ := ap.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m, it, nm.(approver)
}

func mailOut() store.Outgoing {
	return store.Outgoing{To: []string{"priya.k@example.com"}, Subject: "Re: Release cut on Thursday?",
		Body: "Yes, Thursday works.\n\nI'll cut the branch at 10.", Agent: "Claude Code", Why: "tell Priya Thursday is fine"}
}

func TestApproveCard(t *testing.T) {
	m, it, ap := queued(t, "mail", mailOut(), 150, 40)
	v := ap.View()
	for _, want := range []string{"narada · approve a send", "Mail", "1 of 1", "Claude Code wants to email priya.k@example.com",
		"You asked: “tell Priya Thursday is fine”", "the message, exactly", "Release cut", "I'll cut the branch", "Send", "Decline", it.Reason} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	if testing.Verbose() {
		t.Log("\n" + v)
	}
	nm, _ := ap.Update(key("y"))
	if got, _ := m.a.Store.OutboxItem(it.ID); got.Status != store.StatusQueued || !strings.Contains(nm.View(), "Approved") {
		t.Fatalf("y: %s\n%s", got.Status, nm.View())
	}
}

// What you were typing when the popup opened can't answer it, and Enter
// never does.
func TestApproveKeysThatDontAnswer(t *testing.T) {
	m, it, ap := queued(t, "mail", mailOut(), 150, 40)
	ap.shown = time.Now()
	for _, k := range []string{"y", "n"} {
		nm, _ := ap.Update(key(k))
		ap = nm.(approver)
	}
	nm, _ := ap.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got, _ := m.a.Store.OutboxItem(it.ID); got.Status != store.StatusApproval || nm.(approver).done != "" {
		t.Fatalf("answered by accident: %s", got.Status)
	}
	ap.shown = time.Now().Add(-time.Second)
	nm, _ = ap.Update(key("n"))
	if got, _ := m.a.Store.OutboxItem(it.ID); got.Status == store.StatusApproval || !strings.Contains(nm.View(), "Declined") {
		t.Fatalf("n after the pause: %s", got.Status)
	}
}

func TestApproveWarnsAboutOutsiders(t *testing.T) {
	out := mailOut()
	out.To = []string{"Priya <priya@acme.example>"}
	out.Cc = []string{"dana@client.example", "lee@client.example"}
	if far := outside("me@acme.example", out); len(far) != 2 {
		t.Fatalf("outside: %v", far)
	}
	if far := outside("me@gmail.com", out); far != nil {
		t.Fatalf("a personal address has no outside: %v", far)
	}
	if far := outside("work", out); far != nil {
		t.Fatalf("no address, no outside: %v", far)
	}
}

func TestApproveWhatsAppGroup(t *testing.T) {
	_, _, ap := queued(t, "whatsapp", store.Outgoing{Space: "120@g.us", SpaceName: "Storefront", Body: "@919820147460 Sir, please check.", Agent: "Codex"}, 150, 40)
	v := ap.View()
	for _, want := range []string{"WhatsApp", "Codex wants to message the group Storefront", "a group", "tags 1 person", "@919820147460"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
}

// Your edit shows what changed, and goes away with u. An edited body drops
// the HTML, which still says the old thing.
func TestApproveEdit(t *testing.T) {
	out := mailOut()
	out.HTML = "<p>old</p>"
	_, it, ap := queued(t, "mail", out, 150, 40)
	edited := it.Out
	edited.Body, edited.HTML = "Yes, Friday works.\n\nI'll cut the branch at 10.", ""
	ap.out = &edited
	v := ap.View()
	for _, want := range []string{"edited by you", "Send my version", "Undo my edit", "+ Yes, Friday works.", "- Yes, Thursday works."} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	nm, _ := ap.Update(key("d"))
	if v := nm.View(); strings.Contains(v, "Yes, Thursday") {
		t.Fatalf("d keeps the old line:\n%s", v)
	}
	nm, _ = nm.Update(key("u"))
	if nm.(approver).out != nil || strings.Contains(nm.View(), "edited by you") {
		t.Fatalf("u keeps the edit:\n%s", nm.View())
	}
}

// HTML shows as it will arrive: a table is a table.
func TestApproveShowsHTML(t *testing.T) {
	out := mailOut()
	out.HTML = `<table><tr><th>Time</th><th>Tasks</th></tr><tr><td>09:00 AM</td><td>Built the thing</td></tr></table>`
	_, _, ap := queued(t, "mail", out, 150, 40)
	if v := ap.View(); !strings.Contains(v, "│ Time") || !strings.Contains(v, "Built the thing") {
		t.Fatalf("no table:\n%s", v)
	}
}

// Every recipient can be reached, however long the list.
func TestApproveLongRecipientList(t *testing.T) {
	out := mailOut()
	for i := range 40 {
		out.Cc = append(out.Cc, "person"+string(rune('a'+i%26))+string(rune('a'+i/26))+"@example.com")
	}
	_, _, ap := queued(t, "mail", out, 150, 30)
	if v := ap.View(); !strings.Contains(v, "more (J/K)") {
		t.Fatalf("no sign of the rest:\n%s", v)
	}
	var nm tea.Model = ap
	for range 60 {
		nm, _ = nm.Update(key("J"))
	}
	if v := nm.View(); !strings.Contains(v, "persona") && !strings.Contains(v, "personn") {
		t.Fatalf("scrolled away:\n%s", v)
	}
}

// Whatever the size, it fits and the answers show.
func TestApproveAnySize(t *testing.T) {
	out := mailOut()
	out.Body = strings.Repeat("A long line of the message that has to wrap somewhere in the frame. ", 40)
	out.To = []string{"a-very-long-address-that-will-not-fit-in-a-small-terminal@example.com"}
	for _, size := range [][2]int{{200, 60}, {150, 40}, {100, 30}, {80, 24}, {60, 20}, {40, 12}, {30, 8}, {20, 5}, {1, 1}} {
		_, _, ap := queued(t, "mail", out, size[0], size[1])
		v := ap.View()
		if h := lipgloss.Height(v); h > max(size[1], 3) {
			t.Errorf("%v: %d lines", size, h)
		}
		if size[1] >= 12 && !strings.Contains(v, "Decline") {
			t.Errorf("%v: no answers:\n%s", size, v)
		}
		if size[0] >= 40 {
			for _, l := range strings.Split(v, "\n") {
				if w := lipgloss.Width(l); w > size[0] {
					t.Errorf("%v: line of %d columns: %q", size, w, l)
				}
			}
		}
	}
}

func TestLineDiff(t *testing.T) {
	got := lineDiff([]string{"a", "b", "c"}, []string{"a", "x", "c", "d"})
	want := []string{" a", "+x", "-b", " c", "+d"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v", got)
	}
}

// The list fills the height: every row it has room for, however many mails.
func TestMailListUsesFullHeight(t *testing.T) {
	m := model(t)
	m.w, m.h = 190, 50
	m.a.Config.ListStyle = ""
	bh := m.bodyHeight() - 2
	if m.count() < 2 {
		t.Skip("the demo mail has too few threads")
	}
	if rows := m.cardRows(); rows != 4 {
		t.Fatalf("a mail row takes 4 lines on the panel, not %d", rows)
	}
	if v := m.listView(70, bh); lipgloss.Height(v) > bh {
		t.Fatalf("%d lines in %d", lipgloss.Height(v), bh)
	}
}

// AI requests queue in their own section of each place, answered there.
func TestAIRequestsSection(t *testing.T) {
	m := model(t)
	m.w, m.h = 190, 50
	m.a.Config.SendPolicy = "confirm"
	mailIt, err := m.a.Queue("demo", "mail", mailOut(), app.OriginAgent)
	if err != nil {
		t.Fatal(err)
	}
	chatIt, err := m.a.Queue("demo", "chat", store.Outgoing{Space: "spaces/x", SpaceName: "Team", Body: "Update for the team", Agent: "Codex"}, app.OriginAgent)
	if err != nil {
		t.Fatal(err)
	}
	m.reload()
	if v := m.tabsView(); !strings.Contains(v, "1 AI") {
		t.Fatalf("the places don't show it:\n%s", v)
	}
	m.goToAI("mail")
	v := m.View()
	for _, want := range []string{"AI requests 1", "waiting for your OK", "Claude Code wants to email", "the message, exactly"} {
		if !strings.Contains(v, want) {
			t.Fatalf("mail: missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Update for the team") {
		t.Fatalf("chat's request in mail:\n%s", v)
	}
	nm, _ := m.normalKey("y")
	m = nm.(Model)
	if got, _ := m.a.Store.OutboxItem(mailIt.ID); got.Status != store.StatusQueued {
		t.Fatalf("y: %s", got.Status)
	}
	m.goToAI("chat")
	if v := m.View(); !strings.Contains(v, "Codex wants to post in Team") {
		t.Fatalf("chat:\n%s", v)
	}
	nm, _ = m.normalKey("n")
	m = nm.(Model)
	if got, _ := m.a.Store.OutboxItem(chatIt.ID); got.Status == store.StatusApproval {
		t.Fatal("n left it waiting")
	}
	if len(m.waiting) != 0 || !strings.Contains(m.View(), "No AI requests") {
		t.Fatalf("still waiting: %d\n%s", len(m.waiting), m.View())
	}
	nm, _ = m.normalKey("A")
	if !strings.Contains(nm.View(), "No AI requests") {
		t.Fatal("A with nothing waiting moved away")
	}
}
