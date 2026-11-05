package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/narada/internal/store"
)

const (
	tagGroup = "120363000000000001@g.us"
	danaJID  = "919800000011@s.whatsapp.net"
	danJID   = "919800000012@s.whatsapp.net"
	anonJID  = "919800000013@s.whatsapp.net"
)

// a group with Dana (named by you), Dan (named by himself) and someone with no name
func tagModel(t *testing.T) Model {
	t.Helper()
	m := model(t)
	st := m.a.Store
	st.PutSpace(store.Space{Account: store.WhatsAppAccount, Name: tagGroup, DisplayName: "Trek", Type: "SPACE"})
	st.SetWhatsAppMembers(tagGroup, []string{danaJID, danJID, anonJID})
	st.PutUser(store.WhatsAppAccount, danaJID, "Dana Reyes", "")
	st.PutUser(store.WhatsAppAccount, danJID, "~Dan", "")
	m.wa, m.tab = true, tabChat
	m.waNames = nil
	m.reload()
	m.spaces, m.sel = []store.Space{{Account: store.WhatsAppAccount, Name: tagGroup, DisplayName: "Trek"}}, 0
	m.mode, m.focus = modeChatMsg, 1
	return m
}

func TestTagPickerListsAndNarrows(t *testing.T) {
	m := tagModel(t)
	m = typeText(m, "hello @")
	if m.tagStart != 6 || len(m.tagMatches) != 3 {
		t.Fatalf("@ should list all three: start %d, %v", m.tagStart, m.tagMatches)
	}
	// named people first, and no number shown as a name for them
	if m.tagMatches[0].Name != "Dan" || m.tagMatches[1].Name != "Dana Reyes" || m.tagMatches[2].Name != "" {
		t.Fatalf("order: %+v", m.tagMatches)
	}
	if box := ansi.Strip(strings.Join(m.tagBox(60), "\n")); !strings.Contains(box, "Dana Reyes") || !strings.Contains(box, "+919800000013") {
		t.Fatalf("list:\n%s", box)
	}
	m = typeText(m, "rey")
	if len(m.tagMatches) != 1 || m.tagMatches[0].JID != danaJID {
		t.Fatalf("narrowing: %+v", m.tagMatches)
	}
	m = typeText(m, "zzz")
	if m.tagStart != -1 {
		t.Fatal("nobody matches: the list should close")
	}
}

func TestTagPickedGoesOutAsANumberAndShowsAsAName(t *testing.T) {
	m := tagModel(t)
	m = typeText(m, "hi @dana")
	m = press(m, "enter") // picks, doesn't send
	if string(m.input) != "hi @Dana Reyes " {
		t.Fatalf("picked: %q", string(m.input))
	}
	text := m.resolveTags(strings.TrimSpace(string(m.input)) + " see you")
	if text != "hi @919800000011 see you" {
		t.Fatalf("what goes out: %q", text)
	}
	m.waNames = nil
	m.loadWANames()
	shown := m.waTagged(text)
	if !strings.Contains(shown, "@Dana Reyes") || strings.Contains(shown, "9198") {
		t.Fatalf("shown: %q", shown)
	}
	if got := reMention.FindString(shown); got != "@Dana Reyes" {
		t.Fatalf("one mention: %q", got)
	}
	// a name they gave themselves, without its ~; and a number when nothing is known
	if got := m.waTagged("@919800000012 and @919800000013"); got != "@Dan and @919800000013" {
		t.Fatalf("fallbacks: %q", got)
	}
}

func TestTagWithoutANameInsertsTheNumber(t *testing.T) {
	m := tagModel(t)
	m = typeText(m, "@9198000000")
	m = typeText(m, "13")
	m = press(m, "tab")
	if string(m.input) != "@919800000013 " {
		t.Fatalf("%q", string(m.input))
	}
}

func TestTagEscKeepsWhatYouTyped(t *testing.T) {
	m := tagModel(t)
	m = typeText(m, "@dan")
	m = press(m, "esc")
	if m.mode != modeChatMsg || string(m.input) != "@dan" || m.tagStart != -1 {
		t.Fatalf("mode %v, %q", m.mode, string(m.input))
	}
	m = typeText(m, "a") // and it doesn't come back for that @
	if m.tagStart != -1 {
		t.Fatal("the list came back")
	}
}

func TestEmailIsntATag(t *testing.T) {
	m := tagModel(t)
	m = typeText(m, "write to dan@")
	if m.tagStart != -1 {
		t.Fatal("an @ inside a word opened the list")
	}
}

func TestTwoPeopleOfOneNameTagTheRightOne(t *testing.T) {
	m := tagModel(t)
	m.tagged = map[string]string{"Dana Reyes": "919800000099"}
	m.input, m.back, m.tagStart = []rune("@"), 0, 0
	m.pickTag(store.WhatsAppPerson{JID: danaJID, Name: "Dana Reyes"})
	if string(m.input) != "@919800000011 " {
		t.Fatalf("%q", string(m.input))
	}
}
