package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Theyashsawarkar/narada/internal/store"
)

// A voice note for WhatsApp, recorded from the default microphone with
// ffmpeg (PipeWire and PulseAudio both serve "pulse") as the Ogg Opus
// WhatsApp plays.
type recording struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	path  string
	start time.Time
}

// recordCmd starts the recorder; tests swap it.
var recordCmd = func(path string) *exec.Cmd {
	return exec.Command("ffmpeg", "-loglevel", "error", "-y", "-f", "pulse", "-i", "default",
		"-ac", "1", "-c:a", "libopus", "-b:a", "32k", path)
}

func (m *Model) startRecording() error {
	if !m.wa {
		return errors.New("voice notes are for WhatsApp")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil && recordCmd(os.DevNull).Path == "ffmpeg" {
		return errors.New("recording a voice note needs ffmpeg")
	}
	dir := filepath.Join(m.a.Paths.Cache, "voice")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	p := filepath.Join(dir, "voice-"+time.Now().Format("20060102-150405")+".ogg")
	cmd := recordCmd(p)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("recording: %w", err)
	}
	m.rec = &recording{cmd: cmd, stdin: in, path: p, start: time.Now()}
	return nil
}

// stopRecording ends it (ffmpeg finishes the file on "q"); keep adds it
// to the message as a voice note, else it's thrown away.
func (m *Model) stopRecording(keep bool) error {
	r := m.rec
	m.rec = nil
	if r == nil {
		return nil
	}
	_, _ = io.WriteString(r.stdin, "q")
	_ = r.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = r.cmd.Process.Kill()
		<-done
	}
	if !keep {
		os.Remove(r.path)
		return nil
	}
	if st, err := os.Stat(r.path); err != nil || st.Size() == 0 {
		os.Remove(r.path)
		return errors.New("nothing was recorded (is a microphone connected?)")
	}
	m.chatFiles = append(m.chatFiles, store.File{Source: r.path, Name: "voice.ogg", Voice: true})
	return nil
}

// recordingLine is what the box shows while recording.
func (m *Model) recordingLine() string {
	d := time.Since(m.rec.start).Round(time.Second)
	return m.styles.red.Render("● recording") + m.styles.text.Render(fmt.Sprintf(" %d:%02d", int(d.Minutes()), int(d.Seconds())%60)) +
		m.styles.muted.Render(" · ctrl+r stops and adds it · esc drops it")
}
