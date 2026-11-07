package whatsapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func webMsg(id, participant string, at time.Time, msg *waE2E.Message) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
		Key:              &waCommon.MessageKey{RemoteJID: proto.String("status@broadcast"), ID: proto.String(id), FromMe: proto.Bool(false), Participant: proto.String(participant)},
		MessageTimestamp: proto.Uint64(uint64(at.Unix())),
		Message:          msg,
	}}
}

// Statuses in the phone's history come in while they're up (a day),
// photos without a caption too; other broadcasts and newsletters stay out.
// What happens to each is written to the WhatsApp log.
func TestHistoryStatuses(t *testing.T) {
	dir := t.TempDir()
	Log = filepath.Join(dir, "whatsapp.log")
	defer func() { Log = "" }()
	c, err := Open(context.Background(), filepath.Join(dir, "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var got []Message
	c.handler = func(e Event) { got = append(got, e.Messages...) }
	now := time.Now()
	c.history(context.Background(), &events.HistorySync{Data: &waHistorySync.HistorySync{Conversations: []*waHistorySync.Conversation{
		{ID: proto.String("status@broadcast"), Messages: []*waHistorySync.HistorySyncMsg{
			webMsg("S1", "919800000002@s.whatsapp.net", now.Add(-2*time.Hour), &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("At the top!")}}),
			webMsg("S2", "919800000003@s.whatsapp.net", now.Add(-time.Hour), &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}}),
			webMsg("S0", "919800000002@s.whatsapp.net", now.Add(-30*time.Hour), &waE2E.Message{Conversation: proto.String("yesterday's")}),
		}},
		{ID: proto.String("12345@broadcast"), Messages: []*waHistorySync.HistorySyncMsg{
			webMsg("B1", "919800000002@s.whatsapp.net", now, &waE2E.Message{Conversation: proto.String("a broadcast list")}),
		}},
		{ID: proto.String("120363000000000000@newsletter")},
	}}})
	if len(got) != 2 {
		t.Fatalf("got %d statuses: %+v", len(got), got)
	}
	for _, m := range got {
		if m.Chat != StatusChat || m.Sender == "" || m.Text == "" {
			t.Fatalf("status: %+v", m)
		}
	}
	if got[1].Text != "📷 Photo" || got[1].Media == nil {
		t.Fatalf("a photo status without a caption: %+v", got[1])
	}
	b, _ := os.ReadFile(Log)
	if !strings.Contains(string(b), "status from history: S1 by 919800000002@s.whatsapp.net stored") {
		t.Fatalf("log:\n%s", b)
	}
}

// The log never grows past about twice its cap.
func TestLogCap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "w.log")
	l := newLog(p, "").(*fileLog)
	line := strings.Repeat("x", 1000)
	for range 2500 {
		l.Warnf("%s", line)
	}
	st, err := os.Stat(p)
	old, err2 := os.Stat(p + ".1")
	if err != nil || err2 != nil || st.Size() > logCap+2000 || old.Size() > logCap+2000 {
		t.Fatalf("sizes: %v %v %v %v", st, err, old, err2)
	}
}

// A reply to a status names the status chat in its quote, so WhatsApp
// shows it as an answer to that status.
func TestStatusReplyQuote(t *testing.T) {
	msg := outgoing("919800000002@s.whatsapp.net", "Wow", &Message{ID: "S1", Chat: StatusChat, Sender: "919800000002@s.whatsapp.net", Text: "At the top!"})
	ci := msg.GetExtendedTextMessage().GetContextInfo()
	if ci.GetStanzaID() != "S1" || ci.GetRemoteJID() != StatusChat || ci.GetParticipant() != "919800000002@s.whatsapp.net" || ci.GetQuotedMessage().GetConversation() != "At the top!" {
		t.Fatalf("context: %+v", ci)
	}
}
