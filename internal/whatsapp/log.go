package whatsapp

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// logCap: the log is moved to <name>.1 once it passes this, so it never
// holds more than about twice as much.
const logCap = 1 << 20

// fileLog is whatsmeow's logger writing warnings and errors (and narada's
// own notes) to a file, so a WhatsApp problem leaves a trace.
type fileLog struct {
	mu     *sync.Mutex
	path   string
	module string
}

// Log is where the WhatsApp log goes; "" (the default) keeps it off.
// Set it before Open.
var Log string

func newLog(path, module string) waLog.Logger {
	if path == "" {
		return waLog.Noop
	}
	return &fileLog{mu: &sync.Mutex{}, path: path, module: module}
}

func (l *fileLog) write(level, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if st, err := os.Stat(l.path); err == nil && st.Size() > logCap {
		_ = os.Rename(l.path, l.path+".1")
	}
	_ = os.MkdirAll(filepath.Dir(l.path), 0o700)
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	mod := ""
	if l.module != "" {
		mod = l.module + ": "
	}
	fmt.Fprintf(f, "%s %s %s%s\n", time.Now().Format("2006-01-02 15:04:05"), level, mod, fmt.Sprintf(msg, args...))
}

func (l *fileLog) Errorf(msg string, args ...any) { l.write("ERROR", msg, args...) }
func (l *fileLog) Warnf(msg string, args ...any)  { l.write("WARN", msg, args...) }
func (l *fileLog) Infof(msg string, args ...any)  {}
func (l *fileLog) Debugf(msg string, args ...any) {}
func (l *fileLog) Sub(module string) waLog.Logger {
	if l.module != "" {
		module = l.module + "/" + module
	}
	return &fileLog{mu: l.mu, path: l.path, module: module}
}

// note writes one of narada's own lines (what happened to a status, say).
func (cl *Client) note(msg string, args ...any) {
	if f, ok := cl.log.(*fileLog); ok {
		f.write("NOTE", msg, args...)
	}
}
