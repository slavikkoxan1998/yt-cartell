package dictate

// Check tests a dictation setup for Settings → «Диктування» → «Перевірити»
// without recording anything: the streaming services open a session and
// close it before any audio (nothing to bill), the phrase services answer a
// cheap authenticated read, whisper.cpp is looked up on disk.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var checkTimeout = 15 * time.Second

// Check verifies the plan's service; ok with a short Ukrainian line.
func Check(ctx context.Context, p Plan) (bool, string) {
	if !p.Ready {
		return false, planErr(p).Error()
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var err error
	msg := ""
	switch p.Provider {
	case ElevenLabs:
		err = checkWS(ctx, p.BaseURL+"?"+url.Values{"model_id": {firstNonEmpty(p.Model, ElevenModel)}, "audio_format": {"pcm_16000"}}.Encode(),
			http.Header{"Xi-Api-Key": {p.Key}}, nil, "session_started", "ElevenLabs", func(t, m string) error { return elevenErr(t, m) })
		msg = "ElevenLabs Scribe v2 Realtime: ключ працює"
	case OpenAI:
		r := &openaiRec{p: p}
		err = checkWS(ctx, realtimeURL(firstNonEmpty(p.BaseURL, openaiBase)), http.Header{"Authorization": {"Bearer " + p.Key}},
			r.sessionUpdate(), "session.updated", "OpenAI", nil)
		msg = "OpenAI Realtime: ключ працює · модель " + firstNonEmpty(p.Model, OpenAIRealtimeDef)
	case Deepgram:
		r := &deepgramRec{p: p}
		err = checkWS(ctx, r.url(), http.Header{"Authorization": {"Token " + p.Key}}, []byte(`{"type":"CloseStream"}`), "", "Deepgram", nil)
		msg = "Deepgram Nova-3: ключ працює"
	case AssemblyAI:
		r := &assemblyRec{p: p}
		err = checkWS(ctx, r.url(), http.Header{"Authorization": {p.Key}}, nil, "Begin", "AssemblyAI",
			func(t, m string) error { return assemblyErr(0, m) })
		msg = "AssemblyAI Universal-Streaming: ключ працює"
	case OpenAIChunks:
		msg, err = checkModels(ctx, p)
	case Gemini:
		err = checkGemini(ctx, p)
		msg = "Google AI Studio: ключ працює · модель " + firstNonEmpty(p.Model, GeminiDef)
	case Local:
		msg = "whisper.cpp готовий"
		if serverBin(p.Bin) != "" {
			msg += " · текст з'являтиметься під час мовлення (whisper-server)"
		} else {
			msg += " · текст з'являтиметься після кожної фрази (немає whisper-server поруч із whisper-cli)"
		}
	}
	if err != nil {
		return false, err.Error()
	}
	return true, msg
}

// checkWS opens a WebSocket, sends first (optional) and waits for a message
// of type want ("" = the handshake is enough), then closes.
func checkWS(ctx context.Context, u string, hdr http.Header, first []byte, want, who string, typed func(t, msg string) error) error {
	conn, err := wsDial(ctx, u, hdr)
	if err != nil {
		if who == "Deepgram" {
			return deepgramDialErr(ctx, err)
		}
		return dialErr(ctx, who, err)
	}
	defer conn.shut()
	stop := context.AfterFunc(ctx, conn.shut)
	defer stop()
	if first != nil {
		if err := conn.WriteText(first); err != nil {
			return dialErr(ctx, who, err)
		}
	}
	if want == "" {
		conn.CloseNormal(200 * time.Millisecond)
		return nil
	}
	for {
		_, msg, err := conn.Read()
		if err != nil {
			if ctx.Err() != nil {
				return newErr(KindNetwork, err, "%s не відповів вчасно — перевір інтернет", who)
			}
			return streamEnd(ctx, who, err)
		}
		var m struct {
			Type    string `json:"type"`
			MsgType string `json:"message_type"`
			Error   any    `json:"error"`
		}
		_ = json.Unmarshal(msg, &m)
		t := firstNonEmpty(m.Type, m.MsgType)
		switch {
		case t == want:
			conn.CloseNormal(200 * time.Millisecond)
			return nil
		case typed != nil && (strings.HasSuffix(t, "error") || m.Error != nil):
			s, _ := m.Error.(string)
			return typed(t, s)
		case t == "error":
			b, _ := json.Marshal(m.Error)
			var e struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal(b, &e)
			return openaiEventErr(e.Code, e.Message)
		}
	}
}

// checkModels: GET {base}/models with the key; the model should be listed.
func checkModels(ctx context.Context, p Plan) (string, error) {
	t := &openaiHTTP{p: p}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.BaseURL, "/")+"/models", nil)
	if err != nil {
		return "", err
	}
	if p.Key != "" {
		req.Header.Set("Authorization", "Bearer "+p.Key)
	}
	status, b, err := do(req)
	if err != nil {
		return "", netErr(ctx, t.who(), err)
	}
	model := firstNonEmpty(p.Model, WhisperCompatDef)
	if status == http.StatusNotFound {
		return t.who() + " відповідає (без списку моделей) · модель " + model, nil
	}
	if status/100 != 2 {
		return "", httpErr(t.who(), status, b, true)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &list) != nil || len(list.Data) == 0 {
		return t.who() + ": ключ працює · модель " + model, nil
	}
	for _, m := range list.Data {
		if m.ID == model {
			return t.who() + ": ключ працює · модель " + model + " доступна", nil
		}
	}
	return "", newErr(KindService, nil, "%s відповідає, але моделі «%s» там немає — вкажи іншу модель у Налаштуваннях → Диктування", t.who(), model)
}

// checkGemini: GET {base}/models/{model} with the key.
func checkGemini(ctx context.Context, p Plan) error {
	model := firstNonEmpty(p.Model, GeminiDef)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(firstNonEmpty(p.BaseURL, geminiBase), "/")+"/models/"+model, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-goog-api-key", p.Key)
	status, b, err := do(req)
	if err != nil {
		return netErr(ctx, "Google AI Studio", err)
	}
	if status/100 != 2 {
		return geminiErr(status, b, model)
	}
	return nil
}
