package dictate

// OpenAI Realtime API, transcription session (GA; verified 2026-10-02 against
// https://developers.openai.com/api/docs/guides/realtime-transcription, the
// realtime client/server event references and the VAD guide):
//
//	wss://api.openai.com/v1/realtime?intent=transcription   Authorization: Bearer <key>
//	   (no OpenAI-Beta header on the GA interface; the model goes into the
//	   session, not into ?model=)
//	→ {"type":"session.update","session":{"type":"transcription","audio":{"input":{
//	     "format":{"type":"audio/pcm","rate":24000},          (24 kHz only: we upsample)
//	     "transcription":{"model":"gpt-live-transcribe","languages":["uk"]},
//	     "turn_detection":null, "noise_reduction":{"type":"near_field"}}}}}
//	→ {"type":"input_audio_buffer.append","audio":"<base64 PCM16 LE>"} · {"type":"input_audio_buffer.commit"}
//	← session.created · session.updated · input_audio_buffer.committed {item_id, previous_item_id}
//	  · conversation.item.input_audio_transcription.delta {item_id, delta}
//	  · ….completed {item_id, transcript} · ….failed {item_id, error} · error {error:{type, code, message}}
//
// gpt-live-transcribe and gpt-realtime-whisper stream deltas while audio
// arrives but have no server VAD: turn_detection must be null and the client
// commits each turn (here: after a 0.6 s pause, or every 20 s). The other
// models (gpt-transcribe, gpt-4o-transcribe …) use server_vad. Completion
// order across turns is not guaranteed: text is kept per item_id and released
// in the order the items were committed. gpt-live-transcribe and
// gpt-transcribe take `languages` (an array), the older models `language`.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

type openaiRec struct{ p Plan }

func (r *openaiRec) finishTime() time.Duration { return finishStream + 2*time.Second }

// liveModel: deltas while speaking, manual commits.
func liveModel(m string) bool {
	m = strings.ToLower(m)
	return strings.HasPrefix(m, "gpt-live-transcribe") || strings.HasPrefix(m, "gpt-realtime-whisper")
}

// languagesArray: the model takes "languages" instead of "language".
func languagesArray(m string) bool {
	m = strings.ToLower(m)
	return strings.HasPrefix(m, "gpt-live-transcribe") || strings.HasPrefix(m, "gpt-transcribe")
}

// realtimeURL turns the API base into the transcription WebSocket.
func realtimeURL(base string) string {
	b := strings.TrimRight(base, "/")
	switch {
	case strings.HasPrefix(b, "https://"):
		b = "wss://" + strings.TrimPrefix(b, "https://")
	case strings.HasPrefix(b, "http://"):
		b = "ws://" + strings.TrimPrefix(b, "http://")
	}
	return b + "/realtime?intent=transcription"
}

func (r *openaiRec) sessionUpdate() []byte {
	model := firstNonEmpty(r.p.Model, OpenAIRealtimeDef)
	tr := map[string]any{"model": model}
	if l := r.p.Language; len(l) == 2 || (languagesArray(model) && l != "") {
		if languagesArray(model) {
			tr["languages"] = []string{l}
		} else {
			tr["language"] = l
		}
	}
	if strings.HasPrefix(strings.ToLower(model), "gpt-realtime-whisper") {
		tr["delay"] = "low"
	}
	var turn any // null: we commit
	if !liveModel(model) {
		turn = map[string]any{"type": "server_vad", "threshold": 0.5, "prefix_padding_ms": 300, "silence_duration_ms": 500}
	}
	b, _ := json.Marshal(map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type": "transcription",
			"audio": map[string]any{"input": map[string]any{
				"format":          map[string]any{"type": "audio/pcm", "rate": 24000},
				"transcription":   tr,
				"turn_detection":  turn,
				"noise_reduction": map[string]any{"type": "near_field"},
			}},
		},
	})
	return b
}

// items keeps the transcript of every turn and releases finished ones in order.
type items struct {
	mu    sync.Mutex
	order []string
	text  map[string]string
	done  map[string]bool
	next  int // order[:next] are released
}

func newItems() *items { return &items{text: map[string]string{}, done: map[string]bool{}} }

// see places id in the order (after prev when known).
func (it *items) see(id string, prev *string) {
	if id == "" {
		return
	}
	for _, x := range it.order {
		if x == id {
			return
		}
	}
	if prev != nil && *prev != "" {
		for i, x := range it.order {
			if x == *prev && i+1 >= it.next {
				it.order = append(it.order[:i+1], append([]string{id}, it.order[i+1:]...)...)
				return
			}
		}
	}
	it.order = append(it.order, id)
}

// release returns the finals now in order and the interim text after them.
func (it *items) release() (finals []string, interim string) {
	for it.next < len(it.order) && it.done[it.order[it.next]] {
		finals = append(finals, it.text[it.order[it.next]])
		it.next++
	}
	var b []string
	for _, id := range it.order[it.next:] {
		if t := strings.TrimSpace(it.text[id]); t != "" {
			b = append(b, t)
		}
	}
	return finals, strings.Join(b, " ")
}

func (it *items) pending() bool { return it.next < len(it.order) }

