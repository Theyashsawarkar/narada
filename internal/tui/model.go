// Package tui is the narada screen: sections on top, threads or spaces
// on the left, the selected one on the right, keys only.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/cli"
	"github.com/Theyashsawarkar/narada/internal/config"
	"github.com/Theyashsawarkar/narada/internal/store"
	"github.com/Theyashsawarkar/narada/internal/whatsapp"
)

type mode int

const (
	modeNormal mode = iota
	modeFilter
	modeAsk
	modeSearch
	modeSaveName
	modeDraftHint
	modeReview
	modeApprove
	modeHelp
	modeChatMsg // typing a chat message in the preview
	modeLink    // typing a link number after o
	modeRename  // typing a chat's new name
	modePick    // the file picker
	modeWhen    // picking when to send
	modeConvo   // picking people for a new conversation
	modeReact   // picking an emoji for a reaction
	modePhone   // typing your number to link WhatsApp with a code
	modeCompose // the compose screen
	modeFind    // the search window
	modeView    // a picture full size
	modeForward // typing who to forward a WhatsApp message to
	modeVote    // typing the options to vote for in a WhatsApp poll
	modeCommand // typing a : command
)

const (
	tabMail = iota
	tabChat
)

var chatSections = []string{"All", "Unread", "DMs", "Spaces", "Scheduled", aiSection, "Archived", "Status"} // the last two: WhatsApp only

// aiSection is where an agent's sends wait for your answer, in mail, chat
// and WhatsApp alike.
const (
	aiSection = "AI requests"
	aiLabel   = "AGENT" // not a Gmail label: narada's outbox
)

// sectionsWithAI: your mail sections, then the AI requests.
func sectionsWithAI(secs []config.Section) []config.Section {
	return append(secs, config.Section{Name: aiSection, Label: aiLabel})
}

// inAISection: the list is the AI requests of this place.
func (m Model) inAISection() bool {
	if m.tab == tabMail {
		return m.search == nil && m.currentSection().Label == aiLabel
	}
	return chatSections[m.sec%len(chatSections)] == aiSection
}

// aiKind is what the current place's AI requests are.
func (m Model) aiKind() string {
	switch {
	case m.tab == tabMail:
		return "mail"
	case m.wa:
		return "whatsapp"
	}
	return "chat"
}

