package providers

// Ready specs for the audio kinds of the universal adapter (Settings →
// Озвучка / Розпізнавання мови → «Власний сервер» → «Шаблон…») and the
// instructions for drafting one from documentation. Each template
// round-trips through universal_audio_test.go against a mock of its API.
// Wire formats checked 2026-10-05: OpenAI /audio/speech and
// /audio/transcriptions (developers.openai.com), ElevenLabs
// /v1/text-to-speech/{voice_id} and /v1/speech-to-text (elevenlabs.io/docs),
// Lumean /api/public/orders (api.lumean.app/docs/llm.md).

import (
	"strings"
)

// SpecTemplatesFor lists the templates of a kind.
func SpecTemplatesFor(kind SpecKind) []SpecTemplate {
	switch kind {
	case KindTTS:
		return []SpecTemplate{
			{ID: "openai-speech", Label: "OpenAI-сумісний /audio/speech", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini-tts",
				Hint: "Будь-який сервер з /v1/audio/speech: відповідь — сам аудіофайл.", Spec: tplOpenAISpeech},
			{ID: "elevenlabs", Label: "ElevenLabs-сумісний", BaseURL: "https://api.elevenlabs.io", Model: "eleven_multilingual_v2",
				Hint: "Ключ у заголовку xi-api-key; голос — voice_id.", Spec: tplElevenSpeech},
			{ID: "lumean", Label: "Lumean (замовлення за шаблоном)", BaseURL: "https://api.lumean.app/api/public",
				Hint: "У полі «Модель» — UUID свого шаблону Lumean; голос — voice_id ElevenLabs. Краще — сервіс «Lumean» у списку.", Spec: tplLumeanSpeech},
			{ID: "sync-json", Label: "Простий синхронний: POST → аудіо або посилання",
				Hint: "Заміни шлях, назви полів і «result» на свої.", Spec: tplSyncSpeech},
			{ID: "async-jobs", Label: "Асинхронні задачі (каркас)",
				Hint: "Заміни шляхи, поля, статуси й «result» на свої.", Spec: tplAsyncSpeech},
		}
	case KindSTT:
		return []SpecTemplate{
			{ID: "openai-whisper", Label: "OpenAI-сумісний /audio/transcriptions", BaseURL: "https://api.openai.com/v1", Model: "whisper-1",
				Hint: "Таймкоди слів дає лише verbose_json (whisper-1 і сумісні сервери).", Spec: tplWhisperSTT},
			{ID: "elevenlabs-scribe", Label: "ElevenLabs Scribe", BaseURL: "https://api.elevenlabs.io", Model: "scribe_v2",
				Hint: "Ключ у заголовку xi-api-key.", Spec: tplScribeSTT},
			{ID: "sync-json", Label: "Простий синхронний: POST → текст зі словами",
				Hint: "Заміни шлях, назви полів і «result» на свої.", Spec: tplSyncSTT},
			{ID: "async-jobs", Label: "Асинхронні задачі (каркас)",
				Hint: "Заміни шляхи, поля, статуси й «result» на свої.", Spec: tplAsyncSTT},
		}
	case KindVideo:
		return SpecTemplates(true)
	}
	return SpecTemplates(false)
}

// FindSpecTemplateFor returns a template of a kind by id.
func FindSpecTemplateFor(kind SpecKind, id string) (SpecTemplate, bool) {
	for _, t := range SpecTemplatesFor(kind) {
		if t.ID == id {
			return t, true
		}
	}
	return SpecTemplate{}, false
}

const tplOpenAISpeech = `{
  "name": "OpenAI Speech",
  "auth": "bearer",
  "submit": {
    "method": "POST",
    "path": "/audio/speech",
    "body": {
      "model": "{{model}}",
      "input": "{{text}}",
      "voice": "{{voice}}",
      "response_format": "mp3",
      "speed": "{{speed}}"
    }
  },
  "error": "error.message",
  "test": {"method": "GET", "path": "/models"}
}`

const tplElevenSpeech = `{
  "name": "ElevenLabs",
  "auth": "header:xi-api-key",
  "submit": {
    "method": "POST",
    "path": "/v1/text-to-speech/{{voice}}?output_format=mp3_44100_128",
    "headers": {"Accept": "audio/mpeg"},
    "body": {
      "text": "{{text}}",
      "model_id": "{{model}}",
      "language_code": "{{language}}",
      "voice_settings": {"stability": 0.5, "similarity_boost": 0.75, "speed": "{{speed}}"}
    }
  },
  "error": ["detail.message", "detail"],
  "test": {"method": "GET", "path": "/v1/user"}
}`

