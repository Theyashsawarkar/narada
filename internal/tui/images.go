package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/Theyashsawarkar/narada/internal/app"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// Photos, stickers and GIFs show inside the conversation. On kitty (and
// terminals that speak its graphics protocol) they are real images: the
// image goes straight to the terminal (through tmux, to the client's own
// tty, so it works in popups too) and the screen draws kitty's Unicode
// placeholders where it should appear, which any text layout carries.
// Elsewhere they're drawn with coloured half blocks.

const (
	imgOff = iota
	imgBlocks
	imgKitty
)

// program is the running screen, so background work can wake it.
var program *tea.Program

type imgReadyMsg struct{}

type imgEntry struct {
	state      string // fetching, waiting, preparing, ready, error
	err        string
	frames     []string // prepared PNGs: one, or a GIF's frames
	delays     []int    // ms per frame
	pw, ph     int
	id         uint32
	sentCols   int // the placement kitty has (0: none yet)
	sentRows   int
	blocks     []string
	blocksCols int
	video      bool // a video: the first frame, v plays it
}

type imageStore struct {
	mu      sync.Mutex
	entries map[string]*imgEntry
	mode    int
	tty     string // where kitty commands go
	ready   bool
}

var imgs = &imageStore{entries: map[string]*imgEntry{}}

// imageMode decides once how images show: config images = auto, kitty,
// blocks or off; auto is kitty under kitty (also inside tmux), else blocks.
func (st *imageStore) setup(want string) {
	if st.ready {
		return
	}
	st.ready = true
	switch want {
	case "off":
		st.mode = imgOff
		return
	case "blocks":
		st.mode = imgBlocks
		return
	}
	term := os.Getenv("TERM")
	kitty := want == "kitty" || os.Getenv("KITTY_WINDOW_ID") != "" || term == "xterm-kitty" || os.Getenv("TERM_PROGRAM") == "ghostty"
	st.tty = "/dev/tty"
	if os.Getenv("TMUX") != "" {
		out, err := exec.Command("tmux", "display", "-p", "#{client_tty}\t#{client_termname}").Output()
		if err == nil {
			tty, tn, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
			if tty != "" {
				st.tty = tty
			}
			if strings.Contains(tn, "kitty") || strings.Contains(tn, "ghostty") {
				kitty = true
			}
		}
	}
	if kitty {
		st.mode = imgKitty
	} else {
		st.mode = imgBlocks
	}
}

// kittyWrite sends a graphics command to the terminal in one write, so
// it never lands in the middle of a screen update.
func (st *imageStore) kittyWrite(cmds ...string) {
	f, err := os.OpenFile(st.tty, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		st.mode = imgBlocks
		return
	}
	defer f.Close()
	_, _ = f.WriteString(strings.Join(cmds, ""))
}

// forget takes narada's images out of the terminal (when the screen quits).
func (st *imageStore) forget() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.mode != imgKitty {
		return
	}
	var cmds []string
	for _, e := range st.entries {
		if e.sentCols > 0 {
			cmds = append(cmds, fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", e.id))
		}
	}
	if len(cmds) > 0 {
		st.kittyWrite(cmds...)
	}
}

func wake() {
	if program != nil {
		program.Send(imgReadyMsg{})
	}
}

// imageKind: whether a chat file is something to show, and if it's a
// moving picture or a video.
func imageKind(f store.ChatFile) (show, anim, video bool) {
	switch f.Kind {
	case "image", "sticker":
		return true, false, false
	case "gif":
		return true, true, false
	case "video":
		return true, false, true
	case "":
		switch {
		case f.Type == "image/gif":
			return true, true, false
		case strings.HasPrefix(f.Type, "image/"):
			return true, false, false
		}
	}
	return false, false, false
}

// imgWork counts pictures being prepared in the background.
var imgWork sync.WaitGroup

// mediaLines draws a chat message's file as an image at most maxCols
// wide, or nil when it isn't one (or images are off). The first time it
// starts fetching and preparing it in the background.
func (m *Model) mediaLines(account, msgName string, idx int, f store.ChatFile, maxCols int) []string {
	return m.mediaSized(account, msgName, idx, f, min(maxCols, 44), 14)
}