// waitingOf: the AI requests of one kind, oldest first.
func (m Model) waitingOf(kind string) []store.OutboxItem {
	var out []store.OutboxItem
	for _, it := range m.waiting {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

// curWaiting is the request being answered: the one you picked, else the
// oldest. By id, so a refresh can't swap it for another under your eyes.
func (m Model) curWaiting() (store.OutboxItem, bool) {
	for _, it := range m.waiting {
		if it.ID == m.apID {
			return it, true
		}
	}
	if len(m.waiting) > 0 {
		return m.waiting[0], true
	}
	return store.OutboxItem{}, false
}

// aiCard draws a request with the approval screen's card.
func (m Model) aiCard(it store.OutboxItem, w, h int) string {
	same := m.waitingOf(it.Kind)
	i := slices.IndexFunc(same, func(x store.OutboxItem) bool { return x.ID == it.ID })
	if i < 0 {
		same, i = []store.OutboxItem{it}, 0
	}
	return approver{a: m.a, styles: m.styles, items: same, i: i, w: w, h: h, diff: true, scroll: m.apScroll, inline: true}.View()
}

type undo struct {
	what     string // archive, trash, read, unread, star, unstar, send
	account  string
	ids      []string
	outboxID int64
}

type review struct {
	kind      string // mail | chat
	account   string
	out       store.Outgoing
	replaceID int64 // editing a waiting send: it makes way on send
}

type Model struct {
	a      *app.App
	launch string // "", popup, demo
	styles styles
	w, h   int

	// Vim's ways: Space starts a leader key (its menu shows after a
	// moment), v picks a range, : takes a command, and keys can come in
	// twos (gg, dd, yy, za, gf)
	leader      bool
	leaderShown bool
	leaderAt    int // which Space this is, so an old timer doesn't show the menu
	visual      bool
	visualFrom  int    // where v started in the list
	prefix      string // the first key of a two-key command (g, d, y, z)
	cmdHist     []string
	statusN     int // people with a status update you haven't seen

	tab      int
	wa       bool // the chat tab shows WhatsApp instead of Google Chat
	sections []config.Section
	sec      int
	search   *config.Section // a server search shown as a temporary section
	accounts []string
	acct     int // -1: all

	threads   []store.Thread
	spaces    []store.Space
	counts    map[string]int // unread per mail section
	chatN     int
	apID      int64 // the AI request being answered
	apScroll  int
	waN       int     // WhatsApp chats with unread messages
	waLinked  bool    // WhatsApp is linked (else its tab is the link panel)
	waLinking *waLink // linking WhatsApp, from its tab
	sel       int
	offset    int
	focus     int // 0 list, 1 preview
	scroll    int
	selected  map[string]bool

	// preview of the current item
	curKey     string
	msgs       []store.Message
	chatMsgs   []store.ChatMessage
	preview    []string // rendered lines (buildPreview)
	links      []string // the preview's numbered links
	expanded   map[string]bool
	expandAll  bool
	showQuoted bool
	labelNames map[string]string

	mode          mode
	input         []rune
	back          int // the cursor's distance from the end of input
	filter        string
	ai            string
	aiTitle       string
	aiBusy        bool
	toast         string
	toastAt       time.Time
	undo          *undo
	viewer        *viewer // modeView: the picture shown full size
	pending       bool    // the open chat has messages counting down to send
	review        *review
	older         store.Backfill // the download of older mail, all accounts
	olderN        int            // how many "load older" fetches
	loadingOlder  bool
	noOlder       map[string]bool             // sections with nothing older on Gmail
	dbStamp       string                      // when the local copy last changed
	chatScheduled int                         // scheduled chat messages, for the tab
	replaceNext   int64                       // the waiting send the editor is changing
	sentStat      map[string]store.SentStatus // Sent: did they answer?
	outboxRows    map[string]store.OutboxItem // Sent: rows still in the outbox
	comp          *composer
	find          *finder
	flashMsg      string         // a chat message opened from search: marked
	chatMsgLine   map[string]int // where each chat message starts in the preview
	pick          *picker
	when          *whenPicker
	convo         *convoPicker
	chatSel       int               // the message picked in a chat (when the conversation has focus)
	newMark       map[string]int64  // per chat: what was read when it was opened (the "new" line)
	mentions      map[string]int    // per chat: unread messages that mention you
	waNames       map[string]string // WhatsApp names by JID, to show tags by name
	waNamesAt     time.Time
	tagPeople     []store.WhatsAppPerson // who can be tagged in tagPeopleOf
	tagPeopleOf   string
	tagPeopleAt   time.Time
	tagMatches    []store.WhatsAppPerson // who the "@" being typed could be
	tagStart      int                    // where that "@" is in m.input (-1: none)
	tagSel        int
	tagOff        int               // an "@" you pressed Esc on: not asked again
	tagged        map[string]string // names picked for this message, to their numbers
	chatFiles     []store.File      // files for the message being typed
	rec           *recording        // a voice note being recorded
	forwarding    string            // the WhatsApp message f forwards (P: the poll to vote in)
	typedAt       time.Time         // when WhatsApp was last told you're typing
	watched       map[string]time.Time
	editingMsg    string          // the message of yours being edited in the box
	armDelete     string          // d pressed once on this message
	reactSel      int             // the emoji picked in the reaction window
	reactMine     map[string]bool // the reactions on the picked message that are yours
	chatLimit     int             // how many messages of this chat to show (grows as older ones load)
	jumpBack      []string        // replies you jumped from to their originals (Backspace goes back)
	newLine       int             // where the "new" line is in the preview (0: none)
	scrollToNew   bool
	replyTo       *store.ChatMessage // the message the one being typed replies to
	pickMode      mode               // where the picker goes back to
	helpQ         string             // filtering the keys popup
	helpTop       int
	waiting       []store.OutboxItem
	syncing       bool
	synced        time.Time
	err           string
	errAt         time.Time
	atBottom      bool // the preview is scrolled to its end
	themeStamp    string
	lastSync      time.Time
}

func New(a *app.App, launch string) Model {
	m := Model{a: a, launch: launch, styles: newStyles(a.Config.Colors, a.Config.NerdFont), acct: -1, selected: map[string]bool{}, counts: map[string]int{}, noOlder: map[string]bool{}, olderN: a.Config.OlderPage,
		expanded: map[string]bool{}, tagStart: -1, tagOff: -1}
	m.sections = sectionsWithAI(a.Sections())
	m.accounts = a.AccountNames()
	// narada --tab chat (a status bar's chat capsule) opens on chat
	_, m.waLinked = a.Store.WhatsApp()
	switch os.Getenv("NARADA_TAB") {
	case "chat":
		m.tab = tabChat
	case "whatsapp":
		m.tab, m.wa = tabChat, true
	}
	m.reload()
	return m
}

// Run opens the screen.
// Run opens the screen, or, with no account yet, the setup screen that
// takes the credentials and signs one in first.
func Run(a *app.App, launch string) error {
	var first tea.Model = New(a, launch)
	if len(a.AccountNames()) == 0 {
		first = newSetup(a, launch)
	}
	// with no daemon running, the screen holds WhatsApp while it's open
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = a.StartWhatsApp(ctx) }()
	program = tea.NewProgram(first, tea.WithAltScreen())
	final, err := program.Run()
	imgs.forget()
	if fm, ok := final.(Model); ok && len(fm.watched) > 0 && !a.WhatsAppOnline() {
		// you've left: the daemon holding WhatsApp shows you away again
		_, _ = a.WhatsAppDo(context.Background(), whatsapp.DoOnline, "", "", 0)
	}
	if err == nil && demoWanted {
		demoWanted = false
		return app.ErrDemo
	}
	return err
}

// ------------------------------------------------------------------ data

func (m *Model) accountFilter() []string {
	if m.acct < 0 || m.acct >= len(m.accounts) {
		return nil
	}
	return []string{m.accounts[m.acct]}
}

// spaceAccounts: whose chats the chat tab lists: WhatsApp's, or the
// Google accounts (the one picked with u, else all).
func (m *Model) spaceAccounts() []string {
	if m.wa {
		return []string{store.WhatsAppAccount}
	}
	if accs := m.accountFilter(); accs != nil {
		return accs
	}
	return m.accounts
}

// chatKind: an outbox kind that is a chat message (Google Chat or WhatsApp).
// markSpaces marks chats read (or unread) in one go.
func (m *Model) markSpaces(sps []store.Space, read bool, what string) tea.Cmd {
	var todo []store.Space
	for _, sp := range sps {
		if !strings.HasPrefix(sp.Name, outboxPrefix) && (!read || sp.Unread > 0) {
			todo = append(todo, sp)
		}
	}
	if len(todo) == 0 {
		m.say("nothing unread there")
		return nil
	}
	a := m.a
	return m.act(what+" marked "+map[bool]string{true: "read", false: "unread"}[read], func(ctx context.Context) error {
		for _, sp := range todo {
			var err error
			if read {
				err = a.MarkSpaceRead(ctx, sp.Account, sp.Name)
			} else {
				err = a.MarkSpaceUnread(ctx, sp.Account, sp.Name)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// place is where you are: 0 Mail, 1 Chat, 2 WhatsApp.
func (m Model) place() int {
	switch {
	case m.tab == tabMail:
		return 0
	case m.wa:
		return 2
	}
	return 1
}

func chatKind(kind string) bool { return kind == "chat" || kind == "whatsapp" }

func (m *Model) currentSection() config.Section {
	if m.search != nil {
		return *m.search
	}
	if m.sec >= len(m.sections) {
		m.sec = 0
	}
	return m.sections[m.sec]
}

// reload reads the list from the local copy, keeping the selection on the
// same item when it is still there.
func (m *Model) reload() {
	if m.tab == tabChat && !m.wa && m.sec >= len(chatSections)-2 {
		m.sec = 0 // Archived and Status are WhatsApp's
	}
	m.loadWANames()
	keep := m.curKey
	accs := m.accountFilter()
	m.waiting, _ = m.a.Store.Outbox(store.StatusApproval)
	if m.search == nil {
		for _, s := range m.sections {
			ts, _ := m.a.SectionThreads(s, accs)
			n := 0
			for _, t := range ts {
				if t.Unread() {
					n++
				}
			}
			m.counts[s.Name] = n
			if s.Label == "SCHEDULED" {
				m.counts[s.Name] = len(m.outboxItems(accs, "mail", true))
			}
			if s.Label == aiLabel {
				m.counts[s.Name] = len(m.waitingOf("mail"))
			}
		}
	}
	gsps, _ := m.a.Store.Spaces(accs, nil)
	m.chatN = 0
	for _, s := range gsps {
		if s.Unread > 0 {
			m.chatN++
		}
	}
	_, m.waLinked = m.a.Store.WhatsApp()
	m.waN, m.statusN = 0, 0
	if m.waLinked {
		if groups, err := m.a.Statuses(); err == nil {
			for _, g := range groups {
				if !g.Mine {
					m.statusN++ // people with a status up
				}
			}
		}
		wsps, _ := m.a.Store.Spaces([]string{store.WhatsAppAccount}, nil)
		for _, s := range wsps {
			if s.Unread > 0 {
				m.waN++
			}
		}
	}
	sps := gsps
	if m.wa {
		sps, _ = m.a.Store.Spaces(m.spaceAccounts(), nil)
	}
	m.chatScheduled = len(m.outboxItems(m.spaceAccounts(), "chat", true))
	if m.tab == tabMail {
		ts, err := m.a.SectionThreads(m.currentSection(), accs)
		if err != nil {
			m.err = err.Error()
		}
		if m.filter != "" {
			ts = filterThreads(ts, m.filter)
		}
		m.sentStat, m.outboxRows = nil, nil
		if m.search == nil && m.currentSection().Label == "SCHEDULED" {
			ts = m.outboxThreads(m.outboxItems(accs, "mail", true))
		}
		if m.inAISection() {
			ts = m.outboxThreads(m.waitingOf("mail"))
		}
		if m.search == nil && m.currentSection().Label == "SENT" {
			ts = append(m.outboxThreads(m.outboxItems(accs, "mail", false)), ts...)
			m.sentStat = map[string]store.SentStatus{}
			byAcc := map[string][]string{}
			for _, t := range ts {
				byAcc[t.Account] = append(byAcc[t.Account], t.ID)
			}
			for acc, ids := range byAcc {
				for id, st := range m.a.Store.SentStatuses(acc, ids) {
					m.sentStat[acc+"/"+id] = st
				}
			}
		}
		m.threads = ts
	} else if name := chatSections[m.sec%len(chatSections)]; name == "Scheduled" || name == aiSection {
		items := m.outboxItems(m.spaceAccounts(), "chat", true)
		if name == aiSection {
			items = m.waitingOf(m.aiKind())
		}
		m.outboxRows = map[string]store.OutboxItem{}
		types := map[string]string{}
		for _, s := range sps {
			types[s.Account+"/"+s.Name] = s.Type
		}
		var out []store.Space
		for _, it := range items {
			id := fmt.Sprintf("%s%d", outboxPrefix, it.ID)
			m.outboxRows[id] = it
			out = append(out, store.Space{Account: it.Account, Name: id, DisplayName: it.Out.SpaceName, Type: types[it.Account+"/"+it.Out.Space],
				LastActive: it.Out.ScheduledFor, LastText: oneLine(it.Out.Body), LastSender: "You"})
		}
		m.spaces = out
	} else {
		m.outboxRows = nil
		m.mentions = map[string]int{}
		for _, acc := range m.a.AccountNames() {
			if m.wa {
				break
			}
			if a, err := m.a.Store.Account(acc); err == nil && a.ChatUser != "" {
				if me, _, ok := m.a.Store.User(acc, a.ChatUser); ok {
					for sp, n := range m.a.Store.MentionCounts(acc, firstWord(me)) {
						m.mentions[acc+"/"+sp] = n
					}
				}
			}
		}
		var out []store.Space
		for _, s := range sps {
			section := chatSections[m.sec%len(chatSections)]
			if s.Archived != (section == "Archived") || (s.Type == store.TypeStatus) != (section == "Status") {
				continue // archived chats and people's statuses only show in their sections
			}
			switch chatSections[m.sec%len(chatSections)] {
			case "Unread":
				if s.Unread == 0 {
					continue
				}
			case "DMs":
				if s.Type != "DIRECT_MESSAGE" {
					continue
				}
			case "Spaces":
				if s.Type == "DIRECT_MESSAGE" {
					continue
				}
			}
			if m.filter != "" && !strings.Contains(strings.ToLower(cli.SpaceTitle(s)+" "+s.LastText), strings.ToLower(m.filter)) {
				continue
			}
			out = append(out, s)
		}
		// pinned ones first, the rest as they come (most recent first)
		sort.SliceStable(out, func(i, j int) bool { return out[i].Pinned && !out[j].Pinned })
		m.spaces = out
	}
	m.waiting, _ = m.a.Store.Outbox(store.StatusApproval)
	// how far the download of older mail has come
	m.older = store.Backfill{Done: true}
	if _, on := app.KeepQuery(m.a.Config.Keep); on {
		for _, acc := range m.a.AccountNames() {
			if b := m.a.Store.Backfill(acc); !b.Done {
				m.older.Done = false
				m.older.Have += b.Have
				m.older.Total += b.Total
			}
		}
	}
	// keep the selection
	for i := 0; i < m.count(); i++ {
		if m.keyAt(i) == keep {
			m.sel = i
			break
		}
	}
	m.clamp()
	follow := m.tab == tabChat && m.atBottom
	m.loadPreview(m.curKey != keep)
	if follow {
		m.scroll = 1 << 30 // new messages: stay at the bottom, like a chat app
	}
	m.fixView()
}

func filterThreads(ts []store.Thread, f string) []store.Thread {
	f = strings.ToLower(f)
	var out []store.Thread
	for _, t := range ts {
		if strings.Contains(strings.ToLower(t.Subject+" "+t.Participants+" "+t.LastFrom+" "+t.Snippet), f) {
			out = append(out, t)
		}
	}
	return out
}

func (m *Model) count() int {
	if m.tab == tabMail {
		return len(m.threads)
	}
	return len(m.spaces)
}

func (m *Model) keyAt(i int) string {
	if m.tab == tabMail {
		if i < len(m.threads) {
			return m.threads[i].Account + "/" + m.threads[i].ID
		}
		return ""
	}
	if i < len(m.spaces) {
		return m.spaces[i].Account + "/" + m.spaces[i].Name
	}
	return ""
}

func (m *Model) clamp() {
	if m.sel >= m.count() {
		m.sel = m.count() - 1
	}
	if m.sel < 0 {
		m.sel = 0
	}
}

func (m *Model) curThread() (store.Thread, bool) {
	if m.tab != tabMail || m.sel >= len(m.threads) || strings.HasPrefix(m.threads[m.sel].ID, outboxPrefix) {
		return store.Thread{}, false
	}
	return m.threads[m.sel], true
}

type olderMsg struct {
	section string
	n       int
	err     error
}

// loadOlderCmd fetches the next older batch of the section from Gmail.
func (m *Model) loadOlderCmd() tea.Cmd {
	sec := m.currentSection()
	if m.loadingOlder || m.noOlder[sec.Name] || sec.Today || sec.Label == "SCHEDULED" || sec.Label == aiLabel {
		return nil
	}
	oldest := map[string]int64{}
	for _, t := range m.threads {
		if strings.HasPrefix(t.ID, outboxPrefix) {
			continue
		}
		if o, ok := oldest[t.Account]; !ok || t.LastDate < o {
			oldest[t.Account] = t.LastDate
		}
	}
	accs := m.accountFilter()
	if len(accs) == 0 {
		accs = m.accounts
	}
	m.loadingOlder = true
	a, n := m.a, m.olderN
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		total := 0
		for _, acc := range accs {
			got, err := a.LoadOlder(ctx, acc, sec, oldest[acc], n)
			if err != nil {
				return olderMsg{sec.Name, total, err}
			}
			total += got
		}
		return olderMsg{sec.Name, total, nil}
	}
}

// Sent shows mail still in the outbox (sending, failed) above what went out.
const outboxPrefix = "outbox:"

func (m *Model) curOutbox() (store.OutboxItem, bool) {
	key := ""
	switch {
	case m.tab == tabMail && m.sel < len(m.threads):
		key = m.threads[m.sel].ID
	case m.tab == tabChat && m.sel < len(m.spaces):
		key = m.spaces[m.sel].Name
	}
	it, ok := m.outboxRows[key]
	return it, ok
}

// outboxItems: what's still in the outbox, of one kind; scheduled ones,
// or the rest (sending, in the undo window, waiting for approval, failed
// this week), soonest first.
func (m *Model) outboxItems(accs []string, kind string, scheduled bool) []store.OutboxItem {
	items, _ := m.a.Store.Outbox(store.StatusApproval, store.StatusQueued, store.StatusSending, store.StatusFailed)
	var out []store.OutboxItem
	for _, it := range items {
		isSched := it.Status == store.StatusQueued && it.Out.ScheduledFor > 0
		if (it.Kind != kind && !(kind == "chat" && chatKind(it.Kind))) || isSched != scheduled || (len(accs) > 0 && !slices.Contains(accs, it.Account)) ||
			it.Status == store.StatusFailed && time.Since(time.UnixMilli(it.Created)) > 7*24*time.Hour {
			continue
		}
		out = append(out, it)
	}
	if scheduled {
		sort.SliceStable(out, func(i, j int) bool { return out[i].SendAt < out[j].SendAt })
	}
	return out
}

func (m *Model) outboxThreads(items []store.OutboxItem) []store.Thread {
	m.outboxRows = map[string]store.OutboxItem{}
	var out []store.Thread
	for _, it := range items {
		id := fmt.Sprintf("%s%d", outboxPrefix, it.ID)
		m.outboxRows[id] = it
		subject := it.Out.Subject
		if chatKind(it.Kind) {
			subject = "chat message"
		}
		out = append(out, store.Thread{Account: it.Account, ID: id, Subject: subject, Participants: strings.Join(it.Out.To, ", "),
			Snippet: oneLine(it.Out.Body), LastDate: max(it.SendAt, it.Created)})
	}
	return out
}

func (m *Model) curSpace() (store.Space, bool) {
	if m.tab != tabChat || m.sel >= len(m.spaces) || strings.HasPrefix(m.spaces[m.sel].Name, outboxPrefix) {
		return store.Space{}, false
	}
	return m.spaces[m.sel], true
}

// loadPreview reads the selected item's messages; reset scrolls to the
// top (mail) or the bottom (chat).
func (m *Model) loadPreview(reset bool) {
	key := m.keyAt(m.sel)
	if key != m.curKey {
		reset = true
		m.chatLimit, m.armDelete, m.jumpBack = 0, "", nil
	}
	m.curKey = key
	if reset && m.wa {
		m.waHere()
	}
	m.msgs, m.chatMsgs = nil, nil
	if t, ok := m.curThread(); ok {
		m.msgs, _ = m.a.Store.Messages(t.Account, t.ID)
	}
	if s, ok := m.curSpace(); ok {
		m.chatMsgs, _ = m.a.Store.ChatMessages(s.Account, s.Name, max(200, m.chatLimit))
		if s.Type == store.TypeStatus {
			// statuses by person, yours first, then whoever posted last
			if groups, err := m.a.Statuses(); err == nil {
				m.chatMsgs = nil
				for _, g := range groups {
					m.chatMsgs = append(m.chatMsgs, g.Items...)
				}
			}
		}
	}
	if reset {
		m.expandAll, m.showQuoted = false, false
		m.scroll = 0
		if m.tab == tabChat {
			m.scroll = 1 << 30 // clamped to the bottom when drawn
			if sp, ok := m.curSpace(); ok {
				if m.newMark == nil {
					m.newMark = map[string]int64{}
				}
				k := sp.Account + "/" + sp.Name
				if _, seen := m.newMark[k]; !seen {
					m.newMark[k] = 0
					if sp.Unread > 0 {
						m.newMark[k] = sp.LastRead
						m.scrollToNew = true
					}
				}
			}
		}
	}
}

// targets: the selected threads, or the current one.
func (m *Model) targets() []store.Thread {
	var out []store.Thread
	for _, t := range m.threads {
		if m.selected[t.Account+"/"+t.ID] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		if t, ok := m.curThread(); ok {
			out = append(out, t)
		}
	}
	return out
}

// pickingMsg: the conversation has focus, so j/k pick messages.
func (m *Model) pickingMsg() bool {
	_, ok := m.curSpace()
	return ok && m.focus == 1 && len(m.chatMsgs) > 0
}

// moveChatSel picks another message and keeps it in view; going up past
// the first one loads earlier messages.
func (m *Model) moveChatSel(step int) tea.Cmd {
	if step < 0 && m.chatSel == 0 {
		return m.loadOlderChat()
	}
	m.chatSel = max(0, min(len(m.chatMsgs)-1, m.chatSel+step))
	m.fixView()
	if line, ok := m.chatMsgLine[m.chatMsgs[m.chatSel].Name]; ok {
		avail := m.bodyHeight() - 6
		if line < m.scroll+1 || line > m.scroll+avail-3 {
			m.scroll = max(0, line-avail/2)
			m.fixView()
		}
	}
	return nil
}

// jumpToQuote picks the message the picked reply quotes (Enter again goes
// further back); when it's older than what's loaded, earlier messages are
// fetched until it turns up.
func (m *Model) jumpToQuote() tea.Cmd {
	c := m.chatMsgs[m.chatSel]
	if c.Quoted == "" {
		m.say("not a reply · Backspace goes back")
		return nil
	}
	if m.selectChatMsg(c.Quoted) {
		m.jumpBack = append(m.jumpBack, c.Name)
		return nil
	}
	if m.loadingOlder {
		return nil
	}
	// not loaded yet: fetch earlier messages until it's there
	m.loadingOlder = true
	m.say("looking for the original…")
	a, sp, target, from, oldest := m.a, c.Space, c.Quoted, c.Name, m.chatMsgs[0].Created
	acc := c.Account
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		total := 0
		for range 10 {
			if _, err := a.Store.ChatMessage(acc, target); err == nil {
				return jumpMsg{target, from, total, nil}
			}
			n, err := a.LoadOlderChat(ctx, acc, sp, oldest, 100)
			if err != nil || n == 0 {
				return jumpMsg{"", from, total, err}
			}
			total += n
			if t, err := a.Store.OldestChat(acc, sp); err == nil {
				oldest = t
			}
		}
		return jumpMsg{"", from, total, nil}
	}
}

type jumpMsg struct {
	target, from string
	loaded       int
	err          error
}

func (m Model) gotJump(msg jumpMsg) (tea.Model, tea.Cmd) {
	m.loadingOlder = false
	if msg.loaded > 0 {
		m.chatLimit = len(m.chatMsgs) + msg.loaded
		m.loadPreview(false)
	}
	switch {
	case msg.err != nil:
		m.setErr("the original: " + msg.err.Error())
	case msg.target == "" || !m.selectChatMsg(msg.target):
		m.say("the original isn't there any more (deleted, or from before you joined)")
	default:
		m.jumpBack = append(m.jumpBack, msg.from)
	}
	return m, nil
}

// selectChatMsg picks a message by name and scrolls to it.
func (m *Model) selectChatMsg(name string) bool {
	for i, c := range m.chatMsgs {
		if c.Name == name {
			m.chatSel = i
			m.flashMsg = name
			m.moveChatSel(0)
			return true
		}
	}
	return false
}

// replyTarget: the picked message, or else the latest one from someone else.
func (m *Model) replyTarget() *store.ChatMessage {
	if m.pickingMsg() && m.chatSel < len(m.chatMsgs) {
		c := m.chatMsgs[m.chatSel]
		return &c
	}
	for i := len(m.chatMsgs) - 1; i >= 0; i-- {
		if !m.chatMsgs[i].Mine {
			c := m.chatMsgs[i]
			return &c
		}
	}
	return nil
}

// quoteInto makes the message a reply to replyTo by quoting it, the way
// Chat's own reply does: it stays in the conversation, not in a thread
// (Google reports every space as threaded, so that can't decide it).
func (m *Model) quoteInto(out *store.Outgoing, sp store.Space) {
	if r := m.replyTo; r != nil && (r.Space == sp.Name || m.wa && whatsapp.IsGroup(r.Space) && !whatsapp.IsGroup(sp.Name)) {
		out.Quote = r.Name // (a group's message in a DM: a private reply)
	}
}

// replyPrivately answers the picked group message in your chat with its
// sender, quoting it, as WhatsApp's "Reply privately" does.
func (m *Model) replyPrivately() tea.Cmd {
	c := m.chatMsgs[m.chatSel]
	if !whatsapp.IsGroup(c.Space) || c.Mine {
		m.say("R replies privately to someone's message in a group")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sp, err := m.a.WhatsAppChat(ctx, c.Sender)
	if err != nil {
		m.setErr(err.Error())
		return nil
	}
	m.openSpace(sp)
	m.replyTo = &c
	return nil
}

// recolour picks up a theme change (varna's palette, the config or its
// include) without a restart.
func (m *Model) recolour() {
	stamp := config.ThemeStamp(m.a.Paths, m.a.Config)
	if stamp == m.themeStamp {
		return
	}
	first := m.themeStamp == ""
	m.themeStamp = stamp
	if first {
		return
	}
	if c, err := config.Load(m.a.Paths); err == nil {
		m.a.Config.Colors = c.Colors
		m.a.Config.Include = c.Include
		m.styles = newStyles(c.Colors, m.a.Config.NerdFont)
		m.fixView()
	}
}

// sent follows up a queued send: the undo window, or straight out (chat,
// by default), jumping to the end of the chat either way.
func (m *Model) sent(it store.OutboxItem) tea.Cmd {
	if chatKind(it.Kind) {
		m.reload() // your message shows now, before it has gone out
		m.scroll = 1 << 30
		m.fixView()
	}
	if it.Status != store.StatusQueued {
		m.say("waiting for approval")
		return nil
	}
	if it.Out.ScheduledFor > 0 {
		m.say("scheduled for " + app.SendTime(time.UnixMilli(it.Out.ScheduledFor), time.Now()) + " · in Scheduled: t moves it, u cancels")
		if !m.a.DaemonRunning() {
			m.setErr("the background service isn't running, and it's what sends scheduled mail: narada service install")
		}
		return nil
	}
	if w := m.a.UndoWindow(it.Kind); w > 0 {
		m.undo = &undo{what: "send", outboxID: it.ID}
		m.say(fmt.Sprintf("sending in %ds · u cancels", int(w.Seconds())))
		return nil
	}
	if !chatKind(it.Kind) {
		m.say("sending…")
	}
	a := m.a
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		for _, d := range a.DeliverDue(ctx) {
			if d.ID == it.ID && d.Status == store.StatusFailed {
				return doneMsg{err: fmt.Errorf("not sent: %s", d.Error)}
			}
			if d.ID == it.ID && !chatKind(it.Kind) {
				return doneMsg{msg: "sent", sync: true}
			}
		}
		// another process (the daemon) sends it; a chat message's mark
		// shows how far it got, so nothing to announce
		return doneMsg{sync: true}
	}
}

// setErr shows an error on its own line for a while (Esc clears it).
func (m *Model) setErr(e string) {
	m.err, m.errAt = e, time.Now()
}

func (m *Model) say(s string) {
	m.toast = s
	m.toastAt = time.Now()
}

// ------------------------------------------------------------- messages

type tickMsg time.Time
type syncedMsg struct{ errs map[string]error }
type doneMsg struct {
	msg  string
	err  error
	sync bool // sync soon after (a send went out)
}
type aiMsg struct {
	title, text string
	err         error
	draftFor    *review // D: open the editor on this reply with the AI's text
}
type editedMsg struct {
	kind, account, path string
	base                store.Outgoing
	approveID           int64
	toComposer          bool // back to the compose screen, not review
	err                 error
}
type searchMsg struct {
	q       string
	threads []store.Thread
	err     error
}

func tick() tea.Cmd { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }

// Init shows the local copy at once. The background service keeps it
// fresh; only when it isn't running does the screen sync by itself.
func (m Model) Init() tea.Cmd {
	if m.a.DaemonRunning() {
		return tick()
	}
	return tea.Batch(tick(), m.syncCmd())
}

type syncSoonMsg struct{}

// syncSoon asks for a quick sync shortly after a change (a send, an
// archive...), so the copy shows what Google now has. Gmail's change feed
// makes it cheap; the per-account lock keeps it from doubling the
// daemon's work.
func (m *Model) syncSoon() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return syncSoonMsg{} })
}

func (m *Model) syncCmd() tea.Cmd {
	if m.syncing {
		return nil
	}
	m.syncing = true
	a := m.a
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, errs := a.SyncAll(ctx)
		return syncedMsg{errs}
	}
}

