package tgbot

// Files into the chat: the final video (sendVideo with supports_streaming,
// duration, size, the cover as thumbnail and cover; a progress message
// «Надсилаю… 42 %» edited in place), subtitles and the full-quality cover as
// documents, an album of up to ten frames, one voice line as audio.
//
// Telegram takes uploads of at most 50 MB from bots (2000 MB through an own
// Bot API server). A bigger video gets two offers: «Стиснути» — a copy
// re-encoded with FFmpeg to fit (two-pass x264, the bitrate from the length,
// 720p when the budget is small) — or «Частинами» — stream-copied segments
// of ≤ 49 MB, numbered. Temporary files live in the system temp folder and go
// once sent. One delivery at a time (FFmpeg and the uplink are shared).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"ytcartell/internal/media"
	"ytcartell/internal/notify"
	"ytcartell/internal/studio"
)

const (
	// progressGap: the progress message is edited at most this often.
	progressGap = 2 * time.Second
)

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// fileName makes a safe file name from a title ("Стежка додому.mp4").
func fileName(title, ext string) string {
	t := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return ' '
		}
		return r
	}, title)
	t = strings.Join(strings.Fields(t), " ")
	t = strings.Trim(clipName(t, 60), " .…")
	if t == "" {
		t = "video"
	}
	return t + ext
}

// background runs a delivery off the chat's worker.
func (b *Bot) background(fn func(ctx context.Context)) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[tgbot] delivery panic: %v", r)
			}
		}()
		fn(b.ctx)
	}()
}

// heavyTask runs fn as the one delivery at a time; false (and a note) when
// another one is busy.
func (b *Bot) heavyTask(ctx context.Context, chat string, fn func(ctx context.Context)) bool {
	select {
	case b.heavy <- struct{}{}:
	default:
		b.send(ctx, chat, "⏳ Зачекай — я ще надсилаю попереднє відео.", nil)
		return false
	}
	b.background(func(ctx context.Context) {
		defer func() { <-b.heavy }()
		fn(ctx)
	})
	return true
}

// ---- the video ------------------------------------------------------------------------

// videoAction handles «Надіслати відео» (v), «Стиснути» (vz), «Частинами» (vp).
func (b *Bot) videoAction(c *press, p studio.Project, verb string) {
	if !videoReady(p) {
		c.toast("Готового відео ще немає — спершу змонтуй його", true)
		return
	}
	path := b.e.Abs(p.ID, p.Montage.Output.Path)
	if !isFile(path) {
		c.toast("Файл відео не знайдено на комп'ютері — змонтуй його ще раз", true)
		return
	}
	c.toast("Готую відео…", false)
	b.heavyTask(c.ctx, c.s.chat, func(ctx context.Context) { b.deliver(ctx, c.s.chat, p, path, verb) })
}

// deliver sends the final video of p.
func (b *Bot) deliver(ctx context.Context, chat string, p studio.Project, path, verb string) {
	st, err := os.Stat(path)
	if err != nil {
		b.send(ctx, chat, "⚠️ Файл відео не вдалося прочитати: "+esc(err.Error()), nil)
		return
	}
	client := b.client()
	limit := b.uploadLimit(client)
	direct := limit - headroom(limit)
	k := key(p.ID)
	switch {
	case verb == "v" && st.Size() <= direct:
		b.uploadVideo(ctx, chat, p, path, "")
	case verb == "v":
		html := fmt.Sprintf("🎬 %s важить %s, а Telegram приймає від ботів файли до %s.\n"+
			"• <b>Стиснути</b> — зроблю копію, що вміститься (якість трохи нижча; 720p, якщо треба).\n"+
			"• <b>Частинами</b> — надішлю ≈ %s по ≤ %s.",
			title(p.Title), notify.FormatSize(st.Size()), notify.FormatSize(limit),
			count(int((st.Size()+partSize(limit)-1)/partSize(limit)), "файл", "файли", "файлів"), notify.FormatSize(partSize(limit)))
		if !client.Local() && b.opt.UploadLimit == 0 {
			html += "\n\nПорада: свій Bot API сервер знімає це обмеження (до 2000 МБ) — Налаштування → Telegram → «Свій Bot API сервер»."
		}
		b.send(ctx, chat, html, &notify.SendOptions{Markup: notify.InlineKeyboard{row(
			btn("🗜 Стиснути до "+notify.FormatSize(limit), "vz:"+k), btn("✂️ Надіслати частинами", "vp:"+k))}})
	case verb == "vz":
		b.compressAndSend(ctx, chat, p, path, st.Size(), limit)
	case verb == "vp":
		b.sendParts(ctx, chat, p, path, st.Size(), limit)
	}
}

