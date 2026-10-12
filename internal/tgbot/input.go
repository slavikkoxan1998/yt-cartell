package tgbot

// Stories that arrive as files: a voice message (or an audio file) is
// downloaded, recognised with the speech-to-text service of Settings and
// shown for confirmation; a .txt file is read (UTF-8, UTF-16 with a BOM or
// Windows-1251).

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"ytcartell/internal/notify"
)

const (
	maxTextFile   = 1 << 20 // a story file bigger than 1 MB is not a story
	transcribeFor = 5 * time.Minute
)

// download saves a message file into a new temp folder (cleanup removes it).
func (b *Bot) download(ctx context.Context, f *notify.File, name string, max int64) (path string, cleanup func(), err error) {
	c := b.client()
	if !c.Local() && max > notify.MaxDownload {
		max = notify.MaxDownload
	}
	if f.FileSize > max {
		return "", nil, &notify.Error{Code: 413}
	}
	dir, err := os.MkdirTemp("", "ytc-tg-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	info, err := c.GetFile(ctx, f.FileID)
	if err == nil {
		path = filepath.Join(dir, name)
		err = c.Download(ctx, info, path, max)
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

// onVoice recognises a voice message and asks whether to make a project.
func (b *Bot) onVoice(ctx context.Context, s *session, m *notify.Message) {
	f := m.Voice
	ext := ".ogg"
	if f == nil {
		f = m.Audio
		ext = strings.ToLower(filepath.Ext(f.FileName))
		if ext == "" {
			ext = ".mp3"
		}
	}
	if f.FileSize > notify.MaxDownload && !b.client().Local() {
		b.send(ctx, s.chat, "Запис завеликий: я можу прочитати до 20 МБ. Надішли коротший або текстом.", nil)
		return
	}
	msg, _ := b.send(ctx, s.chat, "🎙 Слухаю й розпізнаю…", nil)
	_ = b.client().Action(ctx, s.chat, "typing")
	path, cleanup, err := b.download(ctx, f, "voice"+ext, notify.MaxUploadLocal)
	if err != nil {
		b.show(ctx, s.chat, msg.MessageID, "⚠️ Не вдалося отримати запис: "+esc(errText(err, b.tg().BotToken)), nil)
		return
	}
	defer cleanup()
	tctx, cancel := context.WithTimeout(ctx, transcribeFor)
	tr, err := b.e.TranscribeFile(tctx, path, "")
	cancel()
	if err != nil {
		b.show(ctx, s.chat, msg.MessageID, "⚠️ Не вдалося розпізнати голосове: "+esc(clipName(userErr(err), 300))+
			"\nНадішли історію текстом або перевір «Розпізнавання мови» в налаштуваннях студії.", nil)
		return
	}
	text := strings.TrimSpace(tr.Text)
	if text == "" {
		b.show(ctx, s.chat, msg.MessageID, "🎙 Не почув у записі слів. Спробуй ще раз або надішли текстом.", nil)
		return
	}
	s.pending = text
	if s.flow == flowNewText {
		s.flow = flowNone
	}
	html := "🎙 Ось що я почув:\n<i>" + esc(excerpt(text, 3000)) + "</i>\n\nЗробити з цього відео?"
	b.show(ctx, s.chat, msg.MessageID, html, notify.InlineKeyboard{row(btn("✅ Так, далі", "nw"), btn("✖️ Ні", "nx"))})
}

// onDocument reads a .txt story.
func (b *Bot) onDocument(ctx context.Context, s *session, m *notify.Message) {
	f := m.Document
	ext := strings.ToLower(filepath.Ext(f.FileName))
	if ext != ".txt" && ext != ".md" && !strings.HasPrefix(f.MimeType, "text/") {
		b.send(ctx, s.chat, "Я читаю історії з текстових файлів .txt — або надішли текст повідомленням чи голосовим.", nil)
		return
	}
	if f.FileSize > maxTextFile {
		b.send(ctx, s.chat, "Файл завеликий для історії (до 1 МБ тексту).", nil)
		return
	}
	path, cleanup, err := b.download(ctx, f, "story.txt", maxTextFile)
	if err != nil {
		b.send(ctx, s.chat, "⚠️ Не вдалося отримати файл: "+esc(errText(err, b.tg().BotToken)), nil)
		return
	}
	defer cleanup()
	raw, err := os.ReadFile(path)
	if err != nil {
		b.send(ctx, s.chat, "⚠️ Не вдалося прочитати файл: "+esc(err.Error()), nil)
		return
	}
	text := strings.TrimSpace(decodeText(raw))
	if text == "" {
		b.send(ctx, s.chat, "У файлі немає тексту.", nil)
		return
	}
	if s.flow == flowNewText {
		b.newDraft(ctx, s, text, 0)
		return
	}
	b.offerNew(ctx, s, text, 0)
}

// decodeText reads UTF-8 (with or without a BOM), UTF-16 with a BOM, else
// Windows-1251 (older Ukrainian text files).
func decodeText(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return string(b[3:])
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		le := b[0] == 0xFF
		b = b[2:]
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			if le {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		return string(utf16.Decode(u))
	case utf8.Valid(b):
		return string(b)
	}
	out, err := charmap.Windows1251.NewDecoder().Bytes(b)
	if err != nil {
		return strings.ToValidUTF8(string(b), "")
	}
	return string(out)
}