func (m *Model) act(okMsg string, f func(ctx context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return doneMsg{msg: okMsg, err: f(ctx)}
	}
}

// --------------------------------------------------------------- update

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case waLinkMsg, waLinkTick:
		return m.linkUpdate(msg)
	case imgReadyMsg:
		// a photo finished loading: draw it, staying at the bottom if there
		follow := m.atBottom
		m.fixView()
		if follow {
			m.scroll = 1 << 30
			m.fixView()
		}
		return m, nil
	}
	switch msg := msg.(type) {
	case pastedMsg:
		if msg.err != nil {
			m.say(msg.err.Error())
		} else if msg.path != "" {
			m.chatFiles = append(m.chatFiles, store.File{Source: msg.path})
		} else if msg.text != "" && m.mode == modeChatMsg {
			m.insert([]rune(msg.text)...)
		}
		return m, nil
	case tea.WindowSizeMsg:
		if msg.Width == wideScreen {
			return m, nil // the renderer's, not the terminal's (widenRenderer)
		}
		// the last column stays free: the renderer clears the rest of each
		// row from there, wiping anything a terminal drew too far
		m.w, m.h = max(20, msg.Width-1), msg.Height
		m.fixView()
		m.layoutComposer()
		return m, widenRenderer(msg.Height)
	case leaderTick:
		if m.leader && msg.at == m.leaderAt {
			m.leaderShown = true
		}
		return m, nil
	case tickMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, tick())
		a := m.a
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			for _, it := range a.DeliverDue(ctx) {
				if it.Status == store.StatusFailed {
					return doneMsg{err: fmt.Errorf("send #%d failed: %s", it.ID, it.Error)}
				}
				return doneMsg{msg: "sent", sync: true}
			}
			return nil
		})
		if time.Since(m.toastAt) > 4*time.Second {
			m.toast = ""
		}
		if m.pending {
			m.fixView() // the countdown under the conversation
		}
		// pick up the daemon's syncs and the agents' approvals
		if m.mode != modeReview && m.mode != modeCompose {
			// only when another process (the daemon, an agent) wrote
			// something; Sent also counts down its outbox rows
			if st := m.a.Store.Stamp(); st != m.dbStamp || len(m.outboxRows) > 0 && time.Now().Unix()%3 == 0 {
				m.dbStamp = st
				m.reload()
			}
		}
		if !m.syncing && time.Since(m.lastSync) > m.a.Config.MailInterval.Duration {
			if m.a.DaemonRunning() {
				m.lastSync = time.Now() // the background service has it
			} else {
				cmds = append(cmds, m.syncCmd())
			}
		}
		if time.Now().Unix()%2 == 0 {
			m.recolour()
		}
		if m.mode == modeCompose && time.Now().Unix()%3 == 0 {
			m.saveDraft()
		}
		if m.err != "" && time.Since(m.errAt) > 12*time.Second {
			m.err = ""
		}
		return m, tea.Batch(cmds...)
	case olderMsg:
		m.loadingOlder = false
		if msg.err != nil {
			m.setErr("older mail: " + msg.err.Error())
			return m, nil
		}
		if msg.n == 0 {
			m.noOlder[msg.section] = true
			m.say("that's everything in " + msg.section)
			return m, nil
		}
		m.reload()
		m.say(fmt.Sprintf("loaded %d older", msg.n))
		return m, nil
	case pickedFilesMsg:
		return m.afterPick(msg)
	case syncSoonMsg:
		return m, m.syncCmd()
	case syncedMsg:
		m.syncing = false
		m.lastSync = time.Now()
		for acc, err := range msg.errs {
			m.setErr(acc + ": " + err.Error())
		}
		m.accounts = m.a.AccountNames()
		m.reload()
		return m, nil
	case doneMsg:
		if msg.err != nil {
			m.setErr(msg.err.Error())
		} else if msg.msg != "" {
			m.say(msg.msg)
		}
		m.reload()
		if msg.sync && m.tab == tabChat {
			m.scroll = 1 << 30 // your message: show the end of the chat
			m.fixView()
		}
		if msg.err == nil && (msg.sync || msg.msg != "") {
			return m, m.syncSoon()
		}
		return m, nil
	case searchMsg:
		if msg.err != nil {
			m.err = "search: " + msg.err.Error()
			return m, nil
		}
		m.search = &config.Section{Name: "search: " + msg.q, Query: msg.q}
		m.filter = ""
		m.threads = msg.threads
		m.sel, m.offset = 0, 0
		m.loadPreview(true)
		m.say(fmt.Sprintf("%d found · W saves it as a section · [ ] leave", len(msg.threads)))
		return m, nil
	case aiMsg:
		m.aiBusy = false
		if msg.err != nil {
			m.ai, m.aiTitle = msg.err.Error(), "claude: error"
			return m, nil
		}
		if msg.draftFor != nil {
			r := *msg.draftFor
			quoted := r.out.Body
			r.out.Body = strings.TrimSpace(msg.text)
			m.ai = ""
			return m, m.openComposer(r.account, "reply · drafted by claude", r.out, quoted, 0)
		}
		m.ai, m.aiTitle = strings.TrimSpace(msg.text), msg.title
		return m, nil
	case editedMsg:
		return m.afterEdit(msg)
	case convoDirTick:
		if c := m.convo; c != nil && strings.TrimSpace(c.input.Value()) == msg.q {
			a, acc := m.a, c.account
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				a.SearchDirectory(ctx, acc, msg.q)
				return convoDirMsg{msg.q}
			}
		}
		return m, nil
	case convoDirMsg:
		if c := m.convo; c != nil && strings.TrimSpace(c.input.Value()) == msg.q {
			skip := map[string]bool{}
			for _, p := range c.picked {
				skip[p.key()] = true
			}
			c.sugg = m.people(c.account, msg.q, 7, skip)
		}
		return m, nil
	case convoMsg:
		return m.gotConvo(msg)
	case jumpMsg:
		return m.gotJump(msg)
	case olderChatMsg:
		return m.gotOlderChat(msg)
	case myReactionsMsg:
		if m.mode == modeReact && m.pickingMsg() && m.chatMsgs[m.chatSel].Name == msg.msg {
			m.reactMine = map[string]bool{}
			for _, e := range msg.emoji {
				m.reactMine[e] = true
			}
		}
		return m, nil
	case signedInMsg:
		return m.gotSignedIn(msg)
	case convoURLMsg:
		if m.convo != nil {
			m.convo.signURL = msg.url
		}
		return m, nil
	case pickerFilesMsg:
		m.gotFiles(msg)
		return m, nil
	case findLocalTick:
		return m, m.findLocal(msg.seq)
	case findServerTick:
		return m, m.findServer(msg.seq)
	case findLocalMsg:
		return m, m.gotFindLocal(msg)
	case findServerMsg:
		return m, m.gotFindServer(msg)
	case findPreviewMsg:
		m.gotFindPreview(msg)
		return m, nil
	case dirTickMsg:
		return m, m.dirLookup(msg.q)
	case dirMsg:
		m.gotDirectory(msg)
		return m, nil
	case sigMsg:
		m.gotSignature(msg)
		return m, nil
	case tea.KeyMsg:
		nm, cmd := m.key(msg)
		if mm, ok := nm.(Model); ok {
			mm.fixView()
			return mm, cmd
		}
		return nm, cmd
	}
	return m, nil
}

