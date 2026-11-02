package whatsapp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// The real key store opens on sandesh's SQLite driver, private, unlinked
// (nothing here goes online).
func TestOpenKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whatsapp.db")
	c, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Linked() || c.Me() != "" {
		t.Fatal("a fresh store is linked")
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("keys file: %v %v", st.Mode(), err)
	}
	if err := c.Connect(func(Event) {}); err == nil {
		t.Fatal("connected without linking")
	}
}

func TestText(t *testing.T) {
	for _, c := range []struct {
		msg  *waE2E.Message
		want string
	}{
		{&waE2E.Message{Conversation: proto.String("hi")}, "hi"},
		{&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String(" sunset ")}}, "📷 Photo: sunset"},
		{&waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true)}}, "🎤 Voice message"},
		{&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("bill.pdf")}}, "📎 bill.pdf"},
		{&waE2E.Message{}, ""},
	} {
		if got := Text(c.msg); got != c.want {
			t.Errorf("Text = %q, want %q", got, c.want)
		}
	}
	if Digits("+91 98765-43210") != "919876543210" || PhoneJID("+1 555") != "1555@s.whatsapp.net" || !IsGroup("1203@g.us") {
		t.Fatal("phone helpers")
	}
}