// mediaSized is mediaLines at most maxCols × maxRows (the viewer's size).
func (m *Model) mediaSized(account, msgName string, idx int, f store.ChatFile, maxCols, maxRows int) []string {
	show, anim, video := imageKind(f)
	imgs.setup(m.a.Config.Images)
	if !show || imgs.mode == imgOff {
		return nil
	}
	key := account + "/" + msgName + "/" + fmt.Sprint(idx)
	imgs.mu.Lock()
	e := imgs.entries[key]
	if e == nil {
		e = &imgEntry{state: "fetching", video: video, id: imageID(key)}
		imgs.entries[key] = e
		imgWork.Add(1)
		go func() {
			defer imgWork.Done()
			prepareMedia(m.a, e, account, msgName, idx, f, anim, video)
		}()
	}
	state, errText := e.state, e.err
	imgs.mu.Unlock()
	s := m.styles
	label := map[bool]string{true: "GIF", false: "photo"}[anim]
	if video {
		label = "video"
	}
	switch state {
	case "ready":
	case "error":
		return []string{s.muted.Render("▣ " + label + ": " + errText)}
	case "waiting":
		return []string{s.muted.Render("▣ " + label + " · downloading through WhatsApp…")}
	default:
		return []string{s.muted.Render("▣ " + label + " · loading…")}
	}
	imgs.mu.Lock()
	defer imgs.mu.Unlock()
	lines := e.lines(maxCols, maxRows)
	if video {
		lines = append(append([]string{}, lines...), s.muted.Render("▶ video · v plays it"))
	}
	return lines
}

// fileLines draws a local file (one about to be sent) as a small still
// picture at most maxCols × maxRows, or nil when it isn't an image.
func (m *Model) fileLines(path string, maxCols, maxRows int) []string {
	imgs.setup(m.a.Config.Images)
	if imgs.mode == imgOff || !localImage(path) {
		return nil
	}
	key := "local:" + path
	imgs.mu.Lock()
	e := imgs.entries[key]
	if e == nil {
		e = &imgEntry{state: "preparing", id: imageID(key)}
		imgs.entries[key] = e
		dir := filepath.Join(m.a.Paths.Cache, "media", "view")
		imgWork.Add(1)
		go func() {
			defer imgWork.Done()
			prepareFrom(e, path, dir, false, isVideo(path))
		}()
	}
	defer imgs.mu.Unlock()
	if e.state != "ready" {
		return nil
	}
	return e.lines(maxCols, maxRows)
}

// lines is a ready entry drawn at most maxCols × maxRows (imgs.mu held).
func (e *imgEntry) lines(maxCols, maxRows int) []string {
	cols, rows := fitCells(e.pw, e.ph, maxCols, maxRows)
	if imgs.mode == imgKitty {
		if e.sentCols != cols || e.sentRows != rows {
			imgs.kittyWrite(kittyPlace(e, cols, rows)...)
			e.sentCols, e.sentRows = cols, rows
		}
		return placeholders(e.id, cols, rows)
	}
	if e.blocksCols != cols {
		e.blocks = halfBlocks(e.frames[0], cols, rows)
		e.blocksCols = cols
	}
	return e.blocks
}

// localImage: a file whose name says it is a picture or a video.
func localImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".mp4", ".webm", ".mov", ".mkv":
		return true
	}
	return false
}

// fitCells sizes an image in terminal cells (a cell about twice as tall
// as wide), at most maxCols × maxRows, keeping its shape.
func fitCells(pw, ph, maxCols, maxRows int) (cols, rows int) {
	if pw <= 0 || ph <= 0 {
		return min(maxCols, 20), 6
	}
	cols = maxCols
	rows = max(1, (cols*ph+pw)/(2*pw))
	if rows > maxRows {
		rows = maxRows
		cols = max(2, rows*2*pw/ph)
	}
	if pw < cols*8 { // don't blow small things (stickers, icons) up
		cols = max(4, min(cols, pw/8+1))
		rows = max(1, (cols*ph+pw)/(2*pw))
	}
	return cols, rows
}

func imageID(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	id := h.Sum32() & 0xffffff
	if id == 0 {
		id = 1
	}
	return id
}