// fixView keeps the list offset and the preview scroll in range and
// re-renders the preview, so what View draws and what the keys move agree.
func (m *Model) fixView() {
	visible := max(1, (m.bodyHeight()-3-m.listLabels())/m.cardRows()) // as the list draws it
	if m.sel < m.offset {
		m.offset = m.sel
	}
	if m.sel >= m.offset+visible {
		m.offset = m.sel - visible + 1
	}
	if m.w == 0 {
		return
	}
	m.buildPreview(m.previewWidth())
	avail := m.bodyHeight() - 2 - len(m.aiLines(m.previewWidth())) // the header and its rule
	if _, ok := m.curSpace(); ok {
		avail -= 3 // the message box
	}
	if m.scrollToNew && m.newLine > 0 {
		m.scroll, m.scrollToNew = max(0, m.newLine-2), false
	}
	bottom := max(0, len(m.preview)-max(3, avail))
	m.scroll = max(0, min(m.scroll, bottom))
	m.atBottom = m.scroll >= bottom
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if m.mode == modeCompose && m.comp != nil {
		return m.composerKey(k)
	}
	if m.mode == modeFind && m.find != nil {
		return m.finderKey(k)
	}
	if m.mode == modePick && m.pick != nil {
		return m.pickerKey(k)
	}
	if m.mode == modeWhen && m.when != nil {
		return m.whenKey(k)
	}
	if m.mode == modeConvo && m.convo != nil {
		return m.convoKey(k)
	}
	if m.mode == modeChatMsg && s == "ctrl+o" {
		return m, m.pickFiles("chatbox")
	}
	if m.mode == modeChatMsg && s == "ctrl+v" {
		return m, m.pasteImage()
	}
	if m.mode == modeChatMsg && s == "ctrl+p" && m.wa {
		return m.sendPoll()
	}
	if m.mode == modeChatMsg && s == "ctrl+r" {
		var err error
		if m.rec == nil {
			err = m.startRecording()
		} else {
			err = m.stopRecording(true)
		}
		if err != nil {
			m.say(err.Error())
		}
		return m, nil
	}
	if m.rec != nil && s == "enter" && m.mode == modeChatMsg {
		if err := m.stopRecording(true); err != nil { // Enter sends what was recorded
			m.say(err.Error())
			return m, nil
		}
	}
	if m.rec != nil && (s == "esc" || m.mode != modeChatMsg) {
		_ = m.stopRecording(false)
		m.say("voice note dropped")
		return m, nil
	}
	if m.mode == modeChatMsg && s == "backspace" && len(m.input) == 0 && len(m.chatFiles) > 0 {
		m.chatFiles = m.chatFiles[:len(m.chatFiles)-1]
		return m, nil
	}
	if m.mode == modeReact {
		return m.reactKey(s)
	}
	if m.mode == modeView {
		return m.viewKey(s)
	}
	// Ctrl+t while typing a chat message: send it later
	if m.mode == modeChatMsg && s == "ctrl+t" {
		if strings.TrimSpace(string(m.input)) == "" {
			return m, nil
		}
		text := string(m.input)
		if m.wa {
			text = m.resolveTags(text)
		}
		m.openWhen("chat")
		m.when.text, m.when.back = text, modeNormal
		return m, nil
	}
	if m.mode == modeHelp {
		return m.helpKey(k)
	}
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeHelp:
		if s == "?" || s == "q" || s == "esc" {
			m.mode = modeNormal
		}
		return m, nil
	case modeFilter, modeAsk, modeSearch, modeSaveName, modeDraftHint, modeChatMsg, modeLink, modeRename, modePhone, modeForward, modeVote:
		return m.inputKey(k)
	case modeReview:
		return m.reviewKey(s)
	case modeApprove:
		return m.approveKey(s)
	case modeCommand:
		return m.commandKey(k)
	}
	return m.normalKey(s)
}

