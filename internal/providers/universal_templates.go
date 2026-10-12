package providers

// Ready specs for the universal preset («Шаблон…» in Settings): well-known
// API shapes the user starts from and edits. Each one round-trips through
// universal_test.go against a mock of its API.

// SpecTemplate is a ready spec for the universal preset.
type SpecTemplate struct {
	ID    string // stable id
	Label string // Ukrainian, for the template select
	Hint  string // one line on what to change
	// BaseURL / Model fill the fields when the user picks the template ("" =
	// keep what the user has).
	BaseURL, Model string
	Spec           string // indented JSON
}

// SpecTemplates lists the templates for images (video false) or video.
func SpecTemplates(video bool) []SpecTemplate {
	if video {
		return []SpecTemplate{
			{ID: "royaltechno", Label: "RoyalTechno (задачі)", BaseURL: "https://api.royaltechno.cc", Model: "omni-flash",
				Hint: "Ключ rt_live_… з бота @royaltechno_ai_bot. Довжини й референси — під модель.", Spec: tplRoyalVideo},
			{ID: "replicate", Label: "Replicate (predictions)", BaseURL: "https://api.replicate.com/v1", Model: "google/veo-3.1-fast",
				Hint: "Назви полів input — як у моделі на replicate.com (вкладка API).", Spec: tplReplicateVideo},
			{ID: "fal", Label: "fal.ai (черга)", BaseURL: "https://queue.fal.run", Model: "fal-ai/kling-video/v2.5-turbo/pro/image-to-video",
				Hint: "Поля тіла — як у моделі на fal.ai (вкладка API → Schema).", Spec: tplFalVideo},
			{ID: "sync-url", Label: "Простий синхронний: POST → посилання",
				Hint: "Заміни шлях, назви полів і «result» на свої.", Spec: tplSyncVideo},
			{ID: "async-jobs", Label: "Асинхронні задачі (каркас)",
				Hint: "Заміни шляхи, поля, статуси й «result» на свої.", Spec: tplAsyncVideo},
		}
	}
	return []SpecTemplate{
		{ID: "royaltechno", Label: "RoyalTechno (задачі)", BaseURL: "https://api.royaltechno.cc", Model: "nano-banana-pro",
			Hint: "Ключ rt_live_… з бота @royaltechno_ai_bot.", Spec: tplRoyalImage},
		{ID: "replicate", Label: "Replicate (predictions)", BaseURL: "https://api.replicate.com/v1", Model: "google/nano-banana-2",
			Hint: "Назви полів input — як у моделі на replicate.com (вкладка API).", Spec: tplReplicateImage},
		{ID: "fal", Label: "fal.ai (черга)", BaseURL: "https://queue.fal.run", Model: "fal-ai/nano-banana-2",
			Hint: `Для опорних фото — модель «…/edit» і поле "image_urls": "{{refs}}".`, Spec: tplFalImage},
		{ID: "openai-images", Label: "OpenAI Images (синхронно)", BaseURL: "https://api.openai.com/v1", Model: "gpt-image-2",
			Hint: "Будь-який сервер з /v1/images/generations, що віддає b64_json або url.", Spec: tplOpenAIImages},
		{ID: "sync-url", Label: "Простий синхронний: POST → посилання",
			Hint: "Заміни шлях, назви полів і «result» на свої.", Spec: tplSyncImage},
		{ID: "async-jobs", Label: "Асинхронні задачі (каркас)",
			Hint: "Заміни шляхи, поля, статуси й «result» на свої.", Spec: tplAsyncImage},
	}
}

// FindSpecTemplate returns a template by id.
func FindSpecTemplate(video bool, id string) (SpecTemplate, bool) {
	for _, t := range SpecTemplates(video) {
		if t.ID == id {
			return t, true
		}
	}
	return SpecTemplate{}, false
}

const tplRoyalImage = `{
  "name": "RoyalTechno",
  "auth": "bearer",
  "submit": {
    "method": "POST",
    "path": "/v1/jobs",
    "body": {
      "model": "{{model}}",
      "input": {
        "prompt": "{{prompt}}",
        "aspect_ratio": "{{orientation}}",
        "reference_image_urls": "{{refs}}"
      }
    }
  },
  "id": "id",
  "poll": {"method": "GET", "path": "/v1/jobs/{id}", "every": 5},
  "state": "status",
  "done": ["succeeded"],
  "failed": ["failed", "canceled"],
  "queued": ["queued"],
  "result": ["output.url", "output.images.0.url"],
  "error": "error.message",
  "cancel": {"method": "POST", "path": "/v1/jobs/{id}/cancel"},
  "test": {"method": "GET", "path": "/v1/account"},
  "caps": {"maxRefs": 10, "aspects": ["16:9", "9:16"]}
}`