// kittyPlace transmits the image (by file: nothing big crosses the
// terminal) as a virtual placement of cols × rows cells, and a GIF's other
// frames, looping.
func kittyPlace(e *imgEntry, cols, rows int) []string {
	b64 := func(p string) string { return base64.StdEncoding.EncodeToString([]byte(p)) }
	cmds := []string{fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", e.id),
		fmt.Sprintf("\x1b_Ga=T,U=1,q=2,f=100,t=f,i=%d,c=%d,r=%d;%s\x1b\\", e.id, cols, rows, b64(e.frames[0]))}
	if len(e.frames) > 1 {
		for i, p := range e.frames[1:] {
			cmds = append(cmds, fmt.Sprintf("\x1b_Ga=f,i=%d,q=2,f=100,t=f,z=%d;%s\x1b\\", e.id, e.delays[i+1], b64(p)))
		}
		cmds = append(cmds, fmt.Sprintf("\x1b_Ga=a,i=%d,r=1,z=%d,q=2\x1b\\", e.id, e.delays[0]),
			fmt.Sprintf("\x1b_Ga=a,i=%d,s=3,v=1,q=2\x1b\\", e.id))
	}
	return cmds
}

// placeholders are the cells kitty fills with image id: U+10EEEE, its row
// and column as diacritics, the id as the foreground colour.
func placeholders(id uint32, cols, rows int) []string {
	fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", id>>16&255, id>>8&255, id&255)
	out := make([]string, rows)
	for r := 0; r < rows && r < len(diacritics); r++ {
		var b strings.Builder
		b.WriteString(fg)
		for c := 0; c < cols && c < len(diacritics); c++ {
			b.WriteRune(0x10EEEE)
			b.WriteRune(diacritics[r])
			b.WriteRune(diacritics[c])
		}
		b.WriteString("\x1b[39m")
		out[r] = b.String()
	}
	return out
}

// halfBlocks draws the image with ▀: two pixels a cell, true colour.
func halfBlocks(path string, cols, rows int) []string {
	img, err := loadPNG(path)
	if err != nil {
		return []string{"▣"}
	}
	small := image.NewRGBA(image.Rect(0, 0, cols, rows*2))
	xdraw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), draw.Over, nil)
	out := make([]string, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		for c := 0; c < cols; c++ {
			t := small.RGBAAt(c, 2*r)
			u := small.RGBAAt(c, 2*r+1)
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", t.R, t.G, t.B, u.R, u.G, u.B)
		}
		b.WriteString("\x1b[0m")
		out[r] = b.String()
	}
	return out
}

func loadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// prepareMedia fetches the file and turns it into PNGs the terminal can
// show (scaled down; a GIF or WhatsApp's MP4 GIF frame by frame; a video's
// first frame), then wakes the screen.
func prepareMedia(a *app.App, e *imgEntry, account, msgName string, idx int, f store.ChatFile, anim, video bool) {
	set := func(state, err string) {
		imgs.mu.Lock()
		e.state, e.err = state, err
		imgs.mu.Unlock()
		wake()
	}
	var src string
	for tries := 0; ; tries++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		p, err := a.ChatFile(ctx, account, msgName, idx)
		cancel()
		if errors.Is(err, app.ErrMediaWaiting) && tries < 90 {
			set("waiting", "")
			time.Sleep(2 * time.Second)
			continue
		}
		if err != nil {
			set("error", shortErr(err))
			return
		}
		src = p
		break
	}
	prepareFrom(e, src, filepath.Join(filepath.Dir(src), "view"), anim, video)
}

// prepareFrom makes src's frames (PNGs in dir) and marks e ready.
func prepareFrom(e *imgEntry, src, dir string, anim, video bool) {
	set := func(state, err string) {
		imgs.mu.Lock()
		e.state, e.err = state, err
		imgs.mu.Unlock()
		wake()
	}
	set("preparing", "")
	_ = os.MkdirAll(dir, 0o700)
	base := filepath.Join(dir, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)))
	var frames []string
	var delays []int
	var err error
	switch {
	case video || anim && isVideo(src):
		n := 1
		if anim {
			n = 60
		}
		frames, delays, err = videoFrames(src, base, n)
	case anim:
		frames, delays, err = gifFrames(src, base)
	default:
		frames, err = stillFrame(src, base)
		delays = []int{0}
	}
	if err != nil {
		set("error", shortErr(err))
		return
	}
	img, err := loadPNG(frames[0])
	if err != nil {
		set("error", shortErr(err))
		return
	}
	imgs.mu.Lock()
	e.frames, e.delays = frames, delays
	e.pw, e.ph = img.Bounds().Dx(), img.Bounds().Dy()
	e.state = "ready"
	imgs.mu.Unlock()
	wake()
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 80 {
		s = s[:79] + "…"
	}
	return s
}

