package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

// Every row is exactly the screen's width as the terminal counts it, Hindi
// included: nothing after such text slides out of line.
func TestRowsFitWithIndianScripts(t *testing.T) {
	m, _ := waModel(t, "919800000001@s.whatsapp.net")
	_ = m.a.Store.PutChatMessages([]store.ChatMessage{{Account: store.WhatsAppAccount, Name: "919800000001@s.whatsapp.net/HI",
		Space: "919800000001@s.whatsapp.net", Sender: "919800000001@s.whatsapp.net", SenderName: "Amma",
		Text: "नमस्कार 🙏 मैं कल सुबह फ़ोन करूँगी, स्कूल के बाद", Created: time.Now().UnixMilli()}})
	m.reload()
	m.loadPreview(true)
	for _, w := range []int{150, 90} {
		nm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		m = nm.(Model)
		v := m.View()
		if !strings.Contains(v, "नमस्‌कार") && !strings.Contains(v, "नमस्कार") {
			t.Fatalf("%d: the Hindi isn't shown:\n%s", w, v)
		}
		for i, l := range strings.Split(v, "\n") {
			if got := ansi.StringWidthWc(l); got != w-1 {
				t.Fatalf("%d: row %d is %d wide on the terminal, not %d: %q", w, i, got, w-1, ansi.Strip(l))
			}
		}
	}
	if got := termText("स्क"); got != "स्‌क" {
		t.Fatalf("a joined consonant isn't split: %q", got)
	}
	if termText(termText("नमस्कार")) != termText("नमस्कार") {
		t.Fatal("termText twice adds a second ZWNJ")
	}
}

// The renderer is told the screen is wide, so it never cuts a row by its
// own count; the model keeps the real size.
func TestWideRenderer(t *testing.T) {
	m := model(t)
	nm, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)
	msg, ok := cmd().(tea.WindowSizeMsg)
	if !ok || msg.Width != wideScreen || m.w != 119 {
		t.Fatalf("got %+v, w %d", msg, m.w)
	}
	nm, _ = m.Update(msg)
	if nm.(Model).w != 119 {
		t.Fatal("the renderer's size reached the model")
	}
}

func TestLeaderMenu(t *testing.T) {
	m := model(t)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = nm.(Model)
	if !m.leader || m.leaderShown || strings.Contains(m.View(), "then…") {
		t.Fatal("the menu shouldn't flash before the wait")
	}
	nm, _ = m.Update(cmd()) // the wait is over
	m = nm.(Model)
	if v := m.View(); !strings.Contains(v, "then…") || !strings.Contains(v, "+Tabs") || !strings.Contains(v, "+Mark") || !strings.Contains(m.footerView(), "LEADER") {
		t.Fatalf("no menu:\n%s", v)
	}
	// a group opens its own menu, with its keys; Backspace goes back up
	m = press(m, "t")
	if v := m.View(); !strings.Contains(v, "Tabs") || !strings.Contains(v, "Go to WhatsApp") {
		t.Fatalf("Space t menu:\n%s", v)
	}
	m = press(m, "backspace")
	if m.eng.Pending() != "Space" || !m.leader {
		t.Fatalf("backspace: %q", m.eng.Pending())
	}
	m = press(m, "t", "c") // Space t c: Chat
	if m.leader || m.tab != tabChat || m.wa {
		t.Fatalf("Space t c: tab %d wa %v leader %v", m.tab, m.wa, m.leader)
	}
	// typed straight on, the menu never shows
	m = press(m, " ", "t", "m")
	if m.tab != tabMail || m.leaderShown {
		t.Fatal("Space t m")
	}
	// Esc cancels a sequence half typed
	m = press(m, " ", "m", "esc")
	if m.leader || m.eng.Waiting() || m.tab != tabMail {
		t.Fatal("esc should cancel Space m")
	}
}

