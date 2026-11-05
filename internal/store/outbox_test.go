package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// What a narada from before actions runs to find work. It would send an
// action's Body as a plain message, so it must never find one.
func oldBinaryDue(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE status='queued' AND send_at<=?`, now()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestActionsAreHiddenFromOlderBinaries(t *testing.T) {
	s := openTest(t)
	past := time.Now().Add(-time.Minute).UnixMilli()
	plain, _ := s.AddOutbox(OutboxItem{Account: WhatsAppAccount, Kind: "whatsapp", Status: StatusQueued, SendAt: past, Out: Outgoing{Space: "a", Body: "hi"}})
	act, _ := s.AddOutbox(OutboxItem{Account: WhatsAppAccount, Kind: "whatsapp", Status: StatusQueued, SendAt: past, Out: Outgoing{Space: "a", Body: "the old text", Action: "delete", Target: "a/1"}})
	if n := oldBinaryDue(t, s); n != 1 {
		t.Fatalf("an older binary finds %d items, want only the plain send", n)
	}
	// this version sees both as queued
	due, err := s.DueOutbox()
	if err != nil || len(due) != 2 {
		t.Fatalf("due %d %v", len(due), err)
	}
	for _, it := range due {
		if it.Status != StatusQueued {
			t.Fatalf("#%d reads as %s", it.ID, it.Status)
		}
	}
	if got, _ := s.OutboxItem(act); got.Status != StatusQueued || got.Out.Action != "delete" {
		t.Fatalf("%+v", got)
	}
	if items, _ := s.Outbox(StatusQueued); len(items) != 2 {
		t.Fatalf("Outbox(queued) lists %d", len(items))
	}
	if ok, _ := s.Reschedule(act, Outgoing{Space: "a", Action: "delete", Target: "a/1"}, past); !ok {
		t.Fatal("couldn't reschedule an action")
	}
	// claimed once, and it fails, and it's retried: still hidden
	if ok, _ := s.Transition(act, StatusQueued, StatusSending, 0); !ok {
		t.Fatal("couldn't claim an action")
	}
	if ok, _ := s.Transition(act, StatusQueued, StatusSending, 0); ok {
		t.Fatal("claimed twice")
	}
	_ = s.FinishOutbox(act, StatusFailed, "boom", "")
	if ok, _ := s.Transition(act, StatusFailed, StatusQueued, past); !ok {
		t.Fatal("couldn't retry")
	}
	if n := oldBinaryDue(t, s); n != 1 {
		t.Fatalf("after a retry an older binary finds %d", n)
	}
	// an agent's, once approved, is hidden too; a plain one isn't
	ag, _ := s.AddOutbox(OutboxItem{Account: WhatsAppAccount, Kind: "whatsapp", Status: StatusApproval, Out: Outgoing{Space: "a", Action: "forward", Target: "a/1"}})
	if ok, _ := s.Transition(ag, StatusApproval, StatusQueued, past); !ok {
		t.Fatal("couldn't approve")
	}
	if n := oldBinaryDue(t, s); n != 1 {
		t.Fatalf("after approval an older binary finds %d", n)
	}
	_ = plain
	if ok, _ := s.Transition(plain, StatusQueued, StatusCancelled, 0); !ok {
		t.Fatal("couldn't cancel a plain send")
	}
	if n := oldBinaryDue(t, s); n != 0 {
		t.Fatalf("%d left", n)
	}
}

// Every process runs RecoverSending each second: a long upload isn't stuck.
func TestRecoverSendingLeavesBigUploadsAlone(t *testing.T) {
	s := openTest(t)
	old := time.Now().Add(-10 * time.Minute).UnixMilli()
	add := func(kind string, files []File, at int64) int64 {
		id, _ := s.AddOutbox(OutboxItem{Account: "x", Kind: kind, Status: StatusQueued, SendAt: at, Out: Outgoing{Space: "a", Body: "b", Files: files}})
		if ok, _ := s.Transition(id, StatusQueued, StatusSending, 0); !ok {
			t.Fatal("claim")
		}
		return id
	}
	file := []File{{Name: "app.apk", Path: "/x"}}
	upload := add("whatsapp", file, old)
	text := add("whatsapp", nil, old)
	mail := add("mail", file, old)
	dead := add("whatsapp", file, time.Now().Add(-3*time.Hour).UnixMilli())
	if err := s.RecoverSending(); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{upload: StatusSending, text: StatusFailed, mail: StatusFailed, dead: StatusFailed} {
		if got, _ := s.OutboxItem(id); got.Status != want {
			t.Errorf("#%d is %s, want %s", id, got.Status, want)
		}
	}
}
