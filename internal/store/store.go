// Package store is narada's local copy of mail and chat: one SQLite
// database (WAL, so the daemon, the screen, the CLI and the MCP server
// can all use it at once) with full-text search over mail.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	path string
}

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
  name TEXT PRIMARY KEY, email TEXT NOT NULL DEFAULT '', chat INTEGER NOT NULL DEFAULT 1,
  history_id TEXT NOT NULL DEFAULT '', chat_user TEXT NOT NULL DEFAULT '',
  mail_synced INTEGER NOT NULL DEFAULT 0, chat_synced INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '', position INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS threads (
  account TEXT NOT NULL, id TEXT NOT NULL, subject TEXT NOT NULL DEFAULT '', snippet TEXT NOT NULL DEFAULT '',
  last_date INTEGER NOT NULL DEFAULT 0, labels TEXT NOT NULL DEFAULT ',', participants TEXT NOT NULL DEFAULT '',
  last_from TEXT NOT NULL DEFAULT '', msg_count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (account, id));
CREATE INDEX IF NOT EXISTS threads_date ON threads(account, last_date DESC);
CREATE TABLE IF NOT EXISTS messages (
  account TEXT NOT NULL, id TEXT NOT NULL, thread_id TEXT NOT NULL, from_addr TEXT NOT NULL DEFAULT '',
  from_name TEXT NOT NULL DEFAULT '', to_addrs TEXT NOT NULL DEFAULT '', cc_addrs TEXT NOT NULL DEFAULT '',
  reply_to TEXT NOT NULL DEFAULT '', subject TEXT NOT NULL DEFAULT '', date INTEGER NOT NULL DEFAULT 0,
  labels TEXT NOT NULL DEFAULT ',', body TEXT NOT NULL DEFAULT '', message_id TEXT NOT NULL DEFAULT '',
  refs TEXT NOT NULL DEFAULT '', attachments TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY (account, id));
CREATE INDEX IF NOT EXISTS messages_thread ON messages(account, thread_id, date);
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(account UNINDEXED, id UNINDEXED, thread_id UNINDEXED, subject, from_text, body);
CREATE TABLE IF NOT EXISTS section_threads (account TEXT NOT NULL, section TEXT NOT NULL, thread_id TEXT NOT NULL,
  PRIMARY KEY (account, section, thread_id));
CREATE TABLE IF NOT EXISTS contacts (account TEXT NOT NULL, addr TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
  sent INTEGER NOT NULL DEFAULT 0, received INTEGER NOT NULL DEFAULT 0, last INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (account, addr));
CREATE TABLE IF NOT EXISTS labels (account TEXT NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, type TEXT NOT NULL DEFAULT 'user',
  PRIMARY KEY (account, id));
CREATE TABLE IF NOT EXISTS spaces (
  account TEXT NOT NULL, name TEXT NOT NULL, display_name TEXT NOT NULL DEFAULT '', type TEXT NOT NULL DEFAULT '',
  last_active INTEGER NOT NULL DEFAULT 0, last_read INTEGER NOT NULL DEFAULT 0, synced_to INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (account, name));
CREATE TABLE IF NOT EXISTS chat_messages (
  account TEXT NOT NULL, name TEXT NOT NULL, space TEXT NOT NULL, thread TEXT NOT NULL DEFAULT '',
  sender TEXT NOT NULL DEFAULT '', sender_name TEXT NOT NULL DEFAULT '', text TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (account, name));
CREATE INDEX IF NOT EXISTS chat_space ON chat_messages(account, space, created);
CREATE TABLE IF NOT EXISTS users (account TEXT NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', email TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (account, id));
CREATE TABLE IF NOT EXISTS outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT, account TEXT NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL,
  status TEXT NOT NULL, send_at INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL, origin TEXT NOT NULL DEFAULT 'you',
  reason TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS notified (account TEXT NOT NULL, key TEXT NOT NULL, PRIMARY KEY (account, key));
CREATE TABLE IF NOT EXISTS whatsapp_reactions (space TEXT NOT NULL, msg TEXT NOT NULL, reactor TEXT NOT NULL, emoji TEXT NOT NULL,
  sender TEXT NOT NULL DEFAULT '', pending INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (space, msg, reactor));
CREATE TABLE IF NOT EXISTS whatsapp_members (space TEXT NOT NULL, jid TEXT NOT NULL, PRIMARY KEY (space, jid));
CREATE TABLE IF NOT EXISTS whatsapp_reads (space TEXT NOT NULL, msg TEXT NOT NULL, sender TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL,
  PRIMARY KEY (space, msg));
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	// the local copy is your mail: readable by you only
	_ = os.Chmod(path, 0o600)
	for _, x := range []string{"-wal", "-shm"} {
		_ = os.Chmod(path+x, 0o600)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("upgrading %s: %w", path, err)
	}
	return &Store{db: db, path: path}, nil
}