func TestTwoKeysAndPlaces(t *testing.T) {
	m := model(t)
	m = press(m, "G")
	if m.sel != m.count()-1 {
		t.Fatal("G")
	}
	m = press(m, "g", "g")
	if m.sel != 0 {
		t.Fatal("gg")
	}
	m = press(m, "2", "j")
	if m.sel != 2 {
		t.Fatalf("2j: %d", m.sel)
	}
	m = press(m, "1", "G")
	if m.sel != 0 {
		t.Fatalf("1G: %d", m.sel)
	}
	th, _ := m.curThread()
	m = press(m, "e") // archive
	if got, _ := m.a.Store.Thread(th.Account, th.ID); got.Has("INBOX") {
		t.Fatal("e didn't archive")
	}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")}) // and undo
	m = runCmds(nm.(Model), cmd)
	if got, _ := m.a.Store.Thread(th.Account, th.ID); !got.Has("INBOX") {
		t.Fatal("u didn't undo")
	}
	m = press(m, "L", "L")
	if !m.wa {
		t.Fatal("L L from Mail is WhatsApp")
	}
	m = press(m, "H", "H")
	if m.tab != tabMail {
		t.Fatal("H H back to Mail")
	}
	m = press(m, "g", "t")
	if m.tab != tabChat || m.wa {
		t.Fatal("gt from Mail is Chat")
	}
	m = press(m, "3", "g", "t")
	if !m.wa {
		t.Fatal("3gt is WhatsApp")
	}
	m = press(m, " ", "t", "m", "]", "]")
	if m.sec != 1 {
		t.Fatal("]] is the next section")
	}
	m = press(m, "q")
	if m.prefix != "" || m.leader {
		t.Fatal("state left behind")
	}
}

// dd asks first, and only y says yes.
func TestDeleteAsksFirst(t *testing.T) {
	m := model(t)
	th, _ := m.curThread()
	m = press(m, "d", "d")
	if m.confirm == nil || !strings.Contains(m.footerView(), "trash") {
		t.Fatalf("dd should ask: %s", m.footerView())
	}
	m = press(m, "enter") // Enter is not yes
	if got, _ := m.a.Store.Thread(th.Account, th.ID); got.Has("TRASH") || m.confirm != nil {
		t.Fatal("Enter trashed it")
	}
	m = press(m, "d", "d", "y")
	if got, _ := m.a.Store.Thread(th.Account, th.ID); !got.Has("TRASH") {
		t.Fatal("dd y didn't trash")
	}
}

// An old key says where it went, for one release.
func TestMovedKeysSaySo(t *testing.T) {
	m := model(t)
	m = press(m, "*")
	if !strings.Contains(m.toast, "moved to Space m s") {
		t.Fatalf("* should say where it went: %q", m.toast)
	}
	m = press(m, "c")
	if m.mode == modeCompose || !strings.Contains(m.toast, "moved to o") {
		t.Fatalf("c: %q", m.toast)
	}
}

// Ctrl+j / Ctrl+k scroll what's open by scroll_lines, and never move the
// selection.
func TestScrollKeys(t *testing.T) {
	m := model(t)
	m = press(m, "l")
	sel := m.sel
	m.scroll = 0
	m.a.Config.ScrollLines = 3
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	m = nm.(Model)
	if m.sel != sel {
		t.Fatal("Ctrl+j moved the selection")
	}
	if m.scroll != 3 && !m.atBottom {
		t.Fatalf("Ctrl+j scrolled %d", m.scroll)
	}
}

// Space Space is the palette: type, Enter runs.
func TestPalette(t *testing.T) {
	m := model(t)
	m = press(m, " ", " ")
	if m.pal == nil || !strings.Contains(m.View(), "every action here") {
		t.Fatal("Space Space should open the palette")
	}
	m = typeText(m, "go to whatsapp")
	m = press(m, "enter")
	if m.pal != nil || !m.wa {
		t.Fatalf("the palette didn't take me to WhatsApp: pal %v wa %v", m.pal != nil, m.wa)
	}
}

func TestVisual(t *testing.T) {
	m := model(t)
	m = press(m, "v", "j", "j")
	if !m.visual || len(m.selected) != 3 || !strings.Contains(m.footerView(), "VISUAL") {
		t.Fatalf("v j j picks 3: %d", len(m.selected))
	}
	m = press(m, "k")
	if len(m.selected) != 2 {
		t.Fatalf("k shrinks it: %d", len(m.selected))
	}
	m = press(m, "esc")
	if m.visual || len(m.selected) != 0 {
		t.Fatal("esc clears")
	}
}

func TestCommands(t *testing.T) {
	m := model(t)
	m = press(m, ":")
	m = typeText(m, "wh")
	if m.mode != modeCommand || !strings.Contains(m.View(), "whatsapp") {
		t.Fatalf("no completion:\n%s", m.View())
	}
	m = press(m, "tab", "enter")
	if !m.wa || m.mode != modeNormal {
		t.Fatal(":whatsapp")
	}
	m = press(m, ":")
	m = typeText(m, "nope")
	m = press(m, "enter")
	if !strings.Contains(m.toast, "isn't a command") {
		t.Fatalf("toast %q", m.toast)
	}
}

