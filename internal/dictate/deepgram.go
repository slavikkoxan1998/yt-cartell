package dictate

// Deepgram live streaming (verified 2026-10-02 against
// https://developers.deepgram.com/reference/speech-to-text/listen-streaming and
// the docs on interim results, endpointing, Finalize, CloseStream, KeepAlive):
//
//	wss://api.deepgram.com/v1/listen?model=nova-3&language=uk&encoding=linear16
//	    &sample_rate=16000&channels=1&interim_results=true&punctuate=true
//	    &smart_format=true&endpointing=300          Authorization: Token <key>
//	→ binary frames of raw PCM · {"type":"Finalize"} · {"type":"CloseStream"}
//	← {"type":"Results","is_final":…,"speech_final":…,"channel":{"alternatives":[{"transcript":…}]}}
//	  · Metadata · close frames (1008 DATA-0000 bad audio, 1011 NET-000x timeouts)
//
// Every is_final piece is final text; the interim results in between show
// the words still changing. Nova-3 knows Ukrainian ("uk"); "multi" (used for
// auto) covers 10 languages only — en, es, fr, de, hi, ru, pt, ja, it, nl.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type deepgramRec struct{ p Plan }

func (r *deepgramRec) finishTime() time.Duration { return finishStream }

func (r *deepgramRec) url() string {
	q := url.Values{}
	q.Set("model", firstNonEmpty(r.p.Model, DeepgramModel))
	lang := r.p.Language
	if lang == "" {
		lang = "multi"
	}
	q.Set("language", lang)
	q.Set("encoding", "linear16")
	q.Set("sample_rate", "16000")
	q.Set("channels", "1")
	q.Set("interim_results", "true")
	q.Set("punctuate", "true")
	q.Set("smart_format", "true")
	q.Set("endpointing", "300")
	return firstNonEmpty(r.p.BaseURL, deepgramWS) + "?" + q.Encode()
}

func (r *deepgramRec) run(ctx context.Context, audio <-chan []int16, out sink) error {
	hdr := http.Header{}
	hdr.Set("Authorization", "Token "+r.p.Key)
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, err := wsDial(dctx, r.url(), hdr)
	cancel()
	if err != nil {
		return deepgramDialErr(ctx, err)
	}
	defer conn.shut()
	stop := context.AfterFunc(ctx, conn.shut)
	defer stop()

	readErr := make(chan error, 1)
	go func() {
		for {
			_, msg, err := conn.Read()
			if err != nil {
				readErr <- err
				return
			}
			var m struct {
				Type    string `json:"type"`
				IsFinal bool   `json:"is_final"`
				Channel struct {
					Alternatives []struct {
						Transcript string `json:"transcript"`
					} `json:"alternatives"`
				} `json:"channel"`
			}
			if json.Unmarshal(msg, &m) != nil || m.Type != "Results" || len(m.Channel.Alternatives) == 0 {
				continue
			}
			t := m.Channel.Alternatives[0].Transcript
			if m.IsFinal {
				out.final(t)
				out.interim("")
			} else {
				out.interim(t)
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
					return streamErr(ctx, "Deepgram", readErr, err)
				}
				batch, n = batch[:0], 0
			}
		case err := <-readErr:
			return streamEnd(ctx, "Deepgram", err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
end:
	if len(batch) > 0 {
		_ = conn.WriteBinary(pcmBytes(batch))
	}
	// Flush the last words, then let the server finish and close.
	_ = conn.WriteText([]byte(`{"type":"Finalize"}`))
	_ = conn.WriteText([]byte(`{"type":"CloseStream"}`))
	select {
	case <-readErr: // the server closed after its last results
	case <-ctx.Done():
	}
	return nil
}

// deepgramDialErr adds Deepgram's own reasons (the dg-error header of a refused
// handshake; 400 = no such model/language combination).
func deepgramDialErr(ctx context.Context, err error) error {
	if he, ok := err.(*HandshakeError); ok && he.Status == http.StatusBadRequest {
		low := strings.ToLower(he.Header.Get("dg-error") + " " + he.Body)
		if strings.Contains(low, "language") || strings.Contains(low, "model") || strings.TrimSpace(low) == "" {
			return newErr(KindService, err, "Deepgram не підтримує цю мову з Nova-3 — вибери іншу мову диктування або інший сервіс")
		}
	}
	return dialErr(ctx, "Deepgram", err)
}
