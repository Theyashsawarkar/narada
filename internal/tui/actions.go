package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/cli"
	"github.com/Theyashsawarkar/narada/internal/config"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// The action registry: every action a key in keymap.go names, by that
// name. An action may call actKey with the key name the screen used
// before Vim's keys (the old single-letter actions), which stays as the
// implementation of the older actions until they move here one by one.

// confirmAsk is a question a destructive action asks first: y does it,
// any other key keeps things as they are.
type confirmAsk struct {
	question string
	act      string
	count    int
	target   string // what the question was about (askTarget)
}

// ask asks before act runs: y runs it, anything else doesn't.
func (m *Model) askFirst(question, act string, count int) {
	m.confirm = &confirmAsk{question: question, act: act, count: count, target: m.askTarget()}
}

// askTarget names what a question is about: the picked message, the
// threads it acts on, or the chat or section. y only does it while this is
// unchanged: a sync, a new message shifting the chat, or a click between
// the question and y must never make y act on something else.
func (m *Model) askTarget() string {
	if c, ok := m.picked(); ok {
		return "msg:" + c.Name
	}
	var keys []string
	if m.tab == tabMail {
		for _, t := range m.targets() {
			keys = append(keys, t.Account+"/"+t.ID)
		}
	}
	return fmt.Sprintf("%d/%d/%s/%s", m.place(), m.sec, m.keyAt(m.sel), strings.Join(keys, ","))
}

// confirmKey answers the question being asked.
func (m Model) confirmKey(s string) (tea.Model, tea.Cmd) {
	c := m.confirm
	m.confirm = nil
	if s != "y" {
		if !m.visual {
			m.selected = map[string]bool{} // what d with a motion picked
		}
		m.say("kept")
		return m, nil
	}
	if c.target != m.askTarget() {
		if !m.visual {
			m.selected = map[string]bool{}
		}
		m.say("it changed while asking: nothing done (ask again)")
		return m, nil
	}
	m.confirmed = true
	mm, cmd := m.run(c.act, c.count)
	if x, ok := mm.(Model); ok {
		x.confirmed = false
		return x, cmd
	}
	return mm, cmd
}

// pickInOpenChat: in an open chat with nothing picked, a key for one
// message acts on the latest one.
func (m *Model) pickInOpenChat() {
	if m.focus == 1 && m.tab == tabChat && !m.picking {
		m.startPicking()
	}
}