func (m Model) inputKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeChatMsg && m.tagKey(k.String()) {
		return m, nil
	}
	if m.mode == modeChatMsg && m.wa && m.editingMsg == "" {
		switch k.Type {
		case tea.KeyRunes, tea.KeySpace:
			m.waTyping(false)
		case tea.KeyEnter, tea.KeyEsc:
			m.waTyping(true)
		}
	}
	// a new line in a chat message: alt+enter or ctrl+j (enter sends)
	if m.mode == modeChatMsg && (k.Type == tea.KeyEnter && k.Alt || k.Type == tea.KeyCtrlJ) {
		m.insert('\n')
		return m, nil
	}
	switch k.Type {
	case tea.KeyEsc:
		m.back = 0
		m.replyTo, m.editingMsg, m.chatFiles = nil, "", nil
		m.tagged, m.tagOff = nil, -1
		if m.mode == modeFilter {
			m.filter = ""
			m.reload()
		}
		m.mode, m.input = modeNormal, nil
		return m, nil
	case tea.KeyEnter:
		text := strings.TrimSpace(string(m.input))
		md := m.mode
		if md == modeChatMsg && text == "" && len(m.chatFiles) == 0 && m.editingMsg == "" {
			return m, nil // nothing to send: keep typing
		}
		m.mode, m.input, m.back = modeNormal, nil, 0
		if md == modeChatMsg && m.editingMsg == "" {
			m.mode = modeChatMsg // sent: stay in the box for the next one
		}
		switch md {
		case modeFilter:
			m.filter = text
			m.reload()
		case modeAsk:
			if text != "" {
				return m, m.ask(text)
			}
		case modeSearch:
			if text != "" {
				return m, m.searchCmd(text)
			}
		case modeSaveName:
			if m.search != nil && text == "" {
				text = m.search.Query
			}
			if m.search != nil && text != "" {
				if err := m.a.SaveSearch(text, m.search.Query); err != nil {
					m.err = err.Error()
					return m, nil
				}
				m.sections = sectionsWithAI(m.a.Sections())
				m.sec = len(m.sections) - 1
				m.search = nil
				m.say("saved as " + text)
				m.reload()
				return m, m.syncCmd()
			}
		case modeDraftHint:
			return m, m.aiDraft(text)
		case modeChatMsg:
			if name := m.editingMsg; name != "" {
				sp, _ := m.curSpace()
				m.editingMsg = ""
				a := m.a
				return m, m.act(map[bool]string{true: "editing…", false: "edited"}[m.wa], func(ctx context.Context) error { return a.EditChatMessage(ctx, sp.Account, name, text) })
			}
			if sp, ok := m.curSpace(); ok && sp.Type == store.TypeStatus {
				// an answer to a status goes to whoever posted it, quoting it
				if m.replyTo == nil || text == "" {
					m.say("pick a status and press r to answer it")
					return m, nil
				}
				it, err := m.a.ReplyToStatus(m.replyTo.Name, store.Outgoing{Body: text, Files: m.chatFiles}, app.OriginYou)
				m.chatFiles = nil
				if err != nil {
					m.setErr(err.Error())
					return m, nil
				}
				m.replyTo, m.mode, m.input = nil, modeNormal, nil
				m.say("your answer goes to " + firstNonEmpty(it.Out.SpaceName, "them") + ", quoting their status")
				return m, m.sent(it)
			}
			if sp, ok := m.curSpace(); ok && (text != "" || len(m.chatFiles) > 0) {
				if m.wa {
					text = m.resolveTags(text)
				}
				m.tagOff = -1
				out := store.Outgoing{Space: sp.Name, SpaceName: cli.SpaceTitle(sp), Body: text, Files: m.chatFiles}
				m.chatFiles = nil
				m.quoteInto(&out, sp)
				it, err := m.a.Queue(sp.Account, "chat", out, app.OriginYou)
				if err != nil {
					m.setErr(err.Error())
				} else {
					m.replyTo = nil
					return m, m.sent(it)
				}
			}
		case modeRename:
			if sp, ok := m.curSpace(); ok {
				a := m.a
				return m, func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					onGoogle, err := a.RenameSpace(ctx, sp.Account, sp.Name, text)
					switch {
					case err != nil:
						return doneMsg{err: err}
					case text == "":
						return doneMsg{msg: "back to its own name"}
					case onGoogle:
						if sp.Account == store.WhatsAppAccount {
							return doneMsg{msg: "renamed on WhatsApp"}
						}
						return doneMsg{msg: "renamed on Google Chat"}
					}
					return doneMsg{msg: "named here (Google Chat doesn't name DMs and groups)"}
				}
			}
		case modeLink:
			m.openLink(text)
		case modeVote:
			msg, a := m.forwarding, m.a
			m.forwarding = ""
			picks := strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' })
			it, err := a.VoteWhatsApp(msg, picks, app.OriginYou)
			if err != nil {
				m.setErr(err.Error())
				return m, nil
			}
			m.say(map[bool]string{true: "voting…", false: "taking your vote back…"}[len(picks) > 0])
			return m, m.sent(it)
		case modeForward:
			msg, a := m.forwarding, m.a
			m.forwarding = ""
			if text == "" || msg == "" {
				return m, nil
			}
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				sp, err := a.WhatsAppChat(ctx, text)
				if err != nil {
					return doneMsg{err: err}
				}
				if _, err := a.ForwardWhatsApp(msg, sp, app.OriginYou); err != nil {
					return doneMsg{err: err}
				}
				return doneMsg{msg: "forwarding to " + cli.SpaceTitle(sp) + "…"}
			}
		case modePhone:
			if len([]rune(strings.Trim(text, "+ "))) < 7 {
				m.say("type your number with its country code, like +919876543210")
				return m, nil
			}
			return m, m.startLink(text)
		}
		return m, nil
	case tea.KeyBackspace:
		if k.Alt {
			m.deleteWordBack()
		} else {
			m.deleteBack()
		}
	case tea.KeyDelete:
		m.deleteFwd()
	case tea.KeyCtrlU:
		m.input, m.back = nil, 0
	case tea.KeyCtrlK:
		m.input, m.back = m.input[:m.cursor()], 0
	case tea.KeyCtrlW:
		m.deleteWordBack()
	case tea.KeyLeft, tea.KeyCtrlB:
		if k.Alt {
			m.setCursor(wordBack(m.input, m.cursor()))
		} else {
			m.setCursor(m.cursor() - 1)
		}
	case tea.KeyRight, tea.KeyCtrlF:
		if k.Alt {
			m.setCursor(wordFwd(m.input, m.cursor()))
		} else {
			m.setCursor(m.cursor() + 1)
		}
	case tea.KeyCtrlLeft:
		m.setCursor(wordBack(m.input, m.cursor()))
	case tea.KeyCtrlRight:
		m.setCursor(wordFwd(m.input, m.cursor()))
	case tea.KeyHome, tea.KeyCtrlA:
		m.setCursor(lineStart(m.input, m.cursor()))
	case tea.KeyEnd, tea.KeyCtrlE:
		m.setCursor(lineEnd(m.input, m.cursor()))
	case tea.KeyUp, tea.KeyDown:
		if m.mode == modeChatMsg {
			d := map[bool]int{true: -1, false: 1}[k.Type == tea.KeyUp]
			m.moveLine(m.chatBoxWidth(m.previewInner())-1, d)
		}
	case tea.KeySpace:
		m.insert(' ')
	case tea.KeyRunes:
		r := k.Runes
		if k.Alt && len(r) == 1 {
			switch r[0] {
			case 'b':
				m.setCursor(wordBack(m.input, m.cursor()))
				return m, nil
			case 'f':
				m.setCursor(wordFwd(m.input, m.cursor()))
				return m, nil
			}
		}
		if k.Paste {
			// pasted text keeps its lines in a chat message, flattens elsewhere
			txt := strings.ReplaceAll(strings.ReplaceAll(string(r), "\r\n", "\n"), "\r", "\n")
			if m.mode != modeChatMsg {
				txt = strings.Join(strings.Fields(txt), " ")
			}
			r = []rune(txt)
		}
		m.insert(r...)
	}
	if m.mode == modeFilter {
		m.filter = string(m.input)
		m.sel, m.offset = 0, 0
		m.reload()
	}
	m.updateTag()
	return m, nil
}