const tplRoyalVideo = `{
  "name": "RoyalTechno",
  "auth": "bearer",
  "submit": {
    "method": "POST",
    "path": "/v1/jobs",
    "body": {
      "model": "{{model}}",
      "input": {
        "prompt": "{{prompt}}",
        "aspect_ratio": "{{orientation}}",
        "duration_sec": "{{seconds}}",
        "start_image_url": "{{start_image}}",
        "reference_image_urls": "{{refs}}"
      }
    }
  },
  "id": "id",
  "poll": {"method": "GET", "path": "/v1/jobs/{id}", "every": 10},
  "state": "status",
  "done": ["succeeded"],
  "failed": ["failed", "canceled"],
  "queued": ["queued"],
  "result": ["output.url", "output.videos.0.url"],
  "error": "error.message",
  "cancel": {"method": "POST", "path": "/v1/jobs/{id}/cancel"},
  "test": {"method": "GET", "path": "/v1/account"},
  "caps": {"lengths": [4, 6, 8, 10], "maxRefs": 7, "aspects": ["16:9", "9:16"]}
}`

const tplReplicateImage = `{
  "name": "Replicate",
  "submit": {
    "method": "POST",
    "path": "/models/{{model}}/predictions",
    "headers": {"Prefer": "wait=60"},
    "body": {
      "input": {
        "prompt": "{{prompt}}",
        "aspect_ratio": "{{aspect}}",
        "image_input": "{{refs}}"
      }
    }
  },
  "id": "id",
  "poll": {"method": "GET", "path": "/predictions/{id}", "every": 3},
  "state": "status",
  "done": ["succeeded"],
  "failed": ["failed", "canceled", "aborted"],
  "queued": ["starting"],
  "result": "output",
  "error": "error",
  "cancel": {"method": "POST", "path": "/predictions/{id}/cancel"},
  "test": {"method": "GET", "path": "/account"},
  "caps": {"maxRefs": 10, "aspects": ["16:9", "9:16", "1:1", "4:3", "3:4"]}
}`

const tplReplicateVideo = `{
  "name": "Replicate",
  "submit": {
    "method": "POST",
    "path": "/models/{{model}}/predictions",
    "body": {
      "input": {
        "prompt": "{{prompt}}",
        "image": "{{start_image}}",
        "duration": "{{seconds}}",
        "aspect_ratio": "{{aspect}}"
      }
    }
  },
  "id": "id",
  "poll": {"method": "GET", "path": "/predictions/{id}", "every": 5},
  "state": "status",
  "done": ["succeeded"],
  "failed": ["failed", "canceled", "aborted"],
  "queued": ["starting"],
  "result": "output",
  "error": "error",
  "cancel": {"method": "POST", "path": "/predictions/{id}/cancel"},
  "test": {"method": "GET", "path": "/account"},
  "caps": {"lengths": [4, 6, 8], "aspects": ["16:9", "9:16"]}
}`

const tplFalImage = `{
  "name": "fal.ai",
  "auth": "header:Authorization:Key",
  "submit": {
    "method": "POST",
    "path": "/{{model}}",
    "body": {
      "prompt": "{{prompt}}",
      "aspect_ratio": "{{aspect}}"
    }
  },
  "id": "request_id",
  "poll": {"method": "GET", "path": "{{submit.status_url}}", "every": 3},
  "state": "status",
  "done": ["COMPLETED"],
  "queued": ["IN_QUEUE"],
  "error": "error",
  "fetch": {"method": "GET", "path": "{{submit.response_url}}"},
  "result": ["images.0.url", "image.url"],
  "cancel": {"method": "PUT", "path": "{{submit.cancel_url}}"},
  "caps": {"aspects": ["16:9", "9:16", "1:1", "4:3", "3:4"]}
}`

