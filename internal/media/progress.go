package media

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"sync"
	"time"

	"ytcartell/internal/sysx"
)

// runTimed is Run for a long encode: ffmpeg reports its position through
// -progress on stdout and report receives the done share (0…1) of total
// seconds, at most a few times per second.
func (f FF) runTimed(ctx context.Context, dir string, total float64, report func(float64), args ...string) error {
	if report == nil || total <= 0 {
		return f.run(ctx, dir, nil, args...)
	}
	pw := &progressWriter{total: total, report: report}
	return f.run(ctx, dir, pw, append([]string{"-progress", "pipe:1", "-nostats"}, args...)...)
}

// progressWriter parses ffmpeg's key=value progress lines.
type progressWriter struct {
	total  float64
	report func(float64)
	buf    []byte
	last   time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(p.buf[:i])
		p.buf = p.buf[i+1:]
		// out_time_us (and, despite the name, out_time_ms) are microseconds.
		v, ok := bytes.CutPrefix(line, []byte("out_time_us="))
		if !ok {
			continue
		}
		us, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil || us < 0 {
			continue
		}
		if now := time.Now(); now.Sub(p.last) >= 250*time.Millisecond {
			p.last = now
			p.report(min(1, float64(us)/1e6/p.total))
		}
	}
	if len(p.buf) > 4096 { // no newline in sight: not progress output
		p.buf = p.buf[:0]
	}
	return len(b), nil
}

// ---- available filters ---------------------------------------------------------------------

var (
	filtersMu sync.Mutex
	filtersOf = map[string]map[string]bool{}
	reFilter  = regexp.MustCompile(`(?m)^\s*[TSC.|]{2,3}\s+(\w+)\s`)
)

// hasFilter reports whether this ffmpeg build has the named filter (cached
// per executable). When the list cannot be read every filter is assumed.
func (f FF) hasFilter(ctx context.Context, name string) bool {
	filtersMu.Lock()
	set, ok := filtersOf[f.FFmpeg]
	filtersMu.Unlock()
	if !ok {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		out, err := sysx.Output(command(c, f.FFmpeg, "-hide_banner", "-filters"))
		cancel()
		if err != nil || len(out) == 0 {
			return true
		}
		set = map[string]bool{}
		for _, m := range reFilter.FindAllSubmatch(out, -1) {
			set[string(m[1])] = true
		}
		filtersMu.Lock()
		filtersOf[f.FFmpeg] = set
		filtersMu.Unlock()
	}
	return len(set) == 0 || set[name]
}
