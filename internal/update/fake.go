package update

import (
	"context"
	"sync"
	"time"
)

// Fake is an Updater that never touches the network or the disk: headless
// renders and tests of the UI use it. Latest returns Release (or Err);
// Download reports Steps progress callbacks over Size bytes (waiting Delay
// between them, or until Hold is closed) and returns "fake-download" (or
// DownloadErr); Apply records the path and returns a Restart that records
// its version (or ApplyErr).
type Fake struct {
	Release     Release
	Err         error
	DownloadErr error
	ApplyErr    error
	Size        int64         // 0 = 40 MB
	Steps       int           // 0 = 4
	Delay       time.Duration // between progress steps
	Hold        chan struct{} // Download waits for it after the first step (nil = no wait)

	mu        sync.Mutex
	checks    int
	downloads int
	applied   []string
	restarted []string
}

// Latest returns f.Release or f.Err.
func (f *Fake) Latest(ctx context.Context) (Release, error) {
	f.mu.Lock()
	f.checks++
	f.mu.Unlock()
	if f.Err != nil {
		return Release{}, f.Err
	}
	return f.Release, ctx.Err()
}

// Download pretends to fetch the build.
func (f *Fake) Download(ctx context.Context, rel Release, progress func(done, total int64)) (string, error) {
	f.mu.Lock()
	f.downloads++
	f.mu.Unlock()
	size, steps := f.Size, f.Steps
	if size <= 0 {
		size = 40 << 20
	}
	if steps <= 0 {
		steps = 4
	}
	for i := 1; i <= steps; i++ {
		if progress != nil {
			progress(size*int64(i)/int64(steps), size)
		}
		if i == 1 && f.Hold != nil {
			select {
			case <-f.Hold:
			case <-ctx.Done():
				return "", context.Canceled
			}
		}
		if f.Delay > 0 {
			select {
			case <-time.After(f.Delay):
			case <-ctx.Done():
				return "", context.Canceled
			}
		}
		if ctx.Err() != nil {
			return "", context.Canceled
		}
	}
	if f.DownloadErr != nil {
		return "", f.DownloadErr
	}
	return "fake-download", nil
}

// Apply records the install.
func (f *Fake) Apply(staged string) (Restart, error) {
	if f.ApplyErr != nil {
		return nil, f.ApplyErr
	}
	f.mu.Lock()
	f.applied = append(f.applied, staged)
	f.mu.Unlock()
	return func(from string) error {
		f.mu.Lock()
		f.restarted = append(f.restarted, from)
		f.mu.Unlock()
		return nil
	}, nil
}

// Counts reports the calls so far.
func (f *Fake) Counts() (checks, downloads, applies, restarts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks, f.downloads, len(f.applied), len(f.restarted)
}
