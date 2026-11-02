package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Theyashsawarkar/narada/internal/mime"
	"github.com/Theyashsawarkar/narada/internal/store"
)

// Photos and files the screen shows live in a cache (~/.cache/narada/
// media), downloaded once. WhatsApp's need the connection, so a process
// that isn't holding it asks the one that is (media_fetch).

// ErrMediaWaiting: the file is being downloaded elsewhere; ask again soon.
var ErrMediaWaiting = errors.New("downloading…")

// mediaAuto is the largest WhatsApp photo, sticker or GIF downloaded as
// soon as it arrives (bigger ones, and videos and files, when opened).
const mediaAuto = 8 << 20

const waRef = "wa:"

func (a *App) mediaDir() string { return filepath.Join(a.Paths.Cache, "media") }

// mediaPath is where a file of a message is cached.
func (a *App) mediaPath(key, name string) string {
	h := sha256.Sum256([]byte(key))
	ext := strings.ToLower(filepath.Ext(name))
	if len(ext) > 8 {
		ext = ""
	}
	return filepath.Join(a.mediaDir(), hex.EncodeToString(h[:12])+ext)
}

// CachedChatFile is the cached copy of a chat message's file n (0-based),
// when it has been downloaded.
func (a *App) CachedChatFile(account, msgName string, n int) (string, bool) {
	m, err := a.Store.ChatMessage(account, msgName)
	if err != nil || n < 0 || n >= len(m.Files) {
		return "", false
	}
	p := a.mediaPath(account+"/"+msgName+fmt.Sprint(n), m.Files[n].Name)
	_, err = os.Stat(p)
	return p, err == nil
}

// ChatFile returns the cached copy of a chat message's file n, fetching
// it first. For WhatsApp in a process that isn't online it asks the one
// that is and returns ErrMediaWaiting.
func (a *App) ChatFile(ctx context.Context, account, msgName string, n int) (string, error) {
	if p, ok := a.CachedChatFile(account, msgName, n); ok {
		return p, nil
	}
	m, err := a.Store.ChatMessage(account, msgName)
	if err != nil {
		return "", fmt.Errorf("no chat message %s here", msgName)
	}
	if n < 0 || n >= len(m.Files) {
		return "", fmt.Errorf("that message has no file %d", n+1)
	}
	f := m.Files[n]
	var data []byte
	switch {
	case strings.HasPrefix(f.Ref, waRef):
		c := a.whatsApp()
		if c == nil {
			if e := a.Store.MediaFetchError(account, msgName, n); e != "" {
				return "", errors.New(e)
			}
			_ = a.Store.AskMedia(account, msgName, n)
			return "", ErrMediaWaiting
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(f.Ref, waRef))
		if err != nil {
			return "", err
		}
		dl, ok := c.(interface {
			Download(context.Context, []byte) ([]byte, error)
		})
		if !ok {
			return "", errors.New("this WhatsApp can't download files")
		}
		if data, err = dl.Download(ctx, raw); err != nil {
			return "", err
		}
	case f.Ref == "":
		return "", fmt.Errorf("%s is a Drive file: open it in Chat (b)", f.Name)
	default:
		c, err := a.Client(account)
		if err != nil {
			return "", err
		}
		if data, err = c.ChatMedia(ctx, f.Ref); err != nil {
			return "", err
		}
	}
	p := a.mediaPath(account+"/"+msgName+fmt.Sprint(n), f.Name)
	return p, writeCache(p, data)
}

// CachedMailAttachment and MailAttachment: the same for mail attachments.
func (a *App) CachedMailAttachment(account, msgID string, n int, name string) (string, bool) {
	p := a.mediaPath("mail/"+account+"/"+msgID+fmt.Sprint(n), name)
	_, err := os.Stat(p)
	return p, err == nil
}

func (a *App) MailAttachment(ctx context.Context, account, msgID string, n int) (string, error) {
	c, err := a.Client(account)
	if err != nil {
		return "", err
	}
	gm, err := c.GetMessage(ctx, msgID)
	if err != nil {
		return "", err
	}
	name, _, id, _, ok := mime.FindAttachment(gm.Payload, n)
	if !ok {
		return "", fmt.Errorf("message %s has no attachment %d", msgID, n+1)
	}
	if p, ok := a.CachedMailAttachment(account, msgID, n, name); ok {
		return p, nil
	}
	data, err := c.AttachmentData(ctx, msgID, id)
	if err != nil {
		return "", err
	}
	p := a.mediaPath("mail/"+account+"/"+msgID+fmt.Sprint(n), name)
	return p, writeCache(p, data)
}

func writeCache(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".part"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// fetchAsked downloads what other processes asked for (the holder of
// WhatsApp calls it with its deliveries).
func (a *App) fetchAsked(ctx context.Context) {
	if !a.WhatsAppOnline() {
		return
	}
	asked, _ := a.Store.MediaAsked(4)
	for _, f := range asked {
		fctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, err := a.ChatFile(fctx, f.Account, f.Msg, f.Idx)
		cancel()
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		_ = a.Store.MediaFetched(f.Account, f.Msg, f.Idx, msg)
	}
}

// autoFetch downloads a new WhatsApp photo, sticker or GIF right away, so
// it shows when you open the chat.
func (a *App) autoFetch(ctx context.Context, msgName string, f store.ChatFile) {
	switch f.Kind {
	case "image", "sticker", "gif":
	default:
		return
	}
	if f.Size > mediaAuto {
		return
	}
	fctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, _ = a.ChatFile(fctx, store.WhatsAppAccount, msgName, 0)
}
