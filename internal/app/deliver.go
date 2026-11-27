package app

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Theyashsawarkar/narada/internal/sock"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// The outbox is event-driven: nothing polls it. Whoever queues work
// (Store.OnQueue: an outbox row, an approval, a reschedule, WhatsApp
// receipts, reactions, actions, downloads) kicks the deliverer; a timer
// covers the next send_at. The daemon is the deliverer. Another process
// kicks it through the agents' socket ({"narada_kick":1}) and delivers
// itself only when no daemon runs or it holds WhatsApp.

// idleWait is the longest the deliverer sleeps with nothing due: it
// retries what failed to go (a WhatsApp receipt while offline) and puts
// sends stuck in "sending" by a process that died back to failed.
var idleWait = 5 * time.Minute

// waRetry is idleWait while this process holds WhatsApp: receipts and
// reactions that failed retry this soon.
var waRetry = 30 * time.Second

type deliverState struct {
	once    sync.Once
	wake    chan struct{}
	daemon  atomic.Bool
	pokeMu  sync.Mutex
	poking  bool
	again   bool
	pokes   sync.WaitGroup
	counts  countsState
	wakeups atomic.Int64 // DeliverDue runs, for tests
	running atomic.Bool
}

func (a *App) dl() *deliverState {
	a.dlv.once.Do(func() { a.dlv.wake = make(chan struct{}, 1) })
	return &a.dlv
}

// SetDaemon marks this process as the daemon: the deliverer, the one
// kicks go to.
func (a *App) SetDaemon() { a.dl().daemon.Store(true) }

// Wake wakes this process's deliverer.
func (a *App) Wake() {
	select {
	case a.dl().wake <- struct{}{}:
	default:
	}
}

// Kick says work was queued: this process's deliverer wakes, and the
// daemon does too when this isn't it.
func (a *App) Kick() {
	a.Wake()
	if !a.dl().daemon.Load() {
		go a.pokeDaemon()
	}
}

// pokeDaemon tells the daemon through its socket (a connection starts it
// when systemd holds the socket and it isn't running). A failure means
// no daemon: this process's deliverer, if it runs one, does the work.
// Pokes asked for while one is on its way make one more after it.
func (a *App) pokeDaemon() {
	d := a.dl()
	d.pokeMu.Lock()
	if d.poking {
		d.again = true
		d.pokeMu.Unlock()
		return
	}
	d.poking = true
	d.pokes.Add(1)
	d.pokeMu.Unlock()
	defer d.pokes.Done()
	for {
		if c, err := net.DialTimeout("unix", sock.Path(), time.Second); err == nil {
			_ = c.SetDeadline(time.Now().Add(time.Second))
			_, _ = c.Write([]byte(`{"narada_kick":1}` + "\n"))
			c.Close()
		}
		d.pokeMu.Lock()
		if !d.again {
			d.poking = false
			d.pokeMu.Unlock()
			return
		}
		d.again = false
		d.pokeMu.Unlock()
	}
}

// settle waits (up to 2 s) for pokes still on their way, so a command
// that approved or queued something doesn't exit before the daemon heard.
func (a *App) settle() {
	done := make(chan struct{})
	go func() { a.dl().pokes.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

// delivers says whether this process sends: the daemon always; another
// process when no daemon runs, or for WhatsApp, when it holds it.
func (a *App) delivers() bool {
	return a.dl().daemon.Load() || a.WhatsAppOnline() || !a.DaemonRunning()
}

// RunDeliverer sends what is due until ctx ends, waking when work is
// queued (Kick, Wake) or the next send_at comes, never on a fixed tick.
// done hears what it sent or failed to send.
func (a *App) RunDeliverer(ctx context.Context, done func([]store.OutboxItem)) {
	d := a.dl()
	if !d.running.CompareAndSwap(false, true) {
		return // one per process
	}
	defer d.running.Store(false)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
		case <-timer.C:
		}
		wait := idleWait
		if a.delivers() {
			d.wakeups.Add(1)
			if sent := a.DeliverDue(ctx); len(sent) > 0 && done != nil {
				done(sent)
			}
			if a.WhatsAppOnline() {
				wait = waRetry
			}
			if next := a.Store.NextSendAt(a.WhatsAppOnline()); next > 0 {
				wait = min(wait, max(time.Until(time.UnixMilli(next)), 200*time.Millisecond))
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
	}
}

// DeliverRuns is how many times the deliverer has run DeliverDue (tests).
func (a *App) DeliverRuns() int64 { return a.dl().wakeups.Load() }

// ------------------------------------------------------------ the counts

// The unread counts (narada status --json) go to a small file whenever
// they change, so a status bar can watch it instead of the database,
// which changes all the time (WhatsApp, syncs) without the counts
// moving: ${XDG_STATE_HOME:-~/.local/state}/narada/counts.json.

type countsState struct {
	mu      sync.Mutex
	f       func() any
	pending bool // a recount is scheduled (PublishCounts)
	dirty   bool // the store changed (TrackCounts)
	live    bool
}

// countsDelay gathers a burst of writes into one recount.
var countsDelay = time.Second

// CountsFile is where the counts are kept.
func (a *App) CountsFile() string { return filepath.Join(a.Paths.State, "counts.json") }

// PublishCounts makes this process keep CountsFile up to date: every
// write to the store that changed something recounts (at most once a
// second), and the file is rewritten only when the counts differ.
func (a *App) PublishCounts(counts func() any) {
	c := &a.dl().counts
	c.mu.Lock()
	c.f, c.live = counts, true
	c.mu.Unlock()
	a.Store.OnWrite = a.countsDirty
	a.WriteCounts()
}

// TrackCounts is PublishCounts for a short-lived command: writes are
// only noted, and FlushCounts writes the file once at the end.
func (a *App) TrackCounts(counts func() any) {
	c := &a.dl().counts
	c.mu.Lock()
	c.f = counts
	c.mu.Unlock()
	a.Store.OnWrite = a.countsDirty
}

// FlushCounts writes CountsFile if the store changed since TrackCounts.
func (a *App) FlushCounts() {
	c := &a.dl().counts
	c.mu.Lock()
	dirty := c.dirty
	c.dirty = false
	c.mu.Unlock()
	if dirty {
		a.WriteCounts()
	}
}

func (a *App) countsDirty() {
	c := &a.dl().counts
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dirty = true
	if !c.live || c.pending || c.f == nil {
		return
	}
	c.pending = true
	time.AfterFunc(countsDelay, func() {
		c.mu.Lock()
		c.pending, c.dirty = false, false
		c.mu.Unlock()
		a.WriteCounts()
	})
}

// WriteCounts writes CountsFile now if the counts changed (an atomic
// rename: a reader never sees half a file).
func (a *App) WriteCounts() {
	c := &a.dl().counts
	c.mu.Lock()
	f := c.f
	c.mu.Unlock()
	if f == nil {
		return
	}
	b, err := json.Marshal(f())
	if err != nil {
		return
	}
	b = append(b, '\n')
	c.mu.Lock()
	defer c.mu.Unlock()
	// what is there, not what this process wrote last: the screen and
	// the CLI write it too
	path := a.CountsFile()
	if old, err := os.ReadFile(path); err == nil && string(old) == string(b) {
		return
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
