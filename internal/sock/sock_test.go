package sock

import (
	"bufio"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "nsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// The socket never lives in /tmp: with no XDG_RUNTIME_DIR it goes under
// the state folder, as pranali looks for it.
func TestPathNeverTmp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NARADA_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_STATE_HOME", "")
	if p, want := Path(), filepath.Join(home, ".local", "state", "narada", "run", "mcp.sock"); p != want {
		t.Fatalf("no XDG_RUNTIME_DIR: %s, want %s", p, want)
	}
	t.Setenv("XDG_STATE_HOME", "/srv/state")
	if p := Path(); p != "/srv/state/narada/run/mcp.sock" {
		t.Fatalf("XDG_STATE_HOME: %s", p)
	}
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/4242")
	if p := Path(); p != "/run/user/4242/narada/mcp.sock" {
		t.Fatalf("XDG_RUNTIME_DIR: %s", p)
	}
	t.Setenv("NARADA_HOME", "/srv/n")
	if p := Path(); p != "/srv/n/run/mcp.sock" {
		t.Fatalf("NARADA_HOME: %s", p)
	}
	t.Setenv("HOME", "/home/someone") // t.TempDir is in /tmp itself
	for _, env := range [][2]string{{"", ""}, {"/run/user/4242", ""}} {
		t.Setenv("NARADA_HOME", "")
		t.Setenv("XDG_RUNTIME_DIR", env[0])
		t.Setenv("XDG_STATE_HOME", env[1])
		if p := Path(); strings.HasPrefix(p, "/tmp/") || strings.HasPrefix(p, os.TempDir()+"/") {
			t.Errorf("socket in /tmp: %s", p)
		}
	}
}

func TestCheckDir(t *testing.T) {
	d := shortDir(t)
	open := filepath.Join(d, "open")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(open, 0o755)
	if err := CheckDir(open); err == nil {
		t.Error("a 0755 folder passed")
	}
	link := filepath.Join(d, "link")
	_ = os.Symlink(d, link)
	if err := CheckDir(link); err == nil {
		t.Error("a symlink passed")
	}
	_ = os.Chmod(open, 0o700)
	if err := CheckDir(open); err != nil {
		t.Error(err)
	}
	// Listen fixes a folder of ours an older narada made 0755
	_ = os.Chmod(open, 0o755)
	l, err := Listen(filepath.Join(open, "mcp.sock"))
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if fi, _ := os.Stat(open); fi.Mode().Perm() != 0o700 {
		t.Errorf("folder %o", fi.Mode().Perm())
	}
	if _, err := Listen(filepath.Join(d, strings.Repeat("x", 120))); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Errorf("a path too long: %v", err)
	}
}

func TestListenLeavesOthersSockets(t *testing.T) {
	d := shortDir(t)
	path := filepath.Join(d, "run", "mcp.sock")
	// a socket a dead daemon left: nothing accepts, so it is replaced
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	dead, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	dead.(*net.UnixListener).SetUnlinkOnClose(false)
	dead.Close()
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode: %v %v", fi, err)
	}
	// a live one (this daemon's, or systemd's) is never taken over
	if _, err := Listen(path); err == nil || !strings.Contains(err.Error(), "already served") {
		t.Fatalf("live socket: %v", err)
	}
	// replaced meanwhile (systemd's socket unit started): not ours to
	// remove when we stop
	_ = os.Remove(path)
	other, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	l.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("removed a socket it didn't bind: %v", err)
	}
	other.Close()
	// a file that isn't a socket is left alone
	_ = os.WriteFile(path, []byte("x"), 0o600)
	if _, err := Listen(path); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("regular file: %v", err)
	}
	// and a socket it bound and still owns goes when it stops
	_ = os.Remove(path)
	l, err = Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("own socket left: %v", err)
	}
}

// Socket activation: systemd passes the listener as fd 3 with LISTEN_PID
// and LISTEN_FDS. The child (this test binary again) gets a listener as
// its fd 3 and must serve on it, not bind the path.
func TestActivation(t *testing.T) {
	if os.Getenv("NARADA_SOCK_CHILD") != "" {
		activationChild()
		return
	}
	d := shortDir(t)
	sysPath := filepath.Join(d, "systemd.sock")
	sys, err := net.Listen("unix", sysPath)
	if err != nil {
		t.Fatal(err)
	}
	f, err := sys.(*net.UnixListener).File()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestActivation$")
	cmd.Env = append(os.Environ(), "NARADA_SOCK_CHILD=1", "LISTEN_FDS=1", "LISTEN_FDNAMES=narada-mcp.socket",
		"NARADA_SOCK_PATH="+filepath.Join(d, "self", "mcp.sock"))
	cmd.ExtraFiles = []*os.File{f} // fd 3
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	sys.(*net.UnixListener).SetUnlinkOnClose(false)
	sys.Close() // the child holds it now, as a daemon holds systemd's
	c, err := net.Dial("unix", sysPath)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(c).ReadString('\n')
	c.Close()
	rest, _ := bufio.NewReader(out).ReadString('\n')
	_ = cmd.Wait()
	if strings.TrimSpace(line) != "activated" {
		t.Fatalf("child said %q (%s)", line, rest)
	}
	if _, err := os.Stat(filepath.Join(d, "self")); !errors.Is(err, os.ErrNotExist) {
		t.Error("activated, but it bound its own socket too")
	}
	if _, err := os.Stat(sysPath); err != nil {
		t.Errorf("the activated daemon removed systemd's socket: %v", err)
	}
	if !strings.Contains(rest, "env cleared") {
		t.Errorf("child: %q", rest)
	}
}

func activationChild() {
	os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	l, err := Listen(os.Getenv("NARADA_SOCK_PATH"))
	if err != nil || !l.Activated {
		os.Stdout.WriteString("not activated: " + strings.TrimSpace(func() string {
			if err != nil {
				return err.Error()
			}
			return ""
		}()) + "\n")
		os.Exit(0)
	}
	c, err := l.Accept()
	if err == nil {
		c.Write([]byte("activated\n"))
		c.Close()
	}
	l.Close()
	if os.Getenv("LISTEN_FDS") == "" && os.Getenv("LISTEN_PID") == "" {
		os.Stdout.WriteString("env cleared\n")
	}
	os.Exit(0)
}
