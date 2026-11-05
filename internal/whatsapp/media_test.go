package whatsapp

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

// WhatsApp wants even dimensions and yuv420p; an odd GIF must still convert.
func TestGIFToMP4(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	pal := color.Palette{color.Black, color.White}
	g := &gif.GIF{}
	for i := range 4 {
		f := image.NewPaletted(image.Rect(0, 0, 33, 21), pal)
		f.SetColorIndex(i, i, 1)
		g.Image, g.Delay = append(g.Image, f), append(g.Delay, 10)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "odd.gif")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	mp4, err := convert(context.Background(), p, ".mp4", "-movflags", "faststart", "-pix_fmt", "yuv420p", "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2", "-an")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(mp4)
	out, _ := os.ReadFile(mp4)
	if len(out) < 12 || string(out[4:8]) != "ftyp" {
		t.Fatalf("not an MP4: %d bytes", len(out))
	}
}

// A voice note is Ogg Opus: other audio is converted, Ogg Opus isn't.
func TestVoiceNoteIsOggOpus(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	wav := filepath.Join(dir, "note.wav")
	if b, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", wav).CombinedOutput(); err != nil {
		t.Skip("ffmpeg can't make a test tone: ", string(b))
	}
	if isOggOpus(wav) {
		t.Fatal("a WAV isn't Ogg Opus")
	}
	ogg, err := convert(context.Background(), wav, ".ogg", "-vn", "-ac", "1", "-c:a", "libopus", "-b:a", "32k")
	if err != nil {
		t.Skip("ffmpeg without libopus: ", err)
	}
	defer os.Remove(ogg)
	if !isOggOpus(ogg) {
		t.Fatal("the converted voice note isn't Ogg Opus")
	}
}

func TestReceiptTicks(t *testing.T) {
	if receiptTicks(types.ReceiptTypeRead, false) != TickRead || receiptTicks(types.ReceiptTypeDelivered, false) != TickDelivered {
		t.Fatal("one-to-one")
	}
	if receiptTicks(types.ReceiptTypeRead, true) != TickDelivered {
		t.Fatal("a group shows at most delivered")
	}
	if receiptTicks(types.ReceiptTypeRetry, false) != 0 || historyTicks(4, false) != TickRead || historyTicks(1, false) != 0 {
		t.Fatal("others")
	}
}