func isVideo(path string) bool {
	b := make([]byte, 12)
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	_, _ = f.Read(b)
	return bytes.Contains(b, []byte("ftyp"))
}

// maxSide is the most pixels an image keeps on its longer side.
const maxSide = 720

func scaleDown(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxSide && h <= maxSide {
		return img
	}
	if w >= h {
		h, w = h*maxSide/w, maxSide
	} else {
		w, h = w*maxSide/h, maxSide
	}
	out := image.NewRGBA(image.Rect(0, 0, max(1, w), max(1, h)))
	xdraw.CatmullRom.Scale(out, out.Bounds(), img, b, draw.Over, nil)
	return out
}

func writePNG(path string, img image.Image) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return err
	}
	tmp := path + ".part"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func stillFrame(src, base string) ([]string, error) {
	out := base + ".png"
	if _, err := os.Stat(out); err == nil {
		return []string{out}, nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("can't show this image (%v)", err)
	}
	return []string{out}, writePNG(out, scaleDown(img))
}

// gifFrames composes a GIF's frames (each drawn over the last, as the
// format says) into whole PNGs.
func gifFrames(src, base string) ([]string, []int, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	g, err := gif.DecodeAll(f)
	if err != nil {
		return nil, nil, fmt.Errorf("can't show this GIF (%v)", err)
	}
	w, h := g.Config.Width, g.Config.Height
	if w == 0 || h == 0 {
		b := g.Image[0].Bounds()
		w, h = b.Dx(), b.Dy()
	}
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	var frames []string
	var delays []int
	step := max(1, len(g.Image)/80) // at most ~80 frames
	for i, fr := range g.Image {
		var prev *image.RGBA
		if i < len(g.Disposal) && g.Disposal[i] == gif.DisposalPrevious {
			prev = image.NewRGBA(canvas.Bounds())
			draw.Draw(prev, prev.Bounds(), canvas, image.Point{}, draw.Src)
		}
		draw.Draw(canvas, fr.Bounds(), fr, fr.Bounds().Min, draw.Over)
		if i%step == 0 {
			p := fmt.Sprintf("%s-%03d.png", base, i)
			if err := writePNG(p, scaleDown(canvas)); err != nil {
				return nil, nil, err
			}
			frames = append(frames, p)
			d := 100
			if i < len(g.Delay) && g.Delay[i] > 0 {
				d = g.Delay[i] * 10 * step
			}
			delays = append(delays, max(20, d))
		}
		if i < len(g.Disposal) {
			switch g.Disposal[i] {
			case gif.DisposalBackground:
				draw.Draw(canvas, fr.Bounds(), image.NewUniform(color.Transparent), image.Point{}, draw.Src)
			case gif.DisposalPrevious:
				draw.Draw(canvas, canvas.Bounds(), prev, image.Point{}, draw.Src)
			}
		}
	}
	if len(frames) == 0 {
		return nil, nil, errors.New("an empty GIF")
	}
	return frames, delays, nil
}

// videoFrames takes up to n frames (10 a second) of a video with ffmpeg:
// WhatsApp's GIFs are MP4s; for a video, its first frame.
func videoFrames(src, base string, n int) ([]string, []int, error) {
	existing, _ := filepath.Glob(base + "-v*.png")
	if len(existing) == 0 {
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return nil, nil, errors.New("showing videos and WhatsApp GIFs needs ffmpeg")
		}
		args := []string{"-loglevel", "error", "-y", "-i", src, "-vf", fmt.Sprintf("fps=10,scale='min(%d,iw)':-2", maxSide), "-frames:v", fmt.Sprint(n), base + "-v%03d.png"}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if b, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
			return nil, nil, fmt.Errorf("ffmpeg: %s", strings.TrimSpace(string(b)))
		}
		existing, _ = filepath.Glob(base + "-v*.png")
	}
	sort.Strings(existing)
	if len(existing) == 0 {
		return nil, nil, errors.New("no frames in this video")
	}
	if len(existing) > n {
		existing = existing[:n]
	}
	delays := make([]int, len(existing))
	for i := range delays {
		delays[i] = 100
	}
	return existing, delays, nil
}
