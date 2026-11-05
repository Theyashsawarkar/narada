package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Outbox statuses. Every send goes through here: "approval" waits for
// you, "queued" waits out the undo window, then one process claims it
// ("sending") and delivers it.
const (
	StatusApproval  = "approval"
	StatusQueued    = "queued"
	StatusSending   = "sending"
	StatusSent      = "sent"
	StatusCancelled = "cancelled"
	StatusDeclined  = "declined"
	StatusFailed    = "failed"
)

// statusQueuedAction is how a queued WhatsApp action (Outgoing.Action) is
// kept. narada before ADR 7 knows nothing of actions and would send one's
// Body as a plain message to real people, so a binary still running from
// before an upgrade must not see it as queued. It reads as StatusQueued
// everywhere in this version.
const statusQueuedAction = "queued-action"

// Outgoing is what is sent: a mail or a chat message.
type Outgoing struct {
	// mail
	To         []string `json:"to,omitempty"`
	Cc         []string `json:"cc,omitempty"`
	Bcc        []string `json:"bcc,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	ThreadID   string   `json:"thread_id,omitempty"`
	InReplyTo  string   `json:"in_reply_to,omitempty"`
	References string   `json:"references,omitempty"`
	DraftID    string   `json:"draft_id,omitempty"`
	// chat
	Space     string `json:"space,omitempty"`
	SpaceName string `json:"space_name,omitempty"`
	Thread    string `json:"thread,omitempty"`
	Quote     string `json:"quote,omitempty"` // the message this one replies to (quoting it)
	// WhatsApp: what the item does besides sending a message (ADR 7):
	// "edit" or "delete" (for everyone) the message Target, "forward"
	// Target here, "poll" or "vote" (below); "" sends Body
	Action string `json:"action,omitempty"`
	Target string `json:"target,omitempty"`
	// "poll" asks Poll; "vote" picks Poll.Options in the poll Target
	Poll *Poll `json:"poll,omitempty"`
	// "location" shares Location, "contact" Card
	Location *Location `json:"location,omitempty"`
	Card     *Card     `json:"contact,omitempty"`
	// "pin" pins Target for this long (0: unpins)
	PinSecs int `json:"pin_secs,omitempty"`
	// both
	Body  string `json:"body"`
	HTML  string `json:"html,omitempty"` // mail: sent beside Body when set
	Files []File `json:"files,omitempty"`
	// ScheduledFor (ms) holds it until then instead of the undo window
	ScheduledFor int64 `json:"scheduled_for,omitempty"`
	// AskFirst: an agent's send the user said to show them first
	AskFirst bool `json:"ask_first,omitempty"`
	// an agent's send: who it is (its MCP client name) and what the user
	// asked for, in their words; the approval shows both
	Agent string `json:"agent,omitempty"`
	Why   string `json:"why,omitempty"`
}

// Poll is a WhatsApp poll to ask, or (voting) the options picked.
type Poll struct {
	Question string   `json:"question,omitempty"`
	Options  []string `json:"options"`
	Multi    bool     `json:"multi,omitempty"`
}

// Location is a place to share on WhatsApp.
type Location struct {
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	Name string  `json:"name,omitempty"`
}

// Card is a contact card to share on WhatsApp.
type Card struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

// File is an attachment to send: a private copy made when it was queued
// (so what was approved is what goes out).
type File struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	Path string `json:"path"` // the private copy; "" until it is made
	// Source is where it came from, as typed (shown in the editor)
	Source string `json:"source,omitempty"`
	// Ref is a Gmail attachment to fetch instead of a local file
	// ("account/messageID/index"): a forwarded mail's attachments
	Ref string `json:"ref,omitempty"`
	// Voice: WhatsApp sends this audio file as a voice note
	Voice bool `json:"voice,omitempty"`
}

type OutboxItem struct {
	ID      int64    `json:"id"`
	Account string   `json:"account"`
	Kind    string   `json:"kind"` // mail | chat
	Out     Outgoing `json:"message"`
	Status  string   `json:"status"`
	SendAt  int64    `json:"send_at"`
	Created int64    `json:"created"`
	Origin  string   `json:"origin"` // you | agent
	Reason  string   `json:"reason,omitempty"`
	Error   string   `json:"error,omitempty"`
	Result  string   `json:"result,omitempty"` // the sent message's id
}

func (s *Store) AddOutbox(it OutboxItem) (int64, error) {
	payload, _ := json.Marshal(it.Out)
	status := it.Status
	if status == StatusQueued && it.Out.Action != "" {
		status = statusQueuedAction
	}
	res, err := s.db.Exec(`INSERT INTO outbox(account,kind,payload,status,send_at,created,origin,reason) VALUES(?,?,?,?,?,?,?,?)`,
		it.Account, it.Kind, string(payload), status, it.SendAt, now(), it.Origin, it.Reason)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const outboxCols = `id,account,kind,payload,status,send_at,created,origin,reason,error,result`

func scanOutbox(rows *sql.Rows) ([]OutboxItem, error) {
	defer rows.Close()
	var out []OutboxItem
	for rows.Next() {
		var it OutboxItem
		var payload string
		if err := rows.Scan(&it.ID, &it.Account, &it.Kind, &payload, &it.Status, &it.SendAt, &it.Created, &it.Origin, &it.Reason, &it.Error, &it.Result); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(payload), &it.Out)
		if it.Status == statusQueuedAction {
			it.Status = StatusQueued
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) OutboxItem(id int64) (OutboxItem, error) {
	rows, err := s.db.Query(`SELECT `+outboxCols+` FROM outbox WHERE id=?`, id)
	if err != nil {
		return OutboxItem{}, err
	}
	items, err := scanOutbox(rows)
	if err != nil {
		return OutboxItem{}, err
	}
	if len(items) == 0 {
		return OutboxItem{}, ErrNotFound
	}
	return items[0], nil
}

// Outbox lists items with one of the statuses (none: every item from the
// last week), newest first.
func (s *Store) Outbox(statuses ...string) ([]OutboxItem, error) {
	q := `SELECT ` + outboxCols + ` FROM outbox`
	var args []any
	if len(statuses) > 0 {
		q += ` WHERE status IN (`
		for i, st := range statuses {
			if i > 0 {
				q += ","
			}
			q += "?"
			args = append(args, st)
			if st == StatusQueued {
				q += ",?"
				args = append(args, statusQueuedAction)
			}
		}
		q += `)`
	} else {
		q += ` WHERE created > ?`
		args = append(args, time.Now().Add(-7*24*time.Hour).UnixMilli())
	}
	q += ` ORDER BY id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scanOutbox(rows)
}

