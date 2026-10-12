// Package catalog holds the preset catalogues of «Своє API»: video and image
// generation models across services and aggregators, and the text (LLM)
// providers. The data lives in embedded JSON (video/*.json, image/*.json,
// text/*.json — one file per service or group) so it is updated without code:
// edit a file, run `go test ./internal/providers/catalog` (it checks every
// entry), done.
//
// Every entry must exist: "source" is the page (model page, API reference)
// it was checked against, "checked" (file level, may be overridden per entry)
// the date. Entries are never invented; a model without a verified page is
// left out.
//
// File shape (all lists):
//
//	{"list": "video", "checked": "2026-10-05", "entries": [ {…}, … ]}
//
// A video entry (VideoEntry):
//
//	id          stable unique id "<host>:<model>" (stored in config? no — but keep stable)
//	label       short display name, e.g. "Kling 3.0 Pro · кадр → відео"
//	vendor      the model maker ("Kuaishou Kling", "Google", "ByteDance" …)
//	family      model family for grouping ("Kling 3.0", "Veo 3.1" …)
//	host        the service the request goes to: fal | replicate | runway | luma | minimax |
//	            royaltechno | google | openai | kling | bytedance | alibaba | pixverse | vidu |
//	            lightricks | pika | higgsfield | … (lower-case id)
//	hostLabel   its display name ("fal.ai", "Replicate", "Google Gemini API" …)
//	preset      the studio protocol: fal | replicate | runway | luma | minimax | royaltechno | universal
//	baseUrl     the API base for that preset ("" = the preset's default)
//	model       the model id exactly as the API takes it
//	spec        (preset universal only) a universal-adapter spec object (providers/universal.go)
//	modes       any of: t2v (text → video), i2v (start frame → video), ref (reference images /
//	            elements → video), first-last (first + last frame), v2v (video edit / extend)
//	lengths     clip lengths in seconds the API accepts (ascending)
//	aspects     aspect ratios ("16:9", "9:16", "1:1" …)
//	resolutions "480p", "720p", "1080p", "4k" …
//	maxRefs     reference images one request takes (0 = none)
//	audio       "speech" (generates synced speech/dialogue + sound — «Голос у відео»),
//	            "sound" (generates sound/music but no reliable speech), "none"
//	promptMax   prompt length cap in characters (0 = not documented)
//	price       a price line ONLY when quoted from the provider's docs/page ("" otherwise)
//	notes       one short Ukrainian note (what it is good at, limits)
//	source      URL the entry was verified against
//	popular     0 niche · 1 notable · 2 popular · 3 flagship / most used
//
// An image entry (ImageEntry) has the same identity fields (preset: openai |
// openai-compat | fal | replicate | royaltechno | universal) and
//
//	modes       t2i (text → image), edit (reference / input images → image)
//	maxRefs     reference images one request takes (0 = text only)
//	aspects, sizes ("1024x1024", "2K", "4K" …), promptMax, text (renders legible text well)
//
// A text provider (TextEntry): id, label, protocol (openai | anthropic | gemini |
// azure-openai | ollama …), baseUrl, models, defaultModel, keyHint, keyOptional, local,
// vision, docs, source, popular, notes.
package catalog
