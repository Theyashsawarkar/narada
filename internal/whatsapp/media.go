package whatsapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Media is what a message carries besides text: enough to download it
// later (Raw) and to show something right away (Thumb).
type Media struct {
	Kind  string // image, sticker, gif, video, voice, audio, document
	Name  string
	Mime  string
	Size  int64
	Thumb []byte // WhatsApp's small JPEG preview, when it sent one
	Raw   []byte // the media part of the message, for Download
}

// media finds the media in a message.
func media(msg *waE2E.Message) *Media {
	keep := func(part *waE2E.Message) []byte { b, _ := proto.Marshal(part); return b }
	switch {
	case msg.GetImageMessage() != nil:
		m := msg.GetImageMessage()
		return &Media{Kind: "image", Name: "photo.jpg", Mime: m.GetMimetype(), Size: int64(m.GetFileLength()), Thumb: m.GetJPEGThumbnail(),
			Raw: keep(&waE2E.Message{ImageMessage: m})}
	case msg.GetStickerMessage() != nil:
		m := msg.GetStickerMessage()
		return &Media{Kind: "sticker", Name: "sticker.webp", Mime: firstNonEmpty(m.GetMimetype(), "image/webp"), Size: int64(m.GetFileLength()),
			Thumb: m.GetPngThumbnail(), Raw: keep(&waE2E.Message{StickerMessage: m})}
	case msg.GetVideoMessage() != nil:
		m := msg.GetVideoMessage()
		kind, name := "video", "video.mp4"
		if m.GetGifPlayback() {
			kind, name = "gif", "gif.mp4"
		}
		return &Media{Kind: kind, Name: name, Mime: firstNonEmpty(m.GetMimetype(), "video/mp4"), Size: int64(m.GetFileLength()), Thumb: m.GetJPEGThumbnail(),
			Raw: keep(&waE2E.Message{VideoMessage: m})}
	case msg.GetAudioMessage() != nil:
		m := msg.GetAudioMessage()
		kind, name := "audio", "audio.ogg"
		if m.GetPTT() {
			kind, name = "voice", "voice.ogg"
		}
		return &Media{Kind: kind, Name: name, Mime: m.GetMimetype(), Size: int64(m.GetFileLength()), Raw: keep(&waE2E.Message{AudioMessage: m})}
	case msg.GetDocumentMessage() != nil:
		m := msg.GetDocumentMessage()
		return &Media{Kind: "document", Name: firstNonEmpty(m.GetFileName(), m.GetTitle(), "document"), Mime: m.GetMimetype(), Size: int64(m.GetFileLength()),
			Thumb: m.GetJPEGThumbnail(), Raw: keep(&waE2E.Message{DocumentMessage: m})}
	}
	return nil
}

// Download fetches and decrypts media described by Media.Raw.
func (cl *Client) Download(ctx context.Context, raw []byte) ([]byte, error) {
	d, err := downloadable(raw)
	if err != nil {
		return nil, err
	}
	b, err := cl.c.Download(ctx, d)
	return b, expired(err)
}

func downloadable(raw []byte) (whatsmeow.DownloadableMessage, error) {
	var msg waE2E.Message
	if err := proto.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	switch {
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage(), nil
	case msg.GetStickerMessage() != nil:
		return msg.GetStickerMessage(), nil
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage(), nil
	case msg.GetAudioMessage() != nil:
		return msg.GetAudioMessage(), nil
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage(), nil
	}
	return nil, errors.New("nothing to download in this message")
}

func expired(err error) error {
	if errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404) || errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410) {
		return errors.New("WhatsApp no longer has this file (it expires after a while; it's still on the phone)")
	}
	return err
}

// KindOf says how a file goes on WhatsApp: image, gif, video, audio,
// sticker or document, from its type and name.
func KindOf(name, mime string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case ext == ".gif" || mime == "image/gif":
		return "gif"
	case ext == ".webp" || mime == "image/webp":
		return "sticker"
	case strings.HasPrefix(mime, "image/") && (ext == ".jpg" || ext == ".jpeg" || ext == ".png"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	}
	return "document"
}

// File is a file to send.
type File struct {
	Path    string
	Name    string // what the other side sees ("": the file's own name)
	Caption string
	Voice   bool // an audio file as a voice note (made Ogg Opus with ffmpeg when it isn't)
}

// sniff is a file's type from its first bytes.
func sniff(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	return http.DetectContentType(head[:n]), st.Size(), nil
}

