package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// UnitPath is where the background sync's systemd user unit lives.
func UnitPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "systemd", "user", "narada.service")
}

// SocketPath is where the agents' socket unit lives, beside UnitPath.
func SocketPath() string { return filepath.Join(filepath.Dir(UnitPath()), "narada-mcp.socket") }

// Unit is the systemd user unit that runs `narada daemon`. It takes the
// agents' socket from narada-mcp.socket (Sockets=), and has 30 s to stop:
// the daemon gives calls still running 20 s of it (TimeoutStopSec).
func Unit(bin string) string {
	return fmt.Sprintf(`[Unit]
Description=narada: Gmail and Google Chat sync
Documentation=https://github.com/Theyashsawarkar/narada
After=network-online.target
StartLimitIntervalSec=0

[Service]
ExecStart=%s daemon
Sockets=narada-mcp.socket
Restart=on-failure
RestartSec=5
RestartSteps=5
RestartMaxDelaySec=60
TimeoutStopSec=30

[Install]
WantedBy=default.target
`, bin)
}

// SocketUnit is narada-mcp.socket, pranali's socket template: systemd
// holds the agents' socket, so it outlives daemon restarts (connections
// wait in its backlog meanwhile) and a connection starts the daemon.
const SocketUnit = `[Unit]
Description=narada: the socket agents reach the daemon on (pranali)
Documentation=https://github.com/Theyashsawarkar/narada

[Socket]
ListenStream=%t/narada/mcp.sock
SocketMode=0600
DirectoryMode=0700
Service=narada.service

[Install]
WantedBy=sockets.target
`

// serviceBinary prefers the narada on PATH (it survives rebuilds and
// updates), else this binary.
func serviceBinary() string {
	if p, err := exec.LookPath("narada"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	return Executable()
}

// InstallService writes, enables and (re)starts the background sync and
// its agents' socket. The running daemon stops first: an older one
// removes its own socket file on the way out, which would be the
// socket unit's by then.
func InstallService() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("no systemd here: run `narada daemon` from your session startup instead")
	}
	if err := os.MkdirAll(filepath.Dir(UnitPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(UnitPath(), []byte(Unit(serviceBinary())), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(SocketPath(), []byte(SocketUnit), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "stop", "narada.service").Run() // not there yet on a first install
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "narada-mcp.socket", "narada.service"},
		{"restart", "narada-mcp.socket"}, {"start", "narada.service"}} {
		if out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// ServiceInstalled: the unit file exists.
func ServiceInstalled() bool {
	_, err := os.Stat(UnitPath())
	return err == nil
}
