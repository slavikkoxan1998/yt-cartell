package tgbot

// What the poller keeps on disk (Options.StateDir, next to config.json):
//
//	telegram-bot.json  {"bot": <bot id>, "offset": <next update id>} — written
//	                   BEFORE a batch is handled, so a crash or a restart never
//	                   replays a command (at most once); another bot (token)
//	                   starts from Telegram's own unconfirmed updates
//	telegram-bot.lock  {"pid", "beat"} — one poller per computer: a copy of
//	                   the studio whose lock beat < 45 s ago owns the bot

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	stateFile = "telegram-bot.json"
	lockFile  = "telegram-bot.lock"
	lockBeat  = 15 * time.Second
	lockStale = 45 * time.Second
)

type offsetState struct {
	Bot    int64 `json:"bot"`
	Offset int64 `json:"offset"`
}

// loadOffset is the next update id saved for bot (0 = none).
func loadOffset(dir string, bot int64) int64 {
	b, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		return 0
	}
	var st offsetState
	if json.Unmarshal(b, &st) != nil || st.Bot != bot {
		return 0
	}
	return st.Offset
}

// saveOffset writes the next update id atomically.
func saveOffset(dir string, bot, offset int64) error {
	b, _ := json.Marshal(offsetState{Bot: bot, Offset: offset})
	return writeAtomic(filepath.Join(dir, stateFile), b)
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	var err error
	for i := 0; i < 5; i++ { // Windows: a scanner may hold the file a moment
		if err = os.Rename(tmp, path); err == nil {
			return nil
		}
		time.Sleep(time.Duration(30*(i+1)) * time.Millisecond)
	}
	_ = os.Remove(tmp)
	return err
}

// pollLock makes sure one studio per computer polls the bot.
type pollLock struct {
	path string
	pid  int

	mu   sync.Mutex
	held bool
	stop chan struct{}
}

type lockData struct {
	PID  int   `json:"pid"`
	Beat int64 `json:"beat"` // unix ms
}

func newLock(dir string) *pollLock {
	return &pollLock{path: filepath.Join(dir, lockFile), pid: os.Getpid()}
}

// acquire takes the lock unless another live process holds it.
func (l *pollLock) acquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held {
		return true
	}
	if b, err := os.ReadFile(l.path); err == nil {
		var d lockData
		if json.Unmarshal(b, &d) == nil && d.PID != l.pid && time.Since(time.UnixMilli(d.Beat)) < lockStale {
			return false
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return true // unreadable: do not block the bot on it
	}
	if err := l.write(); err != nil {
		return true
	}
	l.held = true
	l.stop = make(chan struct{})
	go l.beat(l.stop)
	return true
}

func (l *pollLock) write() error {
	b, _ := json.Marshal(lockData{PID: l.pid, Beat: time.Now().UnixMilli()})
	return writeAtomic(l.path, b)
}

func (l *pollLock) beat(stop chan struct{}) {
	t := time.NewTicker(lockBeat)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			l.mu.Lock()
			if l.held {
				_ = l.write()
			}
			l.mu.Unlock()
		}
	}
}

// release frees the lock (only ours).
func (l *pollLock) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held {
		return
	}
	l.held = false
	close(l.stop)
	if b, err := os.ReadFile(l.path); err == nil {
		var d lockData
		if json.Unmarshal(b, &d) == nil && d.PID == l.pid {
			_ = os.Remove(l.path)
		}
	}
}