// SendFile sends a file to chat with an optional caption; quote is the
// message it replies to. A .gif becomes the MP4 WhatsApp plays as a GIF
// (it needs ffmpeg); a .webp goes as a sticker, other images as photos,
// videos as videos, audio as audio (or a voice note), the rest as
// documents. The file is
// streamed, so a 2 GB document doesn't sit in memory. Returns the new
// message's id and what was sent.
func (cl *Client) SendFile(ctx context.Context, chat string, f File, quote *Message) (string, *Media, error) {
	to, err := types.ParseJID(chat)
	if err != nil {
		return "", nil, fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
	}
	name := f.Name
	if name == "" {
		name = filepath.Base(f.Path)
	}
	mime, _, err := sniff(f.Path)
	if err != nil {
		return "", nil, err
	}
	kind := KindOf(name, mime)
	if f.Voice {
		kind = "voice"
	}
	path := f.Path
	switch kind {
	case "gif":
		mp4, err := convert(ctx, f.Path, ".mp4", "-movflags", "faststart", "-pix_fmt", "yuv420p", "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2", "-an")
		if err != nil {
			return "", nil, fmt.Errorf("sending a GIF: %w", err)
		}
		defer os.Remove(mp4)
		path, mime = mp4, "video/mp4"
	case "voice":
		if !isOggOpus(f.Path) {
			ogg, err := convert(ctx, f.Path, ".ogg", "-vn", "-ac", "1", "-c:a", "libopus", "-b:a", "32k")
			if err != nil {
				return "", nil, fmt.Errorf("sending a voice note: %w", err)
			}
			defer os.Remove(ogg)
			path = ogg
		}
		mime = "audio/ogg; codecs=opus"
	}
	if mime == "application/octet-stream" || strings.HasPrefix(mime, "text/plain") {
		if t := mimeByExt(name); t != "" {
			mime = t
		}
	}
	mediaType := map[string]whatsmeow.MediaType{"image": whatsmeow.MediaImage, "gif": whatsmeow.MediaVideo, "video": whatsmeow.MediaVideo,
		"audio": whatsmeow.MediaAudio, "voice": whatsmeow.MediaAudio, "sticker": whatsmeow.MediaImage}[kind]
	// (WhatsApp keeps stickers with the images)
	if mediaType == "" {
		mediaType = whatsmeow.MediaDocument
	}
	in, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	up, err := cl.c.UploadReader(ctx, in, nil, mediaType)
	in.Close()
	if err != nil {
		return "", nil, fmt.Errorf("uploading %s: %w", name, err)
	}
	var ci *waE2E.ContextInfo
	if quote != nil {
		ci = &waE2E.ContextInfo{StanzaID: proto.String(quote.ID), Participant: proto.String(quote.Sender),
			QuotedMessage: &waE2E.Message{Conversation: proto.String(quote.Text)}}
		if quote.Chat != "" && quote.Chat != chat {
			ci.RemoteJID = proto.String(quote.Chat) // replying privately to a group's message
		}
	}
	if exp := cl.expiration(chat); exp > 0 {
		if ci == nil {
			ci = &waE2E.ContextInfo{}
		}
		ci.Expiration = proto.Uint32(uint32(exp))
	}
	n := up.FileLength
	msg := &waE2E.Message{}
	switch kind {
	case "image":
		msg.ImageMessage = &waE2E.ImageMessage{Caption: optional(f.Caption), Mimetype: proto.String(mime), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &n, ContextInfo: ci}
	case "sticker":
		msg.StickerMessage = &waE2E.StickerMessage{Mimetype: proto.String("image/webp"), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &n, ContextInfo: ci}
	case "gif", "video":
		msg.VideoMessage = &waE2E.VideoMessage{Caption: optional(f.Caption), Mimetype: proto.String(mime), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &n, ContextInfo: ci,
			GifPlayback: proto.Bool(kind == "gif")}
	case "audio", "voice":
		msg.AudioMessage = &waE2E.AudioMessage{Mimetype: proto.String(mime), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &n, ContextInfo: ci,
			PTT: proto.Bool(kind == "voice")}
	default:
		msg.DocumentMessage = &waE2E.DocumentMessage{Caption: optional(f.Caption), Mimetype: proto.String(firstNonEmpty(mime, "application/octet-stream")),
			FileName: proto.String(name), Title: proto.String(name), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &n, ContextInfo: ci}
		kind = "document"
	}
	resp, err := cl.c.SendMessage(ctx, to, msg)
	if err != nil {
		return "", nil, err
	}
	sent := media(msg)
	sent.Kind = kind
	if kind == "document" {
		sent.Name = name
	}
	return resp.ID, sent, nil
}

// isOggOpus: the file is already what WhatsApp plays as a voice note.
func isOggOpus(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := io.ReadFull(f, head)
	return bytes.HasPrefix(head[:n], []byte("OggS")) && bytes.Contains(head[:n], []byte("OpusHead"))
}

// DownloadTo fetches and decrypts media described by Media.Raw into path,
// streaming (a 2 GB document doesn't sit in memory).
func (cl *Client) DownloadTo(ctx context.Context, raw []byte, path string) error {
	d, err := downloadable(raw)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	err = cl.c.DownloadToFile(ctx, d, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return expired(err)
	}
	return nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func mimeByExt(name string) string {
	return map[string]string{".pdf": "application/pdf", ".mp4": "video/mp4", ".mov": "video/quicktime", ".mp3": "audio/mpeg", ".ogg": "audio/ogg",
		".m4a": "audio/mp4", ".zip": "application/zip", ".csv": "text/csv", ".txt": "text/plain", ".doc": "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"}[strings.ToLower(filepath.Ext(name))]
}

// convert runs ffmpeg on a file (a GIF to the silent MP4 WhatsApp plays
// as a GIF; audio to the Ogg Opus of a voice note) and returns the new
// file's path (the caller removes it).
func convert(ctx context.Context, path, ext string, args ...string) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", errors.New("it needs ffmpeg")
	}
	out, err := os.CreateTemp("", "narada-*"+ext)
	if err != nil {
		return "", err
	}
	out.Close()
	cmd := exec.CommandContext(ctx, "ffmpeg", append(append([]string{"-y", "-loglevel", "error", "-i", path}, args...), out.Name())...)
	if b, err := cmd.CombinedOutput(); err != nil {
		os.Remove(out.Name())
		return "", fmt.Errorf("ffmpeg: %v %s", err, strings.TrimSpace(string(b)))
	}
	return out.Name(), nil
}

// MediaLabel is the text a message with media shows: "📷 Photo: caption".
func MediaLabel(kind, name, caption string) string {
	label := map[string]string{"image": "📷 Photo", "sticker": "Sticker", "gif": "GIF", "video": "🎬 Video", "voice": "🎤 Voice message",
		"audio": "🎵 Audio"}[kind]
	if label == "" {
		label = "📎 " + firstNonEmpty(name, "Document")
	}
	if caption = strings.TrimSpace(caption); caption != "" {
		return label + ": " + caption
	}
	return label
}