const tplLumeanSpeech = `{
  "name": "Lumean",
  "auth": "header:X-API-KEY",
  "submit": {
    "method": "POST",
    "path": "/orders",
    "body": {
      "template_id": "{{model}}",
      "input_text": "{{text}}",
      "name": "YT Cartell Studio",
      "config_override": {"tts_settings": {"voice_id": "{{voice}}", "language_code": "{{language}}"}}
    }
  },
  "id": "data.id",
  "poll": {"method": "GET", "path": "/orders/{id}", "every": 3},
  "state": "data.status",
  "done": ["completed", "result_delivered"],
  "failed": ["failed", "compensated", "cancelled", "partially_completed"],
  "queued": ["created", "pending"],
  "progress": "data.progress_percent",
  "fetch": {"method": "POST", "path": "/storage/url", "body": {"path": "{{status.data.result.files.0}}"}},
  "result": "data.url",
  "error": "data.result.user_message",
  "cancel": {"method": "POST", "path": "/orders/{id}/cancel"},
  "test": {"method": "GET", "path": "/user"}
}`

const tplSyncSpeech = `{
  "name": "Мій TTS",
  "auth": "bearer",
  "submit": {
    "method": "POST",
    "path": "/tts",
    "body": {
      "text": "{{text}}",
      "voice": "{{voice}}",
      "model": "{{model}}",
      "language": "{{language}}",
      "speed": "{{speed}}",
      "format": "mp3"
    }
  },
  "result": ["audio_url", "url", "audio", "audio_base64", "data"],
  "error": ["error.message", "error", "message"]
}`

const tplAsyncSpeech = `{
  "name": "Мій TTS (задачі)",
  "auth": "bearer",
  "submit": {
    "method": "POST",
    "path": "/v1/speech",
    "body": {"text": "{{text}}", "voice_id": "{{voice}}", "model": "{{model}}", "language": "{{language}}"}
  },
  "id": "id",
  "poll": {"method": "GET", "path": "/v1/speech/{id}", "every": 2},
  "state": "status",
  "done": ["succeeded", "completed"],
  "failed": ["failed", "canceled"],
  "queued": ["queued", "pending"],
  "result": ["output.audio_url", "audio_url"],
  "error": "error",
  "cancel": {"method": "POST", "path": "/v1/speech/{id}/cancel"}
}`

const tplWhisperSTT = `{
  "name": "Whisper",
  "auth": "bearer",
  "submit": {
    "method": "POST",
    "path": "/audio/transcriptions",
    "encoding": "multipart",
    "body": {
      "file": "{{audio}}",
      "model": "{{model}}",
      "language": "{{language}}",
      "response_format": "verbose_json",
      "timestamp_granularities[]": ["word", "segment"]
    },
    "timeout": 1200
  },
  "error": "error.message",
  "test": {"method": "GET", "path": "/models"}
}`

const tplScribeSTT = `{
  "name": "ElevenLabs Scribe",
  "auth": "header:xi-api-key",
  "submit": {
    "method": "POST",
    "path": "/v1/speech-to-text",
    "encoding": "multipart",
    "body": {
      "file": "{{audio}}",
      "model_id": "{{model}}",
      "language_code": "{{language}}",
      "timestamps_granularity": "word",
      "tag_audio_events": "false"
    },
    "timeout": 1200
  },
  "error": ["detail.message", "detail"],
  "test": {"method": "GET", "path": "/v1/user"}
}`

const tplSyncSTT = `{
  "name": "Мій STT",
  "auth": "bearer",
  "submit": {
    "method": "POST",
    "path": "/transcribe",
    "encoding": "multipart",
    "body": {"file": "{{audio}}", "model": "{{model}}", "language": "{{language}}", "word_timestamps": "true"}
  },
  "error": ["error.message", "error"]
}`

const tplAsyncSTT = `{
  "name": "Мій STT (задачі)",
  "auth": "bearer",
  "submit": {
    "method": "POST",
    "path": "/v1/transcriptions",
    "body": {"audio": "{{audio}}", "model": "{{model}}", "language": "{{language}}", "word_timestamps": true}
  },
  "id": "id",
  "poll": {"method": "GET", "path": "/v1/transcriptions/{id}", "every": 3},
  "state": "status",
  "done": ["completed"],
  "failed": ["error", "failed"],
  "queued": ["queued"],
  "error": "error"
}`

