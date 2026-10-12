package dictate

// Microphone capture through FFmpeg (already required by the app; Windows
// builds have no cgo, so no native audio API): FFmpeg opens the device and
// writes 16 kHz mono s16le PCM to its stdout, which the session reads in
// 20 ms frames.
//
//	Windows  list:    ffmpeg -list_devices true -f dshow -i dummy
//	         capture: ffmpeg -f dshow -audio_buffer_size 50 -i audio=<alternative name>
//	         (-audio_buffer_size is in ms; DirectShow's default is ~500 ms of
//	         latency. The alternative name "@device_cm_{…}\wave_{…}" is unique
//	         and ASCII — friendly names may repeat or be localised.)
//	macOS    list:    ffmpeg -f avfoundation -list_devices true -i ""
//	         capture: ffmpeg -f avfoundation -i ":<audio index>" (":default" =
//	         the system input). The first capture makes macOS ask for the
//	         microphone on behalf of the app (Info.plist
//	         NSMicrophoneUsageDescription, see build-mac.sh).
//	Linux    list:    ffmpeg -sources pulse;  capture: ffmpeg -f pulse -i <source|default>
//
// Docs: https://ffmpeg.org/ffmpeg-devices.html (dshow, avfoundation, pulse).
// Output: -ac 1 -ar 16000 -f s16le -flush_packets 1 pipe:1 (flushed per
// packet: a pipe would otherwise buffer ~1 s).

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/sysx"
)

// Device is a microphone FFmpeg can open.
type Device struct {
	Name string // what the user sees ("Microphone (Realtek(R) Audio)")
	ID   string // what FFmpeg opens: dshow alternative name, avfoundation index, pulse source
	// Default marks the system's default input where the OS says so
	// (pulse); elsewhere the first device stands for it.
	Default bool
}

// Seams for tests.
var (
	goos        = runtime.GOOS
	listTimeout = 12 * time.Second
)

// ListDevices asks FFmpeg for the microphones. It records nothing.
func ListDevices(ctx context.Context, ffmpeg string) ([]Device, error) {
	if ffmpeg == "" {
		return nil, errNoFFmpeg()
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	var args []string
	switch goos {
	case "windows":
		args = []string{"-hide_banner", "-nostdin", "-list_devices", "true", "-f", "dshow", "-i", "dummy"}
	case "darwin":
		args = []string{"-hide_banner", "-nostdin", "-f", "avfoundation", "-list_devices", "true", "-i", ""}
	default:
		args = []string{"-hide_banner", "-nostdin", "-sources", "pulse"}
	}
	buf := &tailBuf{max: 256 << 10}
	cmd := sysx.Command(ctx, ffmpeg, args...)
	cmd.Stdout, cmd.Stderr = buf, buf
	cmd.WaitDelay = 2 * time.Second
	err := sysx.Run(cmd) // listing always "fails" (there is no input "dummy")
	out := buf.text()
	if ctx.Err() != nil && out == "" {
		return nil, newErr(KindNoMic, ctx.Err(), "FFmpeg не відповів на запит списку мікрофонів — спробуй ще раз")
	}
	var devs []Device
	switch goos {
	case "windows":
		devs = parseDShow(out)
	case "darwin":
		devs = parseAVFoundation(out)
	default:
		devs = parsePulse(out)
	}
	if len(devs) == 0 && err != nil && strings.Contains(strings.ToLower(out), "unknown input format") {
		return nil, newErr(KindNoMic, err, "Цей FFmpeg не вміє працювати з мікрофоном (немає %s) — встанови повну збірку FFmpeg", inputFormat())
	}
	return devs, nil
}

func inputFormat() string {
	switch goos {
	case "windows":
		return "dshow"
	case "darwin":
		return "avfoundation"
	}
	return "pulse"
}

var (
	logPrefix = regexp.MustCompile(`^\[[^\]]*\]\s*`)
	quotedRe  = regexp.MustCompile(`^"(.*)"\s*(\(([^)]*)\))?\s*$`)
	altRe     = regexp.MustCompile(`^Alternative name\s+"(.*)"\s*$`)
)

// parseDShow reads `-list_devices true -f dshow` output: since FFmpeg 4.4
// each device line ends with its kind — `"Microphone (Realtek(R) Audio)"
// (audio)` — followed by `Alternative name "@device_cm_{…}"`; older builds
// group the names under "DirectShow audio devices".
func parseDShow(out string) []Device {
	var devs []Device
	section := ""
	lastAudio := false
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(logPrefix.ReplaceAllString(strings.TrimSpace(sc.Text()), ""))
		low := strings.ToLower(line)
		switch {
		case strings.Contains(low, "directshow audio devices"):
			section, lastAudio = "audio", false
			continue
		case strings.Contains(low, "directshow video devices"):
			section, lastAudio = "video", false
			continue
		}
		if m := altRe.FindStringSubmatch(line); m != nil {
			if lastAudio && len(devs) > 0 && devs[len(devs)-1].ID == "" {
				devs[len(devs)-1].ID = m[1]
			}
			continue
		}
		if m := quotedRe.FindStringSubmatch(line); m != nil {
			kind := strings.ToLower(m[3])
			audio := strings.Contains(kind, "audio") || (kind == "" && section == "audio")
			lastAudio = audio
			if audio && m[1] != "" {
				devs = append(devs, Device{Name: m[1]})
			}
			continue
		}
		lastAudio = false
	}
	return devs
}

