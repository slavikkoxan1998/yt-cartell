package sysx

import "time"

// Single is the app's single-instance lock, held until the process exits
// (or Release). Two running copies of the studio would each keep their own
// view of the settings and overwrite each other's changes.
type Single struct{ release func() }

// Release frees the lock (the process exiting frees it too).
func (s *Single) Release() {
	if s != nil && s.release != nil {
		s.release()
		s.release = nil
	}
}

// AcquireSingle takes the lock called name. ok is false while another copy
// holds it; wait > 0 keeps trying that long (a restart after an update while
// the old copy is still closing).
func AcquireSingle(name string, wait time.Duration) (*Single, bool) {
	deadline := time.Now().Add(wait)
	for {
		if rel, ok := acquire(name); ok {
			return &Single{release: rel}, true
		}
		if time.Now().After(deadline) {
			return nil, false
		}
		time.Sleep(250 * time.Millisecond)
	}
}