func TestFindActions(t *testing.T) {
	m := model(t)
	m = press(m, "/")
	m = typeText(m, "archive")
	if m.find == nil || len(m.find.actions) == 0 || !strings.Contains(m.View(), "ACTIONS") {
		t.Fatalf("no actions:\n%s", m.View())
	}
	th, _ := m.curThread()
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmds(nm.(Model), cmd)
	if got, _ := m.a.Store.Thread(th.Account, th.ID); got.Has("INBOX") {
		t.Fatal("the archive action didn't archive")
	}
}

// r and e on a status answer whoever posted it, in your chat with them,
// quoting it; never to everyone.
func TestStatusReplyAndReact(t *testing.T) {
	m, w := waModel(t, whatsapp.StatusChat)
	m.sec = slices.Index(chatSections, "Status") // where you'd open it
	m.reload()
	if len(m.spaces) != 2 {
		t.Fatalf("a row for each person: %+v", m.spaces)
	}
	open := func(who string) {
		for i, sp := range m.spaces {
			if strings.HasSuffix(sp.Name, who) {
				m.sel = i
			}
		}
		m.loadPreview(true)
		m.focus = 1
	}
	open("919800000002@s.whatsapp.net") // Ravi's
	if !m.isStatus() || len(m.chatMsgs) != 2 {
		t.Fatalf("Ravi's statuses: %d", len(m.chatMsgs))
	}
	m = pickMsg(t, m, "D1")
	m = press(m, "r")
	if m.mode != modeChatMsg || m.replyTo == nil {
		t.Fatal("r should open the box")
	}
	m = typeText(m, "Congrats!")
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmds(nm.(Model), cmd)
	m.a.DeliverDue(t.Context())
	if len(w.Sent) == 0 {
		t.Fatal("nothing sent")
	}
	if got := w.Sent[0]; got.Chat != "919800000002@s.whatsapp.net" || got.Text != "Congrats!" || len(w.Quotes) == 0 || w.Quotes[0].ID != "D1" {
		t.Fatalf("sent %+v quotes %+v", w.Sent, w.Quotes)
	}
	open("919800000003@s.whatsapp.net") // Sneha's
	m = pickMsg(t, m, "D3")
	m = press(m, " ", "i", "e")
	if m.mode != modeReact {
		t.Fatal("e should open the reactions")
	}
	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmds(nm.(Model), cmd)
	m.a.DeliverDue(t.Context())
	if len(w.Sent) < 2 {
		t.Fatalf("the reaction wasn't sent: %+v", w.Sent)
	}
	if got := w.Sent[1]; got.Chat != "919800000003@s.whatsapp.net" || got.Text != reactions[0] {
		t.Fatalf("reaction sent %+v", got)
	}
	for _, s := range w.Sent {
		if s.Chat == whatsapp.StatusChat {
			t.Fatal("something went to everyone's status")
		}
	}
}

// Only / opens the search: Ctrl+k is left for scrolling.
func TestOnlySlashSearches(t *testing.T) {
	m := model(t)
	m = press(m, "ctrl+k")
	if m.mode == modeFind {
		t.Fatal("ctrl+k opened the search")
	}
	m = press(m, "/")
	if m.mode != modeFind {
		t.Fatal("/ didn't open the search")
	}
}

// Only the keys on the key sheet act: the old undocumented letters (x
// picked, s searched, Q folded, D/S/T asked Claude) do nothing now.
func TestNoHiddenKeys(t *testing.T) {
	m := model(t)
	before := m.sel
	for _, k := range []string{"x", "s", "Q", "D", "S", "T", "%"} {
		m = press(m, k)
		if m.mode != modeNormal || len(m.selected) != 0 || m.sel != before || m.find != nil {
			t.Fatalf("%s acted: mode %v, %d picked, sel %d", k, m.mode, len(m.selected), m.sel)
		}
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	if m = nm.(Model); len(m.selected) != 0 {
		t.Fatal("Ctrl+x picked everything without being a key")
	}
}

// Keys typed fast can arrive as one message ("dd", "ihi"): each is a key,
// and once a key starts typing, the rest is text.
func TestFastKeys(t *testing.T) {
	m := model(t)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("dd")})
	if m = nm.(Model); m.confirm == nil {
		t.Fatal("dd typed fast should ask")
	}
	m = press(m, "n", " ", "t", "c")
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ihi")})
	if m = nm.(Model); m.mode != modeChatMsg || string(m.input) != "hi" {
		t.Fatalf("ihi: mode %v input %q", m.mode, string(m.input))
	}
}