// headroom keeps uploads safely under the limit (multipart headers, the
// rounding of "50 MB"): 1 MB, less for tiny limits.
func headroom(limit int64) int64 { return min(1<<20, limit/25) }

// partSize is the biggest part «Частинами» makes.
func partSize(limit int64) int64 { return limit - headroom(limit) }

// probe reads a video's length and size (zero values when FFmpeg is missing).
func (b *Bot) probe(ctx context.Context, path string) media.ProbeInfo {
	ff := b.e.FF()
	if ff.FFprobe == "" {
		return media.ProbeInfo{}
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := ff.Probe(pctx, path)
	if err != nil {
		return media.ProbeInfo{}
	}
	return info
}

// thumbs prepares the video's thumbnail (≤ 320 px JPEG) and its cover.
func (b *Bot) thumbs(p studio.Project) (thumb, cover []byte) {
	pic := b.coverPath(p)
	if pic != "" {
		cover, _ = notify.PhotoJPEG(pic)
	} else {
		for _, s := range p.Scenes {
			if s.Frame.Status == studio.StatusDone && s.Frame.Path != "" {
				if f := b.e.Abs(p.ID, s.Frame.Path); isFile(f) {
					pic = f
					break
				}
			}
		}
	}
	if pic != "" {
		thumb, _ = notify.ThumbJPEG(pic)
	}
	return thumb, cover
}

// progressMsg is a message that shows an upload's or an encode's progress.
type progressMsg struct {
	b    *Bot
	chat string
	id   int
	head string // "⬆️ Надсилаю відео «…»"
	pct  atomic.Int64
	stop chan struct{}
	done chan struct{}
}

// startProgress sends the progress message and edits it every few seconds.
func (b *Bot) startProgress(ctx context.Context, chat, head, action string) *progressMsg {
	pm := &progressMsg{b: b, chat: chat, head: head, stop: make(chan struct{}), done: make(chan struct{})}
	m, err := b.send(ctx, chat, head+"\n"+bar(0), &notify.SendOptions{Silent: true})
	if err == nil {
		pm.id = m.MessageID
	}
	go func() {
		defer close(pm.done)
		t := time.NewTicker(progressGap)
		defer t.Stop()
		shown, acted := int64(0), time.Time{}
		for {
			select {
			case <-pm.stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if action != "" && time.Since(acted) > 4*time.Second {
					_ = b.client().Action(ctx, chat, action)
					acted = time.Now()
				}
				pct := pm.pct.Load()
				if pm.id == 0 || pct == shown || !b.pace.try(chat) {
					continue
				}
				if err := b.client().Edit(ctx, chat, pm.id, pm.head+"\n"+bar(int(pct)), nil); err == nil {
					shown = pct
				}
			}
		}
	}()
	return pm
}

func (pm *progressMsg) set(pct int) { pm.pct.Store(int64(max(0, min(100, pct)))) }

// phase switches the message to another stage (compressing → sending).
func (pm *progressMsg) phase(ctx context.Context, head string) {
	pm.head = head
	pm.pct.Store(0)
	if pm.id > 0 {
		pm.b.edit(ctx, pm.chat, pm.id, head+"\n"+bar(0), nil)
	}
}

// end stops the updates; with html the message shows it (else it goes away).
func (pm *progressMsg) end(ctx context.Context, html string, kb notify.InlineKeyboard) {
	close(pm.stop)
	<-pm.done
	if pm.id == 0 {
		if html != "" {
			pm.b.send(ctx, pm.chat, html, &notify.SendOptions{Markup: kb})
		}
		return
	}
	if html == "" {
		_ = pm.b.remove(ctx, pm.chat, pm.id)
		return
	}
	pm.b.edit(ctx, pm.chat, pm.id, html, kb)
}

// uploadVideo sends one mp4 (part "" = the whole film, else "частина 2 з 3").
func (b *Bot) uploadVideo(ctx context.Context, chat string, p studio.Project, path, part string) bool {
	pm := b.startProgress(ctx, chat, "⬆️ Надсилаю відео "+title(p.Title)+partNote(part)+"…", "upload_video")
	err := b.sendVideoFile(ctx, chat, p, path, part, func(sent, total int64) {
		if total > 0 {
			pm.set(int(sent * 100 / total))
		}
	})
	if err != nil {
		pm.end(ctx, "⚠️ Не вдалося надіслати відео: "+esc(errText(err, b.tg().BotToken)),
			notify.InlineKeyboard{row(btn("🔁 Ще раз", "v:"+key(p.ID)))})
		return false
	}
	pm.end(ctx, "", nil)
	return true
}

func partNote(part string) string {
	if part == "" {
		return ""
	}
	return " · " + part
}

// sendVideoFile uploads path with sendVideo (once more after a 429).
func (b *Bot) sendVideoFile(ctx context.Context, chat string, p studio.Project, path, part string, progress func(sent, total int64)) error {
	info := b.probe(ctx, path)
	dur := info.Duration
	if dur <= 0 && part == "" {
		dur = p.Montage.Output.Duration
	}
	params := map[string]string{"supports_streaming": "true"}
	if dur > 0 {
		params["duration"] = strconv.Itoa(int(math.Round(dur)))
	}
	if info.Width > 0 && info.Height > 0 {
		params["width"], params["height"] = strconv.Itoa(info.Width), strconv.Itoa(info.Height)
	}
	thumb, cover := b.thumbs(p)
	caption := "🎬 " + title(p.Title)
	if dur > 0 {
		caption += " · " + clock(dur)
	}
	caption += partNote(part)
	name := fileName(p.Title, ".mp4")
	if part != "" {
		name = fileName(p.Title+" "+strings.Fields(strings.TrimPrefix(part, "частина "))[0], ".mp4")
	}
	up := notify.Upload{Method: "sendVideo", Field: "video", ChatID: chat, Path: path, Name: name, MIME: "video/mp4",
		Caption: caption, Params: params, Thumb: thumb, Cover: cover, Progress: progress}
	return b.retry(ctx, chat, func() error {
		_, err := b.client().Upload(ctx, up)
		return err
	})
}

// compressAndSend re-encodes the film to fit limit and sends the copy.
func (b *Bot) compressAndSend(ctx context.Context, chat string, p studio.Project, path string, size, limit int64) {
	ff := b.e.FF()
	if !ff.OK() {
		b.send(ctx, chat, "⚠️ Щоб стиснути відео, потрібен FFmpeg — встанови його в налаштуваннях студії.", nil)
		return
	}
	info := b.probe(ctx, path)
	if info.Duration <= 0 {
		info.Duration = p.Montage.Output.Duration
	}
	dir, err := os.MkdirTemp("", "ytc-tg-")
	if err != nil {
		b.send(ctx, chat, "⚠️ Не вдалося створити тимчасову папку: "+esc(err.Error()), nil)
		return
	}
	defer os.RemoveAll(dir)
	pm := b.startProgress(ctx, chat, "🗜 Стискаю відео "+title(p.Title)+"…", "upload_video")
	out := filepath.Join(dir, "video.mp4")
	err = compress(ctx, ff, path, dir, out, info, limit-2*headroom(limit), func(f float64) { pm.set(int(f * 100)) })
	if err == nil {
		if st, serr := os.Stat(out); serr != nil || st.Size() > limit-headroom(limit) {
			err = errors.New("стиснуте відео все одно завелике — надішли його частинами")
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			pm.end(context.Background(), "", nil)
			return
		}
		pm.end(ctx, "⚠️ Не вдалося стиснути відео: "+esc(clipName(err.Error(), 300)),
			notify.InlineKeyboard{row(btn("✂️ Надіслати частинами", "vp:"+key(p.ID)))})
		return
	}
	pm.phase(ctx, "⬆️ Надсилаю стиснуте відео "+title(p.Title)+"…")
	err = b.sendVideoFile(ctx, chat, p, out, "", func(sent, total int64) {
		if total > 0 {
			pm.set(int(sent * 100 / total))
		}
	})
	if err != nil {
		pm.end(ctx, "⚠️ Не вдалося надіслати відео: "+esc(errText(err, b.tg().BotToken)),
			notify.InlineKeyboard{row(btn("🔁 Ще раз", "vz:"+key(p.ID)))})
		return
	}
	pm.end(ctx, "", nil)
}

// sendParts cuts the film into parts under the limit and sends each.
func (b *Bot) sendParts(ctx context.Context, chat string, p studio.Project, path string, size, limit int64) {
	ff := b.e.FF()
	if !ff.OK() {
		b.send(ctx, chat, "⚠️ Щоб розрізати відео, потрібен FFmpeg — встанови його в налаштуваннях студії.", nil)
		return
	}
	info := b.probe(ctx, path)
	if info.Duration <= 0 {
		info.Duration = p.Montage.Output.Duration
	}
	dir, err := os.MkdirTemp("", "ytc-tg-")
	if err != nil {
		b.send(ctx, chat, "⚠️ Не вдалося створити тимчасову папку: "+esc(err.Error()), nil)
		return
	}
	defer os.RemoveAll(dir)
	pm := b.startProgress(ctx, chat, "✂️ Ріжу відео "+title(p.Title)+" на частини…", "upload_video")
	parts, err := split(ctx, ff, path, dir, size, info.Duration, partSize(limit))
	if err != nil {
		if ctx.Err() != nil {
			pm.end(context.Background(), "", nil)
			return
		}
		pm.end(ctx, "⚠️ Не вдалося розрізати відео: "+esc(clipName(err.Error(), 300)), nil)
		return
	}
	pm.end(ctx, "", nil)
	for i, part := range parts {
		label := fmt.Sprintf("частина %d з %d", i+1, len(parts))
		if !b.uploadVideo(ctx, chat, p, part, label) {
			return
		}
	}
}

// ---- documents, pictures, voices -------------------------------------------------------

// sendDocument uploads a file as a document (no compression).
func (b *Bot) sendDocument(ctx context.Context, chat, path, name, mime, caption string) error {
	up := notify.Upload{Method: "sendDocument", Field: "document", ChatID: chat, Path: path, Name: name, MIME: mime, Caption: caption}
	return b.retry(ctx, chat, func() error {
		_, err := b.client().Upload(ctx, up)
		return err
	})
}

// sendSRT sends the film's subtitles.
func (b *Bot) sendSRT(ctx context.Context, chat string, p studio.Project) {
	path := b.e.Abs(p.ID, p.Montage.SRTPath)
	if p.Montage.SRTPath == "" || !isFile(path) {
		b.send(ctx, chat, "Субтитрів ще немає — вони з'являються після монтажу з субтитрами.", nil)
		return
	}
	b.background(func(ctx context.Context) {
		if err := b.sendDocument(ctx, chat, path, fileName(p.Title, ".srt"), "application/x-subrip", "📄 Субтитри "+title(p.Title)); err != nil {
			b.send(ctx, chat, "⚠️ Не вдалося надіслати субтитри: "+esc(errText(err, b.tg().BotToken)), nil)
		}
	})
}

// coverAction sends the cover in full quality, or offers to draw one.
func (b *Bot) coverAction(c *press, p studio.Project) {
	if path := b.coverPath(p); path != "" {
		c.toast("Надсилаю обкладинку…", false)
		chat := c.s.chat
		b.background(func(ctx context.Context) {
			if err := b.sendDocument(ctx, chat, path, fileName(p.Title+" — обкладинка", filepath.Ext(path)), "",
				"🖼 Обкладинка "+title(p.Title)+" в повній якості"); err != nil {
				b.send(ctx, chat, "⚠️ Не вдалося надіслати обкладинку: "+esc(errText(err, b.tg().BotToken)), nil)
			}
		})
		return
	}
	if err := b.e.Run(p.ID, studio.StageCover, false, ""); err != nil {
		c.toast(clipName(userErr(err), 190), true)
		return
	}
	c.toast("🎨 Малюю обкладинку", false)
	if q, ok := b.e.Get(p.ID); ok {
		html, kb := b.card(q)
		b.live.watch(p.ID, c.s.chat, c.show(html, kb))
	}
}

// frames lists the finished scene frames (absolute paths, scene order).
func (b *Bot) frames(p studio.Project) []string {
	var out []string
	for _, s := range p.Scenes {
		if s.Frame.Status == studio.StatusDone && s.Frame.Path != "" {
			if f := b.e.Abs(p.ID, s.Frame.Path); isFile(f) {
				out = append(out, f)
			}
		}
	}
	return out
}

// sendFrames sends an album of up to ten frames from from.
func (b *Bot) sendFrames(ctx context.Context, chat string, p studio.Project, from int) {
	all := b.frames(p)
	if len(all) == 0 {
		b.send(ctx, chat, "Кадрів ще немає.", nil)
		return
	}
	from = max(0, min(from, len(all)-1))
	to := min(len(all), from+notify.MaxAlbum)
	batch := all[from:to]
	k := key(p.ID)
	b.background(func(ctx context.Context) {
		caption := fmt.Sprintf("🖼 %s · кадри %d–%d з %d", title(p.Title), from+1, to, len(all))
		if !b.pace.wait(ctx, chat) {
			return
		}
		_ = b.client().Action(ctx, chat, "upload_photo")
		err := b.client().SendAlbum(ctx, chat, batch, caption)
		if d := notify.RetryAfter(err); d > 0 && d < time.Minute {
			b.pace.block(chat, d)
			if b.pace.wait(ctx, chat) {
				err = b.client().SendAlbum(ctx, chat, batch, caption)
			}
		}
		if err != nil {
			b.send(ctx, chat, "⚠️ Не вдалося надіслати кадри: "+esc(errText(err, b.tg().BotToken)), nil)
			return
		}
		if to < len(all) {
			b.send(ctx, chat, fmt.Sprintf("Показано %d з %d кадрів.", to, len(all)),
				&notify.SendOptions{Silent: true, Markup: notify.InlineKeyboard{row(btn("▶️ Наступні кадри", fmt.Sprintf("pf:%s:%d", k, to)), btn("📂 Проєкт", "p:"+k))}})
		}
	})
}

const voicesPage = 8

// voicesView lists the scenes with a finished voice line.
func (b *Bot) voicesView(p studio.Project, page int) (string, notify.InlineKeyboard) {
	var idx []int
	for i, s := range p.Scenes {
		if s.Audio.Status == studio.StatusDone && s.Audio.Path != "" {
			idx = append(idx, i)
		}
	}
	k := key(p.ID)
	if len(idx) == 0 {
		return "🎙 Озвучки в " + title(p.Title) + " ще немає.", notify.InlineKeyboard{row(btn("📂 Проєкт", "p:"+k))}
	}
	pages := (len(idx) + voicesPage - 1) / voicesPage
	page = max(0, min(page, pages-1))
	html := "🎙 Озвучка " + title(p.Title) + "\nВибери сцену — надішлю її голос."
	var kb notify.InlineKeyboard
	for _, i := range idx[page*voicesPage : min(len(idx), (page+1)*voicesPage)] {
		s := p.Scenes[i]
		text := s.Narration
		if text == "" {
			text = s.Title
		}
		kb = append(kb, row(btn(fmt.Sprintf("%d · %s", i+1, excerpt(text, 40)), fmt.Sprintf("pw:%s:%d", k, i))))
	}
	var nav []notify.Button
	if page > 0 {
		nav = append(nav, btn("◀️", fmt.Sprintf("pv:%s:%d", k, page-1)))
	}
	nav = append(nav, btn("📂 Проєкт", "p:"+k))
	if page < pages-1 {
		nav = append(nav, btn("▶️", fmt.Sprintf("pv:%s:%d", k, page+1)))
	}
	return html, append(kb, nav)
}

// sendVoice sends one scene's voice line as audio.
func (b *Bot) sendVoice(ctx context.Context, chat string, p studio.Project, i int) {
	if i < 0 || i >= len(p.Scenes) || p.Scenes[i].Audio.Path == "" {
		b.send(ctx, chat, "Цієї озвучки вже немає.", nil)
		return
	}
	s := p.Scenes[i]
	path := b.e.Abs(p.ID, s.Audio.Path)
	if !isFile(path) {
		b.send(ctx, chat, "Файл озвучки не знайдено на комп'ютері.", nil)
		return
	}
	params := map[string]string{"title": fmt.Sprintf("Сцена %d", i+1), "performer": clipName(p.Title, 60)}
	if s.Audio.Duration > 0 {
		params["duration"] = strconv.Itoa(int(math.Round(s.Audio.Duration)))
	}
	caption := fmt.Sprintf("🎙 Сцена %d · %s", i+1, title(p.Title))
	if t := strings.TrimSpace(s.Narration); t != "" {
		caption += "\n<i>" + esc(excerpt(t, 800)) + "</i>"
	}
	up := notify.Upload{Method: "sendAudio", Field: "audio", ChatID: chat, Path: path,
		Name: fmt.Sprintf("scene-%02d%s", i+1, filepath.Ext(path)), Caption: caption, Params: params}
	b.background(func(ctx context.Context) {
		err := b.retry(ctx, chat, func() error {
			_, err := b.client().Upload(ctx, up)
			return err
		})
		if err != nil {
			b.send(ctx, chat, "⚠️ Не вдалося надіслати озвучку: "+esc(errText(err, b.tg().BotToken)), nil)
		}
	})
}

// partsIn lists the parts split wrote, in order.
func partsIn(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "part*.mp4"))
	sort.Strings(m)
	return m
}