// goToAI opens the AI requests of a kind in its place.
func (m *Model) goToAI(kind string) {
	m.tab, m.wa, m.sel, m.offset, m.focus = tabChat, kind == "whatsapp", 0, 0, 0
	m.sec = slices.Index(chatSections, aiSection)
	if kind == "mail" {
		m.tab, m.search = tabMail, nil
		m.sec = slices.IndexFunc(m.sections, func(s config.Section) bool { return s.Label == aiLabel })
	}
	m.reload()
}

// normalKey is a key in NORMAL mode: Vim's keys first (vim.go), then
// the action it names.
func (m Model) normalKey(s string) (tea.Model, tea.Cmd) {
	// on an AI request, its own keys answer it, before Vim's
	if it, ok := m.curOutbox(); ok && it.Status == store.StatusApproval && m.inAISection() && !m.leader && m.prefix == "" {
		switch s {
		case "y", "n", "e":
			m.apID = it.ID
			return m.approveKey(s)
		}
	}
	mm, cmd, old, done := m.vimKey(s)
	if done {
		return mm, cmd
	}
	return mm.(Model).actKey(old)
}

// actKey runs an action by its key name (the names before Vim's keys:
// vimKey translates, so dd reaches here as e).
func (m Model) actKey(s string) (tea.Model, tea.Cmd) {
	page := max(1, m.previewHeight()/2)
	// on an AI request, the approval keys answer it
	if it, ok := m.curOutbox(); ok && it.Status == store.StatusApproval && m.inAISection() {
		switch s {
		case "y", "n", "e":
			m.apID = it.ID
			return m.approveKey(s)
		}
	}
	if m.tab == tabChat && m.wa && !m.waLinked {
		if mm, cmd, ok := m.linkKey(s); ok {
			return mm, cmd
		}
		switch s {
		case "tab", "q", "ctrl+c", "?", "esc":
		default:
			return m, nil // nothing else to act on yet
		}
	}
	if what := m.notOnWhatsApp(s); what != "" {
		m.say(what + " isn't on WhatsApp in narada yet")
		return m, nil
	}
	if mm, cmd, ok := m.waChatKey(s); ok {
		return mm, cmd
	}
	switch s {
	case "q", "esc":
		switch {
		case m.focus == 1:
			m.focus = 0
		case m.err != "":
			m.err = ""
		case m.ai != "" || m.aiBusy:
			m.ai, m.aiBusy = "", false
		case len(m.selected) > 0:
			m.selected = map[string]bool{}
		case m.filter != "":
			m.filter = ""
			m.reload()
		case m.search != nil:
			m.search = nil
			m.reload()
		case s == "q":
			return m, tea.Quit
		}
		return m, nil
	case "?":
		m.mode = modeHelp
	case "j", "down":
		if m.pickingMsg() {
			return m, m.moveChatSel(1)
		} else if m.focus == 1 {
			m.scroll++
		} else if m.tab == tabMail && m.sel >= m.count()-1 && m.count() > 0 {
			return m, m.loadOlderCmd()
		} else if m.sel < m.count()-1 {
			m.sel++
			m.ai = ""
			m.loadPreview(true)
		}
	case "k", "up":
		if m.pickingMsg() {
			return m, m.moveChatSel(-1)
		} else if m.focus == 1 {
			m.scroll = max(0, m.scroll-1)
		} else if m.sel > 0 {
			m.sel--
			m.ai = ""
			m.loadPreview(true)
		}
	case "g":
		if m.pickingMsg() {
			return m, m.moveChatSel(-len(m.chatMsgs))
		} else if m.focus == 1 {
			m.scroll = 0
		} else {
			m.sel = 0
			m.loadPreview(true)
		}
	case "G":
		if m.pickingMsg() {
			return m, m.moveChatSel(len(m.chatMsgs))
		} else if m.focus == 1 {
			m.scroll = 1 << 30
		} else {
			m.sel = max(0, m.count()-1)
			m.loadPreview(true)
		}
	case "J":
		m.scroll++
	case "K":
		m.scroll = max(0, m.scroll-1)
	case "ctrl+d":
		m.scroll += page
	case "ctrl+u":
		m.scroll = max(0, m.scroll-page)
	case "h", "l":
		n := len(m.sections)
		if m.tab == tabChat {
			n = len(chatSections)
			if !m.wa {
				n -= 2 // Google Chat has no archive or status
				m.sec %= n
			}
		}
		if m.search != nil {
			m.search = nil
		} else if s == "l" {
			m.sec = (m.sec + 1) % n
		} else {
			m.sec = (m.sec + n - 1) % n
		}
		m.sel, m.offset, m.filter = 0, 0, ""
		m.selected = map[string]bool{}
		m.reload()
	case "tab", "shift+tab", "alt+m", "alt+c", "alt+w":
		// Tab: Mail → Chat → WhatsApp → Mail; Shift+Tab the other way;
		// Alt+m / Alt+c / Alt+w straight to one (Alt+1/2/3 are tmux's windows)
		at := m.place()
		switch s {
		case "tab":
			at = (at + 1) % 3
		case "shift+tab":
			at = (at + 2) % 3
		default:
			at = strings.Index("mcw", s[len(s)-1:])
		}
		if at == m.place() {
			return m, nil
		}
		m.tab, m.wa = tabChat, at == 2
		if at == 0 {
			m.tab = tabMail
		}
		m.sec, m.sel, m.offset, m.filter, m.search, m.ai = 0, 0, 0, "", nil, ""
		m.selected = map[string]bool{}
		m.reload()
	case "1":
		m.focus = 0
	case "2":
		m.focus = 1
		m.chatSel = len(m.chatMsgs) - 1 // the latest message
		m.fixView()
	case "enter", " ", "space":
		if m.pickingMsg() {
			// a picture opens full size (Enter only when it isn't a reply)
			if c := m.chatMsgs[m.chatSel]; (s != "enter" || c.Quoted == "") && m.openViewer(c.Name) {
				return m, nil
			}
			if s == "enter" {
				return m, m.jumpToQuote()
			}
			return m, nil
		}
		if s != "enter" {
			return m, nil
		}
		if it, ok := m.curOutbox(); ok {
			// a mail or message still waiting: open it to change it
			switch it.Status {
			case store.StatusApproval:
				m.apID, m.apScroll, m.mode = it.ID, 0, modeApprove
				return m, nil
			case store.StatusQueued, store.StatusFailed:
				if chatKind(it.Kind) {
					m.replaceNext = it.ID
					return m, m.editChat(it.Account, it.Out, 0)
				}
				if it.Kind == "mail" {
					body, quoted := splitQuoted(it.Out.Body)
					out := it.Out
					out.Body = body
					cmd := m.openComposer(it.Account, "edit · "+map[bool]string{true: "scheduled", false: "failed"}[it.Status == store.StatusQueued], out, quoted, 0)
					if m.comp != nil {
						m.comp.replaceID = it.ID
					}
					return m, cmd
				}
			}
			return m, nil
		}
		if m.focus == 1 && m.tab == tabMail {
			m.expandAll = !m.expandAll
			return m, nil
		}
		m.focus = 1
		m.chatSel = len(m.chatMsgs) - 1 // in a chat: pick its latest message
		m.fixView()
		if t, ok := m.curThread(); ok && t.Unread() {
			return m, m.act("", func(ctx context.Context) error { return m.a.MarkRead(ctx, t.Account, t.ID, true) })
		}
		if sp, ok := m.curSpace(); ok && sp.Unread > 0 {
			return m, m.act("", func(ctx context.Context) error { return m.a.MarkSpaceRead(ctx, sp.Account, sp.Name) })
		}
	case "/", "s":
		m.openFinder()
	case "W":
		if m.search != nil {
			m.mode, m.input = modeSaveName, nil // Enter alone names it after the search
		} else {
			m.say("search first (s), then W saves it")
		}
	case "X":
		if m.tab == tabMail && m.search == nil {
			name := m.currentSection().Name
			if m.a.RemoveSearch(name) {
				m.sections = sectionsWithAI(m.a.Sections())
				m.sec = 0
				m.say("removed section " + name)
				m.reload()
			} else {
				m.say(name + " comes from config.toml; remove it there")
			}
		}
	case "u":
		if len(m.accounts) > 1 {
			m.acct++
			if m.acct >= len(m.accounts) {
				m.acct = -1
			}
			m.sel, m.offset = 0, 0
			m.reload()
			if m.acct < 0 {
				m.say("all accounts")
			} else {
				m.say("account " + m.accounts[m.acct])
			}
		} else {
			m.say("only one account (narada auth add <name> adds another)")
		}
	case "R":
		if m.wa && m.pickingMsg() {
			return m, m.replyPrivately()
		}
		if it, ok := m.curOutbox(); ok && it.Status == store.StatusFailed {
			if err := m.a.Retry(it.ID); err != nil {
				m.setErr(err.Error())
			} else {
				m.say("sending again…")
				m.reload()
			}
			return m, m.sent(it)
		}
		m.say("syncing…")
		return m, m.syncCmd()
	case "P":
		if m.wa && m.pickingMsg() {
			c := m.chatMsgs[m.chatSel]
			if _, err := m.a.Store.WhatsAppPollOf(c.Name); err != nil {
				m.say("P votes in a poll: pick one first")
				return m, nil
			}
			m.forwarding = c.Name
			m.mode, m.input = modeVote, nil
			return m, nil
		}
	case "ctrl+x":
		// pick everything in the list, or nothing when it all is
		all := m.count() > 0
		for i := range m.count() {
			if k := m.keyAt(i); k != "" && !m.selected[k] {
				all = false
			}
		}
		m.selected = map[string]bool{}
		if !all {
			for i := range m.count() {
				if k := m.keyAt(i); k != "" && !strings.Contains(k, outboxPrefix) {
					m.selected[k] = true
				}
			}
			m.say(fmt.Sprintf("%d picked · m marks them read, M unread · V again clears", len(m.selected)))
		}
		return m, nil
	case "alt+r":
		// everything in this list, read
		if m.tab == tabMail {
			m.selected = map[string]bool{}
			for i := range m.count() {
				if k := m.keyAt(i); k != "" {
					m.selected[k] = true
				}
			}
			return m, m.modify("m")
		}
		return m, m.markSpaces(m.spaces, true, "everything here")
	case "x":
		if key := m.keyAt(m.sel); key != "" && !strings.Contains(key, outboxPrefix) {
			if m.selected[key] {
				delete(m.selected, key)
			} else {
				m.selected[key] = true
			}
			if m.sel < m.count()-1 {
				m.sel++
				m.loadPreview(true)
			}
		}
	case "e", "d", "*":
		if m.pickingMsg() {
			c := m.chatMsgs[m.chatSel]
			switch s {
			case "e": // react to it (or take a reaction back)
				return m, m.openReact()
			case "d": // delete it (yours), d twice
				forMe := m.wa && !c.Mine // WhatsApp: someone else's goes for you, here
				if !c.Mine && !forMe {
					m.say("only your own messages can be deleted")
					return m, nil
				}
				if m.armDelete != c.Name {
					m.armDelete = c.Name
					m.say(map[bool]string{false: "dd again deletes it, for everyone", true: "dd again deletes it for you (here only)"}[forMe])
					return m, nil
				}
				m.armDelete = ""
				a := m.a
				if forMe {
					return m, m.act("deleted for you", func(ctx context.Context) error {
						_, err := a.DeleteWhatsApp(c.Name, false, app.OriginYou)
						return err
					})
				}
				return m, m.act(map[bool]string{true: "deleting…", false: "deleted"}[m.wa], func(ctx context.Context) error { return a.DeleteChatMessage(ctx, c.Account, c.Name) })
			}
			return m, nil
		}
		return m, m.modify(s)
	case "m", "M":
		read := s == "m"
		if m.tab == tabChat {
			if len(m.selected) > 0 {
				var picked []store.Space
				for _, sp := range m.spaces {
					if m.selected[sp.Account+"/"+sp.Name] {
						picked = append(picked, sp)
					}
				}
				m.selected = map[string]bool{}
				return m, m.markSpaces(picked, read, fmt.Sprintf("%d chats", len(picked)))
			}
			if sp, ok := m.curSpace(); ok {
				if read {
					return m, m.act("marked read", func(ctx context.Context) error { return m.a.MarkSpaceRead(ctx, sp.Account, sp.Name) })
				}
				return m, m.act("marked unread", func(ctx context.Context) error { return m.a.MarkSpaceUnread(ctx, sp.Account, sp.Name) })
			}
			return m, nil
		}
		return m, m.modify(s)
	case "Q":
		m.showQuoted = !m.showQuoted
		m.say(map[bool]string{true: "showing quoted text", false: "quoted text folded"}[m.showQuoted])
	case "E":
		if m.pickingMsg() {
			// in a chat: edit the picked message (yours)
			c := m.chatMsgs[m.chatSel]
			if !c.Mine {
				m.say("only your own messages can be edited")
				return m, nil
			}
			m.editingMsg, m.replyTo = c.Name, nil
			m.mode, m.input = modeChatMsg, []rune(c.Source())
			return m, nil
		}
		m.expandAll = !m.expandAll
	case "o":
		switch len(m.links) {
		case 0:
			m.say("no links here")
		case 1:
			m.openLink("1")
		default:
			m.mode, m.input = modeLink, nil
		}
	case "n":
		if sp, ok := m.curSpace(); ok {
			m.mode, m.input = modeRename, []rune(cli.SpaceTitle(sp))
		}
	case "i":
		if m.tab == tabChat {
			if _, ok := m.curSpace(); ok {
				m.mode, m.input = modeChatMsg, nil
			}
		}
	case "+", "-":
		if m.tab == tabMail {
			sizes := []int{10, 25, 50, 100}
			i := slices.Index(sizes, m.olderN)
			if s == "+" {
				i = min(len(sizes)-1, i+1)
			} else {
				i = max(0, i-1)
			}
			m.olderN = sizes[max(0, i)]
			m.say(fmt.Sprintf("older mail loads %d at a time", m.olderN))
		}
	case "N":
		if m.tab == tabChat {
			m.openConvo()
		}
	case "backspace":
		// back from an original to the reply you jumped from
		if m.pickingMsg() && len(m.jumpBack) > 0 {
			name := m.jumpBack[len(m.jumpBack)-1]
			m.jumpBack = m.jumpBack[:len(m.jumpBack)-1]
			m.selectChatMsg(name)
		}
	case "p":
		if sp, ok := m.curSpace(); ok {
			_ = m.a.Store.SetPinned(sp.Account, sp.Name, !sp.Pinned)
			m.reload()
			m.say(map[bool]string{true: "pinned on top", false: "unpinned"}[!sp.Pinned])
		}
	case "[", "]":
		if m.tab == tabChat && m.count() > 0 {
			step := map[bool]int{true: -1, false: 1}[s == "["]
			m.sel = max(0, min(m.count()-1, m.sel+step))
			m.loadPreview(true)
			if m.focus == 1 {
				m.chatSel = len(m.chatMsgs) - 1
			}
			m.fixView()
		}
	case "ctrl+k":
		m.tab, m.wa = tabChat, false
		m.openFinder()
		m.find.scope = scopeAllChat
		m.find.placeholder()

	case "t":
		if it, ok := m.curOutbox(); ok && it.Status == store.StatusQueued {
			m.openWhen("outbox")
			m.when.outboxID = it.ID
		}
	case "z":
		if it, ok := m.curOutbox(); ok && (it.Status == store.StatusQueued || it.Status == store.StatusApproval) {
			if err := m.a.Cancel(it.ID); err != nil {
				m.setErr(err.Error())
			} else {
				m.say("cancelled, not sent")
				m.reload()
			}
			return m, nil
		}
		return m, m.undoCmd()
	case "c":
		return m, m.compose()
	case "r", "a", "f":
		if s == "f" && m.wa && m.pickingMsg() {
			c := m.chatMsgs[m.chatSel]
			if store.IsPending(c.Name) {
				m.say("wait until it has gone out")
				return m, nil
			}
			m.forwarding = c.Name
			m.mode, m.input = modeForward, nil
			return m, nil
		}
		if m.tab == tabChat {
			// reply to the picked message (or the latest from someone
			// else), quoting it, right in the message box
			if _, ok := m.curSpace(); ok && s == "r" {
				m.replyTo = m.replyTarget()
				m.mode, m.input = modeChatMsg, nil
			}
			return m, nil
		}
		return m, m.replyCmd(s)
	case "L":
		return m, m.labelCmd()
	case "v", "V":
		return m, m.saveCmd(s == "v") // v opens it (in its app), V only saves
	case "U":
		if m.tab != tabChat {
			m.say("U uploads into a chat space; for mail, reply and press + (or write Attach: in the editor)")
			return m, nil
		}
		return m, m.pickFiles("chat")
	case "A":
		if len(m.waiting) > 0 {
			m.goToAI(m.waiting[0].Kind)
		} else {
			m.say("no AI requests waiting")
		}
	case "b":
		if u := m.link(); u != "" {
			_ = exec.Command("xdg-open", u).Start()
			m.say("opened in the browser")
		}
	case "y":
		if m.pickingMsg() {
			m.say(copyText(m.chatMsgs[m.chatSel].Source()))
		} else if u := m.link(); u != "" {
			m.say(copyText(u))
		}
	case "@":
		m.mode, m.input = modeAsk, nil
	case "S":
		return m, m.summarise()
	case "D":
		if _, ok := m.curThread(); ok {
			m.mode, m.input = modeDraftHint, nil
		}
	case "T":
		return m, m.digest()
	}
	return m, nil
}

