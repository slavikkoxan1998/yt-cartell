package dictate

// AssemblyAI Universal-Streaming v3 (verified 2026-10-02 against
// https://www.assemblyai.com/docs/streaming/api-spec/streaming-websocket,
// …/streaming/message-sequence, …/streaming/endpoints-and-data-zones and
// …/streaming/common-session-errors-and-closures):
//
//	wss://streaming.assemblyai.com/v3/ws?sample_rate=16000&encoding=pcm_s16le
//	    &speech_model=universal-3-6-pro[&language_detection=true | &language_codes=en]
//	    Authorization: <key> (no "Bearer"; streaming.eu.assemblyai.com for the EU)
//	→ binary PCM frames of 50–1000 ms (here 100 ms, at the microphone's pace)
//	  · {"type":"Terminate"} at the end
//	← Begin · Turn {turn_order, end_of_turn, turn_is_formatted, transcript}
//	  · Termination · Error {error_code, error} before a close
//
// A Turn with end_of_turn=false is the turn's text so far (it replaces the
// previous one); the turn is final when end_of_turn and turn_is_formatted are
// both true (format_turns=true makes the Universal-Streaming models send that
// formatted final; the Pro models always format). After Terminate the
// server sends what is left and Termination. No streaming model knows
// Ukrainian (the Pro models cover 32 languages, Russian among them).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// AssemblyAI ids and endpoints.
const (
	AssemblyAI     = "assemblyai"
	AssemblyModel  = "universal-3-6-pro"
	assemblyKeyURL = "https://www.assemblyai.com/dashboard"
)

var (
	assemblyWS   = "wss://streaming.assemblyai.com/v3/ws"
	assemblyWSEU = "wss://streaming.eu.assemblyai.com/v3/ws"
)

type assemblyRec struct{ p Plan }

func (r *assemblyRec) finishTime() time.Duration { return finishStream }

func (r *assemblyRec) url() string {
	q := url.Values{}
	q.Set("sample_rate", "16000")
	q.Set("encoding", "pcm_s16le")
	model := firstNonEmpty(r.p.Model, AssemblyModel)
	q.Set("speech_model", model)
	if strings.HasPrefix(model, "universal-streaming") {
		q.Set("format_turns", "true")
	}
	if l := r.p.Language; l != "" {
		q.Set("language_codes", l)
	} else {
		q.Set("language_detection", "true")
	}
	return firstNonEmpty(r.p.BaseURL, assemblyWS) + "?" + q.Encode()
}

func (r *assemblyRec) run(ctx context.Context, audio <-chan []int16, out sink) error {
	hdr := http.Header{}
	hdr.Set("Authorization", r.p.Key)
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, err := wsDial(dctx, r.url(), hdr)
	cancel()
	if err != nil {
		return dialErr(ctx, "AssemblyAI", err)
	}
	defer conn.shut()
	stop := context.AfterFunc(ctx, conn.shut)
	defer stop()

	readErr := make(chan error, 1)
	ended := make(chan struct{})
	go func() {
		pendingTurn, pending := -1, ""
		flush := func() { // a turn that ended without its formatted version
			if pending != "" {
				out.final(pending)
				out.interim("")
			}
			pendingTurn, pending = -1, ""
		}
		for {
			_, msg, err := conn.Read()
			if err != nil {
				flush()
				readErr <- err
				return
			}
			var m struct {
				Type      string `json:"type"`
				Order     int    `json:"turn_order"`
				End       bool   `json:"end_of_turn"`
				Formatted bool   `json:"turn_is_formatted"`
				Text      string `json:"transcript"`
				Code      int    `json:"error_code"`
				Error     string `json:"error"`
			}
			if json.Unmarshal(msg, &m) != nil {
				continue
			}
			switch m.Type {
			case "Turn":
				if pendingTurn >= 0 && m.Order != pendingTurn {
					flush()
				}
				switch {
				case m.End && m.Formatted:
					out.final(m.Text)
					out.interim("")
					pendingTurn, pending = -1, ""
				case m.End:
					pendingTurn, pending = m.Order, m.Text // the formatted one follows
					out.interim(m.Text)
				default:
					out.interim(m.Text)
				}
			case "Termination":
				flush()
				close(ended)
				readErr <- nil
				return
			case "Error":
				flush()
				readErr <- assemblyErr(m.Code, m.Error)
				return
			}
		}
	}()

	var batch []int16
	n := 0
	for {
		select {
		case f, ok := <-audio:
			if !ok {
				goto end
			}
			batch = append(batch, f...)
			if n++; n >= chunkFrames {
				if err := conn.WriteBinary(pcmBytes(batch)); err != nil {
					return streamErr(ctx, "AssemblyAI", readErr, err)
				}
				batch, n = batch[:0], 0
			}
		case err := <-readErr:
			if err == nil {
				err = errors.New("terminated")
			}
			return streamEnd(ctx, "AssemblyAI", err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
end:
	if len(batch) >= FrameSize*3 { // frames under 50 ms are refused
		_ = conn.WriteBinary(pcmBytes(batch))
	}
	_ = conn.WriteText([]byte(`{"type":"Terminate"}`))
	select {
	case <-ended:
	case err := <-readErr:
		var ce *CloseError
		if err != nil && !errors.As(err, &ce) && KindOf(err) != KindOther && ctx.Err() == nil {
			return err
		}
	case <-ctx.Done():
	}
	return nil
}

// assemblyErr maps the Error message sent before a close.
func assemblyErr(code int, msg string) error {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "balance") || strings.Contains(low, "insufficient") || strings.Contains(low, "funds"):
		return newErr(KindQuota, nil, "AssemblyAI: на рахунку закінчились кошти — поповни баланс або вибери інший сервіс диктування")
	case code == 1008 && strings.Contains(low, "concurrent"), code == 3009:
		return newErr(KindQuota, nil, "AssemblyAI: забагато одночасних сесій — зачекай хвилину й спробуй ще раз")
	case code == 1008 || strings.Contains(low, "unauthorized"):
		return newErr(KindAuth, nil, "AssemblyAI не прийняв ключ — перевір його в Налаштуваннях → Диктування")
	case code == 3008:
		return newErr(KindService, nil, "AssemblyAI завершив сесію: вичерпано її максимальну тривалість")
	}
	return newErr(KindService, nil, "AssemblyAI не зміг розпізнати мову: %s", clip(firstNonEmpty(msg, "помилка "+strconv.Itoa(code)), 160))
}
