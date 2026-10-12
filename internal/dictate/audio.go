package dictate

// Audio helpers: the stream is 16 kHz mono signed 16-bit PCM in frames of
// 20 ms. The level meter and the voice detector are plain energy measures —
// cheap, deterministic (tests feed synthetic audio) and good enough to find
// the pauses between phrases of one speaker at a desk microphone.

import (
	"bytes"
	"encoding/binary"
	"math"
)

// Stream format.
const (
	SampleRate  = 16000
	FrameMs     = 20
	FrameSize   = SampleRate * FrameMs / 1000 // samples per frame
	bytesPerSec = SampleRate * 2
)

// frameDB is the RMS level of a frame in dBFS (−100 for digital silence).
func frameDB(f []int16) float64 {
	if len(f) == 0 {
		return -100
	}
	var sum float64
	for _, s := range f {
		v := float64(s)
		sum += v * v
	}
	rms := math.Sqrt(sum/float64(len(f))) / 32768
	if rms <= 1e-5 {
		return -100
	}
	return 20 * math.Log10(rms)
}

// allZero reports digital silence (a muted or blocked microphone delivers
// exact zeros, a real one never does).
func allZero(f []int16) bool {
	for _, s := range f {
		if s != 0 {
			return false
		}
	}
	return true
}

// meterLevel maps dBFS to the meter's 0…1 (−60 dB → 0, −10 dB → 1).
func meterLevel(db float64) float32 {
	v := (db + 60) / 50
	return float32(math.Max(0, math.Min(1, v)))
}

// vad is an energy voice detector. The noise floor is the quietest frame of
// the last 3 s (speech always dips between syllables, so the floor stays at
// the room's noise; a steady noise becomes the floor itself); in the first
// seconds it is at most −50 dBFS so speech right at the start counts. A
// frame is speech when it is 11 dB over the floor and over −48 dBFS; speech
// goes on for a 240 ms hangover so the gaps between words do not cut it.
type vad struct {
	hist     [vadWindow]float64
	n        int // frames seen
	hang     int // frames of hangover left
	Speaking bool
}

const (
	vadWindow     = 150   // frames of history for the floor (3 s)
	vadStartFloor = -50.0 // the floor's ceiling before a full window
	vadMinDB      = -48.0 // nothing quieter is speech
	vadAboveFloor = 11.0  // dB over the noise floor
	vadHangFrames = 12    // 240 ms
)

// push classifies one frame and returns whether speech is going on.
func (v *vad) push(db float64) bool {
	v.hist[v.n%vadWindow] = db
	v.n++
	floor := 1e9
	for i := 0; i < min(v.n, vadWindow); i++ {
		floor = math.Min(floor, v.hist[i])
	}
	if v.n < vadWindow {
		floor = math.Min(floor, vadStartFloor)
	}
	floor = math.Max(floor, -75)
	if db > vadMinDB && db > floor+vadAboveFloor {
		v.hang = vadHangFrames
		v.Speaking = true
		return true
	}
	if v.hang > 0 {
		v.hang--
		return true
	}
	v.Speaking = false
	return false
}

// wav wraps PCM in a WAV file (16 kHz mono 16-bit).
func wav(pcm []int16) []byte {
	var b bytes.Buffer
	n := uint32(len(pcm) * 2)
	b.Grow(int(n) + 44)
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, 36+n)
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&b, binary.LittleEndian, uint16(1)) // mono
	_ = binary.Write(&b, binary.LittleEndian, uint32(SampleRate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(bytesPerSec))
	_ = binary.Write(&b, binary.LittleEndian, uint16(2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, n)
	_ = binary.Write(&b, binary.LittleEndian, pcm)
	return b.Bytes()
}

// pcmBytes is the little-endian byte form of PCM samples.
func pcmBytes(pcm []int16) []byte {
	out := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(out[2*i:], uint16(s))
	}
	return out
}

// upsampler converts 16 kHz to 24 kHz for services that take 24 kHz only:
// linear interpolation, continuous across frames. Output sample k sits at
// input position 2k/3, so input sample n (n ≥ 1) closes the span (n−1, n],
// which holds one output when n is odd (at n−1/3) and two when n is even
// (at n−2/3 and at n itself).
type upsampler struct {
	last int16
	n    int // index of the last input sample
	has  bool
}

func (u *upsampler) push(in []int16) []int16 {
	out := make([]int16, 0, len(in)*3/2+2)
	for _, s := range in {
		if !u.has {
			u.last, u.has, u.n = s, true, 0
			out = append(out, s)
			continue
		}
		u.n++
		a, b := float64(u.last), float64(s)
		if u.n%2 == 1 {
			out = append(out, int16(math.Round(a+(b-a)*2/3)))
		} else {
			out = append(out, int16(math.Round(a+(b-a)/3)), s)
		}
		u.last = s
	}
	return out
}
