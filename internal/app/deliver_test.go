package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Theyashsawarkar/narada/internal/store"
)

func waitSent(t *testing.T, a *App, id int64, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < within {
		if it, _ := a.Store.OutboxItem(id); it.Status == store.StatusSent {
			return time.Since(start)
		}
		time.Sleep(10 * time.Millisecond)
	}
	it, _ := a.Store.OutboxItem(id)
	t.Fatalf("#%d not sent within %s: %s %s", id, within, it.Status, it.Error)
	return 0
}

// The outbox wakes on a queued send and at its send_at, and sleeps
// otherwise: no ticker.
func TestDelivererIsEventDriven(t *testing.T) {
	a, g := setup(t)
	a.SyncAll(context.Background())
	a.SetDaemon()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.RunDeliverer(ctx, nil)

	// idle: one look at the start, then nothing
	time.Sleep(2500 * time.Millisecond)
	if n := a.DeliverRuns(); n != 1 {
		t.Fatalf("%d runs in 2.5 s with nothing queued", n)
	}

	// queued with no undo window: sent at once
	it, err := a.Queue("work", "mail", store.Outgoing{To: []string{"priya.k@example.com"}, Subject: "now", Body: "b"}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	if d := waitSent(t, a, it.ID, 2*time.Second); d > 500*time.Millisecond {
		t.Errorf("sent %s after queueing", d)
	}

	// scheduled: sent at its time, not before, not a second late
	at := time.Now().Add(1500 * time.Millisecond)
	it, err = a.Queue("work", "mail", store.Outgoing{To: []string{"priya.k@example.com"}, Subject: "later", Body: "b", ScheduledFor: at.UnixMilli()}, OriginYou)
	if err != nil {
		t.Fatal(err)
	}
	before := a.DeliverRuns()
	waitSent(t, a, it.ID, 3*time.Second)
	if late := time.Since(at); late < 0 || late > 400*time.Millisecond {
		t.Errorf("scheduled send went %s after its time", late)
	}
	if n := a.DeliverRuns() - before; n > 3 {
		t.Errorf("%d runs while waiting 1.5 s for one send", n)
	}

	// an approval (another process, here: the same) wakes it too
	a.Config.SendPolicy = "confirm"
	it, _ = a.Queue("work", "mail", store.Outgoing{To: []string{"rohan.d@example.com"}, Subject: "agent", Body: "b"}, OriginAgent)
	if it.Status != store.StatusApproval {
		t.Fatalf("status %s", it.Status)
	}
	if err := a.Approve(it.ID, nil); err != nil {
		t.Fatal(err)
	}
	waitSent(t, a, it.ID, 2*time.Second)
	if len(g.Sent) != 3 {
		t.Errorf("sent %d", len(g.Sent))
	}
}

// counts.json is rewritten when the counts change, and only then.
func TestCountsFile(t *testing.T) {
	a, _ := setup(t)
	old := countsDelay
	countsDelay = 50 * time.Millisecond
	t.Cleanup(func() { countsDelay = old })
	a.SyncAll(context.Background())
	unread := func() any {
		ts, _ := a.Store.Threads(store.ThreadQuery{Label: "INBOX", Unread: true})
		return map[string]int{"mail": len(ts)}
	}
	a.PublishCounts(unread)
	b, err := os.ReadFile(a.CountsFile())
	if err != nil || !strings.HasPrefix(string(b), `{"mail":`) {
		t.Fatalf("counts: %q %v", b, err)
	}
	fi, _ := os.Stat(a.CountsFile())
	// a write that doesn't move the counts leaves the file alone
	_ = a.Store.SetSpaceRead("work", "spaces/none", time.Now().UnixMilli())
	a.WriteCounts()
	time.Sleep(200 * time.Millisecond)
	if fi2, _ := os.Stat(a.CountsFile()); !fi2.ModTime().Equal(fi.ModTime()) {
		t.Error("rewritten with the same counts")
	}
	// reading a thread does
	ts, _ := a.Store.Threads(store.ThreadQuery{Label: "INBOX", Unread: true})
	if len(ts) == 0 {
		t.Fatal("no unread mail in the fake")
	}
	if err := a.MarkRead(context.Background(), "work", ts[0].ID, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	b2, _ := os.ReadFile(a.CountsFile())
	if string(b2) == string(b) {
		t.Errorf("counts didn't follow a read: %s", b2)
	}
}
