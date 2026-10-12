package studio

// Duplicate, and the project-copy machinery shared with Translate: a new
// project folder whose files keep their relative paths. Generated media are
// hard-linked (no extra disk space; the studio never rewrites them in place),
// everything else — and every file when linking is impossible (another
// drive, FAT…) — is copied.

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Duplicate makes a full copy of a project under a new id — every file, the
// voices and the final render included — titled "<title> (копія)". The copy
// is not linked to its source (no SourceID) and has no job history.
func (e *Engine) Duplicate(id string) (Project, error) {
	src := e.state(id)
	if src == nil {
		return Project{}, ErrNotFound
	}
	snap, ok := e.Get(id)
	if !ok {
		return Project{}, ErrNotFound
	}
	p := cloneProject(&snap)
	p.Title = clipRunes(strings.TrimSpace(snap.Title)+" (копія)", 120)
	p.SourceID = ""
	st, err := e.createCopy(src, &p, projectFiles(src.dir, &snap))
	if err != nil {
		return Project{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return cloneProject(st.p), nil
}

// projectFiles lists every file a project references (relative paths).
func projectFiles(dir string, p *Project) []string {
	set := assetPaths(p)
	add := func(rel string) {
		if rel != "" {
			set[rel] = true
		}
	}
	add(p.Montage.CoverFinal.Path)
	add(p.Input.VoiceFile)
	add(p.Input.TranscriptFile)
	for _, m := range p.Input.Materials {
		add(m.Path)
	}
	if p.Input.VoiceFile != "" && isFile(filepath.Join(dir, filepath.FromSlash(transcriptRel))) {
		add(transcriptRel)
	}
	for _, s := range p.Scenes {
		if rel := LastFrameRel(s.ID); isFile(filepath.Join(dir, filepath.FromSlash(rel))) {
			add(rel)
		}
	}
	out := make([]string, 0, len(set))
	for rel := range set {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// createCopy registers p as a new project: a fresh id and folder, the files
// rels linked or copied over from src (an asset whose file is missing starts
// empty), no job, running assets settled. p is modified.
func (e *Engine) createCopy(src *proj, p *Project, rels []string) (*proj, error) {
	if e.closing.Load() {
		return nil, ErrClosing
	}
	id, dir, err := e.newProjectDir()
	if err != nil {
		return nil, err
	}
	missing := func() map[string]bool {
		src.fmu.RLock() // the source folder must not vanish mid-copy
		defer src.fmu.RUnlock()
		miss := map[string]bool{}
		for _, rel := range rels {
			if src.deleted.Load() || cloneFile(src.dir, dir, rel) != nil {
				miss[rel] = true
			}
		}
		return miss
	}()
	dropMissing(p, missing)

	now := nowMs()
	p.ID, p.CreatedAt, p.UpdatedAt, p.Job = id, now, now, nil
	forEachAsset(p, func(a *Asset) {
		if a.Status == StatusRunning || a.Status == StatusQueued {
			a.Status, a.Progress = StatusEmpty, 0
			if a.Path != "" {
				a.Status = StatusDone
			}
		}
	})
	normalize(p)

	st := newProj(id, dir, p)
	st.dirty = true
	if err := e.writeNow(st); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	e.mu.Lock()
	e.projects[id] = st
	e.mu.Unlock()
	e.notify("")
	e.notify(id)
	return st, nil
}

// newProjectDir picks an unused project id and creates its folder.
func (e *Engine) newProjectDir() (id, dir string, err error) {
	e.mu.Lock()
	root := projectsRoot(e.dataDir)
	for {
		id = newID(10)
		if _, taken := e.projects[id]; taken {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, id)); err == nil {
			continue
		}
		break
	}
	e.mu.Unlock()
	dir = filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("не вдалося створити папку проєкту: %w", err)
	}
	return id, dir, nil
}

// cloneFile puts srcDir/rel at dstDir/rel: a hard link for media the studio
// writes once, else (or when linking fails) a copy with the same mtime (the
// last-frame cache compares mtimes). Absolute references are shared as is.
func cloneFile(srcDir, dstDir, rel string) error {
	if rel == "" || filepath.IsAbs(rel) {
		return nil
	}
	from := filepath.Join(srcDir, filepath.FromSlash(rel))
	to := filepath.Join(dstDir, filepath.FromSlash(rel))
	if !within(srcDir, from) || !within(dstDir, to) {
		return errors.New("шлях поза папкою проєкту")
	}
	fi, err := os.Stat(from)
	if err != nil || fi.IsDir() {
		return fmt.Errorf("файл %s не знайдено", rel)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if linkable(rel) && os.Link(from, to) == nil {
		return nil
	}
	if err := copyFile(from, to); err != nil {
		return err
	}
	_ = os.Chtimes(to, fi.ModTime(), fi.ModTime())
	return nil
}

// linkable: generated or imported media under images/, videos/, audio/ and
// user/, and the montage outputs (final-<ms>.mp4, subtitles-<ms>.srt: the
// montage unlinks the old ones before writing new names) are never rewritten
// in place — unlike the last-frame cache (ffmpeg overwrites it) and the
// transcript sidecar, which are copied.
func linkable(rel string) bool {
	top, _, _ := strings.Cut(rel, "/")
	switch top {
	case "images", "videos", "audio", "user", "montage":
	default:
		return false
	}
	return !strings.HasPrefix(path.Base(rel), "last-") && rel != transcriptRel
}

// dropMissing forgets references to files that could not be copied: such
// assets start empty (to be generated again), music/SRT/recording are unset.
func dropMissing(p *Project, missing map[string]bool) {
	if len(missing) == 0 {
		return
	}
	forEachAsset(p, func(a *Asset) {
		if a.Path != "" && missing[a.Path] {
			*a = EmptyAsset()
		}
	})
	m := &p.Montage
	if missing[m.MusicPath] {
		m.MusicPath, m.MusicName = "", ""
	}
	if missing[m.SRTPath] {
		m.SRTPath = ""
	}
	if missing[p.Input.VoiceFile] {
		p.Input.VoiceFile, p.Input.VoiceFileName = "", ""
	}
	if missing[p.Input.TranscriptFile] {
		p.Input.TranscriptFile = ""
	}
	var kept []Material // a fresh array: the source's snapshot shares the old one
	for _, m := range p.Input.Materials {
		if !missing[m.Path] {
			kept = append(kept, m)
		}
	}
	p.Input.Materials = kept
}

// forEachAsset visits every asset of a project.
func forEachAsset(p *Project, fn func(a *Asset)) {
	for i := range p.Characters {
		fn(&p.Characters[i].Image)
	}
	for i := range p.Locations {
		fn(&p.Locations[i].Image)
	}
	for i := range p.Scenes {
		s := &p.Scenes[i]
		fn(&s.Frame)
		fn(&s.Board)
		fn(&s.Video)
		fn(&s.Audio)
	}
	fn(&p.Montage.Output)
	fn(&p.Montage.Cover)
	fn(&p.Montage.CoverFinal)
}
