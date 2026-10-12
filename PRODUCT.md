# Product

<!-- impeccable:product-schema 1 -->

## Platform

desktop — a native Windows 10/11 (64-bit) app: a single `YT-Cartell-Studio.exe` with its own window, no browser and no Node.js. (The web edition described by the repository-root PRODUCT.md is a separate product surface.)

## Stack

Go 1.27 + Gio v0.10.3 (gioui.org), built CGO-free on Windows (`CGO_ENABLED=0`, `-H windowsgui`); the UI kit `internal/gui` (Fira Sans embedded, Lucide icons) and the screens in `internal/appui`; audio playback through oto v3. FFmpeg + ffprobe do montage, thumbnails and preview playback (looked up in the Settings path, then next to the exe, then `PATH`). Generation runs through local or user-configured providers: G-Labs Studio (Webhook API, Google Flow) or Google Gemini / Veo for frames and video; CarteLink or any OpenAI-compatible endpoint, or Google Gemini, for text; ElevenLabs, an OpenAI-compatible TTS or a custom endpoint for voice; ElevenLabs Scribe v2 or a Whisper-compatible server for speech recognition («Своя озвучка»); an optional Telegram bot for notifications.

## Users

Anyone who downloads the open-source studio (MIT), first of all the YT CARTELL YouTube community. Many are non-technical creators running "faceless" YouTube / Shorts channels. They run the studio locally on Windows next to their own generation tools (CarteLink text gateway, G-Labs Studio for Google Flow, ElevenLabs). The UI must be understandable by a newcomer without explanations.

## Product Purpose

Turn a story idea or a finished script into a finished, voiced, consistent animated video: plan → storyboard → clips + voiceover → montage → mp4 (with subtitles and a YouTube cover). Success = a newcomer produces a watchable video with consistent characters on the first try, and an experienced user can steer every prompt.

## Positioning

A step-by-step production pipeline where every intermediate artifact (characters, locations, per-scene voice lines and prompts, reference images, frames) is visible and editable before credits are spent on video, with character consistency enforced through reference images bound by @tags. The desktop edition packages the whole pipeline as one standalone program, with an Autopilot that walks all five steps for a newcomer.

## Operating Context

- A native window titled "YT Cartell Studio": opens at 1440 × 900 dp (maximized when the screen's work area is smaller), shrinks to 480 × 560 dp, where the rail becomes a bottom bar. Ukrainian UI; light, dark «Вечір» or system theme; a «Менше анімацій» switch.
- Data stays on the computer: projects and every generated file in `Documents\YT Cartell Studio\projects\<id>\` (the folder can be changed in Settings → Дані); replaced frames, clips and voices kept in the project's `trash\`; settings and API keys in `%AppData%\YT Cartell Studio\config.json`, profiles in `profiles.json`, the log in `studio.log` (rewritten each launch).
- Content: long stories (up to 150 scenes, about 20–25 min), Shorts / Reels 15–60 s in 9:16, medium 1–5 min.
- Generation costs real subscription credits (Google Flow via G-Labs, ElevenLabs characters), so expensive steps are explicit, and single items can be regenerated.
- Jobs run minutes to hours inside the app; progress for all projects is live in «Черга» and survives leaving the project. Closing the window stops the work (the app asks first), and Telegram notifications arrive only while it is open.
- A first-run tour (Home → story → profiles → Autopilot → queue) can be replayed from Settings. Web-edition data can be imported once (Settings → Дані); the two editions do not sync.
- Tested on Windows only; macOS and Linux builds need cgo and are untested.

## Capabilities and Constraints

- Steps: 1 Сценарій (modes Ідея / Готовий сценарій / Своя озвучка; duration 15 s … 30 min or custom; 16:9 or 9:16; 93 languages with «Авто»; sound — a narrator voice-over, the characters speaking inside the clips with the video model's own voices, or no voice; what is in frame — characters, no people, motion graphics or mixed, with animated on-screen text; narrator voice, tone, 71 styles in 6 categories, 36 genres and niches, camera, pace, frame model, video mode incl. 3×3 storyboard and frame chaining; director's wishes), 2 План (editable title, logline, shared frame style, characters with their own voices — or objects and motifs, locations, scenes: narration or on-camera lines, speaker, cast, location, on-screen text, frame and motion prompts), 3 Розкадровка (reference images, scene frames, 3×3 sheets), 4 Відео й озвучка, 5 Монтаж (timeline with reorder, trim and per-cut transitions; 23 transitions, 12 motion effects, 15 filters, 8 subtitle styles; music with ducking; cover studio with 12 templates) → mp4, `.srt`, JPEG cover.
- Autopilot walks all five steps with retries (0–3) and an optional cover; profiles store complete presets (four starters, custom ones saved from a project).
- Project translation (same frames and clips; new narration, voices and subtitles), «Своя озвучка» (transcribe a recording → scenes timed to speech), duplicate project.
- Scene = one clip (4 / 6 / 8 s, default 8; Omni Flash up to 10 s). Duration → scene count; at most 150 scenes per film.
- One job per project at a time; scenes shot in parallel up to the video service's documented limit (G-Labs 10, RoyalTechno 20, Google Veo 4 …; hard cap 20; frame chaining shoots one at a time).
- The 3×3 storyboard and frame chaining need G-Labs Omni Flash; 4 and 6 s scenes in G-Labs need Google Ultra; the ElevenLabs free tier offers only premade voices via the API.
- The built-in player is for preview (FFmpeg, reduced size); cover text supports Latin and Cyrillic only.

## Brand Commitments

- Name: YT CARTELL AI MAKER; the desktop program is "YT Cartell Studio". App icon: `internal/assets/icon256.png` (embedded through `rsrc_windows_amd64.syso`); in the app the mark is a warm orange square with a clapperboard at the top of the rail. Web logo assets: `apps/web/dist/ytcartell-logo.svg`, `ytcartell-icon-dark.svg`.
- Telegram community: https://t.me/YT_cartell (the rail's «Спільнота» link).
- The previous interface was black & white; the owner asked for a full redesign that is "beautiful but understandable".

## Evidence on Hand

- Style preview images: `internal/assets/styles/<id>.jpg` (23 embedded previews).
- Screenshots of every screen and state, light and dark, at 1920 × 1080 and 390 × 844: `screenshots/review/`; the component gallery renders with `go run ./cmd/guikit`.
- Feedback on the earlier desktop UI (GUI.md): "half of the screen is empty, elements are badly structured, there is no smoothness".
- No testimonials, pricing or usage statistics exist; do not invent them.

## Product Principles

1. Show the next step: at every moment one obvious primary action.
2. Nothing expensive happens implicitly — spending credits is always an explicit click with a clear count.
3. Everything the AI wrote is editable in place before it is used.
4. One broken item never blocks the rest: per-item status, retry, and regenerate.
5. Plain Ukrainian words over jargon; technical detail lives in Settings.
