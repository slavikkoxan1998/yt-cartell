package dictate

// A dictation session: FFmpeg capture → level meter, voice detector,
// silence timer → the recogniser → finals and the interim text the UI takes.
//
// Threading: Start returns at once; one goroutine reads the microphone (and
// owns the stop decisions), the recogniser runs on its own goroutines. The
// UI reads State and Take from its goroutine (both lock briefly) and is
// woken through Options.Notify. Nothing ever blocks the caller.
//
// Timing is counted in audio (frames of 20 ms), not on the wall clock: the
// silence stop and the meter behave the same in tests that feed a file.

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Phase is where a session is.
type Phase int

const (
	Starting  Phase = iota // opening the microphone and the service
	Listening              // recording
	Finishing              // the microphone is off; the last words are being recognised
	Done
)

// StopReason says why a session ended.
type StopReason int

const (
	StopNone    StopReason = iota
	StopUser               // «Стоп», Esc or the mic button
	StopSilence            // the silence timeout
	StopLimit              // the longest dictation (MaxLength)
	StopGone               // the story box went away (the UI stopped beating)
	StopEnded              // the audio input ended by itself
	StopError              // something failed (State.Err)
)

// State is a snapshot of a session for the UI.
type State struct {
	Phase    Phase
	Level    float32       // microphone level now, 0…1
	Speaking bool          // the voice detector hears speech
	Heard    bool          // any speech so far
	Elapsed  time.Duration // audio recorded
	// Mute: the microphone delivers exact zeros (muted, or blocked by the
	// OS privacy settings).
	Mute   bool
	Device string // the microphone in use
	Label  string // the service
	Reason StopReason
	Err    error // with StopError
}

// Options start a session.
type Options struct {
	Plan   Plan
	FFmpeg string
	// Notify (optional) is called when State or the text changed; any
	// goroutine; must not block.
	Notify func()
}

// Tunables (package vars: tests shorten them).
var (
	MaxLength     = 15 * time.Minute // the longest dictation
	beatTimeout   = 4 * time.Second  // no UI heartbeat → the box went away
	finishStream  = 6 * time.Second  // waiting for the last words: streaming services
	finishChunked = 20 * time.Second // phrase services (a slow computer for local)
	muteAfter     = 1500 * time.Millisecond
)

// Session is one dictation.
type Session struct {
	opt    Options
	ctx    context.Context
	cancel context.CancelFunc
	stopCh chan StopReason
	done   chan struct{}
	beat   atomic.Int64

	mu      sync.Mutex
	st      State
	finals  []string
	interim string
	ver     uint64
	taken   uint64
}

// Start begins a session in the background (errors arrive in State).
func Start(opt Options) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{opt: opt, ctx: ctx, cancel: cancel, stopCh: make(chan StopReason, 4), done: make(chan struct{})}
	s.st.Label = opt.Plan.Label
	go s.run()
	return s
}

// Stop ends the dictation the normal way: the microphone goes off and the
// last words are still recognised. A second Stop while finishing cancels.
func (s *Session) Stop() { s.StopFor(StopUser) }

// StopFor stops with a reason.
func (s *Session) StopFor(r StopReason) {
	s.mu.Lock()
	ph := s.st.Phase
	s.mu.Unlock()
	if ph == Finishing && r == StopUser {
		s.cancel()
		return
	}
	select {
	case s.stopCh <- r:
	default:
	}
}

// Cancel aborts at once; text not recognised yet is lost.
func (s *Session) Cancel() {
	s.cancel()
	select {
	case s.stopCh <- StopUser:
	default:
	}
}

// Heartbeat tells the session its text box is still on screen (the UI calls
// it every frame); without beats for a few seconds it stops (StopGone).
func (s *Session) Heartbeat() { s.beat.Store(time.Now().UnixNano()) }

// Done is closed when the session ended (Phase Done).
func (s *Session) Done() <-chan struct{} { return s.done }

