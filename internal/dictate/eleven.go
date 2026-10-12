package dictate

// ElevenLabs Scribe v2 Realtime (verified 2026-10-02 against
// https://elevenlabs.io/docs/api-reference/speech-to-text/v-1-speech-to-text-realtime
// and the guides under /docs/eleven-api/guides/how-to/speech-to-text/realtime/):
//
//	wss://api.elevenlabs.io/v1/speech-to-text/realtime?model_id=scribe_v2_realtime
//	    &audio_format=pcm_16000&commit_strategy=vad&vad_silence_threshold_secs=0.8
//	    [&language_code=uk]            header xi-api-key
//	→ {"message_type":"input_audio_chunk","audio_base_64":"…","commit":false,"sample_rate":16000}
//	   (0.1–1 s per chunk; the end: an empty chunk with "commit":true)
//	← session_started · partial_transcript {text} (replaces the previous partial)
//	  · committed_transcript {text} (final) · errors {"message_type":"auth_error"|
//	  "quota_exceeded"|"rate_limited"|…, "error":"…"} after which the server closes.
//
// The server closes after 15 s without messages: the microphone streams
// continuously, so that never happens while dictating.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type elevenRec struct{ p Plan }

func (r *elevenRec) finishTime() time.Duration { return finishStream }

const chunkFrames = 5 // 100 ms per message