var (
	avIndexRe = regexp.MustCompile(`^\[(\d+)\]\s+(.+)$`)
	avIDTail  = regexp.MustCompile(`\s*\[(uid|serial):[^\]]*\]\s*$`) // FFmpeg master lists them after the name
)

// parseAVFoundation reads `-f avfoundation -list_devices true -i ""`:
// "AVFoundation audio devices:" followed by "[0] MacBook Pro Microphone".
func parseAVFoundation(out string) []Device {
	var devs []Device
	audio := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(logPrefix.ReplaceAllString(strings.TrimSpace(sc.Text()), ""))
		low := strings.ToLower(line)
		switch {
		case strings.Contains(low, "avfoundation audio devices"):
			audio = true
			continue
		case strings.Contains(low, "avfoundation video devices"):
			audio = false
			continue
		}
		if !audio {
			continue
		}
		if m := avIndexRe.FindStringSubmatch(line); m != nil {
			name := m[2]
			for avIDTail.MatchString(name) {
				name = avIDTail.ReplaceAllString(name, "")
			}
			devs = append(devs, Device{Name: strings.TrimSpace(name), ID: m[1]})
		} else if line != "" && !strings.HasPrefix(line, "[") {
			audio = false // the listing ended (an error line follows)
		}
	}
	return devs
}

var pulseRe = regexp.MustCompile(`^(\*?)\s*(\S+)\s+\[(.*?)\](\s*\(.*\))?\s*$`)

// parsePulse reads `ffmpeg -sources pulse`: "* <source> [<description>]"
// (the star marks the default); monitors of outputs are not microphones.
func parsePulse(out string) []Device {
	var devs []Device
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		m := pulseRe.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if m == nil || strings.HasSuffix(m[2], ".monitor") {
			continue
		}
		devs = append(devs, Device{Name: m[3], ID: m[2], Default: m[1] == "*"})
	}
	return devs
}

// pickDevice chooses the configured microphone by name (or id), else the
// default one, else the first. ok is false when there is none at all.
func pickDevice(devs []Device, want string) (Device, bool) {
	want = strings.TrimSpace(want)
	if want != "" {
		for _, d := range devs {
			if d.Name == want || d.ID == want {
				return d, true
			}
		}
		for _, d := range devs {
			if strings.EqualFold(d.Name, want) {
				return d, true
			}
		}
	}
	for _, d := range devs {
		if d.Default {
			return d, true
		}
	}
	if len(devs) > 0 {
		return devs[0], true
	}
	return Device{}, false
}

// inputArgs are FFmpeg's input options for dev.
func inputArgs(dev Device) []string {
	low := []string{"-fflags", "nobuffer", "-probesize", "32", "-analyzeduration", "0"}
	switch goos {
	case "windows":
		id := dev.ID
		if id == "" {
			id = dev.Name
		}
		return append(low, "-f", "dshow", "-audio_buffer_size", "50", "-i", "audio="+id)
	case "darwin":
		id := dev.ID
		if id == "" {
			id = "default"
		}
		return append(low, "-f", "avfoundation", "-i", ":"+id)
	}
	id := dev.ID
	if id == "" {
		id = "default"
	}
	return append(low, "-f", "pulse", "-fragment_size", "1024", "-i", id)
}

// outputArgs make FFmpeg write the stream format to stdout.
var outputArgs = []string{"-vn", "-ac", "1", "-ar", "16000", "-acodec", "pcm_s16le", "-f", "s16le", "-flush_packets", "1", "pipe:1"}

// capture is a running FFmpeg capture.
type capture struct {
	cmd    *exec.Cmd
	out    io.ReadCloser
	log    *tailBuf
	dev    Device
	stopMu sync.Mutex
	killed bool
}