// State returns a snapshot.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// Take returns the finals recognised since the last call and the current
// interim text; changed is false when nothing happened since.
func (s *Session) Take() (finals []string, interim string, changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	finals, s.finals = s.finals, nil
	changed = s.ver != s.taken
	s.taken = s.ver
	return finals, s.interim, changed
}

func (s *Session) notify() {
	if s.opt.Notify != nil {
		s.opt.Notify()
	}
}

func (s *Session) update(fn func(st *State)) {
	s.mu.Lock()
	fn(&s.st)
	s.mu.Unlock()
	s.notify()
}

// sink implementation for the recognisers.
func (s *Session) final(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	s.finals = append(s.finals, text)
	s.ver++
	s.mu.Unlock()
	s.notify()
}

func (s *Session) setInterim(text string) {
	text = strings.TrimSpace(text)
	s.mu.Lock()
	if s.interim == text {
		s.mu.Unlock()
		return
	}
	s.interim = text
	s.ver++
	s.mu.Unlock()
	s.notify()
}

// finish ends the session.
func (s *Session) finish(r StopReason, err error) {
	s.mu.Lock()
	if s.interim != "" {
		// Words the service never finalised are better kept than lost.
		s.finals = append(s.finals, s.interim)
		s.interim = ""
		s.ver++
	}
	s.st.Phase, s.st.Level, s.st.Speaking = Done, 0, false
	if s.st.Reason == StopNone || err != nil {
		s.st.Reason = r
	}
	if err != nil {
		s.st.Reason, s.st.Err = StopError, err
	}
	s.mu.Unlock()
	s.cancel()
	close(s.done)
	s.notify()
}

type sinkFuncs struct{ s *Session }

func (k sinkFuncs) final(t string)   { k.s.final(t) }
func (k sinkFuncs) interim(t string) { k.s.setInterim(t) }

// recognisers get their audio and report text through these.
type sink interface {
	final(text string)
	interim(text string)
}

type recognizer interface {
	// run consumes 20 ms frames until audio is closed (the end of the
	// recording), finishes the text and returns. A returned error ends
	// the session.
	run(ctx context.Context, audio <-chan []int16, out sink) error
	// finishTime is how long the session waits for the last words.
	finishTime() time.Duration
}

// newRecognizer is the recogniser for a plan (a var: tests replace it).
var newRecognizer = func(p Plan) (recognizer, error) {
	switch p.Provider {
	case ElevenLabs:
		return &elevenRec{p: p}, nil
	case OpenAI:
		return &openaiRec{p: p}, nil
	case Deepgram:
		return &deepgramRec{p: p}, nil
	case AssemblyAI:
		return &assemblyRec{p: p}, nil
	case OpenAIChunks:
		return newChunkRec(&openaiHTTP{p: p}, chunkCloud, p.Language), nil
	case Gemini:
		return newChunkRec(&geminiT{p: p}, chunkCloud, p.Language), nil
	case Local:
		return newLocalRec(p), nil
	}
	return nil, planErr(p)
}

// listDevices and startCap are seams for tests.
var (
	listDevices = ListDevices
	startCap    = startCapture
)