func (r *openaiRec) run(ctx context.Context, audio <-chan []int16, out sink) error {
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+r.p.Key)
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, err := wsDial(dctx, realtimeURL(firstNonEmpty(r.p.BaseURL, openaiBase)), hdr)
	cancel()
	if err != nil {
		return dialErr(ctx, "OpenAI", err)
	}
	defer conn.shut()
	stop := context.AfterFunc(ctx, conn.shut)
	defer stop()
	if err := conn.WriteText(r.sessionUpdate()); err != nil {
		return dialErr(ctx, "OpenAI", err)
	}
	model := firstNonEmpty(r.p.Model, OpenAIRealtimeDef)
	live := liveModel(model)

	it := newItems()
	readErr := make(chan error, 1)
	news := make(chan struct{}, 1)
	ping := func() {
		select {
		case news <- struct{}{}:
		default:
		}
	}
	var stMu sync.Mutex
	ready, commits := false, 0 // commits sent and not yet acknowledged
	go func() {
		for {
			_, msg, err := conn.Read()
			if err != nil {
				readErr <- err
				return
			}
			var ev struct {
				Type       string  `json:"type"`
				ItemID     string  `json:"item_id"`
				Previous   *string `json:"previous_item_id"`
				Delta      string  `json:"delta"`
				Transcript string  `json:"transcript"`
				Error      *struct {
					Type    string `json:"type"`
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(msg, &ev) != nil {
				continue
			}
			changed := false
			it.mu.Lock()
			switch ev.Type {
			case "session.updated":
				stMu.Lock()
				ready = true
				stMu.Unlock()
			case "input_audio_buffer.committed":
				it.see(ev.ItemID, ev.Previous)
				stMu.Lock()
				commits = max(0, commits-1)
				stMu.Unlock()
				changed = true
			case "conversation.item.input_audio_transcription.delta":
				it.see(ev.ItemID, nil)
				it.text[ev.ItemID] += ev.Delta
				changed = true
			case "conversation.item.input_audio_transcription.completed":
				it.see(ev.ItemID, nil)
				it.text[ev.ItemID], it.done[ev.ItemID] = ev.Transcript, true
				changed = true
			case "conversation.item.input_audio_transcription.failed":
				it.see(ev.ItemID, nil)
				it.done[ev.ItemID] = true // keep what the deltas said
				changed = true
			case "error":
				code, msgText := "", ""
				if ev.Error != nil {
					code, msgText = ev.Error.Code, ev.Error.Message
				}
				stMu.Lock()
				setup := !ready
				if strings.Contains(code, "commit_empty") {
					commits = max(0, commits-1)
				}
				stMu.Unlock()
				if setup || code == "invalid_api_key" || strings.Contains(code, "insufficient_quota") {
					it.mu.Unlock()
					readErr <- openaiEventErr(code, msgText)
					return
				}
				changed = true
			}
			var finals []string
			interim := ""
			if changed {
				finals, interim = it.release()
			}
			it.mu.Unlock()
			if changed {
				for _, f := range finals {
					out.final(f)
				}
				out.interim(interim)
				ping()
			}
		}
	}()

	var up upsampler
	var v vad
	var batch []int16
	n, spoken, quietRun, since := 0, 0, 0, 0
	send := func(pcm []int16) error {
		b, _ := json.Marshal(map[string]string{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(pcmBytes(up.push(pcm)))})
		return conn.WriteText(b)
	}
	commit := func() error {
		stMu.Lock()
		commits++
		stMu.Unlock()
		spoken, quietRun, since = 0, 0, 0
		return conn.WriteText([]byte(`{"type":"input_audio_buffer.commit"}`))
	}
	for {
		select {
		case f, ok := <-audio:
			if !ok {
				goto end
			}
			batch = append(batch, f...)
			since++
			if v.push(frameDB(f)) {
				spoken++
				quietRun = 0
			} else {
				quietRun++
			}
			if n++; n >= chunkFrames {
				if err := send(batch); err != nil {
					return streamErr(ctx, "OpenAI", readErr, err)
				}
				batch, n = batch[:0], 0
				// Our turn detection for the live models: a pause after
				// speech, or 20 s of talking.
				if live && spoken >= 15 && (quietRun >= 30 || since >= 1000) {
					if err := commit(); err != nil {
						return streamErr(ctx, "OpenAI", readErr, err)
					}
				}
			}
		case err := <-readErr:
			return streamEnd(ctx, "OpenAI", err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
end:
	if len(batch) > 0 {
		_ = send(batch)
	}
	it.mu.Lock()
	heard := it.pending() // the server already heard words of a turn
	it.mu.Unlock()
	if !live || spoken > 0 || heard {
		_ = commit()
	}
	for {
		it.mu.Lock()
		pend := it.pending()
		it.mu.Unlock()
		stMu.Lock()
		waiting := commits > 0
		stMu.Unlock()
		if !pend && !waiting {
			break
		}
		select {
		case <-news:
		case err := <-readErr:
			if e := streamEnd(ctx, "OpenAI", err); e != nil && ctx.Err() == nil {
				return e
			}
			return nil
		case <-ctx.Done():
			return nil // the session keeps the interim text
		}
	}
	conn.CloseNormal(300 * time.Millisecond)
	return nil
}

// openaiEventErr maps an "error" event.
func openaiEventErr(code, msg string) error {
	low := strings.ToLower(code + " " + msg)
	switch {
	case strings.Contains(low, "invalid_api_key") || strings.Contains(low, "incorrect api key"):
		return newErr(KindAuth, nil, "OpenAI не прийняв ключ — перевір його в Налаштуваннях → Диктування")
	case strings.Contains(low, "quota") || strings.Contains(low, "billing"):
		return newErr(KindQuota, nil, "OpenAI: на рахунку закінчились кошти — поповни баланс або вибери інший сервіс диктування")
	case strings.Contains(low, "model"):
		return newErr(KindService, nil, "OpenAI не приймає модель розпізнавання (%s) — вкажи іншу в Налаштуваннях → Диктування", clip(msg, 140))
	}
	return newErr(KindService, nil, "OpenAI відхилив налаштування розпізнавання: %s", clip(firstNonEmpty(msg, code), 160))
}
