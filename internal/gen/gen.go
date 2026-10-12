// Package gen holds the provider-neutral request types shared by the image
// and video backends (G-Labs Studio, Google Gemini/Veo). It has no deps so
// every other package can import it without cycles.
package gen

// RefImage is an input image (character/location reference, start frame).
type RefImage struct {
	Data []byte
	MIME string // image/png | image/jpeg | image/webp
	// Name binds the image to "@name" in the prompt (G-Labs @tag binding).
	// Use "<tag>.<ext>", e.g. "taras.jpg". Empty = unnamed.
	Name string
}

// ImageRequest asks for one still image.
type ImageRequest struct {
	Prompt  string
	UIModel string // "NARWHAL" (Nano Banana 2) | "GEM_PIX_2" (Pro) | "GEM_PIX" (2 Lite)
	Aspect  string // "16:9" | "9:16" | "1:1" | "3:4" | "4:3"
	Refs    []RefImage
}

// Video modes (VideoRequest.Mode).
const (
	// ModeAuto animates Start (image-to-video) when it is set, else renders
	// from the prompt alone (text-to-video).
	ModeAuto = ""
	// ModeComponents renders from named reference images (Refs) + the prompt:
	// G-Labs "components" mode (Veo up to 3 images, Omni Flash up to 7),
	// Google Gemini Omni image references (up to 10, bound as <IMAGE_REF_N>)
	// or Google Veo 3.1 "asset" referenceImages (up to 3, 8 s clips).
	ModeComponents = "components"
)

// VideoRequest asks for one clip.
type VideoRequest struct {
	Prompt string
	// Start is the start frame; nil = text-to-video. In ModeComponents G-Labs
	// and Google Omni ignore it (pass the frame as a named ref instead);
	// Google Veo uses it when the model cannot take reference images.
	Start      *RefImage
	Aspect     string // "16:9" | "9:16" (anything else → 16:9)
	Seconds    int    // 4 | 6 | 8 (10: Omni Flash — G-Labs omni_flash, Google Gemini Omni); 0 = Settings
	Model      string // provider model id (G-Labs: veo_31_fast…); "" = provider default
	Resolution string // "720p" | "1080p" | "4K" (G-Labs, Google Gemini Omni)
	// Mode is ModeAuto ("") or ModeComponents; anything else behaves like ModeAuto.
	Mode string
	// Refs are the ModeComponents references in PRIORITY order: when the model
	// takes fewer, the first ones are kept. Name binds a ref to "@name" in the
	// prompt (G-Labs). Ignored in the other modes.
	Refs []RefImage
}

// Progress reports 0–100 and a short phase ("queued", "rendering", …).
type Progress func(pct int, phase string)

// NoProgress is a Progress that ignores updates.
func NoProgress(int, string) {}