func (r *elevenRec) run(ctx context.Context, audio <-chan []int16, out sink) error {
	q := url.Values{}
	q.Set("model_id", firstNonEmpty(r.p.Model, ElevenModel))
	q.Set("audio_format", "pcm_16000")
	q.Set("commit_strategy", "vad")
	q.Set("vad_silence_threshold_secs", "0.8")
	if l := r.p.Language; l != "" {
		q.Set("language_code", l)
	}
	hdr := http.Header{}
	hdr.Set("xi-api-key", r.p.Key)
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, err := wsDial(dctx, r.p.BaseURL+"?"+q.Encode(), hdr)
	cancel()
	if err != nil {
		return dialErr(ctx, "ElevenLabs", err)
	}
	defer conn.shut()
	stop := context.AfterFunc(ctx, conn.shut)
	defer stop()

	var mu sync.Mutex
	partial, commits := "", 0
	committed := make(chan struct{}, 8)
	readErr := make(chan error, 1)
	go func() {
		for {
			_, msg, err := conn.Read()
			if err != nil {
				readErr <- err
				return
			}
			var m struct {
				Type  string `json:"message_type"`
				Text  string `json:"text"`
				Error string `json:"error"`
			}
			if json.Unmarshal(msg, &m) != nil {
				continue
			}
			switch m.Type {
			case "partial_transcript":
				mu.Lock()
				partial = m.Text
				mu.Unlock()
				out.interim(m.Text)
			case "committed_transcript":
				mu.Lock()
				partial = ""
				commits++
				mu.Unlock()
				out.final(m.Text)
				out.interim("")
				select {
				case committed <- struct{}{}:
				default:
				}
			case "session_started", "committed_transcript_with_timestamps", "warning":
			default:
				if m.Error != "" || strings.HasSuffix(m.Type, "error") || m.Type == "quota_exceeded" ||
					m.Type == "rate_limited" || m.Type == "resource_exhausted" || m.Type == "queue_overflow" ||
					m.Type == "unaccepted_terms" || m.Type == "session_time_limit_exceeded" || m.Type == "insufficient_audio_activity" ||
					m.Type == "chunk_size_exceeded" || m.Type == "commit_throttled" || m.Type == "invalid_request" {
					readErr <- elevenErr(m.Type, m.Error)
					return
				}
			}
		}
	}()

	send := func(pcm []int16, commit bool) error {
		b, _ := json.Marshal(map[string]any{
			"message_type":  "input_audio_chunk",
			"audio_base_64": base64.StdEncoding.EncodeToString(pcmBytes(pcm)),
			"commit":        commit,
			"sample_rate":   SampleRate,
		})
		return conn.WriteText(b)
	}
	var batch []int16
	var v vad
	n, spoke := 0, false
	for {
		select {
		case f, ok := <-audio:
			if !ok {
				goto end
			}
			batch = append(batch, f...)
			if v.push(frameDB(f)) {
				spoke = true
			}
			if n++; n >= chunkFrames {
				if err := send(batch, false); err != nil {
					return streamErr(ctx, "ElevenLabs", readErr, err)
				}
				batch, n = batch[:0], 0
			}
		case err := <-readErr:
			return streamEnd(ctx, "ElevenLabs", err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
end:
	// The end of the recording: the rest, then a commit for the last words.
	// The server runs a little behind, so the commit goes out whenever
	// anything was said; then wait for a commit that empties the partial
	// (or a short grace when nothing was pending).
	if len(batch) > 0 {
		_ = send(batch, false)
	}
	if !spoke {
		conn.CloseNormal(300 * time.Millisecond)
		return nil
	}
	mu.Lock()
	before := commits
	mu.Unlock()
	if err := send(nil, true); err != nil {
		return nil
	}
	grace := time.NewTimer(1500 * time.Millisecond)
	defer grace.Stop()
	for {
		mu.Lock()
		settled := commits > before && partial == ""
		pending := partial != ""
		mu.Unlock()
		if settled {
			break
		}
		select {
		case <-committed:
		case <-grace.C:
			if !pending {
				conn.CloseNormal(300 * time.Millisecond)
				return nil
			}
			grace.Reset(500 * time.Millisecond) // words still pending: the session's timeout bounds this
		case err := <-readErr:
			if e := streamEnd(ctx, "ElevenLabs", err); e != nil && KindOf(e) != KindService {
				return e
			}
			return nil
		case <-ctx.Done():
			return nil // the session keeps the partial as text
		}
	}
	conn.CloseNormal(500 * time.Millisecond)
	return nil
}

// elevenErr maps an error message of the realtime API.
func elevenErr(kind, msg string) error {
	switch kind {
	case "auth_error":
		return newErr(KindAuth, errors.New(kind), "ElevenLabs не прийняв ключ — перевір його в Налаштуваннях → Диктування (потрібен дозвіл Speech to Text)")
	case "quota_exceeded":
		return newErr(KindQuota, errors.New(kind), "ElevenLabs: закінчились кредити на тарифі — поповни баланс або вибери інший сервіс диктування")
	case "rate_limited", "resource_exhausted", "queue_overflow", "commit_throttled":
		return newErr(KindQuota, errors.New(kind), "ElevenLabs зараз перевантажений або вичерпано ліміт одночасних сесій — спробуй за хвилину")
	case "unaccepted_terms":
		return newErr(KindAuth, errors.New(kind), "ElevenLabs просить прийняти умови використання розпізнавання — зайди на elevenlabs.io і прийми їх")
	case "session_time_limit_exceeded":
		return newErr(KindService, errors.New(kind), "ElevenLabs завершив сесію: вичерпано її максимальну тривалість")
	case "insufficient_audio_activity":
		return newErr(KindService, errors.New(kind), "ElevenLabs не почув мови — говори ближче до мікрофона або перевір, чи вибрано правильний мікрофон")
	}
	return newErr(KindService, errors.New(kind), "ElevenLabs не зміг розпізнати мову: %s", clip(firstNonEmpty(msg, kind), 160))
}

// ---- shared WebSocket error helpers ---------------------------------------------------------

// dialErr maps a failed connection.
func dialErr(ctx context.Context, who string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var he *HandshakeError
	if errors.As(err, &he) {
		body := strings.ToLower(he.Body)
		switch {
		case he.Status == http.StatusUnauthorized || strings.Contains(body, "invalid_api_key") || strings.Contains(body, "invalid api key"):
			return newErr(KindAuth, err, "%s не прийняв ключ (401) — перевір його в Налаштуваннях → Диктування", who)
		case he.Status == http.StatusPaymentRequired || strings.Contains(body, "quota") || strings.Contains(body, "insufficient"):
			return newErr(KindQuota, err, "%s: закінчились кредити — поповни баланс або вибери інший сервіс диктування", who)
		case he.Status == http.StatusForbidden:
			return newErr(KindAuth, err, "%s відмовив у доступі (403) — у ключа немає дозволу на розпізнавання мови в реальному часі", who)
		case he.Status == http.StatusTooManyRequests:
			return newErr(KindQuota, err, "%s перевантажений (429) — спробуй за хвилину", who)
		case he.Status >= 500:
			return newErr(KindService, err, "%s тимчасово недоступний (%d) — спробуй ще раз", who, he.Status)
		}
		detail := clip(firstNonEmpty(errDetail([]byte(he.Body)), he.Body), 160)
		return newErr(KindService, err, "%s відхилив підключення (%d): %s", who, he.Status, detail)
	}
	return newErr(KindNetwork, err, "Немає з'єднання з %s — перевір інтернет (%s)", who, clip(err.Error(), 120))
}

// streamErr: a send failed; the reader usually knows why.
func streamErr(ctx context.Context, who string, readErr <-chan error, err error) error {
	select {
	case rerr := <-readErr:
		return streamEnd(ctx, who, rerr)
	case <-time.After(300 * time.Millisecond):
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return newErr(KindNetwork, err, "З'єднання з %s обірвалось — перевір інтернет і спробуй ще раз", who)
}

// streamEnd maps the end of the server's stream while audio still flowed.
func streamEnd(ctx context.Context, who string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var de *Error
	if errors.As(err, &de) {
		return de
	}
	var ce *CloseError
	if errors.As(err, &ce) {
		if ce.Code == 1000 {
			return newErr(KindService, err, "%s завершив сесію розпізнавання", who)
		}
		return newErr(KindService, err, "%s закрив з'єднання (%d %s)", who, ce.Code, clip(ce.Reason, 120))
	}
	return newErr(KindNetwork, err, "З'єднання з %s обірвалось — перевір інтернет і спробуй ще раз", who)
}

// errDetail digs a message out of the usual JSON error shapes.
func errDetail(b []byte) string {
	var v struct {
		Detail  json.RawMessage `json:"detail"`
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		ErrMsg  string          `json:"err_msg"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	if v.Message != "" || v.ErrMsg != "" {
		return firstNonEmpty(v.Message, v.ErrMsg)
	}
	for _, raw := range []json.RawMessage{v.Detail, v.Error} {
		if len(raw) == 0 {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
		var o struct {
			Message string `json:"message"`
			Msg     string `json:"msg"`
			Status  string `json:"status"`
		}
		if json.Unmarshal(raw, &o) == nil && (o.Message != "" || o.Msg != "") {
			return o.Message + o.Msg
		}
	}
	return ""
}