// run does the action act, count times where that means something.
func (m Model) run(act string, count int) (tea.Model, tea.Cmd) {
	count = max(1, count)
	old := func(k string) (tea.Model, tea.Cmd) { return m.actKey(k) }
	repeat := func(k string) (tea.Model, tea.Cmd) {
		var mm tea.Model = m
		var cmds []tea.Cmd
		for range count {
			var cmd tea.Cmd
			mm, cmd = mm.(Model).actKey(k)
			cmds = append(cmds, cmd)
		}
		return mm, tea.Batch(cmds...)
	}
	switch {
	case strings.HasPrefix(act, "mark.set."):
		return m.setMark(strings.TrimPrefix(act, "mark.set."))
	case strings.HasPrefix(act, "mark.go."):
		return m.goMark(strings.TrimPrefix(act, "mark.go."))
	}
	switch act {
	// ---- moving
	case "down":
		return repeat("j")
	case "up":
		return repeat("k")
	case "focus.list":
		m.focus = 0
		return m, nil
	case "focus.open":
		if m.focus == 0 {
			return old("2")
		}
		return m, nil
	case "focus.other":
		if m.focus == 0 {
			return old("2")
		}
		m.focus = 0
		return m, nil
	case "top", "bottom":
		m.recordJump()
		return m.jumpTo(act == "bottom", count, m.lastCounted)
	case "place.next", "place.prev":
		step := map[bool]int{true: 1, false: 3}[act == "place.next"]
		return m.goPlace((m.placeAt() + step*count) % 4)
	case "place.goto":
		if m.lastCounted {
			return m.goPlace(min(4, count) - 1)
		}
		return m.goPlace((m.placeAt() + 1) % 4)
	case "place.mail":
		return m.goPlace(0)
	case "place.chat":
		return m.goPlace(1)
	case "place.wa":
		return m.goPlace(2)
	case "place.ai":
		return m.goPlace(3)
	case "section.next":
		return repeat("l")
	case "section.prev":
		return repeat("h")
	case "chat.next":
		return repeat("]")
	case "chat.prev":
		return repeat("[")
	case "msg.next", "msg.prev":
		return m.stepMessage(map[bool]int{true: 1, false: -1}[act == "msg.next"] * count)
	case "unread.next", "unread.prev":
		if m.convQ != "" {
			// a search inside what's open is on: n / N step through it
			var mm tea.Model = m
			var cmd tea.Cmd
			for range count {
				mm, cmd = mm.(Model).convFind(map[bool]int{true: 1, false: -1}[act == "unread.next"])
			}
			return mm, cmd
		}
		var mm tea.Model = m
		var cmd tea.Cmd
		for range count {
			x := mm.(Model)
			mm, cmd = x.nextUnread(map[bool]int{true: 1, false: -1}[act == "unread.next"], false)
		}
		return mm, cmd
	case "scroll.half-down", "scroll.half-up", "scroll.page-down", "scroll.page-up",
		"scroll.down", "scroll.up", "scroll.line-down", "scroll.line-up":
		m.scrollBy(act, count)
		return m, nil
	case "view.middle", "view.top", "view.bottom":
		m.placePicked(strings.TrimPrefix(act, "view."))
		return m, nil
	case "msg.quote":
		if !m.pickingMsg() {
			m.say("gd jumps to what a reply quotes: pick a message first (j/k in the chat)")
			return m, nil
		}
		return m, m.jumpToQuote()
	case "msg.back":
		return old("backspace")
	case "open":
		m.recordJump()
		return old("enter")
	case "back":
		return old("q")
	case "back.esc":
		if m.convQ != "" {
			m.convQ = ""
			m.say("search stopped")
			return m, nil
		}
		return old("esc")
	case "find":
		return m.findAnything(true)
	case "find.here":
		if m.focus == 1 || m.pickingMsg() {
			return m.startConvSearch()
		}
		return m.findAnything(false)
	case "help":
		return old("?")
	case "command":
		m.openCommand()
		return m, nil
	case "palette":
		m.openPalette()
		return m, nil
	case "quit":
		return m, tea.Quit

	// ---- writing and replying
	case "write", "write.all":
		if m.tab == tabChat {
			return old("i")
		}
		if act == "write.all" {
			return old("a")
		}
		return old("r")
	case "reply":
		if m.focus == 1 && m.tab == tabChat && !m.picking && m.isStatus() {
			m.startPicking() // r answers the latest status
		}
		return old("r")
	case "reply.all":
		if m.tab == tabMail {
			if _, ok := m.curOutbox(); !ok {
				return old("a")
			}
		}
		m.pickInOpenChat()
		return old("R")
	case "new":
		return old("c")
	case "new.convo":
		if m.tab != tabChat || m.wa {
			mm, _ := m.goPlace(1)
			m = mm.(Model)
		}
		m.openConvo()
		return m, nil
	case "send.again":
		if it, ok := m.curOutbox(); ok && it.Status == store.StatusFailed {
			return old("R")
		}
		m.say("nothing failed here: a failed send is in Scheduled, in red")
		return m, nil
	case "send.later":
		return old("t")
	case "file.send":
		return old("U")
	case "paste":
		if _, ok := m.curSpace(); !ok {
			m.say("p pastes into a chat's message box: open one first")
			return m, nil
		}
		mm, _ := m.actKey("i")
		x := mm.(Model)
		if x.mode != modeChatMsg {
			return x, nil
		}
		return x, x.pasteImage()

	// ---- changing things
	case "delete":
		return m.deleteIt(count)
	case "archive":
		return m.archiveIt()
	case "copy":
		m.pickInOpenChat()
		return old("y")
	case "copy.link":
		return old("Y")
	case "read.toggle":
		return m.toggleRead()
	case "read":
		return old("m")
	case "unread":
		return old("M")
	case "read.all":
		return old("alt+r")
	case "star":
		m.pickInOpenChat()
		return old("*")
	case "label":
		return old("L")
	case "pin":
		return old("p")
	case "undo":
		return old("z")
	case "redo":
		return m, m.redoCmd()
	case "repeat":
		return m.repeatChange()
	case "delete.down", "delete.up", "delete.bottom", "delete.top":
		return m.deleteMotion(act, count)
	case "visual.again":
		return m.visualAgain()
	case "jump.back":
		return m.jumpBy(-count)
	case "jump.forward":
		return m.jumpBy(count)
	case "sender.next":
		return m.senderStep(1)
	case "sender.prev":
		return m.senderStep(-1)
	case "ui.zoom":
		m.zoom = !m.zoom
		m.say(map[bool]string{true: "what's open takes the whole width (Space u f again: the list comes back)", false: "the list is back"}[m.zoom])
		return m, nil
	case "ui.preview":
		m.noPreview = !m.noPreview
		m.say(map[bool]string{true: "the list takes the whole width (Space u p again: the preview)", false: "the preview is back"}[m.noPreview])
		return m, nil
	case "ui.compact":
		m.compact = !m.compact
		m.say(map[bool]string{true: "compact list", false: "roomy list"}[m.compact])
		return m, nil
	case "ui.images":
		if m.a.Config.Images == "off" {
			m.a.Config.Images = "auto"
		} else {
			m.a.Config.Images = "off"
		}
		m.loadPreview(false)
		m.say(map[bool]string{true: "pictures off", false: "pictures inline"}[m.a.Config.Images == "off"])
		return m, nil
	case "visual":
		if m.pickingMsg() {
			m.say("v picks threads or chats in the list: h goes back to it")
			return m, nil
		}
		m.visual, m.visualFrom = true, m.sel
		m.selected = map[string]bool{}
		if k := m.keyAt(m.sel); k != "" {
			m.selected[k] = true
		}
		return m, nil
	case "visual.all":
		return old("ctrl+x")
	case "fold":
		return old("E")
	case "fold.close", "fold.open":
		m.expandAll = act == "fold.open"
		return m, nil
	case "quoted":
		return old("Q")
	case "files.open":
		return old("v")
	case "files.save":
		return old("V")
	case "link":
		return old("o")
	case "browser":
		return old("b")
	case "sync":
		m.say("syncing…")
		return m, m.syncCmd()
	case "older":
		if m.tab != tabMail {
			return m, nil
		}
		return m, m.loadOlderCmd()
	case "older.more":
		return old("+")
	case "older.fewer":
		return old("-")
	case "section.save":
		return old("W")
	case "section.remove":
		if m.tab != tabMail || m.search != nil {
			m.say("go to a saved-search section first")
			return m, nil
		}
		if !m.confirmed {
			m.askFirst("remove the section "+m.currentSection().Name+"? (the mail stays)", act, count)
			return m, nil
		}
		return old("X")
	case "account":
		return m.nextAccount()
	case "chat.rename":
		if sp, ok := m.curSpace(); ok {
			m.mode, m.input = modeRename, []rune(cli.SpaceTitle(sp))
		} else {
			m.say("Space e r renames a chat: pick one in Chat or WhatsApp")
		}
		return m, nil
	case "msg.edit":
		m.pickInOpenChat()
		if !m.pickingMsg() {
			m.say("Space e e edits a message of yours: open the chat and pick it")
			return m, nil
		}
		return old("E")
	case "msg.forward":
		m.pickInOpenChat()
		return old("f")
	case "msg.react":
		m.pickInOpenChat()
		if !m.pickingMsg() {
			m.say("Space i e reacts to a message: open the chat and pick it")
			return m, nil
		}
		return old("e")
	case "msg.private":
		m.pickInOpenChat()
		if !m.pickingMsg() {
			m.say("Space i p answers privately: pick a group message first")
			return m, nil
		}
		return old("R")
	case "go.unread":
		return m.goSectionNamed("Unread")
	case "go.inbox":
		return m.goSectionNamed("Inbox")
	case "go.starred":
		return m.goSectionNamed("Starred")
	case "go.sent":
		return m.goSectionNamed("Sent")
	case "go.scheduled":
		return m.goSectionNamed("Scheduled")
	case "go.status":
		return m.goSectionNamed("Status")
	case "go.archived":
		return m.goSectionNamed("Archived")

	// ---- WhatsApp
	case "wa.link":
		return old("L")
	case "wa.link-code":
		return old("P")
	case "wa.mute":
		return old("Z")
	case "wa.block":
		if !m.confirmed {
			m.askFirst("block (or unblock) the person in this chat?", act, count)
			return m, nil
		}
		// the question above is the one ask (waChatKey's own asks for B twice)
		if sp, ok := m.curSpace(); ok {
			m.armDelete = "block:" + sp.Name
		}
		return old("B")
	case "wa.disappear":
		return old("X")
	case "wa.info":
		return old("I")
	case "wa.vote":
		m.pickInOpenChat()
		return old("P")

	// ---- Claude
	case "ai.ask":
		return old("@")
	case "ai.summary":
		return old("S")
	case "ai.write":
		return old("A")
	case "ai.digest":
		return old("T")
	}
	m.say(act + " isn't an action")
	return m, nil
}

