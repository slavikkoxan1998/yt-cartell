package studio

// Small public hooks for the Telegram remote control (package tgbot): the
// buttons under the "video is ready" message and under a failed job's
// message, the end of every job, «Знайти мій чат» while the bot polls, and
// speech-to-text of a voice message.

import (
	"context"
	"errors"
	"log"
	"os"

	"ytcartell/internal/notify"
	"ytcartell/internal/stt"
)

// TelegramHooks connect the job-end notifications (notify.go) with the bot
// that controls the studio. Every hook is optional.
type TelegramHooks struct {
	// FinalMarkup returns the buttons under the "video is ready" message of
	// a project (nil = none, e.g. the remote control is off).
	FinalMarkup func(projectID string) notify.InlineKeyboard
	// ErrorMarkup returns the buttons under the message of a job that ended
	// with an error (nil = none) — e.g. the montage choices of an autopilot
	// that stopped before the montage (Job.Blocked = BlockedGaps).
	ErrorMarkup func(projectID string, job Job) notify.InlineKeyboard
	// JobEnded runs once per finished job (done, error, stopped) after its
	// notification was handled; notified reports that a Telegram message
	// about it went out. It must return quickly.
	JobEnded func(projectID string, job Job, notified bool)
	// FindChat answers «Знайти мій чат» while the bot reads its updates
	// itself (a second getUpdates reader would cut its long poll).
	FindChat func(ctx context.Context) (chatID, title string, err error)
}

// SetTelegramHooks installs the remote control's hooks (a zero value removes
// them).
func (e *Engine) SetTelegramHooks(h TelegramHooks) {
	t := e.tele()
	t.mu.Lock()
	t.hooks = h
	t.mu.Unlock()
}

func (e *Engine) telegramHooks() TelegramHooks {
	t := e.tele()
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hooks
}

// finalMarkup asks the hook for the buttons of a final video message.
func (e *Engine) finalMarkup(projectID string) (kb notify.InlineKeyboard) {
	h := e.telegramHooks().FinalMarkup
	if h == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[studio:%s] telegram buttons: %v", projectID, r)
			kb = nil
		}
	}()
	return h(projectID)
}

// errorMarkup asks the hook for the buttons of a failed job's message.
func (e *Engine) errorMarkup(projectID string, job Job) (kb notify.InlineKeyboard) {
	h := e.telegramHooks().ErrorMarkup
	if h == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[studio:%s] telegram buttons: %v", projectID, r)
			kb = nil
		}
	}()
	return h(projectID, job)
}

// jobEnded tells the hook about a finished job.
func (e *Engine) jobEnded(projectID string, job Job, notified bool) {
	h := e.telegramHooks().JobEnded
	if h == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[studio:%s] telegram job end: %v", projectID, r)
		}
	}()
	h(projectID, job, notified)
}

// JobLabel names a job kind in Ukrainian ("Кадри сцен", "Монтаж", "Автопілот").
func JobLabel(k JobKind) string { return queueJobLabel(k, false) }

// TranscribeFile turns a recording (a Telegram voice message, any audio
// FFmpeg reads) into text with the speech-to-text service of Settings; lang
// "" = detect. Online services get a compact MP3 first.
func (e *Engine) TranscribeFile(ctx context.Context, path, lang string) (stt.Transcript, error) {
	if e.closing.Load() {
		return stt.Transcript{}, ErrClosing
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return stt.Transcript{}, errors.New("Запис не знайдено")
	}
	client := e.stt()
	upload := path
	if provider, _ := client.Model(); provider != stt.ProviderLocal {
		compact, cleanup, err := e.sttUpload(ctx, path)
		if err != nil {
			return stt.Transcript{}, err
		}
		defer cleanup()
		upload = compact
	}
	tr, err := client.Transcribe(ctx, upload, lang)
	if err != nil {
		return stt.Transcript{}, err
	}
	return tr, nil
}