func (s *Session) run() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("dictate: panic: %v", r)
			s.finish(StopError, newErr(KindOther, nil, "Диктування зупинилось через внутрішню помилку"))
		}
	}()
	p := s.opt.Plan
	if !p.Ready {
		s.finish(StopError, planErr(p))
		return
	}
	if s.opt.FFmpeg == "" {
		s.finish(StopError, errNoFFmpeg())
		return
	}
	devs, err := listDevices(s.ctx, s.opt.FFmpeg)
	if err != nil {
		s.finish(StopError, err)
		return
	}
	dev, ok := pickDevice(devs, p.Device)
	if !ok {
		if goos == "darwin" || goos == "windows" {
			s.finish(StopError, newErr(KindNoMic, nil, "Мікрофон не знайдено — підключи його (або дозволь доступ до нього) і спробуй ще раз"))
			return
		}
		dev = Device{ID: "default", Name: "типовий"}
	}
	s.update(func(st *State) { st.Device = dev.Name })
	rec, err := newRecognizer(p)
	if err != nil {
		s.finish(StopError, err)
		return
	}
	cp, err := startCap(s.ctx, s.opt.FFmpeg, dev)
	if err != nil {
		s.finish(StopError, err)
		return
	}

	rctx, rcancel := context.WithCancel(s.ctx)
	defer rcancel()
	audio := make(chan []int16, 4096) // ~80 s: rides out a slow connection
	recDone := make(chan error, 1)
	go func() {
		var err error
		defer func() {
			if r := recover(); r != nil {
				log.Printf("dictate: recogniser panic: %v", r)
				err = newErr(KindOther, nil, "Розпізнавання зупинилось через внутрішню помилку")
			}
			recDone <- err
		}()
		err = rec.run(rctx, audio, sinkFuncs{s})
	}()

	reason := StopNone
	var recErr error
	recEnded := false
	var v vad
	frames, quiet, zeros := 0, 0, 0
	heard := false
	silence := p.Silence * 1000 / FrameMs
	limit := int(MaxLength / (FrameMs * time.Millisecond))
	muteFrames := int(muteAfter / (FrameMs * time.Millisecond))
	beatFrames := 0
	capErr := cp.read(func(f []int16) bool {
		frames++
		db := frameDB(f)
		sp := v.push(db)
		if sp {
			quiet, heard = 0, true
		} else {
			quiet++
		}
		if allZero(f) {
			zeros++
		} else {
			zeros = 0
		}
		mute := zeros >= muteFrames
		select {
		case audio <- f:
		default:
			recErr = newErr(KindNetwork, nil, "Сервіс розпізнавання не встигає за мовленням (повільний інтернет?) — спробуй ще раз")
			reason = StopError
			return false
		}
		if frames%3 == 0 || frames == 1 {
			lvl := meterLevel(db)
			el := time.Duration(frames) * FrameMs * time.Millisecond
			s.update(func(st *State) {
				st.Phase, st.Level, st.Speaking, st.Heard, st.Elapsed, st.Mute = Listening, lvl, sp, heard, el, mute
			})
		}
		select {
		case r := <-s.stopCh:
			reason = r
			return false
		case err := <-recDone:
			recEnded, recErr = true, err
			if err == nil {
				reason = StopEnded
			} else {
				reason = StopError
			}
			return false
		default:
		}
		if silence > 0 && quiet >= silence {
			reason = StopSilence
			return false
		}
		if frames >= limit {
			reason = StopLimit
			return false
		}
		// The UI's heartbeat, checked every half second of audio.
		if beatFrames++; beatFrames >= 25 {
			beatFrames = 0
			if b := s.beat.Load(); b != 0 && time.Since(time.Unix(0, b)) > beatTimeout {
				reason = StopGone
				return false
			}
		}
		return true
	})
	cp.stop()
	close(audio)
	if reason == StopNone {
		switch {
		case capErr == nil || errors.Is(capErr, io.EOF):
			reason = StopEnded
		case s.ctx.Err() != nil:
			reason = StopUser
		default:
			reason, recErr = StopError, capErr
		}
	}
	if recErr != nil && reason == StopError {
		rcancel()
		if !recEnded {
			<-recDone
		}
		s.finish(StopError, recErr)
		return
	}
	s.update(func(st *State) { st.Phase, st.Level, st.Speaking, st.Reason = Finishing, 0, false, reason })
	if !recEnded {
		t := time.NewTimer(rec.finishTime())
		select {
		case recErr = <-recDone:
		case <-t.C:
			rcancel()
			<-recDone
		case <-s.ctx.Done(): // a second «Стоп»
			rcancel()
			<-recDone
		}
		t.Stop()
	}
	if recErr != nil && s.ctx.Err() == nil && !errors.Is(recErr, context.Canceled) {
		s.finish(StopError, recErr)
		return
	}
	s.finish(reason, nil)
}
