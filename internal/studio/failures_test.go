package studio

import "testing"

func TestJobFailuresAndRetryable(t *testing.T) {
	p := &Project{
		Characters: []Character{{ID: "c1", Image: Asset{Status: StatusError}}},
		Scenes: []Scene{
			{ID: "s1", Frame: Asset{Status: StatusDone}, Video: Asset{Status: StatusError}, Audio: Asset{Status: StatusError}},
			{ID: "s2", Frame: Asset{Status: StatusError}, Video: Asset{Status: StatusDone}, Audio: Asset{Status: StatusDone}},
		},
	}
	cases := map[JobKind]int{
		JobPlan:      0,
		JobRefs:      1,
		JobFrames:    2, // missing references + frames
		JobVideos:    1,
		JobVoices:    1,
		JobRender:    2,
		JobMontage:   0,
		JobAutopilot: 4,
	}
	for k, want := range cases {
		if got := jobFailures(p, k); got != want {
			t.Errorf("%s: %d failures, want %d", k, got, want)
		}
	}
	done := QueueItem{Kind: JobFrames, Status: JobDone, Failures: 2}
	if !done.PartlyFailed() || !done.Retryable() {
		t.Fatal("a job done with failures must be retryable")
	}
	clean := QueueItem{Kind: JobFrames, Status: JobDone}
	if clean.PartlyFailed() || clean.Retryable() {
		t.Fatal("a clean job is not retryable")
	}
}