// modify archives, trashes, stars or toggles read on the targets, with undo.
func (m *Model) modify(key string) tea.Cmd {
	if m.tab != tabMail {
		return nil
	}
	ts := m.targets()
	if len(ts) == 0 {
		return nil
	}
	var ids []string
	for _, t := range ts {
		ids = append(ids, t.ID)
	}
	account := ts[0].Account
	var what, done string
	var f func(ctx context.Context, acc, id string) error
	switch key {
	case "e":
		what, done = "archive", "archived"
		f = m.a.Archive
	case "d":
		what, done = "trash", "moved to trash"
		f = m.a.Trash
	case "*":
		on := !ts[0].Starred()
		what, done = map[bool]string{true: "star", false: "unstar"}[on], map[bool]string{true: "starred", false: "unstarred"}[on]
		f = func(ctx context.Context, acc, id string) error { return m.a.Star(ctx, acc, id, on) }
	case "m", "M":
		read := key == "m"
		what, done = map[bool]string{true: "read", false: "unread"}[read], map[bool]string{true: "marked read", false: "marked unread"}[read]
		f = func(ctx context.Context, acc, id string) error { return m.a.MarkRead(ctx, acc, id, read) }
		for _, t := range ts {
			if read {
				_ = m.a.Store.SetThreadLabels(t.Account, t.ID, nil, []string{"UNREAD"})
			} else {
				_ = m.a.Store.SetThreadLabels(t.Account, t.ID, []string{"UNREAD"}, nil)
			}
		}
	}
	// change the local copy now so the list reacts at once; Google after
	byAcc := map[string][]string{}
	for _, t := range ts {
		byAcc[t.Account] = append(byAcc[t.Account], t.ID)
	}
	switch key {
	case "e":
		for _, t := range ts {
			_ = m.a.Store.SetThreadLabels(t.Account, t.ID, nil, []string{"INBOX"})
		}
	case "d":
		for _, t := range ts {
			_ = m.a.Store.SetThreadLabels(t.Account, t.ID, []string{"TRASH"}, []string{"INBOX"})
		}
	}
	m.selected = map[string]bool{}
	m.undo = &undo{what: what, account: account, ids: ids}
	msg := done
	if len(ids) > 1 {
		msg = fmt.Sprintf("%s %d", done, len(ids))
	}
	if key == "e" || key == "d" {
		msg += " · z undoes"
	}
	m.reload()
	return m.act(msg, func(ctx context.Context) error {
		for acc, list := range byAcc {
			for _, id := range list {
				if err := f(ctx, acc, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (m *Model) undoCmd() tea.Cmd {
	u := m.undo
	if u == nil {
		// a send in its undo window?
		items, _ := m.a.Store.Outbox(store.StatusQueued)
		// newest first; the open chat's own before any other
		var here []store.OutboxItem
		for _, it := range slices.Backward(m.pendingHere()) {
			if it.Status == store.StatusQueued || it.Status == store.StatusApproval {
				here = append(here, it)
			}
		}
		items = append(here, items...)
		if len(items) == 0 {
			m.say("nothing to undo")
			return nil
		}
		u = &undo{what: "send", outboxID: items[0].ID}
	}
	m.undo = nil
	if u.what == "send" {
		if err := m.a.Cancel(u.outboxID); err != nil {
			m.err = err.Error()
			return nil
		}
		m.say("send cancelled, nothing went out")
		return nil
	}
	return m.act("undone", func(ctx context.Context) error {
		for _, id := range u.ids {
			t, err := m.a.Store.Thread(u.account, id)
			acc := u.account
			if err == nil {
				acc = t.Account
			}
			switch u.what {
			case "archive":
				err = m.a.Unarchive(ctx, acc, id)
			case "trash":
				err = m.a.Untrash(ctx, acc, id)
			case "star":
				err = m.a.Star(ctx, acc, id, false)
			case "unstar":
				err = m.a.Star(ctx, acc, id, true)
			case "read":
				err = m.a.MarkRead(ctx, acc, id, false)
			case "unread":
				err = m.a.MarkRead(ctx, acc, id, true)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *Model) searchCmd(q string) tea.Cmd {
	acc := ""
	if f := m.accountFilter(); len(f) > 0 {
		acc = f[0]
	}
	a := m.a
	m.say("searching Gmail…")
	return func() tea.Msg {
		account, err := a.ResolveAccount(acc)
		if err != nil {
			return searchMsg{q: q, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		ts, err := a.SearchServer(ctx, account.Name, q, 50)
		// kept like a section, so a refresh shows it and "load older" adds to it
		var ids []string
		for _, t := range ts {
			ids = append(ids, t.ID)
		}
		_ = a.Store.SetSectionThreads(account.Name, "search: "+q, ids)
		return searchMsg{q, ts, err}
	}
}

func (m *Model) link() string {
	if t, ok := m.curThread(); ok {
		acc, _ := m.a.Store.Account(t.Account)
		return fmt.Sprintf("https://mail.google.com/mail/u/%s/#all/%s", acc.Email, t.ID)
	}
	if s, ok := m.curSpace(); ok {
		id := strings.TrimPrefix(s.Name, "spaces/")
		if s.Type == "DIRECT_MESSAGE" {
			return "https://mail.google.com/chat/u/0/#chat/dm/" + id
		}
		return "https://mail.google.com/chat/u/0/#chat/space/" + id
	}
	return ""
}

// openLink opens the preview's link number n in the browser.
func (m *Model) openLink(n string) {
	var i int
	if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &i); err != nil || i < 1 || i > len(m.links) {
		m.say(fmt.Sprintf("no link %s (there are %d)", n, len(m.links)))
		return
	}
	u := m.links[i-1]
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		m.say("not opening a non-web link: " + u)
		return
	}
	_ = exec.Command("xdg-open", u).Start()
	m.say("opened link " + n)
}

func copyText(s string) string {
	for _, c := range [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "-b"}} {
		if _, err := exec.LookPath(c[0]); err == nil {
			cmd := exec.Command(c[0], c[1:]...)
			cmd.Stdin = strings.NewReader(s)
			if cmd.Run() == nil {
				return "copied the link"
			}
		}
	}
	// OSC 52: the terminal (and tmux with set-clipboard) copies it
	fmt.Fprintf(os.Stderr, "\033]52;c;%s\a", b64(s))
	return "copied the link (OSC 52)"
}

// notOnWhatsApp: what key s would do that narada can't do on WhatsApp
// yet ("" when it can).
func (m Model) notOnWhatsApp(s string) string {
	if !m.wa || m.tab != tabChat {
		return ""
	}
	switch s {
	case "N":
		return "starting a chat here (use narada whatsapp send <number>)"
	}
	return ""
}