// jumpTo is gg and G: the first / last, or with a count the n-th, of the
// picked chat's messages or the list.
func (m Model) jumpTo(last bool, n int, counted bool) (tea.Model, tea.Cmd) {
	if m.pickingMsg() {
		target := 0
		switch {
		case counted:
			target = n - 1
		case last:
			target = len(m.chatMsgs) - 1
		}
		return m, m.moveChatSel(target - m.chatSel)
	}
	if m.focus == 1 && !counted {
		if last {
			m.scroll = 1 << 30
		} else {
			m.scroll = 0
		}
		return m, nil
	}
	switch {
	case counted:
		m.sel = max(0, min(m.count()-1, n-1))
	case last:
		m.sel = max(0, m.count()-1)
	default:
		m.sel = 0
	}
	m.ai = ""
	m.loadPreview(true)
	return m, nil
}

// stepMessage is J / K: the next / previous message in what's open. In a
// chat it picks; in mail it reads the next / previous thread.
func (m Model) stepMessage(step int) (tea.Model, tea.Cmd) {
	if m.tab == tabChat {
		if _, ok := m.curSpace(); !ok {
			return m, nil
		}
		if !m.pickingMsg() {
			if m.focus == 0 {
				mm, _ := m.actKey("2")
				m = mm.(Model)
			}
			m.startPicking()
			if step > 0 {
				return m, nil
			}
			step++
		}
		return m, m.moveChatSel(step)
	}
	if m.count() == 0 {
		return m, nil
	}
	m.sel = max(0, min(m.count()-1, m.sel+step))
	m.ai = ""
	m.loadPreview(true)
	m.focus, m.scroll = 1, 0
	return m, nil
}

