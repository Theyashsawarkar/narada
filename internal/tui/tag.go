package tui

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/store"
)

// Tagging people on WhatsApp. WhatsApp's own text holds "@<number>", and each
// phone shows its own name for it, so that's what goes out; here you type "@",
// pick a name, and read names. A number only shows when no name is known.

const tagRows = 6

// jidDigits is a JID's phone number.
func jidDigits(jid string) string { return strings.TrimSuffix(jid, "@s.whatsapp.net") }

// personName is what to call someone: their name, or +number when there's none.
func personName(p store.WhatsAppPerson) string {
	if p.Name != "" {
		return p.Name
	}
	return "+" + jidDigits(p.JID)
}

// loadWANames refreshes the names shown for tags, at most every 30 seconds.
func (m *Model) loadWANames() {
	if len(m.waNames) > 0 && time.Since(m.waNamesAt) < 30*time.Second {
		return
	}
	if names, err := m.a.Store.WhatsAppNames(); err == nil {
		m.waNames, m.waNamesAt = names, time.Now()
	}
}

var reWATag = regexp.MustCompile(`(^|[^\w@])@(\d{7,15})\b`)

// waTagged shows a WhatsApp text's tags by name. The name's spaces become
// no-break spaces, so it's marked as one mention and wraps as one.
func (m Model) waTagged(text string) string {
	if len(m.waNames) == 0 || !strings.Contains(text, "@") {
		return text
	}
	return reWATag.ReplaceAllStringFunc(text, func(t string) string {
		sub := reWATag.FindStringSubmatch(t)
		name := strings.TrimPrefix(m.waNames[sub[2]+"@s.whatsapp.net"], "~")
		if name == "" {
			return t
		}
		return sub[1] + "@" + strings.ReplaceAll(name, " ", " ")
	})
}

// updateTag looks at what's before the cursor: "@" starting a word, then what
// you've typed of a name, opens the list of who that could be.
func (m *Model) updateTag() {
	m.tagMatches, m.tagStart = nil, -1
	if m.mode != modeChatMsg || !m.wa || m.editingMsg != "" {
		return
	}
	cur := m.cursor()
	at := -1
	for i := cur - 1; i >= 0 && cur-i <= 40; i-- {
		if m.input[i] == '\n' {
			return
		}
		if m.input[i] == '@' {
			at = i
			break
		}
	}
	if at < 0 || at == m.tagOff {
		return
	}
	if at > 0 && !strings.ContainsRune(" \n\t(", m.input[at-1]) {
		return // the middle of a word, or an email address
	}
	sp, ok := m.curSpace()
	if !ok {
		return
	}
	if m.tagPeopleOf != sp.Name || time.Since(m.tagPeopleAt) > 30*time.Second {
		m.tagPeople, m.tagPeopleOf, m.tagPeopleAt = m.a.WhatsAppPeople(sp.Name), sp.Name, time.Now()
		sort.SliceStable(m.tagPeople, func(i, j int) bool {
			a, b := m.tagPeople[i], m.tagPeople[j]
			if (a.Name == "") != (b.Name == "") {
				return b.Name == "" // people with names first
			}
			return strings.ToLower(personName(a)) < strings.ToLower(personName(b))
		})
	}
	q := strings.ToLower(strings.TrimPrefix(string(m.input[at+1:cur]), "+"))
	var starts, inside []store.WhatsAppPerson
	for _, p := range m.tagPeople {
		name := strings.ToLower(personName(p))
		switch {
		case strings.HasPrefix(name, q) || strings.HasPrefix(jidDigits(p.JID), q):
			starts = append(starts, p)
		case strings.Contains(name, q) || strings.Contains(jidDigits(p.JID), q):
			inside = append(inside, p)
		}
	}
	m.tagMatches = append(starts, inside...)
	if len(m.tagMatches) == 0 {
		return
	}
	m.tagStart = at
	m.tagSel = max(0, min(m.tagSel, len(m.tagMatches)-1))
}

// tagKey handles the keys of the open list; false for the ones it doesn't use.
func (m *Model) tagKey(s string) bool {
	if m.tagStart < 0 {
		return false
	}
	n := len(m.tagMatches)
	switch s {
	case "up", "ctrl+p":
		m.tagSel = (m.tagSel + n - 1) % n
	case "down", "ctrl+n":
		m.tagSel = (m.tagSel + 1) % n
	case "tab", "enter":
		m.pickTag(m.tagMatches[m.tagSel])
	case "esc": // no tag after all: keep typing what you typed
		m.tagOff, m.tagStart, m.tagMatches = m.tagStart, -1, nil
	default:
		return false
	}
	return true
}

// pickTag puts the person's name in the text; the send turns it into the tag.
func (m *Model) pickTag(p store.WhatsAppPerson) {
	digits := jidDigits(p.JID)
	token := digits // no name, or a second person of that name: the number, so it tags the right one
	if p.Name != "" && (m.tagged[p.Name] == "" || m.tagged[p.Name] == digits) {
		token = p.Name
		if m.tagged == nil {
			m.tagged = map[string]string{}
		}
		m.tagged[p.Name] = digits
	}
	cur, at := m.cursor(), m.tagStart
	text := append(append(append([]rune{}, m.input[:at]...), []rune("@"+token+" ")...), m.input[cur:]...)
	back := len(m.input) - cur
	m.input, m.back = text, back
	m.tagStart, m.tagMatches, m.tagSel = -1, nil, 0
}

// resolveTags turns the names you picked into the tags WhatsApp needs, longest
// name first ("@Raj Kumar" before "@Raj"). Only what you picked changes: a
// word that merely looks like one stays as you wrote it.
func (m *Model) resolveTags(text string) string {
	names := make([]string, 0, len(m.tagged))
	for n := range m.tagged {
		names = append(names, n)
	}
	slices.SortFunc(names, func(a, b string) int { return len(b) - len(a) })
	for _, n := range names {
		text = strings.ReplaceAll(text, "@"+n, "@"+m.tagged[n])
	}
	m.tagged = nil
	return text
}

// tagBox is the list above the message box.
func (m Model) tagBox(w int) []string {
	s := m.styles
	first := max(0, min(m.tagSel-tagRows/2, len(m.tagMatches)-tagRows))
	var lines []string
	for i := first; i < min(first+tagRows, len(m.tagMatches)); i++ {
		p := m.tagMatches[i]
		name, row := personName(p), ""
		num := ""
		if p.Name != "" {
			num = s.muted.Render("  +" + jidDigits(p.JID))
		}
		if i == m.tagSel {
			row = s.accent.Bold(true).Render("▶ "+name) + num
		} else {
			row = "  " + s.text.Render(name) + num
		}
		lines = append(lines, ansi.Truncate(row, w-4, "…"))
	}
	if len(m.tagMatches) > tagRows {
		lines = append(lines, s.muted.Render(fmt.Sprintf("  %d of %d", m.tagSel+1, len(m.tagMatches))))
	}
	return bubble(lines, w, m.chatAccent(), false)
}