// Transition moves an item from one status to another; false when it was
// no longer in `from` (someone else got there first).
func (s *Store) Transition(id int64, from, to string, sendAt int64) (bool, error) {
	also := from
	if from == StatusQueued {
		also = statusQueuedAction
	}
	res, err := s.db.Exec(`UPDATE outbox SET status=CASE WHEN ?=? AND json_extract(payload,'$.action') IS NOT NULL THEN ? ELSE ? END,
	  send_at=CASE WHEN ?>0 THEN ? ELSE send_at END WHERE id=? AND status IN (?,?)`,
		to, StatusQueued, statusQueuedAction, to, sendAt, sendAt, id, from, also)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) UpdateOutgoing(id int64, out Outgoing) error {
	payload, _ := json.Marshal(out)
	_, err := s.db.Exec(`UPDATE outbox SET payload=? WHERE id=?`, string(payload), id)
	return err
}

func (s *Store) FinishOutbox(id int64, status, errMsg, result string) error {
	_, err := s.db.Exec(`UPDATE outbox SET status=?, error=?, result=? WHERE id=?`, status, errMsg, result, id)
	return err
}

// DueOutbox: queued items whose undo window is over.
func (s *Store) DueOutbox() ([]OutboxItem, error) {
	rows, err := s.db.Query(`SELECT `+outboxCols+` FROM outbox WHERE status IN (?,?) AND send_at<=?`, StatusQueued, statusQueuedAction, now())
	if err != nil {
		return nil, err
	}
	return scanOutbox(rows)
}

// RecoverSending puts items stuck in "sending" (a process died mid-send)
// older than a minute back to failed, so they are visible, not lost. Every
// process calls it, so a WhatsApp file (up to 2 GB, uploading for a while)
// gets two hours before it counts as stuck.
func (s *Store) RecoverSending() error {
	_, err := s.db.Exec(`UPDATE outbox SET status=?, error='interrupted while sending; check Sent before retrying' WHERE status=? AND send_at < ?
	  AND (kind<>'whatsapp' OR json_array_length(payload,'$.files') IS NULL OR json_array_length(payload,'$.files')=0 OR send_at < ?)`,
		StatusFailed, StatusSending, time.Now().Add(-time.Minute).UnixMilli(), time.Now().Add(-2*time.Hour).UnixMilli())
	return err
}

// Reschedule moves a waiting send to another time (and its payload too).
func (s *Store) Reschedule(id int64, out Outgoing, at int64) (bool, error) {
	payload, _ := json.Marshal(out)
	res, err := s.db.Exec(`UPDATE outbox SET send_at=?, payload=? WHERE id=? AND status IN (?,?)`, at, string(payload), id, StatusQueued, statusQueuedAction)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// SentSince counts sends of one kind queued since t that weren't called
// off (cancelled or declined).
func (s *Store) SentSince(kind string, t time.Time) int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE kind=? AND created>=? AND status NOT IN (?,?)`, kind, t.UnixMilli(), StatusCancelled, StatusDeclined).Scan(&n)
	return n
}