const tplFalVideo = `{
  "name": "fal.ai",
  "auth": "header:Authorization:Key",
  "submit": {
    "method": "POST",
    "path": "/{{model}}",
    "body": {
      "prompt": "{{prompt}}",
      "image_url": "{{start_image}}",
      "duration": "{{seconds|text}}"
    }
  },
  "id": "request_id",
  "poll": {"method": "GET", "path": "{{submit.status_url}}", "every": 5},
  "state": "status",
  "done": ["COMPLETED"],
  "queued": ["IN_QUEUE"],
  "error": "error",
  "fetch": {"method": "GET", "path": "{{submit.response_url}}"},
  "result": "video.url",
  "cancel": {"method": "PUT", "path": "{{submit.cancel_url}}"},
  "caps": {"lengths": [5, 10], "aspects": ["16:9", "9:16", "1:1"]}
}`

const tplOpenAIImages = `{
  "name": "OpenAI Images",
  "submit": {
    "method": "POST",
    "path": "/images/generations",
    "body": {
      "model": "{{model}}",
      "prompt": "{{prompt}}",
      "n": 1,
      "size": "{{size}}"
    }
  },
  "result": ["data.0.b64_json", "data.0.url"],
  "error": "error.message",
  "test": {"method": "GET", "path": "/models"},
  "caps": {"aspects": ["16:9", "9:16", "1:1"], "size": 1536}
}`

const tplSyncImage = `{
  "submit": {
    "method": "POST",
    "path": "/v1/generate",
    "body": {
      "model": "{{model}}",
      "prompt": "{{prompt}}",
      "aspect_ratio": "{{aspect}}",
      "width": "{{width}}",
      "height": "{{height}}",
      "reference_images": "{{refs}}"
    }
  },
  "result": ["url", "image.url", "data.0.url", "output"],
  "error": ["error.message", "error", "message"],
  "caps": {"maxRefs": 4, "aspects": ["16:9", "9:16", "1:1"]}
}`

const tplSyncVideo = `{
  "submit": {
    "method": "POST",
    "path": "/v1/generate",
    "body": {
      "model": "{{model}}",
      "prompt": "{{prompt}}",
      "aspect_ratio": "{{aspect}}",
      "duration": "{{seconds}}",
      "image": "{{start_image}}"
    }
  },
  "result": ["url", "video.url", "data.0.url", "output"],
  "error": ["error.message", "error", "message"],
  "caps": {"lengths": [5, 10], "aspects": ["16:9", "9:16"]}
}`

const tplAsyncImage = `{
  "submit": {
    "method": "POST",
    "path": "/v1/tasks",
    "body": {
      "model": "{{model}}",
      "prompt": "{{prompt}}",
      "aspect_ratio": "{{aspect}}",
      "reference_images": "{{refs}}"
    }
  },
  "id": "id",
  "poll": {"method": "GET", "path": "/v1/tasks/{id}", "every": 5},
  "state": "status",
  "done": ["succeeded", "completed", "success", "done"],
  "failed": ["failed", "error", "canceled", "cancelled"],
  "queued": ["queued", "pending", "waiting"],
  "progress": "progress",
  "result": ["result.url", "output.url", "url", "output"],
  "error": ["error.message", "error"],
  "cancel": {"method": "POST", "path": "/v1/tasks/{id}/cancel"},
  "caps": {"maxRefs": 4, "aspects": ["16:9", "9:16", "1:1"]}
}`

const tplAsyncVideo = `{
  "submit": {
    "method": "POST",
    "path": "/v1/tasks",
    "body": {
      "model": "{{model}}",
      "prompt": "{{prompt}}",
      "aspect_ratio": "{{aspect}}",
      "duration": "{{seconds}}",
      "image": "{{start_image}}"
    }
  },
  "id": "id",
  "poll": {"method": "GET", "path": "/v1/tasks/{id}", "every": 10},
  "state": "status",
  "done": ["succeeded", "completed", "success", "done"],
  "failed": ["failed", "error", "canceled", "cancelled"],
  "queued": ["queued", "pending", "waiting"],
  "progress": "progress",
  "result": ["result.url", "output.url", "url", "output"],
  "error": ["error.message", "error"],
  "cancel": {"method": "POST", "path": "/v1/tasks/{id}/cancel"},
  "caps": {"lengths": [5, 10], "aspects": ["16:9", "9:16"]}
}`