// startCapture starts FFmpeg on dev; frames come from read.
func startCapture(ctx context.Context, ffmpeg string, dev Device) (*capture, error) {
	args := append([]string{"-hide_banner", "-nostdin", "-nostats", "-loglevel", "error"}, inputArgs(dev)...)
	args = append(args, outputArgs...)
	cmd := sysx.Command(ctx, ffmpeg, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	log := &tailBuf{max: 16 << 10}
	cmd.Stderr = log
	cmd.WaitDelay = 2 * time.Second
	if err := sysx.Start(cmd); err != nil {
		return nil, newErr(KindNoFFmpeg, err, "Не вдалося запустити FFmpeg для мікрофона: %s", clip(err.Error(), 160))
	}
	return &capture{cmd: cmd, out: out, log: log, dev: dev}, nil
}

// read delivers 20 ms frames to fn until FFmpeg stops (or fn returns
// false), then waits for it and returns why it stopped (nil after stop).
func (c *capture) read(fn func(frame []int16) bool) error {
	br := bufio.NewReaderSize(c.out, 16<<10)
	raw := make([]byte, FrameSize*2)
	got := 0
	var rerr error
	quit := false
	for {
		if _, err := io.ReadFull(br, raw); err != nil {
			rerr = err
			break
		}
		got++
		f := make([]int16, FrameSize)
		for i := range f {
			f[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
		}
		if !fn(f) {
			quit = true
			break
		}
	}
	if quit {
		c.stop()
	}
	werr := c.cmd.Wait()
	c.stopMu.Lock()
	killed := c.killed
	c.stopMu.Unlock()
	if killed {
		return nil
	}
	if got == 0 {
		return c.openErr(werr)
	}
	if werr != nil || (rerr != nil && !errors.Is(rerr, io.EOF) && !errors.Is(rerr, io.ErrUnexpectedEOF)) {
		return newErr(KindNoMic, werr, "Мікрофон перестав передавати звук (його відключили?) — перевір підключення й спробуй ще раз")
	}
	return io.EOF // the input ended by itself (a test file)
}

// stop ends the capture (kills FFmpeg; the PCM need not be finished).
func (c *capture) stop() {
	c.stopMu.Lock()
	defer c.stopMu.Unlock()
	if c.killed || c.cmd.Process == nil || c.cmd.ProcessState != nil {
		return
	}
	c.killed = true
	_ = c.cmd.Process.Kill()
}

// openErr explains why FFmpeg delivered no audio at all.
func (c *capture) openErr(werr error) error {
	log := c.log.text()
	low := strings.ToLower(log)
	name := c.dev.Name
	if name == "" {
		name = "типовий"
	}
	switch {
	case strings.Contains(low, "not authorized") || strings.Contains(low, "not permitted") ||
		strings.Contains(low, "access denied") || strings.Contains(low, "permission"):
		return permissionErr(werr)
	case strings.Contains(low, "could not find audio") || strings.Contains(low, "no such") ||
		strings.Contains(low, "i/o error") || strings.Contains(low, "invalid audio device") ||
		strings.Contains(low, "does not exist"):
		return newErr(KindNoMic, werr, "Мікрофон «%s» не знайдено — підключи його або вибери інший у Налаштуваннях → Диктування", name)
	case strings.Contains(low, "could not run graph") || strings.Contains(low, "in use") || strings.Contains(low, "busy"):
		return newErr(KindNoMic, werr, "Мікрофон «%s» зайнятий іншою програмою або вимкнений — закрий її або вибери інший мікрофон", name)
	}
	detail := lastLine(log)
	if detail == "" && werr != nil {
		detail = werr.Error()
	}
	return newErr(KindNoMic, werr, "Не вдалося увімкнути мікрофон «%s»: %s", name, clip(detail, 160))
}

// permissionErr is the OS refusing the microphone.
func permissionErr(cause error) *Error {
	if goos == "darwin" {
		return newErr(KindPermission, cause, "macOS не дає студії мікрофон — дозволь його в Системних параметрах → Конфіденційність і безпека → Мікрофон")
	}
	return newErr(KindPermission, cause, "Windows не дає програмам мікрофон — увімкни «Доступ до мікрофона» для класичних програм у Параметрах → Конфіденційність → Мікрофон")
}

func errNoFFmpeg() *Error {
	return newErr(KindNoFFmpeg, nil, "Для диктування потрібен FFmpeg (він слухає мікрофон) — встанови його або вкажи шлях у Налаштуваннях → FFmpeg")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(logPrefix.ReplaceAllString(strings.TrimSpace(lines[i]), "")); l != "" {
			return l
		}
	}
	return ""
}

// tailBuf keeps the last max bytes written to it (FFmpeg's log).
type tailBuf struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append([]byte(nil), t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuf) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// describe names a device for messages.
func (d Device) String() string {
	if d.Name != "" {
		return d.Name
	}
	return fmt.Sprintf("#%s", d.ID)
}