// scrollBy scrolls what's open.
func (m *Model) scrollBy(act string, n int) {
	page := max(1, m.previewHeight())
	by := map[string]int{
		"scroll.half-down": page / 2, "scroll.half-up": -page / 2,
		"scroll.page-down": page, "scroll.page-up": -page,
		"scroll.down": m.a.Config.ScrollLines, "scroll.up": -m.a.Config.ScrollLines,
		"scroll.line-down": 1, "scroll.line-up": -1,
	}[act]
	m.scroll = max(0, m.scroll+by*n)
}

// placePicked is zz / zt / zb: the picked message to the middle, top or
// bottom of what's open.
func (m *Model) placePicked(where string) {
	if !m.pickingMsg() {
		m.say("z" + where[:1] + " moves the picked message: pick one first (j/k in the chat)")
		return
	}
	m.fixView()
	line, ok := m.chatMsgLine[m.chatMsgs[m.chatSel].Name]
	if !ok {
		return
	}
	avail := max(3, m.bodyHeight()-6)
	switch where {
	case "middle":
		m.scroll = max(0, line-avail/2)
	case "top":
		m.scroll = max(0, line-1)
	case "bottom":
		m.scroll = max(0, line-avail+3)
	}
}

// deleteIt is dd: mail to the trash, a picked message deleted; it asks
// first, and only y says yes.
func (m Model) deleteIt(count int) (tea.Model, tea.Cmd) {
	switch {
	case m.pickingMsg():
		c := m.chatMsgs[m.chatSel]
		forMe := m.wa && !c.Mine // WhatsApp: someone else's goes for you, here
		if !c.Mine && !forMe {
			m.say("only your own messages can be deleted")
			return m, nil
		}
		if !m.confirmed {
			m.askFirst(map[bool]string{false: "delete this message for everyone?", true: "delete this message for you (here only)?"}[forMe], "delete", count)
			return m, nil
		}
		a := m.a
		if forMe {
			return m, m.act("deleted for you", func(ctx context.Context) error {
				_, err := a.DeleteWhatsApp(c.Name, false, app.OriginYou)
				return err
			})
		}
		return m, m.act(map[bool]string{true: "deleting…", false: "deleted"}[m.wa], func(ctx context.Context) error { return a.DeleteChatMessage(ctx, c.Account, c.Name) })
	case m.tab == tabMail:
		if len(m.targets()) == 0 {
			return m, nil
		}
		if !m.confirmed {
			n := len(m.targets())
			q := "move this thread to the trash?"
			if n > 1 {
				q = "move " + itoa(n) + " threads to the trash?"
			}
			m.askFirst(q+" (u undoes it)", "delete", count)
			return m, nil
		}
		return m, m.modify("d") // modify trashes what's picked, then clears it
	}
	m.say("dd deletes a message of yours: pick it in the chat (e archives a WhatsApp chat)")
	return m, nil
}