// schemaVersion: bump it with a step in migrate.
const schemaVersion = 16

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v < 2 {
		// v2 keeps each message's HTML for the formatted preview; mail
		// synced before has none, so the next sync fetches it again
		if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN body_html TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
		if _, err := db.Exec(`UPDATE accounts SET history_id=''`); err != nil {
			return err
		}
	}
	if v < 3 {
		// v3 stops keeping "user 1234" placeholders as names: forget them
		// (and the DM titles built from them) so they are looked up again
		if _, err := db.Exec(`DELETE FROM users WHERE name LIKE 'user %' OR name=''`); err != nil {
			return err
		}
		if _, err := db.Exec(`UPDATE spaces SET display_name='' WHERE type<>'SPACE' AND display_name LIKE '%user %'`); err != nil {
			return err
		}
		if _, err := db.Exec(`UPDATE chat_messages SET text='(a message with no text)' WHERE text=''`); err != nil {
			return err
		}
	}
	if v < 4 {
		// v4: when each thread was fetched, so an interrupted first sync
		// resumes instead of starting over
		if _, err := db.Exec(`ALTER TABLE threads ADD COLUMN fetched INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if v < 5 {
		// v5: messages stored empty (attachments, cards) are fetched again
		if _, err := db.Exec(`UPDATE spaces SET synced_to=0 WHERE name IN (SELECT DISTINCT space FROM chat_messages WHERE text='(a message with no text)')`); err != nil {
			return err
		}
		if _, err := db.Exec(`DELETE FROM chat_messages WHERE text='(a message with no text)'`); err != nil {
			return err
		}
	}
	if v < 6 {
		// v6: chat messages keep their files, so they can be downloaded;
		// spaces with files in them are fetched again to fill it in
		if _, err := db.Exec(`ALTER TABLE chat_messages ADD COLUMN files TEXT NOT NULL DEFAULT '[]'`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
		if _, err := db.Exec(`UPDATE spaces SET synced_to=0 WHERE name IN (SELECT DISTINCT space FROM chat_messages WHERE text LIKE '%📎%')`); err != nil {
			return err
		}
	}
	if v < 7 {
		// v7: lookups that failed (a person's name, a DM's title), so they
		// aren't retried on every sync
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS tries (account TEXT NOT NULL, key TEXT NOT NULL, at INTEGER NOT NULL, PRIMARY KEY (account, key))`); err != nil {
			return err
		}
	}
	if v < 8 {
		// v8: the backfill of older mail, page by page
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS backfill (account TEXT PRIMARY KEY, page TEXT NOT NULL DEFAULT '',
		  done INTEGER NOT NULL DEFAULT 0, total INTEGER NOT NULL DEFAULT 0, query TEXT NOT NULL DEFAULT '')`); err != nil {
			return err
		}
	}
	if v < 9 {
		// v9: your own name for a chat (only on this machine)
		if _, err := db.Exec(`ALTER TABLE spaces ADD COLUMN alias TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if v < 10 {
		// v10: quote-replies and whether a space uses threads; recent
		// conversations are fetched again to fill them in
		for _, q := range []string{
			`ALTER TABLE chat_messages ADD COLUMN quoted TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE spaces ADD COLUMN threaded INTEGER NOT NULL DEFAULT 0`,
		} {
			if _, err := db.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column") {
				return err
			}
		}
		if _, err := db.Exec(`UPDATE spaces SET synced_to=0 WHERE last_active > ?`, time.Now().AddDate(0, 0, -14).UnixMilli()); err != nil {
			return err
		}
	}
	if v < 11 {
		// v11: reactions and edits on chat messages; spaces you pinned
		for _, q := range []string{
			`ALTER TABLE chat_messages ADD COLUMN reactions TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE chat_messages ADD COLUMN edited INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE spaces ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`,
		} {
			if _, err := db.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column") {
				return err
			}
		}
		if _, err := db.Exec(`UPDATE spaces SET synced_to=0 WHERE last_active > ?`, time.Now().AddDate(0, 0, -7).UnixMilli()); err != nil {
			return err
		}
	}
	if v < 12 {
		// v12: accounts have a kind: google (mail and chat) or whatsapp
		if _, err := db.Exec(`ALTER TABLE accounts ADD COLUMN kind TEXT NOT NULL DEFAULT 'google'`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if v < 13 {
		// v13: WhatsApp files asked for by a process that isn't online:
		// the one holding WhatsApp downloads them
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS media_fetch (account TEXT NOT NULL, msg TEXT NOT NULL, idx INTEGER NOT NULL,
		  asked INTEGER NOT NULL, error TEXT NOT NULL DEFAULT '', PRIMARY KEY (account, msg, idx))`); err != nil {
			return err
		}
	}
	if v < 14 {
		// v14: WhatsApp's ticks on messages you sent (1 sent, 2 delivered, 3 read)
		if _, err := db.Exec(`ALTER TABLE chat_messages ADD COLUMN ticks INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if v < 15 {
		// v15: how many people are in a chat (a group, a space), for the
		// approval of a send to it
		if _, err := db.Exec(`ALTER TABLE spaces ADD COLUMN members INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if v < 16 {
		// v16: WhatsApp chat state (archived, muted until, ms, -1 for
		// ever), starred messages, and the queue of WhatsApp actions the
		// process holding the connection does (ADR 7)
		for _, q := range []string{
			`ALTER TABLE spaces ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE spaces ADD COLUMN muted_until INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE chat_messages ADD COLUMN starred INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE spaces ADD COLUMN blocked INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE spaces ADD COLUMN disappear INTEGER NOT NULL DEFAULT 0`,
			`CREATE TABLE IF NOT EXISTS whatsapp_polls (msg TEXT PRIMARY KEY, question TEXT NOT NULL, options TEXT NOT NULL, multi INTEGER NOT NULL DEFAULT 0)`,
			`CREATE TABLE IF NOT EXISTS whatsapp_votes (msg TEXT NOT NULL, voter TEXT NOT NULL, options TEXT NOT NULL, PRIMARY KEY (msg, voter))`,
			`CREATE TABLE IF NOT EXISTS whatsapp_presence (jid TEXT PRIMARY KEY, online INTEGER NOT NULL DEFAULT 0,
			  last_seen INTEGER NOT NULL DEFAULT 0, at INTEGER NOT NULL DEFAULT 0, typing_in TEXT NOT NULL DEFAULT '',
			  typing TEXT NOT NULL DEFAULT '', typing_at INTEGER NOT NULL DEFAULT 0)`,
			`CREATE TABLE IF NOT EXISTS whatsapp_actions (id INTEGER PRIMARY KEY AUTOINCREMENT, action TEXT NOT NULL, chat TEXT NOT NULL,
			  arg TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL, done INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
			  result TEXT NOT NULL DEFAULT '')`,
		} {
			if _, err := db.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column") {
				return err
			}
		}
	}
	_, err := db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion))
	return err
}

func (s *Store) Close() error { return s.db.Close() }

func now() int64 { return time.Now().UnixMilli() }

// ---------------------------------------------------------------- accounts

type Account struct {
	Name       string `json:"name"`
	Email      string `json:"email"`
	Chat       bool   `json:"chat"`
	HistoryID  string `json:"-"`
	ChatUser   string `json:"-"`
	MailSynced int64  `json:"mail_synced"`
	ChatSynced int64  `json:"chat_synced"`
	LastError  string `json:"last_error,omitempty"`
}

// Accounts are the Google accounts (WhatsApp is apart: WhatsApp()).
func (s *Store) Accounts() ([]Account, error) { return s.accounts(KindGoogle) }

func (s *Store) accounts(kind string) ([]Account, error) {
	rows, err := s.db.Query(`SELECT name,email,chat,history_id,chat_user,mail_synced,chat_synced,last_error FROM accounts WHERE kind=? ORDER BY position, name`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.Name, &a.Email, &a.Chat, &a.HistoryID, &a.ChatUser, &a.MailSynced, &a.ChatSynced, &a.LastError); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Account kinds.
const (
	KindGoogle   = "google"
	KindWhatsApp = "whatsapp"
)

// WhatsAppAccount is the account name WhatsApp's chats are kept under.
const WhatsAppAccount = "whatsapp"

// WhatsApp is the linked WhatsApp account; ok is false before linking.
// Email holds the phone number, ChatUser your own JID.
func (s *Store) WhatsApp() (acc Account, ok bool) {
	all, err := s.accounts(KindWhatsApp)
	if err != nil || len(all) == 0 {
		return Account{}, false
	}
	return all[0], true
}

// AddWhatsApp records the linked WhatsApp account (me: your JID).
func (s *Store) AddWhatsApp(phone, me string) error {
	_, err := s.db.Exec(`INSERT INTO accounts(name,email,chat,chat_user,kind,position) VALUES(?,?,1,?,?,1000)
	  ON CONFLICT(name) DO UPDATE SET email=excluded.email, chat_user=excluded.chat_user, kind=excluded.kind, last_error=''`, WhatsAppAccount, phone, me, KindWhatsApp)
	return err
}

func (s *Store) Account(name string) (Account, error) {
	all, err := s.Accounts()
	if err != nil {
		return Account{}, err
	}
	if wa, ok := s.WhatsApp(); ok {
		all = append(all, wa)
	}
	for _, a := range all {
		if a.Name == name {
			return a, nil
		}
	}
	return Account{}, fmt.Errorf("no account %q (narada auth list)", name)
}

func (s *Store) AddAccount(a Account) error {
	_, err := s.db.Exec(`INSERT INTO accounts(name,email,chat,position) VALUES(?,?,?,(SELECT COALESCE(MAX(position),0)+1 FROM accounts))
	  ON CONFLICT(name) DO UPDATE SET email=excluded.email, chat=excluded.chat`, a.Name, a.Email, a.Chat)
	return err
}

func (s *Store) RemoveAccount(name string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range []string{"accounts WHERE name=?", "threads WHERE account=?", "messages WHERE account=?", "messages_fts WHERE account=?",
		"section_threads WHERE account=?", "contacts WHERE account=?", "labels WHERE account=?", "spaces WHERE account=?",
		"chat_messages WHERE account=?", "users WHERE account=?", "notified WHERE account=?"} {
		if _, err := tx.Exec("DELETE FROM "+t, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SetHistory(account, historyID string) error {
	_, err := s.db.Exec(`UPDATE accounts SET history_id=?, mail_synced=?, last_error='' WHERE name=?`, historyID, now(), account)
	return err
}

func (s *Store) SetChatSynced(account, chatUser string) error {
	_, err := s.db.Exec(`UPDATE accounts SET chat_synced=?, chat_user=CASE WHEN ?='' THEN chat_user ELSE ? END WHERE name=?`, now(), chatUser, chatUser, account)
	return err
}

func (s *Store) SetError(account, msg string) error {
	_, err := s.db.Exec(`UPDATE accounts SET last_error=? WHERE name=?`, msg, account)
	return err
}

// ------------------------------------------------------------------- mail

type Attachment struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int    `json:"size"`
}

type Message struct {
	Account     string       `json:"account"`
	ID          string       `json:"id"`
	ThreadID    string       `json:"thread_id"`
	FromAddr    string       `json:"from"`
	FromName    string       `json:"from_name"`
	To          string       `json:"to"`
	Cc          string       `json:"cc,omitempty"`
	ReplyTo     string       `json:"reply_to,omitempty"`
	Subject     string       `json:"subject"`
	Date        int64        `json:"date"`
	Labels      []string     `json:"labels"`
	Body        string       `json:"body"`
	HTML        string       `json:"-"`
	MessageID   string       `json:"-"`
	References  string       `json:"-"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type Thread struct {
	Account      string   `json:"account"`
	ID           string   `json:"id"`
	Subject      string   `json:"subject"`
	Snippet      string   `json:"snippet"`
	LastDate     int64    `json:"last_date"`
	Labels       []string `json:"labels"`
	Participants string   `json:"participants"`
	LastFrom     string   `json:"last_from"`
	Count        int      `json:"messages"`
}

func (t Thread) Has(label string) bool {
	for _, l := range t.Labels {
		if l == label {
			return true
		}
	}
	return false
}
func (t Thread) Unread() bool  { return t.Has("UNREAD") }
func (t Thread) Starred() bool { return t.Has("STARRED") }

func joinLabels(l []string) string { return "," + strings.Join(l, ",") + "," }
func splitLabels(s string) []string {
	s = strings.Trim(s, ",")
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// PutThread replaces a thread and all its messages.
func (s *Store) PutThread(account string, t Thread, msgs []Message) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteThreadTx(tx, account, t.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO threads(account,id,subject,snippet,last_date,labels,participants,last_from,msg_count,fetched) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		account, t.ID, t.Subject, t.Snippet, t.LastDate, joinLabels(t.Labels), t.Participants, t.LastFrom, len(msgs), now()); err != nil {
		return err
	}
	for _, m := range msgs {
		att, _ := json.Marshal(m.Attachments)
		if _, err := tx.Exec(`INSERT INTO messages(account,id,thread_id,from_addr,from_name,to_addrs,cc_addrs,reply_to,subject,date,labels,body,message_id,refs,attachments,body_html)
		  VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, account, m.ID, t.ID, m.FromAddr, m.FromName, m.To, m.Cc, m.ReplyTo, m.Subject, m.Date,
			joinLabels(m.Labels), m.Body, m.MessageID, m.References, string(att), m.HTML); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO messages_fts(account,id,thread_id,subject,from_text,body) VALUES(?,?,?,?,?,?)`,
			account, m.ID, t.ID, m.Subject, m.FromName+" "+m.FromAddr, m.Body); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func deleteThreadTx(tx *sql.Tx, account, id string) error {
	for _, q := range []string{`DELETE FROM threads WHERE account=? AND id=?`, `DELETE FROM messages WHERE account=? AND thread_id=?`,
		`DELETE FROM messages_fts WHERE account=? AND thread_id=?`} {
		if _, err := tx.Exec(q, account, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteThread(account, id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteThreadTx(tx, account, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM section_threads WHERE account=? AND thread_id=?`, account, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetThreadLabels changes labels locally, before (or instead of) the next
// sync, so the screen reacts at once.
func (s *Store) SetThreadLabels(account, id string, add, remove []string) error {
	t, err := s.Thread(account, id)
	if err != nil {
		return err
	}
	set := map[string]bool{}
	for _, l := range t.Labels {
		set[l] = true
	}
	for _, l := range add {
		set[l] = true
	}
	for _, l := range remove {
		delete(set, l)
	}
	var labels []string
	for l := range set {
		labels = append(labels, l)
	}
	_, err = s.db.Exec(`UPDATE threads SET labels=? WHERE account=? AND id=?`, joinLabels(labels), account, id)
	return err
}

const threadCols = `account,id,subject,snippet,last_date,labels,participants,last_from,msg_count`

func scanThreads(rows *sql.Rows) ([]Thread, error) {
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		var t Thread
		var labels string
		if err := rows.Scan(&t.Account, &t.ID, &t.Subject, &t.Snippet, &t.LastDate, &labels, &t.Participants, &t.LastFrom, &t.Count); err != nil {
			return nil, err
		}
		t.Labels = splitLabels(labels)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Thread(account, id string) (Thread, error) {
	rows, err := s.db.Query(`SELECT `+threadCols+` FROM threads WHERE account=? AND id=?`, account, id)
	if err != nil {
		return Thread{}, err
	}
	ts, err := scanThreads(rows)
	if err != nil {
		return Thread{}, err
	}
	if len(ts) == 0 {
		return Thread{}, ErrNotFound
	}
	return ts[0], nil
}

// FindThread looks a thread id up across accounts (ids are unique enough).
func (s *Store) FindThread(id string) (Thread, error) {
	rows, err := s.db.Query(`SELECT `+threadCols+` FROM threads WHERE id=? LIMIT 1`, id)
	if err != nil {
		return Thread{}, err
	}
	ts, err := scanThreads(rows)
	if err != nil {
		return Thread{}, err
	}
	if len(ts) == 0 {
		return Thread{}, fmt.Errorf("no thread %q in the local copy (try narada sync)", id)
	}
	return ts[0], nil
}

var ErrNotFound = errors.New("not found")

type ThreadQuery struct {
	Accounts []string // empty: all
	Label    string
	Section  string // section name with a server query
	Since    int64  // last_date >= Since (ms)
	Unread   bool
	Text     string // local full-text search
	Limit    int
}

func (s *Store) Threads(q ThreadQuery) ([]Thread, error) {
	var where []string
	var args []any
	if len(q.Accounts) > 0 {
		where = append(where, "t.account IN ("+strings.TrimSuffix(strings.Repeat("?,", len(q.Accounts)), ",")+")")
		for _, a := range q.Accounts {
			args = append(args, a)
		}
	}
	if q.Label != "" {
		where = append(where, "t.labels LIKE ?")
		args = append(args, "%,"+q.Label+",%")
	}
	if q.Section != "" {
		where = append(where, "EXISTS (SELECT 1 FROM section_threads st WHERE st.account=t.account AND st.thread_id=t.id AND st.section=?)")
		args = append(args, q.Section)
	}
	if q.Since > 0 {
		where = append(where, "t.last_date >= ?")
		args = append(args, q.Since)
	}
	if q.Unread {
		where = append(where, "t.labels LIKE '%,UNREAD,%'")
	}
	if q.Text != "" {
		// one lookup in the index, not one per thread (that took seconds)
		where = append(where, "(t.account, t.id) IN (SELECT account, thread_id FROM messages_fts WHERE messages_fts MATCH ?)")
		args = append(args, ftsQuery(q.Text))
	}
	sqlq := `SELECT ` + strings.ReplaceAll("t."+strings.ReplaceAll(threadCols, ",", ",t."), " ", "") + ` FROM threads t`
	if len(where) > 0 {
		sqlq += " WHERE " + strings.Join(where, " AND ")
	}
	sqlq += " ORDER BY t.last_date DESC"
	if q.Limit > 0 {
		sqlq += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	rows, err := s.db.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	return scanThreads(rows)
}

// ftsQuery turns free words into an FTS5 query: every word must match,
// as a prefix, so "relea priya" finds "release" from Priya.
func ftsQuery(text string) string {
	var parts []string
	for _, w := range strings.Fields(text) {
		w = strings.Map(func(r rune) rune {
			if r == '"' {
				return -1
			}
			return r
		}, w)
		if w != "" {
			parts = append(parts, `"`+w+`"*`)
		}
	}
	return strings.Join(parts, " ")
}

func (s *Store) Messages(account, threadID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT account,id,thread_id,from_addr,from_name,to_addrs,cc_addrs,reply_to,subject,date,labels,body,message_id,refs,attachments,body_html
	  FROM messages WHERE account=? AND thread_id=? ORDER BY date`, account, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var labels, att string
		if err := rows.Scan(&m.Account, &m.ID, &m.ThreadID, &m.FromAddr, &m.FromName, &m.To, &m.Cc, &m.ReplyTo, &m.Subject, &m.Date, &labels,
			&m.Body, &m.MessageID, &m.References, &att, &m.HTML); err != nil {
			return nil, err
		}
		m.Labels = splitLabels(labels)
		_ = json.Unmarshal([]byte(att), &m.Attachments)
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddSectionThreads adds threads to a search section without replacing it.
func (s *Store) AddSectionThreads(account, section string, ids []string) error {
	for _, id := range ids {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO section_threads(account,section,thread_id) VALUES(?,?,?)`, account, section, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SetSectionThreads(account, section string, ids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM section_threads WHERE account=? AND section=?`, account, section); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO section_threads(account,section,thread_id) VALUES(?,?,?)`, account, section, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FetchedSince reports whether the thread was fetched after `since` (ms).
func (s *Store) FetchedSince(account, id string, since int64) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM threads WHERE account=? AND id=? AND fetched>?`, account, id, since).Scan(&n)
	return n > 0
}

// Backfill is how far the download of older mail has come.
type Backfill struct {
	Page  string // the next page of thread ids to look at
	Done  bool
	Total int    // threads in the mailbox (Gmail's count)
	Query string // what it covers ("" = everything); a new one restarts it
	Have  int    // threads in the local copy
}

func (s *Store) Backfill(account string) Backfill {
	var b Backfill
	_ = s.db.QueryRow(`SELECT page,done,total,query FROM backfill WHERE account=?`, account).Scan(&b.Page, &b.Done, &b.Total, &b.Query)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM threads WHERE account=?`, account).Scan(&b.Have)
	return b
}

func (s *Store) SetBackfill(account string, b Backfill) error {
	_, err := s.db.Exec(`INSERT INTO backfill(account,page,done,total,query) VALUES(?,?,?,?,?)
	  ON CONFLICT(account) DO UPDATE SET page=excluded.page, done=excluded.done, total=excluded.total, query=excluded.query`,
		account, b.Page, b.Done, b.Total, b.Query)
	return err
}

// SentStatus is what happened after your last message in a thread.
type SentStatus struct {
	To        string // who your last message went to
	SentAt    int64
	ReplyFrom string // the first answer after it ("" = none yet)
	ReplyAt   int64
}

// SentStatuses looks up, for each thread, your last message and the first
// one from someone else after it: one query, no bodies.
func (s *Store) SentStatuses(account string, ids []string) map[string]SentStatus {
	out := map[string]SentStatus{}
	if len(ids) == 0 {
		return out
	}
	args := []any{account}
	ph := make([]string, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args = append(args, id)
	}
	rows, err := s.db.Query(`WITH mine AS (
	    SELECT thread_id, MAX(date) AS at FROM messages WHERE account=?1 AND labels LIKE '%,SENT,%' AND thread_id IN (`+strings.Join(ph, ",")+`)
	    GROUP BY thread_id)
	  SELECT m.thread_id, m.at,
	    (SELECT to_addrs FROM messages x WHERE x.account=?1 AND x.thread_id=m.thread_id AND x.date=m.at LIMIT 1),
	    COALESCE((SELECT CASE WHEN from_name<>'' THEN from_name ELSE from_addr END || char(9) || date FROM messages y
	      WHERE y.account=?1 AND y.thread_id=m.thread_id AND y.date>m.at AND y.labels NOT LIKE '%,SENT,%' ORDER BY y.date LIMIT 1), '')
	  FROM mine m`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, to, reply string
		var st SentStatus
		if rows.Scan(&id, &st.SentAt, &to, &reply) != nil {
			continue
		}
		st.To = to
		if name, at, ok := strings.Cut(reply, "\t"); ok {
			st.ReplyFrom = name
			fmt.Sscan(at, &st.ReplyAt)
		}
		out[id] = st
	}
	return out
}

// HasThread reports whether the thread is cached with this many messages.
func (s *Store) HasThread(account, id string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM threads WHERE account=? AND id=?`, account, id).Scan(&n)
	return n > 0
}

// --------------------------------------------------------------- contacts

type Contact struct {
	Addr     string `json:"email"`
	Name     string `json:"name"`
	Sent     int    `json:"times_you_wrote"`
	Received int    `json:"times_they_wrote"`
	Last     int64  `json:"last"`
}

// NoteContact counts an address seen on mail. sent=true: you wrote to it.
func (s *Store) NoteContact(account, addr, name string, sent bool, when int64) error {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if addr == "" {
		return nil
	}
	sentN, recvN := 0, 1
	if sent {
		sentN, recvN = 1, 0
	}
	_, err := s.db.Exec(`INSERT INTO contacts(account,addr,name,sent,received,last) VALUES(?,?,?,?,?,?)
	  ON CONFLICT(account,addr) DO UPDATE SET name=CASE WHEN excluded.name<>'' THEN excluded.name ELSE name END,
	  sent=sent+excluded.sent, received=received+excluded.received, last=MAX(last,excluded.last)`, account, addr, name, sentN, recvN, when)
	return err
}

func (s *Store) Contacts(account, query string, limit int) ([]Contact, error) {
	if limit <= 0 {
		limit = 10
	}
	like := "%" + strings.ToLower(query) + "%"
	rows, err := s.db.Query(`SELECT addr,name,sent,received,last FROM contacts WHERE account=? AND (addr LIKE ? OR LOWER(name) LIKE ?)
	  ORDER BY sent DESC, received DESC, last DESC LIMIT ?`, account, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.Addr, &c.Name, &c.Sent, &c.Received, &c.Last); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SuggestContacts ranks contacts for autocomplete: a name word or the
// address starting with what was typed beats a match inside it; then
// people you write to, then people who write to you, then recent ones.
func (s *Store) SuggestContacts(account, query string, limit int, skip map[string]bool) ([]Contact, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	all, err := s.Contacts(account, q, 300)
	if err != nil {
		return nil, err
	}
	type scored struct {
		c Contact
		s float64
	}
	var list []scored
	for _, c := range all {
		if skip[strings.ToLower(c.Addr)] || strings.HasPrefix(strings.ToLower(c.Addr), "noreply") || strings.Contains(strings.ToLower(c.Addr), "no-reply") {
			continue
		}
		sc := 0.0
		name, addr := strings.ToLower(c.Name), strings.ToLower(c.Addr)
		switch {
		case q == "":
		case strings.HasPrefix(name, q) || strings.HasPrefix(addr, q):
			sc += 30
		case strings.Contains(" "+name, " "+q) || strings.Contains(addr, "."+q):
			sc += 20
		default:
			sc += 5
		}
		sc += 8*math.Log1p(float64(c.Sent)) + 2*math.Log1p(float64(c.Received))
		if days := time.Since(time.UnixMilli(c.Last)).Hours() / 24; c.Last > 0 && days < 90 {
			sc += 4 * (1 - days/90)
		}
		list = append(list, scored{c, sc})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].s > list[j].s })
	var out []Contact
	for _, x := range list {
		if len(out) == limit {
			break
		}
		out = append(out, x.c)
	}
	return out, nil
}

// PutContact remembers someone from the directory (no mail counted).
func (s *Store) PutContact(account, addr, name string) error {
	addr = strings.ToLower(strings.TrimSpace(addr))
	_, err := s.db.Exec(`INSERT INTO contacts(account,addr,name) VALUES(?,?,?)
	  ON CONFLICT(account,addr) DO UPDATE SET name=CASE WHEN contacts.name='' THEN excluded.name ELSE contacts.name END`, account, addr, name)
	return err
}

// KnownContact: you have written to this address before.
func (s *Store) KnownContact(account, addr string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT sent FROM contacts WHERE account=? AND addr=?`, account, strings.ToLower(strings.TrimSpace(addr))).Scan(&n)
	return n > 0
}

func (s *Store) ResetContacts(account string) error {
	_, err := s.db.Exec(`DELETE FROM contacts WHERE account=?`, account)
	return err
}

// ----------------------------------------------------------------- labels

type Label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func (s *Store) PutLabels(account string, labels []Label) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM labels WHERE account=?`, account); err != nil {
		return err
	}
	for _, l := range labels {
		if _, err := tx.Exec(`INSERT INTO labels(account,id,name,type) VALUES(?,?,?,?)`, account, l.ID, l.Name, l.Type); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Labels(account string) ([]Label, error) {
	rows, err := s.db.Query(`SELECT id,name,type FROM labels WHERE account=? ORDER BY type DESC, name`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Label
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.ID, &l.Name, &l.Type); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ------------------------------------------------------------------- chat

// TypeStatus is the WhatsApp space that holds people's status updates.
const TypeStatus = "STATUS"

type Space struct {
	Account     string `json:"account"`
	Name        string `json:"id"`
	DisplayName string `json:"name"`
	Alias       string `json:"alias,omitempty"`       // your own name for it, on this machine
	Threaded    bool   `json:"threaded,omitempty"`    // replies go in threads (else inline)
	Pinned      bool   `json:"pinned,omitempty"`      // kept on top of the list (on this machine; WhatsApp: everywhere)
	Archived    bool   `json:"archived,omitempty"`    // WhatsApp: out of the list, in Archived
	MutedUntil  int64  `json:"muted_until,omitempty"` // WhatsApp: no notifications until (ms; -1 for ever)
	Blocked     bool   `json:"blocked,omitempty"`     // WhatsApp: you blocked them
	Disappear   int    `json:"disappear,omitempty"`   // WhatsApp: messages disappear after this many seconds
	Type        string `json:"type"`                  // SPACE, GROUP_CHAT, DIRECT_MESSAGE
	Members     int    `json:"members,omitempty"`     // people in it, when known
	LastActive  int64  `json:"last_active"`
	LastRead    int64  `json:"-"`
	SyncedTo    int64  `json:"-"`
	Unread      int    `json:"unread"`
	LastText    string `json:"last_message"`
	LastSender  string `json:"last_sender"`
}

type ChatMessage struct {
	Account    string     `json:"-"`
	Name       string     `json:"id"`
	Space      string     `json:"space"`
	Thread     string     `json:"thread,omitempty"`
	Sender     string     `json:"-"`
	SenderName string     `json:"from"`
	Text       string     `json:"text"`
	Created    int64      `json:"time"`
	Mine       bool       `json:"mine,omitempty"`
	Files      []ChatFile `json:"files,omitempty"`
	Quoted     string     `json:"quoted,omitempty"`    // the message this one quotes (a reply)
	Reactions  string     `json:"reactions,omitempty"` // "👍 2 · 😂 1"
	Edited     bool       `json:"edited,omitempty"`
	Ticks      int        `json:"ticks,omitempty"` // WhatsApp, yours: 1 sent, 2 delivered, 3 read
	Starred    bool       `json:"starred,omitempty"`
}

// Ticks of a message of yours that only exists here so far: it shows in
// the chat the moment you send it, and the real one replaces it.
const (
	TickPending = -1 // on its way
	TickFailed  = -2 // didn't go out
)

const pendingPrefix = "pending/"

// PendingName is the local name of an outbox item's message until it's sent.
func PendingName(outboxID int64) string { return pendingPrefix + strconv.FormatInt(outboxID, 10) }

// IsPending: this message only exists here so far.
func IsPending(name string) bool { return strings.HasPrefix(name, pendingPrefix) }

// PendingOutboxID is the outbox item behind a pending message.
func PendingOutboxID(name string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimPrefix(name, pendingPrefix), 10, 64)
	return id, err == nil && IsPending(name)
}

// SetTicks sets a message's ticks (RaiseTicks is for receipts).
func (s *Store) SetTicks(account, name string, level int) error {
	_, err := s.db.Exec(`UPDATE chat_messages SET ticks=? WHERE account=? AND name=?`, level, account, name)
	return err
}

// WhatsAppPerson is someone you can tag: their best name ("" when none is
// known), and their JID.
type WhatsAppPerson struct {
	JID, Name string
}

// SetWhatsAppMembers remembers who's in a group (only the process holding the
// connection can ask WhatsApp; every other one reads it here).
func (s *Store) SetWhatsAppMembers(space string, jids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM whatsapp_members WHERE space=?`, space); err != nil {
		return err
	}
	for _, j := range jids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO whatsapp_members(space,jid) VALUES(?,?)`, space, j); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// WhatsAppNames is every name known for a WhatsApp number, by JID: your saved
// name or their own from the contacts, else the one on their latest message.
func (s *Store) WhatsAppNames() (map[string]string, error) {
	out := map[string]string{}
	rows, err := s.db.Query(`SELECT sender, sender_name FROM chat_messages WHERE account=? AND sender<>'' AND sender_name<>'' AND sender_name<>'You' ORDER BY created`, WhatsAppAccount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var j, n string
		if err := rows.Scan(&j, &n); err != nil {
			return nil, err
		}
		out[j] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	urows, err := s.db.Query(`SELECT id, name FROM users WHERE account=? AND name<>''`, WhatsAppAccount)
	if err != nil {
		return nil, err
	}
	defer urows.Close()
	for urows.Next() {
		var j, n string
		if err := urows.Scan(&j, &n); err != nil {
			return nil, err
		}
		// a name you saved beats everything; one they gave themselves ("~") only fills a gap
		if _, have := out[j]; !have || !strings.HasPrefix(n, "~") {
			out[j] = n
		}
	}
	return out, urows.Err()
}

// WhatsAppPeople is who you can tag in a chat: its members, everyone who has
// written there, and the other person of a one-to-one chat.
func (s *Store) WhatsAppPeople(space string) ([]WhatsAppPerson, error) {
	names, err := s.WhatsAppNames()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []WhatsAppPerson
	add := func(j string) {
		if j != "" && !seen[j] && strings.HasSuffix(j, "@s.whatsapp.net") {
			seen[j] = true
			out = append(out, WhatsAppPerson{JID: j, Name: strings.TrimPrefix(names[j], "~")})
		}
	}
	add(space)
	for _, q := range []string{`SELECT jid FROM whatsapp_members WHERE space=?`,
		`SELECT DISTINCT sender FROM chat_messages WHERE account='` + WhatsAppAccount + `' AND space=?`} {
		rows, err := s.db.Query(q, space)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var j string
			if err := rows.Scan(&j); err != nil {
				rows.Close()
				return nil, err
			}
			add(j)
		}
		rows.Close()
	}
	return out, nil
}

// WhatsAppReaction is one person's reaction to a message (WhatsApp allows one
// each). Sender is who wrote the message, and Pending says it hasn't reached
// WhatsApp yet: only the process holding the connection can send it. An
// empty Emoji is a reaction being taken back.
type WhatsAppReaction struct {
	Space, Msg, Reactor, Emoji, Sender string
	Pending                            bool
}

// SetWhatsAppReaction stores a reaction, or forgets one taken back.
func (s *Store) SetWhatsAppReaction(r WhatsAppReaction) error {
	if r.Emoji == "" && !r.Pending {
		_, err := s.db.Exec(`DELETE FROM whatsapp_reactions WHERE space=? AND msg=? AND reactor=?`, r.Space, r.Msg, r.Reactor)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO whatsapp_reactions(space,msg,reactor,emoji,sender,pending,created) VALUES(?,?,?,?,?,?,?)
	  ON CONFLICT(space,msg,reactor) DO UPDATE SET emoji=excluded.emoji, sender=excluded.sender, pending=excluded.pending, created=excluded.created`,
		r.Space, r.Msg, r.Reactor, r.Emoji, r.Sender, r.Pending, time.Now().UnixMilli())
	return err
}

// WhatsAppReactionOf is one person's reaction to a message ("" when none).
func (s *Store) WhatsAppReactionOf(space, msg, reactor string) (string, error) {
	var e string
	err := s.db.QueryRow(`SELECT emoji FROM whatsapp_reactions WHERE space=? AND msg=? AND reactor=?`, space, msg, reactor).Scan(&e)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return e, err
}

// WhatsAppReactionSummary is a message's reactions as chats show them:
// "👍 2 · 😂 1", most used first.
func (s *Store) WhatsAppReactionSummary(space, msg string) (string, error) {
	rows, err := s.db.Query(`SELECT emoji, COUNT(*) FROM whatsapp_reactions WHERE space=? AND msg=? AND emoji!='' GROUP BY emoji ORDER BY COUNT(*) DESC, MIN(created)`, space, msg)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var e string
		var n int
		if err := rows.Scan(&e, &n); err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%s %d", e, n))
	}
	return strings.Join(parts, " · "), rows.Err()
}

// PendingWhatsAppReactions are your reactions still to send, dropping those
// older than maxAge.
func (s *Store) PendingWhatsAppReactions(maxAge time.Duration) ([]WhatsAppReaction, error) {
	if _, err := s.db.Exec(`DELETE FROM whatsapp_reactions WHERE pending=1 AND created<?`, time.Now().Add(-maxAge).UnixMilli()); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT space,msg,reactor,emoji,sender FROM whatsapp_reactions WHERE pending=1 ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WhatsAppReaction
	for rows.Next() {
		r := WhatsAppReaction{Pending: true}
		if err := rows.Scan(&r.Space, &r.Msg, &r.Reactor, &r.Emoji, &r.Sender); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// WhatsAppReactionSent: it reached WhatsApp, unless you changed it since.
func (s *Store) WhatsAppReactionSent(r WhatsAppReaction) error {
	if r.Emoji == "" {
		_, err := s.db.Exec(`DELETE FROM whatsapp_reactions WHERE space=? AND msg=? AND reactor=? AND emoji='' AND pending=1`, r.Space, r.Msg, r.Reactor)
		return err
	}
	_, err := s.db.Exec(`UPDATE whatsapp_reactions SET pending=0 WHERE space=? AND msg=? AND reactor=? AND emoji=?`, r.Space, r.Msg, r.Reactor, r.Emoji)
	return err
}

// WhatsAppRead is a read receipt waiting for the process that holds the
// WhatsApp connection (any process can mark a chat read).
type WhatsAppRead struct {
	Space, Msg, Sender string
}

// AddWhatsAppReads queues read receipts; one already queued stays as it is.
func (s *Store) AddWhatsAppReads(reads []WhatsAppRead) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range reads {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO whatsapp_reads(space,msg,sender,created) VALUES(?,?,?,?)`, r.Space, r.Msg, r.Sender, now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// WhatsAppReads are the queued read receipts, dropping those older than
// maxAge (the phone has long moved on).
func (s *Store) WhatsAppReads(maxAge time.Duration) ([]WhatsAppRead, error) {
	if _, err := s.db.Exec(`DELETE FROM whatsapp_reads WHERE created<?`, time.Now().Add(-maxAge).UnixMilli()); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT space,msg,sender FROM whatsapp_reads ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WhatsAppRead
	for rows.Next() {
		var r WhatsAppRead
		if err := rows.Scan(&r.Space, &r.Msg, &r.Sender); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DoneWhatsAppReads forgets receipts that went out.
func (s *Store) DoneWhatsAppReads(reads []WhatsAppRead) error {
	for _, r := range reads {
		if _, err := s.db.Exec(`DELETE FROM whatsapp_reads WHERE space=? AND msg=?`, r.Space, r.Msg); err != nil {
			return err
		}
	}
	return nil
}

// ChatFile is a file in a chat message: Ref is Chat's media name (it can
// be downloaded), Drive a Drive file id (opened in the browser).
type ChatFile struct {
	Name  string `json:"name"`
	Type  string `json:"type,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Drive string `json:"drive,omitempty"`
	// WhatsApp: image, sticker, gif, video, voice, audio or document; its
	// size, and its small JPEG preview (base64)
	Kind  string `json:"kind,omitempty"`
	Size  int64  `json:"size,omitempty"`
	Thumb string `json:"thumb,omitempty"`
}

func (s *Store) PutSpace(sp Space) error {
	_, err := s.db.Exec(`INSERT INTO spaces(account,name,display_name,type,last_active,threaded,members) VALUES(?,?,?,?,?,?,?)
	  ON CONFLICT(account,name) DO UPDATE SET display_name=CASE WHEN excluded.display_name<>'' THEN excluded.display_name ELSE display_name END,
	  type=excluded.type, last_active=MAX(last_active,excluded.last_active), threaded=excluded.threaded,
	  members=CASE WHEN excluded.members>0 THEN excluded.members ELSE members END`, sp.Account, sp.Name, sp.DisplayName, sp.Type, sp.LastActive, sp.Threaded, sp.Members)
	return err
}

// ChatHit is a chat message found by a search, with its space's title.
type ChatHit struct {
	ChatMessage
	SpaceTitle string
	SpaceType  string
}

// SearchChat finds chat messages whose text or sender contains every word
// of the query, newest first.
func (s *Store) SearchChat(accounts []string, query string, limit int, space string) ([]ChatHit, error) {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil, nil
	}
	var where []string
	var args []any
	for _, w := range words {
		where = append(where, "(LOWER(c.text) LIKE ? OR LOWER(c.sender_name) LIKE ?)")
		args = append(args, "%"+w+"%", "%"+w+"%")
	}
	if space != "" {
		where = append(where, "c.space=?")
		args = append(args, space)
	}
	if len(accounts) > 0 {
		where = append(where, "c.account IN (?"+strings.Repeat(",?", len(accounts)-1)+")")
		for _, a := range accounts {
			args = append(args, a)
		}
	} else {
		where = append(where, "a.kind='google'")
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT c.account,c.name,c.space,c.thread,c.sender,c.sender_name,c.text,c.created, c.sender=a.chat_user, sp.display_name, sp.type
	  FROM chat_messages c JOIN accounts a ON a.name=c.account LEFT JOIN spaces sp ON sp.account=c.account AND sp.name=c.space
	  WHERE `+strings.Join(where, " AND ")+` ORDER BY c.created DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatHit
	for rows.Next() {
		var h ChatHit
		var title, typ sql.NullString
		if err := rows.Scan(&h.Account, &h.Name, &h.Space, &h.Thread, &h.Sender, &h.SenderName, &h.Text, &h.Created, &h.Mine, &title, &typ); err != nil {
			return nil, err
		}
		h.SpaceTitle, h.SpaceType = title.String, typ.String
		out = append(out, h)
	}
	return out, rows.Err()
}

// RaiseTicks moves messages' ticks up to level (never down: receipts can
// arrive out of order).
func (s *Store) RaiseTicks(account string, names []string, level int) error {
	for _, n := range names {
		if _, err := s.db.Exec(`UPDATE chat_messages SET ticks=MAX(ticks,?) WHERE account=? AND name=?`, level, account, n); err != nil {
			return err
		}
	}
	return nil
}

// ChatMessage returns one stored chat message.
func (s *Store) ChatMessage(account, name string) (ChatMessage, error) {
	var m ChatMessage
	var files string
	err := s.db.QueryRow(`SELECT account,name,space,thread,sender,sender_name,text,created,files,quoted,reactions,edited,ticks,starred FROM chat_messages WHERE account=? AND name=?`, account, name).
		Scan(&m.Account, &m.Name, &m.Space, &m.Thread, &m.Sender, &m.SenderName, &m.Text, &m.Created, &files, &m.Quoted, &m.Reactions, &m.Edited, &m.Ticks, &m.Starred)
	if err != nil {
		return m, err
	}
	_ = json.Unmarshal([]byte(files), &m.Files)
	return m, nil
}

// ResetDMTitles forgets DM and group titles so they are worked out again.
func (s *Store) ResetDMTitles(account string) error {
	_, err := s.db.Exec(`UPDATE spaces SET display_name='' WHERE account=? AND type<>'SPACE'`, account)
	return err
}

// MentionCounts: per chat, unread messages that mention you (@name).
func (s *Store) MentionCounts(account, name string) map[string]int {
	out := map[string]int{}
	name = strings.TrimSpace(name)
	if name == "" {
		return out
	}
	rows, err := s.db.Query(`SELECT c.space, COUNT(*) FROM chat_messages c JOIN spaces s ON s.account=c.account AND s.name=c.space
	  JOIN accounts a ON a.name=c.account
	  WHERE c.account=? AND c.created>s.last_read AND c.sender<>a.chat_user AND LOWER(c.text) LIKE ? GROUP BY c.space`,
		account, "%@"+strings.ToLower(name)+"%")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sp string
		var n int
		if rows.Scan(&sp, &n) == nil {
			out[sp] = n
		}
	}
	return out
}

// OldestChat is when the oldest stored message of a chat was sent.
func (s *Store) OldestChat(account, space string) (int64, error) {
	var t sql.NullInt64
	err := s.db.QueryRow(`SELECT MIN(created) FROM chat_messages WHERE account=? AND space=?`, account, space).Scan(&t)
	return t.Int64, err
}

// SetPinned keeps a chat on top of the list (on this machine).
func (s *Store) SetPinned(account, space string, on bool) error {
	_, err := s.db.Exec(`UPDATE spaces SET pinned=? WHERE account=? AND name=?`, on, account, space)
	return err
}

// WhatsAppPoll is a poll asked in a chat (Msg is the message's name) and
// its votes: voter → the options they picked.
type WhatsAppPoll struct {
	Msg      string
	Question string
	Options  []string
	Multi    bool
	Votes    map[string][]string
}

func (s *Store) PutWhatsAppPoll(p WhatsAppPoll) error {
	opts, _ := json.Marshal(p.Options)
	_, err := s.db.Exec(`INSERT INTO whatsapp_polls(msg,question,options,multi) VALUES(?,?,?,?)
	  ON CONFLICT(msg) DO UPDATE SET question=excluded.question, options=excluded.options, multi=excluded.multi`, p.Msg, p.Question, string(opts), p.Multi)
	return err
}

// SetWhatsAppVote records a vote (no options: taken back).
func (s *Store) SetWhatsAppVote(msg, voter string, options []string) error {
	if len(options) == 0 {
		_, err := s.db.Exec(`DELETE FROM whatsapp_votes WHERE msg=? AND voter=?`, msg, voter)
		return err
	}
	b, _ := json.Marshal(options)
	_, err := s.db.Exec(`INSERT INTO whatsapp_votes(msg,voter,options) VALUES(?,?,?) ON CONFLICT(msg,voter) DO UPDATE SET options=excluded.options`, msg, voter, string(b))
	return err
}

func (s *Store) WhatsAppPollOf(msg string) (WhatsAppPoll, error) {
	p := WhatsAppPoll{Msg: msg, Votes: map[string][]string{}}
	var opts string
	if err := s.db.QueryRow(`SELECT question,options,multi FROM whatsapp_polls WHERE msg=?`, msg).Scan(&p.Question, &opts, &p.Multi); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, err
	}
	_ = json.Unmarshal([]byte(opts), &p.Options)
	rows, err := s.db.Query(`SELECT voter,options FROM whatsapp_votes WHERE msg=?`, msg)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var voter, o string
		if rows.Scan(&voter, &o) == nil {
			var picked []string
			_ = json.Unmarshal([]byte(o), &picked)
			p.Votes[voter] = picked
		}
	}
	return p, rows.Err()
}

// SetChatText changes a stored message's text without marking it edited
// (a poll's tally).
func (s *Store) SetChatText(account, name, text string) error {
	_, err := s.db.Exec(`UPDATE chat_messages SET text=? WHERE account=? AND name=?`, text, account, name)
	return err
}

// SetArchived, SetMuted, SetStarred: WhatsApp chat and message state.
func (s *Store) SetArchived(account, space string, on bool) error {
	_, err := s.db.Exec(`UPDATE spaces SET archived=? WHERE account=? AND name=?`, on, account, space)
	return err
}

func (s *Store) SetMuted(account, space string, until int64) error {
	_, err := s.db.Exec(`UPDATE spaces SET muted_until=? WHERE account=? AND name=?`, until, account, space)
	return err
}

func (s *Store) SetDisappear(account, space string, secs int) error {
	_, err := s.db.Exec(`UPDATE spaces SET disappear=? WHERE account=? AND name=?`, max(secs, 0), account, space)
	return err
}

func (s *Store) SetBlocked(account, space string, on bool) error {
	_, err := s.db.Exec(`UPDATE spaces SET blocked=? WHERE account=? AND name=?`, on, account, space)
	return err
}

// Muted: the chat's notifications are off now.
func (sp Space) Muted() bool {
	return sp.MutedUntil < 0 || sp.MutedUntil > time.Now().UnixMilli()
}

// Presence is what WhatsApp last said about someone: online (or when
// last seen), and typing in a chat.
type Presence struct {
	JID      string
	Online   bool
	LastSeen int64 // ms; 0 unknown (hidden)
	At       int64 // when this was heard
	TypingIn string
	Typing   string // composing, recording or ""
	TypingAt int64
}

// SetPresence records someone's online state.
func (s *Store) SetPresence(jid string, online bool, lastSeen int64) error {
	_, err := s.db.Exec(`INSERT INTO whatsapp_presence(jid,online,last_seen,at) VALUES(?,?,?,?)
	  ON CONFLICT(jid) DO UPDATE SET online=excluded.online, last_seen=CASE WHEN excluded.last_seen>0 THEN excluded.last_seen ELSE last_seen END, at=excluded.at`,
		jid, online, lastSeen, now())
	return err
}

// SetTyping records someone typing (or stopping, typing "") in a chat;
// typing means they're online.
func (s *Store) SetTyping(jid, chat, typing string) error {
	_, err := s.db.Exec(`INSERT INTO whatsapp_presence(jid,online,at,typing_in,typing,typing_at) VALUES(?,1,?,?,?,?)
	  ON CONFLICT(jid) DO UPDATE SET online=1, at=excluded.at, typing_in=excluded.typing_in, typing=excluded.typing, typing_at=excluded.typing_at`,
		jid, now(), chat, typing, now())
	return err
}

// PresenceOf is what's known about someone ("" JID when nothing is).
func (s *Store) PresenceOf(jid string) Presence {
	p := Presence{}
	_ = s.db.QueryRow(`SELECT jid,online,last_seen,at,typing_in,typing,typing_at FROM whatsapp_presence WHERE jid=?`, jid).
		Scan(&p.JID, &p.Online, &p.LastSeen, &p.At, &p.TypingIn, &p.Typing, &p.TypingAt)
	return p
}

// TypingIn lists who is typing in a chat now (heard in the last 15 seconds).
func (s *Store) TypingIn(chat string) []Presence {
	rows, err := s.db.Query(`SELECT jid,online,last_seen,at,typing_in,typing,typing_at FROM whatsapp_presence
	  WHERE typing_in=? AND typing<>'' AND typing_at>?`, chat, now()-15000)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Presence
	for rows.Next() {
		var p Presence
		if rows.Scan(&p.JID, &p.Online, &p.LastSeen, &p.At, &p.TypingIn, &p.Typing, &p.TypingAt) == nil {
			out = append(out, p)
		}
	}
	return out
}

func (s *Store) SetStarred(account, name string, on bool) error {
	_, err := s.db.Exec(`UPDATE chat_messages SET starred=? WHERE account=? AND name=?`, on, account, name)
	return err
}

// Starred lists starred messages, newest first.
func (s *Store) Starred(account string, limit int) ([]ChatMessage, error) {
	rows, err := s.db.Query(`SELECT name FROM chat_messages WHERE account=? AND starred=1 ORDER BY created DESC LIMIT ?`, account, limit)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, n)
	}
	rows.Close()
	var out []ChatMessage
	for _, n := range names {
		if m, err := s.ChatMessage(account, n); err == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// WhatsAppAction is something only the process holding WhatsApp can do,
// queued for it (ADR 7): Done once it has, with its Result or Error.
type WhatsAppAction struct {
	ID      int64
	Action  string
	Chat    string
	Arg     string
	Created int64
	Done    bool
	Error   string
	Result  string
}

func (s *Store) AddWhatsAppAction(action, chat, arg string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO whatsapp_actions(action,chat,arg,created) VALUES(?,?,?,?)`, action, chat, arg, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanActions(rows *sql.Rows, err error) ([]WhatsAppAction, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WhatsAppAction
	for rows.Next() {
		var a WhatsAppAction
		var done int
		if err := rows.Scan(&a.ID, &a.Action, &a.Chat, &a.Arg, &a.Created, &done, &a.Error, &a.Result); err != nil {
			return nil, err
		}
		a.Done = done == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

const actionCols = `id,action,chat,arg,created,done,error,result`

// PendingWhatsAppActions lists what's waiting, oldest first; older than
// maxAge is dropped (and finished ones a day old are cleared).
func (s *Store) PendingWhatsAppActions(maxAge time.Duration) ([]WhatsAppAction, error) {
	if _, err := s.db.Exec(`DELETE FROM whatsapp_actions WHERE (done<>1 AND created<?) OR (done=1 AND created<?)`,
		time.Now().Add(-maxAge).UnixMilli(), time.Now().Add(-24*time.Hour).UnixMilli()); err != nil {
		return nil, err
	}
	// one claimed by a process that died doing it goes back
	_, _ = s.db.Exec(`UPDATE whatsapp_actions SET done=0 WHERE done=2 AND created<?`, time.Now().Add(-5*time.Minute).UnixMilli())
	return scanActions(s.db.Query(`SELECT ` + actionCols + ` FROM whatsapp_actions WHERE done=0 ORDER BY id`))
}

func (s *Store) WhatsAppActionByID(id int64) (WhatsAppAction, error) {
	as, err := scanActions(s.db.Query(`SELECT `+actionCols+` FROM whatsapp_actions WHERE id=?`, id))
	if err != nil {
		return WhatsAppAction{}, err
	}
	if len(as) == 0 {
		return WhatsAppAction{}, ErrNotFound
	}
	return as[0], nil
}

// ClaimWhatsAppAction takes an action to do; false when another process
// (or another flush in this one) has it.
func (s *Store) ClaimWhatsAppAction(id int64) bool {
	res, err := s.db.Exec(`UPDATE whatsapp_actions SET done=2 WHERE id=? AND done=0`, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// FinishWhatsAppAction records how it went; a failure that may pass
// (offline) leaves it waiting when retry is set.
func (s *Store) FinishWhatsAppAction(id int64, result, errText string) error {
	_, err := s.db.Exec(`UPDATE whatsapp_actions SET done=1, result=?, error=? WHERE id=?`, result, errText, id)
	return err
}

// EditChatText changes a stored message's text (after an edit).
func (s *Store) EditChatText(account, name, text string) error {
	_, err := s.db.Exec(`UPDATE chat_messages SET text=?, edited=1 WHERE account=? AND name=?`, text, account, name)
	return err
}

// DeleteChatOlderThan drops a chat's messages from before a time.
func (s *Store) DeleteChatOlderThan(account, space string, before int64) error {
	_, err := s.db.Exec(`DELETE FROM chat_messages WHERE account=? AND space=? AND created<?`, account, space, before)
	return err
}

// DeleteChatMessage drops a deleted message from the local copy.
func (s *Store) DeleteChatMessage(account, name string) error {
	_, err := s.db.Exec(`DELETE FROM chat_messages WHERE account=? AND name=?`, account, name)
	return err
}

// SetReactions stores a message's reaction summary.
func (s *Store) SetReactions(account, name, reactions string) error {
	_, err := s.db.Exec(`UPDATE chat_messages SET reactions=? WHERE account=? AND name=?`, reactions, account, name)
	return err
}

// SetAlias names a chat on this machine only ("" removes the name).
func (s *Store) SetAlias(account, space, alias string) error {
	_, err := s.db.Exec(`UPDATE spaces SET alias=? WHERE account=? AND name=?`, alias, account, space)
	return err
}

// RenameSpace sets a DM or group's title (they have none of their own).
func (s *Store) RenameSpace(account, space, name string) error {
	_, err := s.db.Exec(`UPDATE spaces SET display_name=? WHERE account=? AND name=?`, name, account, space)
	return err
}

func (s *Store) SetSpaceRead(account, space string, when int64) error {
	_, err := s.db.Exec(`UPDATE spaces SET last_read=MAX(last_read,?) WHERE account=? AND name=?`, when, account, space)
	return err
}

// ForceSpaceRead sets the read time even backwards (marking unread).
// Stamp changes whenever any process writes to the database (the WAL
// file's size and time move on every commit): a cheap "anything new?".
func (s *Store) Stamp() string {
	st, err := os.Stat(s.path + "-wal")
	if err != nil {
		if st, err = os.Stat(s.path); err != nil {
			return ""
		}
	}
	return fmt.Sprint(st.Size(), st.ModTime().UnixNano())
}

// Tried notes a lookup that failed just now.
func (s *Store) Tried(account, key string) error {
	_, err := s.db.Exec(`INSERT INTO tries(account,key,at) VALUES(?,?,?) ON CONFLICT(account,key) DO UPDATE SET at=excluded.at`, account, key, now())
	return err
}

// TryAgain: the lookup never failed, or failed longer than `after` ago.
func (s *Store) TryAgain(account, key string, after time.Duration) bool {
	var at int64
	if err := s.db.QueryRow(`SELECT at FROM tries WHERE account=? AND key=?`, account, key).Scan(&at); err != nil {
		return true
	}
	return time.Since(time.UnixMilli(at)) > after
}

// LastOtherMessage is when someone else last wrote in the space (0: never).
func (s *Store) LastOtherMessage(account, space string) int64 {
	var t sql.NullInt64
	_ = s.db.QueryRow(`SELECT MAX(c.created) FROM chat_messages c JOIN accounts a ON a.name=c.account
	  WHERE c.account=? AND c.space=? AND c.sender<>a.chat_user`, account, space).Scan(&t)
	return t.Int64
}

func (s *Store) ForceSpaceRead(account, space string, when int64) error {
	_, err := s.db.Exec(`UPDATE spaces SET last_read=? WHERE account=? AND name=?`, when, account, space)
	return err
}

func (s *Store) SetSpaceSynced(account, space string, when int64) error {
	_, err := s.db.Exec(`UPDATE spaces SET synced_to=MAX(synced_to,?) WHERE account=? AND name=?`, when, account, space)
	return err
}

func (s *Store) PutChatMessages(msgs []ChatMessage) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range msgs {
		files, _ := json.Marshal(m.Files)
		if m.Files == nil {
			files = []byte("[]")
		}
		if _, err := tx.Exec(`INSERT INTO chat_messages(account,name,space,thread,sender,sender_name,text,created,files,quoted,reactions,edited,ticks) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		  ON CONFLICT(account,name) DO UPDATE SET text=excluded.text, files=excluded.files, quoted=excluded.quoted, reactions=excluded.reactions, edited=excluded.edited,
		    ticks=MAX(ticks, excluded.ticks),
		    sender_name=CASE WHEN excluded.sender_name<>'' THEN excluded.sender_name ELSE sender_name END`,
			m.Account, m.Name, m.Space, m.Thread, m.Sender, m.SenderName, m.Text, m.Created, string(files), m.Quoted, m.Reactions, m.Edited, m.Ticks); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Spaces lists spaces, most recently active first, with unread counts
// (messages from others after the space's read time). No accounts means
// every Google account: WhatsApp's chats only come when asked by name.
func (s *Store) Spaces(accounts []string, me map[string]string) ([]Space, error) {
	q := `SELECT s.account,s.name,s.display_name,s.alias,s.threaded,s.pinned,s.archived,s.muted_until,s.blocked,s.disappear,s.type,s.last_active,s.last_read,s.synced_to,s.members,
	  (SELECT COUNT(*) FROM chat_messages c JOIN accounts a ON a.name=c.account WHERE c.account=s.account AND c.space=s.name AND c.created>s.last_read AND c.sender<>a.chat_user),
	  COALESCE((SELECT c.text FROM chat_messages c WHERE c.account=s.account AND c.space=s.name ORDER BY c.created DESC LIMIT 1),''),
	  COALESCE((SELECT CASE WHEN c.sender<>'' AND c.sender=(SELECT chat_user FROM accounts WHERE name=s.account) THEN 'You' ELSE c.sender_name END
	    FROM chat_messages c WHERE c.account=s.account AND c.space=s.name ORDER BY c.created DESC LIMIT 1),'')
	  FROM spaces s`
	var args []any
	if len(accounts) > 0 {
		q += " WHERE s.account IN (" + strings.TrimSuffix(strings.Repeat("?,", len(accounts)), ",") + ")"
		for _, a := range accounts {
			args = append(args, a)
		}
	} else {
		q += " WHERE s.account IN (SELECT name FROM accounts WHERE kind='google')"
	}
	q += " ORDER BY s.last_active DESC"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Space
	for rows.Next() {
		var sp Space
		if err := rows.Scan(&sp.Account, &sp.Name, &sp.DisplayName, &sp.Alias, &sp.Threaded, &sp.Pinned, &sp.Archived, &sp.MutedUntil, &sp.Blocked, &sp.Disappear, &sp.Type, &sp.LastActive, &sp.LastRead, &sp.SyncedTo, &sp.Members, &sp.Unread, &sp.LastText, &sp.LastSender); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (s *Store) Space(account, name string) (Space, error) {
	all, err := s.Spaces([]string{account}, nil)
	if err != nil {
		return Space{}, err
	}
	for _, sp := range all {
		if sp.Name == name {
			return sp, nil
		}
	}
	return Space{}, ErrNotFound
}

func (s *Store) ChatMessages(account, space string, limit int) ([]ChatMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT c.account,c.name,c.space,c.thread,c.sender,c.sender_name,c.text,c.created, c.sender=a.chat_user, c.files, c.quoted, c.reactions, c.edited, c.ticks, c.starred
	  FROM chat_messages c JOIN accounts a ON a.name=c.account WHERE c.account=? AND c.space=? ORDER BY c.created DESC LIMIT ?`, account, space, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var files string
		if err := rows.Scan(&m.Account, &m.Name, &m.Space, &m.Thread, &m.Sender, &m.SenderName, &m.Text, &m.Created, &m.Mine, &files, &m.Quoted, &m.Reactions, &m.Edited, &m.Ticks, &m.Starred); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(files), &m.Files)
		out = append(out, m)
	}
	// oldest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// ThreadStart is a chat thread's first message we have, and how many
// messages it holds.
func (s *Store) ThreadStart(account, space, thread string) (first ChatMessage, n int, err error) {
	if thread == "" {
		return first, 0, ErrNotFound
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM chat_messages WHERE account=? AND space=? AND thread=?`, account, space, thread).Scan(&n); err != nil {
		return first, 0, err
	}
	if n == 0 {
		return first, 0, ErrNotFound
	}
	err = s.db.QueryRow(`SELECT name,sender_name,text,created FROM chat_messages WHERE account=? AND space=? AND thread=? ORDER BY created LIMIT 1`,
		account, space, thread).Scan(&first.Name, &first.SenderName, &first.Text, &first.Created)
	return first, n, err
}

// ChatUser is someone you've seen in chat.
type ChatUser struct{ ID, Name, Email string }

// ChatUsers finds people from chat by name or address.
func (s *Store) ChatUsers(account, query string, limit int) ([]ChatUser, error) {
	like := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	rows, err := s.db.Query(`SELECT u.id,u.name,u.email FROM users u JOIN accounts a ON a.name=u.account
	  WHERE u.account=? AND u.id<>a.chat_user AND u.name<>'' AND (LOWER(u.name) LIKE ? OR LOWER(u.email) LIKE ?) ORDER BY u.name LIMIT ?`,
		account, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatUser
	for rows.Next() {
		var u ChatUser
		if err := rows.Scan(&u.ID, &u.Name, &u.Email); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) User(account, id string) (name, email string, ok bool) {
	err := s.db.QueryRow(`SELECT name,email FROM users WHERE account=? AND id=?`, account, id).Scan(&name, &email)
	return name, email, err == nil
}

func (s *Store) PutUser(account, id, name, email string) error {
	_, err := s.db.Exec(`INSERT INTO users(account,id,name,email) VALUES(?,?,?,?) ON CONFLICT(account,id) DO UPDATE SET name=excluded.name, email=excluded.email`,
		account, id, name, email)
	return err
}

// FixSenderNames fills sender names learned after their messages arrived.
func (s *Store) FixSenderNames(account string) error {
	_, err := s.db.Exec(`UPDATE chat_messages SET sender_name=(SELECT name FROM users u WHERE u.account=chat_messages.account AND u.id=chat_messages.sender)
	  WHERE account=? AND EXISTS (SELECT 1 FROM users u WHERE u.account=chat_messages.account AND u.id=chat_messages.sender AND u.name<>'')`, account)
	return err
}

// ------------------------------------------------------------------ notify

// FirstNotify records key and reports whether it was new.
func (s *Store) FirstNotify(account, key string) bool {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO notified(account,key) VALUES(?,?)`, account, key)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// SetUnreadCount sets a chat's read time so its n newest messages from
// others count as unread (WhatsApp's history gives a count, not a time).
func (s *Store) SetUnreadCount(account, space string, n int) error {
	_, err := s.db.Exec(`UPDATE spaces SET last_read=COALESCE((SELECT c.created FROM chat_messages c JOIN accounts a ON a.name=c.account
	  WHERE c.account=? AND c.space=? AND c.sender<>a.chat_user ORDER BY c.created DESC LIMIT 1 OFFSET ?), CASE WHEN ?>0 THEN 0 ELSE last_read END)
	  WHERE account=? AND name=?`, account, space, n, n, account, space)
	return err
}

// UnreadChatMessages are the messages from others after the chat's read time.
func (s *Store) UnreadChatMessages(account, space string) ([]ChatMessage, error) {
	all, err := s.ChatMessages(account, space, 500)
	if err != nil {
		return nil, err
	}
	sp, err := s.Space(account, space)
	if err != nil {
		return nil, err
	}
	var out []ChatMessage
	for _, m := range all {
		if !m.Mine && m.Created > sp.LastRead {
			out = append(out, m)
		}
	}
	return out, nil
}

// RepairWhatsAppNames fixes names stored before narada knew better:
// WhatsApp's service chat goes, Meta AI gets its name, and in one-to-one
// chats the other person's messages carry the chat's name (not the one
// they gave themselves).
func (s *Store) RepairWhatsAppNames() error {
	for _, q := range []string{
		`DELETE FROM chat_messages WHERE account='whatsapp' AND (space='0@s.whatsapp.net' OR space LIKE 'status@%')`,
		`DELETE FROM spaces WHERE account='whatsapp' AND (name='0@s.whatsapp.net' OR name LIKE 'status@%')`,
		`UPDATE spaces SET display_name='Meta AI' WHERE account='whatsapp' AND name='13135550002@s.whatsapp.net'`,
		`UPDATE chat_messages SET sender_name=(SELECT sp.display_name FROM spaces sp WHERE sp.account=chat_messages.account AND sp.name=chat_messages.space)
		  WHERE account='whatsapp' AND sender=space AND space NOT LIKE '%@g.us'
		    AND (SELECT sp.display_name FROM spaces sp WHERE sp.account=chat_messages.account AND sp.name=chat_messages.space) NOT LIKE '+%'
		    AND (SELECT sp.display_name FROM spaces sp WHERE sp.account=chat_messages.account AND sp.name=chat_messages.space) <> ''`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// MediaFetch is a file one process asked another (WhatsApp's holder) to download.
type MediaFetch struct {
	Account, Msg string
	Idx          int
	Error        string
}

// AskMedia queues a file for download (asking again clears an old error).
func (s *Store) AskMedia(account, msg string, idx int) error {
	_, err := s.db.Exec(`INSERT INTO media_fetch(account,msg,idx,asked) VALUES(?,?,?,?)
	  ON CONFLICT(account,msg,idx) DO UPDATE SET asked=excluded.asked, error=''`, account, msg, idx, now())
	return err
}

// MediaAsked lists the downloads waiting (not failed), oldest first.
func (s *Store) MediaAsked(limit int) ([]MediaFetch, error) {
	rows, err := s.db.Query(`SELECT account,msg,idx,error FROM media_fetch WHERE error='' ORDER BY asked LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaFetch
	for rows.Next() {
		var f MediaFetch
		if err := rows.Scan(&f.Account, &f.Msg, &f.Idx, &f.Error); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// MediaPending: the file was asked for and hasn't been fetched (or failed) yet.
func (s *Store) MediaPending(account, msg string, idx int) bool {
	var n int
	_ = s.db.QueryRow(`SELECT count(*) FROM media_fetch WHERE account=? AND msg=? AND idx=? AND error=''`, account, msg, idx).Scan(&n)
	return n > 0
}

// MediaFetchError is why a download failed ("" when it didn't, or wasn't asked).
func (s *Store) MediaFetchError(account, msg string, idx int) string {
	var e string
	_ = s.db.QueryRow(`SELECT error FROM media_fetch WHERE account=? AND msg=? AND idx=?`, account, msg, idx).Scan(&e)
	return e
}

// MediaFetched ends a request: done (err "") or failed.
func (s *Store) MediaFetched(account, msg string, idx int, err string) error {
	var e error
	if err == "" {
		_, e = s.db.Exec(`DELETE FROM media_fetch WHERE account=? AND msg=? AND idx=?`, account, msg, idx)
	} else {
		_, e = s.db.Exec(`UPDATE media_fetch SET error=? WHERE account=? AND msg=? AND idx=?`, err, account, msg, idx)
	}
	return e
}