// SpecPromptFor is SpecPrompt for any kind: the system instructions for
// drafting a spec from documentation.
func SpecPromptFor(kind SpecKind) string {
	switch kind {
	case KindTTS, KindSTT:
	default:
		return SpecPrompt(kind == KindVideo)
	}
	tts := kind == KindTTS
	unit, job := "speech-to-text (transcription)", "transcribes ONE recording with word timestamps"
	if tts {
		unit, job = "text-to-speech", "voices ONE line of narration and returns the audio (mp3 preferred)"
	}
	var b strings.Builder
	w := func(s string) { b.WriteString(s + "\n") }
	w("You configure the \"universal\" adapter of YT Cartell Studio, a desktop app that makes videos from stories. The adapter talks to any")
	w("HTTP " + unit + " API, driven by a JSON description (the \"spec\"). Read the API documentation the user gives you and write the spec")
	w("that " + job + " per request.")
	w("")
	w("Reply with ONLY one JSON object, no prose and no code fences:")
	w(`{"spec": {…the spec…}, "baseUrl": "https://api.example.com/v1", "model": "model-id", "notes": ["…"]}`)
	w(`- baseUrl: the API root every spec path is relative to; "" if unknown. model: the docs' recommended model id ("" = no choice).`)
	w(`- notes: 0–6 short sentences in UKRAINIAN, informal "ти", about what you guessed or the user must check.`)
	w("")
	w("SPEC REFERENCE (only submit is required):")
	w(`{"name": "…", "auth": "bearer" | "basic" | "none" | "header:X-API-Key" | "header:Authorization:Token" | "query:key",`)
	w(` "submit": {"method": "POST", "path": "/…", "headers": {…}, "encoding": "json" | "multipart" | "form", "body": {…template…}, "timeout": 600},`)
	w(` "id": "job id path (async only)", "poll": {"method": "GET", "path": "/jobs/{id}", "every": 3}, "state": "status",`)
	w(` "done": ["completed"], "failed": ["failed"], "queued": ["queued"], "progress": "progress",`)
	w(` "fetch": {"method": "GET", "path": "/jobs/{id}/result"}  (optional; may have a body; {{status.<path>}} / {{submit.<path>}} usable),`)
	if tts {
		w(` "result": ["audio_url", "data.0.b64"]  (JSON path of the audio: URL, data: URI or base64; OMIT when the reply body IS the audio),`)
	} else {
		w(` "result": "transcript"  (JSON path of the transcript object with words/segments; OMIT when the whole reply is the transcript),`)
	}
	w(` "error": "error.message", "cancel": {"method": "POST", "path": "/jobs/{id}/cancel"}, "test": {"method": "GET", "path": "/account"}}`)
	w("JSON paths are dotted with array indexes: data.0.url.")
	w("Placeholders inside submit (path, headers, body string values):")
	if tts {
		w(`  {{text}} the line to speak · {{voice}} the voice id · {{model}} the model field · {{language}} ISO 639-1 ("uk")`)
		w(`  {{language_name}} ("Ukrainian") · {{speed}} speaking rate (number, 1 = normal) · {{format}} "mp3"`)
	} else {
		w(`  {{audio}} the recording as a data: URI — with "encoding": "multipart" it becomes a FILE part · {{audio_b64}} raw base64`)
		w(`  {{audio_name}} "voice.mp3" · {{audio_mime}} "audio/mpeg" · {{language}} ISO 639-1 ("uk", may be absent = detect) · {{model}}`)
	}
	w(`A string that is exactly one placeholder becomes its typed value; a placeholder without a value removes its key.`)
	w("")
	w("RULES")
	w(`- NEVER put the API key or a placeholder for it in the spec: "auth" handles it.`)
	w(`- Copy field names, paths and status values exactly as the docs write them. Do not invent endpoints.`)
	if tts {
		w(`- Prefer mp3 output (query or body field). If the endpoint streams/returns the audio bytes, omit "result" and set "Accept": "audio/mpeg".`)
	} else {
		w(`- Ask for WORD-level timestamps (verbose_json, timestamps_granularity=word, word_timestamps …) — the studio cuts scenes by them.`)
		w(`- File uploads: "encoding": "multipart" and the file field = "{{audio}}".`)
	}
	w(`- "test": a cheap authenticated GET that generates nothing (account, user, models), if documented.`)
	w("")
	w("EXAMPLE:")
	if tts {
		w(`{"spec": {"name": "Acme TTS", "auth": "header:X-API-Key", "submit": {"method": "POST", "path": "/v2/speak",`)
		w(`  "headers": {"Accept": "audio/mpeg"}, "body": {"text": "{{text}}", "voice_id": "{{voice}}", "lang": "{{language}}", "rate": "{{speed}}"}},`)
		w(`  "error": "error.message", "test": {"method": "GET", "path": "/v2/me"}}, "baseUrl": "https://api.acme.ai", "model": "", "notes": []}`)
	} else {
		w(`{"spec": {"name": "Acme STT", "submit": {"method": "POST", "path": "/v1/transcribe", "encoding": "multipart",`)
		w(`  "body": {"file": "{{audio}}", "language": "{{language}}", "timestamps": "word"}}, "result": "result",`)
		w(`  "error": "error", "test": {"method": "GET", "path": "/v1/usage"}}, "baseUrl": "https://api.acme.ai", "model": "", "notes": []}`)
	}
	return strings.TrimRight(b.String(), "\n")
}