// archiveIt is e: the thread or WhatsApp chat out of the inbox (u undoes it).
func (m Model) archiveIt() (tea.Model, tea.Cmd) {
	switch {
	case m.tab == tabMail:
		return m, m.modify("e")
	case m.wa:
		m.picking = false // the chat, not the picked message
		mm, cmd, ok := m.waChatKey("e")
		if ok {
			return mm, cmd
		}
		return mm, nil
	}
	m.say("Google Chat has no archive")
	return m, nil
}

// toggleRead is ~: read becomes unread and unread read (the first picked
// one decides for all of them).
func (m Model) toggleRead() (tea.Model, tea.Cmd) {
	unread := false
	switch {
	case m.tab == tabMail:
		if ts := m.targets(); len(ts) > 0 {
			unread = ts[0].Unread()
		}
	default:
		if sp, ok := m.curSpace(); ok {
			unread = sp.Unread > 0
		}
		for _, sp := range m.spaces {
			if m.selected[sp.Account+"/"+sp.Name] {
				unread = sp.Unread > 0
				break
			}
		}
	}
	if unread {
		return m.actKey("m")
	}
	return m.actKey("M")
}

// goSectionNamed opens a section by name, or says there is none here.
func (m Model) goSectionNamed(name string) (tea.Model, tea.Cmd) {
	have := false
	if m.tab == tabMail {
		have = slices.ContainsFunc(m.sections, func(s config.Section) bool { return s.Name == name })
	} else {
		have = slices.Contains(chatSections, name)
	}
	if !have {
		m.say("no " + name + " section here")
		return m, nil
	}
	return m.goSection(name)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
