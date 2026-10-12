package config

import "strings"

// Remake is «Перезняти за прикладом»: which model watches a YouTube example
// (package reference, studio/remake.go).
type Remake struct {
	// Analysis (stored ids, never renamed): "" = auto (the Google AI Studio
	// keys when there are some, else the text service if it can watch
	// YouTube) | "google" (Gemini API with Google.Keys) | "text" (the
	// OpenAI-compatible Text endpoint, e.g. CarteLink with a Gemini model).
	Analysis string `json:"analysis,omitempty"`
	// Model is the Gemini model that watches ("" = Google.StoryModel).
	Model string `json:"model,omitempty"`
}

// Remake analysis ids.
const (
	RemakeAuto   = ""
	RemakeGoogle = "google"
	RemakeText   = "text"
)

func (r *Remake) normalize() {
	r.Analysis = strings.ToLower(strings.TrimSpace(r.Analysis))
	switch r.Analysis {
	case RemakeGoogle, RemakeText:
	default:
		r.Analysis = RemakeAuto
	}
	r.Model = strings.TrimPrefix(strings.TrimSpace(r.Model), "models/")
}
