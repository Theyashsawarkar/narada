// Package sock is where agents reach `narada daemon`: the socket's path,
// its folder's safety check, and the listener (handed over by systemd's
// narada-mcp.socket, or bound here for dev runs and tests). The client
// end is pranali (docs/adr/0011-pranali.md); its protocol is pranali's
// docs/PROTOCOL.md.
package sock

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// MaxPath is the longest socket path a sockaddr_un holds (108 bytes with
// its terminating NUL).
const MaxPath = 107

// Path is where the daemon serves agents, as pranali looks for it:
// $XDG_RUNTIME_DIR/narada/mcp.sock, else
// ${XDG_STATE_HOME:-~/.local/state}/narada/run/mcp.sock, never /tmp.
// Under NARADA_HOME (tests, demos) it is $NARADA_HOME/run/mcp.sock, so a
// test daemon never meets the real one; `narada mcp` hands that to
// pranali as PRANALI_SOCKET.
func Path() string {
	if h := os.Getenv("NARADA_HOME"); h != "" {
		return filepath.Join(h, "run", "mcp.sock")
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "narada", "mcp.sock")
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, _ := os.UserHomeDir()
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "narada", "run", "mcp.sock")
}

// CheckDir refuses a socket folder that isn't a real directory owned by
// this user with no group or other permissions (pranali refuses it too).
func CheckDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !fi.IsDir():
		return fmt.Errorf("%s is not a directory", dir)
	case !ok || int(st.Uid) != os.Getuid():
		return fmt.Errorf("%s is not owned by you", dir)
	case fi.Mode().Perm()&0o077 != 0:
		return fmt.Errorf("%s is open to others (mode %o): it must be 0700", dir, fi.Mode().Perm())
	}
	return nil
}

// checkLen refuses a path a unix socket can't hold.
func checkLen(path string) error {
	if len(path) > MaxPath {
		return fmt.Errorf("socket path %s is %d bytes, over the %d a unix socket allows: use a shorter XDG_RUNTIME_DIR or NARADA_HOME", path, len(path), MaxPath)
	}
	return nil
}

// Listener is the socket the daemon serves on. Activated: systemd
// handed it over (it owns the path: never unlink it).
type Listener struct {
	net.Listener
	Path      string
	Activated bool
	ino       uint64 // the socket file this daemon bound (0: none)
	once      sync.Once
}

// Close stops listening; a socket this daemon bound is removed, but only
// while the file at Path is still that one (systemd's narada-mcp.socket,
// started meanwhile, may have replaced it).
func (l *Listener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() {
		if l.ino == 0 {
			return
		}
		var st syscall.Stat_t
		if syscall.Lstat(l.Path, &st) == nil && st.Ino == l.ino {
			_ = os.Remove(l.Path)
		}
	})
	return err
}

// Listen takes the socket from systemd when it was socket-activated
// (LISTEN_FDS/LISTEN_PID, fd 3), else binds path itself: the folder made
// 0700 and checked, the socket 0600. A socket left at path by a daemon
// that died (nothing accepts on it) is replaced; one that something
// serves, or anything that isn't a socket, is left alone and refused.
// A self-bound socket is removed when the listener closes (if it is
// still the file at path).
func Listen(path string) (*Listener, error) {
	if l, err := activated(); l != nil || err != nil {
		return &Listener{Listener: l, Path: path, Activated: true}, err
	}
	if err := checkLen(path); err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if fi, err := os.Lstat(dir); err == nil && fi.IsDir() && fi.Mode().Perm()&0o077 != 0 {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) == os.Getuid() {
			_ = os.Chmod(dir, 0o700) // ours, made by an older narada
		}
	}
	if err := CheckDir(dir); err != nil {
		return nil, err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket: not touching it", path)
		}
		c, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			c.Close()
			return nil, fmt.Errorf("%s is already served (by systemd's narada-mcp.socket?) but this daemon wasn't handed it", path)
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		// nothing accepts on it: a daemon that died left it
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	// the folder is 0700, so nobody else reaches it before the chmod
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false) // Close checks it is still ours
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		l.Close()
		return nil, err
	}
	ours := &Listener{Listener: l, Path: path, ino: st.Ino}
	if err := os.Chmod(path, 0o600); err != nil {
		ours.Close()
		return nil, err
	}
	return ours, nil
}

// activated is the listener systemd passed (nil, nil when none). With
// more than one fd it picks the one named narada-mcp.socket (systemd's
// default FileDescriptorName) in LISTEN_FDNAMES. The variables are
// cleared so programs the daemon starts don't think they got it.
func activated() (net.Listener, error) {
	pid, fds := os.Getenv("LISTEN_PID"), os.Getenv("LISTEN_FDS")
	if pid == "" || fds == "" || pid != strconv.Itoa(os.Getpid()) {
		return nil, nil
	}
	names := strings.Split(os.Getenv("LISTEN_FDNAMES"), ":")
	for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		os.Unsetenv(k)
	}
	n, err := strconv.Atoi(fds)
	if err != nil || n < 1 {
		return nil, fmt.Errorf("LISTEN_FDS=%q: no socket", fds)
	}
	const first = 3 // SD_LISTEN_FDS_START
	fd := first
	if n > 1 {
		fd = -1
		for i, name := range names {
			if i < n && (name == "narada-mcp.socket" || name == "mcp") {
				fd = first + i
			}
		}
		if fd < 0 {
			return nil, fmt.Errorf("systemd passed %d sockets and none is narada-mcp.socket (LISTEN_FDNAMES=%s)", n, strings.Join(names, ":"))
		}
	}
	syscall.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), "narada-mcp.socket")
	l, err := net.FileListener(f) // a dup: systemd keeps its own
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("the socket systemd passed: %w", err)
	}
	if ul, ok := l.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false) // systemd's path, not ours
	}
	return l, nil
}
