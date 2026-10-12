package studio

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// SetCoverImage makes an image the cover picture (Montage.Cover) — a scene
// frame of the project or any picture of the user — instead of drawing one,
// then re-renders the finished cover with its text (Montage.CoverFinal).
// Refused while a cover or autopilot job runs (it would overwrite the cover).
// Synchronous (copies the file): call it from a goroutine.
func (e *Engine) SetCoverImage(id, srcPath string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	var busy JobKind
	if !e.read(st, func(p *Project) bool {
		if j := p.Job; j != nil && j.Status == JobRunning && (j.Kind == JobCover || j.Kind == JobAutopilot) {
			busy = j.Kind
		}
		return true
	}) {
		return ErrNotFound
	}
	if busy != "" {
		return &busyError{busy}
	}
	info, err := os.Stat(srcPath)
	if err != nil || info.IsDir() {
		return fmt.Errorf("файл не знайдено: %s", srcPath)
	}
	if info.Size() > maxImportImage {
		return errors.New("Файл завеликий (максимум 40 МБ)")
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("не вдалося прочитати файл: %w", err)
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		ext := strings.ToLower(filepath.Ext(srcPath))
		if !imageExts[ext] {
			return errors.New("Це не зображення (потрібен PNG, JPG або WEBP)")
		}
		mime = mimeByExt[ext]
	}
	rel, err := e.writeAsset(st, "images", fmt.Sprintf("cover-%s.%s", newID(12), imageExt(mime)), data)
	if err != nil {
		return err
	}
	var drop []string
	if !e.update(st, func(p *Project) {
		c := &p.Montage.Cover
		if c.Path != "" && c.Path != rel {
			drop = append(drop, c.Path)
		}
		c.Path, c.Status, c.Error, c.Stale, c.Progress = rel, StatusDone, "", false, 100
		c.UpdatedAt = nowMs()
	}) {
		e.removeFiles(st, []string{rel})
		return ErrNotFound
	}
	e.discardFiles(st, drop)
	if err := e.ApplyCoverText(id); err != nil && !errors.Is(err, ErrNoCover) {
		return err
	}
	return nil
}
