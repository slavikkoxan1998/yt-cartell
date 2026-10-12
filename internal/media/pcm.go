package media

// The film's sound is assembled here, in Go, from exact-length PCM parts
// instead of concatenating AAC segments: every scene's ambient part lands on
// the scene's first sample (overlapping transitions sum two parts, each
// already crossfaded), every voice line on the sample where the editor put
// it. The result is sample-exact — no AAC priming gaps at the cuts, no drift
// after 150 scenes — and a look-ahead limiter keeps the mix under −1 dBFS
// (voice lines moved over each other are allowed and simply summed).

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"sort"
)

const (
	pcmFrameBytes = samplesPerFrame * 4 // one video frame of 16-bit stereo audio
	mixChunk      = sampleRate          // samples summed at a time (1 s)
	limBlock      = 240                 // limiter block: 5 ms
	// limCeil is the loudest sample the mix may reach: −1 dBFS.
	limCeil = 32767 * 0.8912509381337456
	// limRelease: per block the gain recovers this share of the way to 1 (~80 ms).
	limRelease = 0.06
)

// pcmPart is a raw s16le stereo file placed on the film timeline.
type pcmPart struct {
	path string
	at   int // first sample on the film
	n    int // length in samples (padded with silence / cut to it)
}

// mixParts writes a 16-bit stereo 48 kHz WAV of exactly total samples to
// out: every part summed at its place; with limit the sum is held under
// −1 dBFS by a look-ahead limiter (transparent where nothing is too loud),
// otherwise it is only clamped to the 16-bit range.
func mixParts(out string, total int, parts []pcmPart, limit bool) error {
	data := int64(total) * 4
	if data > math.MaxUint32-36 {
		return errors.New("фільм задовгий для одного звукового файлу")
	}
	parts = append([]pcmPart(nil), parts...)
	sort.SliceStable(parts, func(a, b int) bool { return parts[a].at < parts[b].at })
	fh, err := os.Create(out)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(fh, 1<<20)
	err = func() error {
		if err := writeWAVHeader(w, uint32(data)); err != nil {
			return err
		}
		sink := &pcmSink{w: w, gain: 1, limit: limit}
		acc := make([]int32, 2*mixChunk)
		type open struct {
			p   pcmPart
			buf []byte
		}
		var active []open
		next := 0
		for c0 := 0; c0 < total; c0 += mixChunk {
			c1 := min(total, c0+mixChunk)
			for ; next < len(parts) && parts[next].at < c1; next++ {
				p := parts[next]
				if p.n <= 0 || p.at+p.n <= c0 {
					continue
				}
				buf, err := readPart(p.path, p.n*4)
				if err != nil {
					return err
				}
				active = append(active, open{p, buf})
			}
			a := acc[:2*(c1-c0)]
			clear(a)
			keep := active[:0]
			for _, o := range active {
				from, to := max(o.p.at, c0), min(o.p.at+o.p.n, c1)
				for i := from; i < to; i++ {
					src := o.buf[(i-o.p.at)*4:]
					d := a[2*(i-c0):]
					d[0] += int32(int16(binary.LittleEndian.Uint16(src)))
					d[1] += int32(int16(binary.LittleEndian.Uint16(src[2:])))
				}
				if o.p.at+o.p.n > c1 {
					keep = append(keep, o)
				}
			}
			active = keep
			for b := 0; b < len(a); b += 2 * limBlock {
				if err := sink.push(a[b:min(len(a), b+2*limBlock)]); err != nil {
					return err
				}
			}
		}
		return sink.flush()
	}()
	if err == nil {
		err = w.Flush()
	}
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(out)
	}
	return err
}

// pcmSink turns summed blocks into 16-bit samples. With limit it looks one
// block ahead: the gain is already down when a too-loud block arrives (no
// sample ever exceeds limCeil) and recovers smoothly afterwards.
type pcmSink struct {
	w     io.Writer
	limit bool
	gain  float64 // gain at the end of the last written block
	pend  []int32 // block waiting for its look-ahead
	out   []byte
}

func peakOf(b []int32) float64 {
	m := int32(0)
	for _, v := range b {
		if v < 0 {
			v = -v
		}
		m = max(m, v)
	}
	return float64(m)
}

func needGain(peak float64) float64 {
	if peak <= limCeil {
		return 1
	}
	return limCeil / peak
}

func (s *pcmSink) push(block []int32) error {
	if !s.limit {
		return s.write(block, 1, 1)
	}
	if s.pend != nil {
		if err := s.process(s.pend, peakOf(block)); err != nil {
			return err
		}
	}
	s.pend = append(s.pend[:0], block...)
	return nil
}

func (s *pcmSink) flush() error {
	if !s.limit || s.pend == nil {
		return nil
	}
	err := s.process(s.pend, 0)
	s.pend = nil
	return err
}

// process writes block with a gain ramp that never exceeds what the block
// itself needs and already reaches what the next block needs.
func (s *pcmSink) process(block []int32, nextPeak float64) error {
	cur := needGain(peakOf(block))
	target := math.Min(cur, needGain(nextPeak))
	start := math.Min(s.gain, cur)
	end := target
	if target > start { // release: recover slowly, then exactly to unity
		end = math.Min(target, start+(1-start)*limRelease)
		if target >= 1 && end > 0.998 {
			end = 1
		}
	}
	s.gain = end
	return s.write(block, start, end)
}

// write converts block to 16-bit with a linear gain ramp from g0 to g1.
func (s *pcmSink) write(block []int32, g0, g1 float64) error {
	n := len(block) / 2
	s.out = s.out[:0]
	for i := 0; i < n; i++ {
		g := g1
		if g0 != g1 {
			g = g0 + (g1-g0)*float64(i+1)/float64(n)
		}
		for c := 0; c < 2; c++ {
			v := float64(block[2*i+c])
			if g != 1 {
				v = math.Round(v * g)
			}
			v = math.Max(math.MinInt16, math.Min(math.MaxInt16, v))
			s.out = binary.LittleEndian.AppendUint16(s.out, uint16(int16(v)))
		}
	}
	_, err := s.w.Write(s.out)
	return err
}

// readPart reads a raw PCM part, padded with silence / cut to exactly n bytes.
func readPart(path string, n int) ([]byte, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	buf := make([]byte, n)
	if _, err := io.ReadFull(fh, buf); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf, nil
}

func writeWAVHeader(w io.Writer, data uint32) error {
	var h [44]byte
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+data)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], 2) // stereo
	binary.LittleEndian.PutUint32(h[24:], sampleRate)
	binary.LittleEndian.PutUint32(h[28:], sampleRate*4)
	binary.LittleEndian.PutUint16(h[32:], 4)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], data)
	_, err := w.Write(h[:])
	return err
}
