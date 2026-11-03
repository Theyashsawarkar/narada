package whatsapp

import (
	"context"
	"errors"
	"fmt"
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
	var msg waE2E.Message
	if err := proto.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	var d whatsmeow.DownloadableMessage
	switch {
	case msg.GetImageMessage() != nil:
		d = msg.GetImageMessage()
	case msg.GetStickerMessage() != nil:
		d = msg.GetStickerMessage()
	case msg.GetVideoMessage() != nil:
		d = msg.GetVideoMessage()
	case msg.GetAudioMessage() != nil:
		d = msg.GetAudioMessage()
	case msg.GetDocumentMessage() != nil:
		d = msg.GetDocumentMessage()
	default:
		return nil, errors.New("nothing to download in this message")
	}
	b, err := cl.c.Download(ctx, d)
	if errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404) || errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410) {
		return nil, errors.New("WhatsApp no longer has this file (it expires after a while; it's still on the phone)")
	}
	return b, err
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

// SendFile sends a file to chat as name (what the other side sees; empty:
// the file's own) with an optional caption; quote is the
// message it replies to. A .gif becomes the MP4 WhatsApp plays as a GIF
// (it needs ffmpeg); other images go as photos, videos as videos, the
// rest as documents. Returns the new message's id and what was sent.
func (cl *Client) SendFile(ctx context.Context, chat, path, name, caption string, quote *Message) (string, *Media, error) {
	to, err := types.ParseJID(chat)
	if err != nil {
		return "", nil, fmt.Errorf("%q is not a WhatsApp chat: %w", chat, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	if name == "" {
		name = filepath.Base(path)
	}
	mime := http.DetectContentType(data)
	kind := KindOf(name, mime)
	if kind == "gif" {
		mp4, err := gifToMP4(ctx, path)
		if err != nil {
			return "", nil, err
		}
		data, mime = mp4, "video/mp4"
	}
	if mime == "application/octet-stream" || strings.HasPrefix(mime, "text/plain") {
		if t := mimeByExt(name); t != "" {
			mime = t
		}
	}
	mediaType := map[string]whatsmeow.MediaType{"image": whatsmeow.MediaImage, "gif": whatsmeow.MediaVideo, "video": whatsmeow.MediaVideo,
		"audio": whatsmeow.MediaAudio, "sticker": whatsmeow.MediaImage}[kind]
	if mediaType == "" {
		mediaType = whatsmeow.MediaDocument
	}
	up, err := cl.c.Upload(ctx, data, mediaType)
	if err != nil {
		return "", nil, fmt.Errorf("uploading %s: %w", name, err)
	}
	var ci *waE2E.ContextInfo
	if quote != nil {
		ci = &waE2E.ContextInfo{StanzaID: proto.String(quote.ID), Participant: proto.String(quote.Sender),
			QuotedMessage: &waE2E.Message{Conversation: proto.String(quote.Text)}}
	}
	n := uint64(len(data))
	msg := &waE2E.Message{}
	switch kind {
	case "image", "sticker":
		msg.ImageMessage = &waE2E.ImageMessage{Caption: optional(caption), Mimetype: proto.String(mime), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &n, ContextInfo: ci}
		kind = "image"
	case "gif", "video":
		msg.VideoMessage = &waE2E.VideoMessage{Caption: optional(caption), Mimetype: proto.String(mime), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &n, ContextInfo: ci,
			GifPlayback: proto.Bool(kind == "gif")}
	case "audio":
		msg.AudioMessage = &waE2E.AudioMessage{Mimetype: proto.String(mime), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &n, ContextInfo: ci}
	default:
		msg.DocumentMessage = &waE2E.DocumentMessage{Caption: optional(caption), Mimetype: proto.String(firstNonEmpty(mime, "application/octet-stream")),
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

// gifToMP4 turns a GIF into the silent MP4 WhatsApp sends for GIFs.
func gifToMP4(ctx context.Context, path string) ([]byte, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, errors.New("sending a GIF needs ffmpeg (WhatsApp sends GIFs as MP4)")
	}
	out, err := os.CreateTemp("", "narada-gif-*.mp4")
	if err != nil {
		return nil, err
	}
	out.Close()
	defer os.Remove(out.Name())
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-loglevel", "error", "-i", path, "-movflags", "faststart", "-pix_fmt", "yuv420p",
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2", "-an", out.Name())
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v %s", err, strings.TrimSpace(string(b)))
	}
	return os.ReadFile(out.Name())
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
