package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"ropeshift.com/ropeshift/internal/lanes"
)

// evidenceLimit bounds each file of a probe's evidence, in bytes, and evidenceCut ends a file that was cut at it.
var evidenceLimit = 256 << 10

const evidenceCut = "\n[truncated]\n"

// evidence is what one session of a probe showed, and the checks that session ran. The probe keeps it only when
// one of those checks did not pass.
type evidence struct {
	file     string
	surfaces []lanes.Surface
	body     []byte
	// cut says the session showed more than the bound let the probe hold.
	cut bool
}

// keep notes a session's evidence for the end of the probe.
func (p *prober) keep(e evidence) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evidence = append(p.evidence, e)
}

// keepStream notes what a headless session wrote, for the checks it ran. It runs after the session ended, so
// the stream is whole.
func (p *prober) keepStream(name string, s *probeSession, surfaces ...lanes.Surface) {
	body, cut := s.rawStream()
	p.keep(evidence{file: name + ".stream", surfaces: surfaces, body: body, cut: cut})
}

// keepScreen notes the pane's screen as it stands, for the start check. It runs before the pane is closed, and
// after a stop of the probe too, so it carries a bound of its own.
func (p *prober) keepScreen(ctx context.Context, pane string) {
	calls, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeCloseWait)
	defer cancel()
	screen, err := startCheckScreen(calls, p.d.terminal, pane)
	if err != nil {
		return
	}
	body := []byte(screen)
	p.keep(evidence{file: "screens.screen", surfaces: []lanes.Surface{SurfaceStartScreens}, body: body[:min(len(body), evidenceLimit)], cut: len(body) > evidenceLimit})
}

// writeEvidence replaces the driver's evidence with this probe's: the sessions that ran a check, of the checks the
// probe runs, that failed or that no session reached, and nothing else of the probe. A probe that passed every check leaves none. Files are
// private to the person the worker runs as, and none is longer than evidenceLimit. A write that fails leaves what it
// could not write unkept, and never changes the probe's result.
func (p *prober) writeEvidence(q lanes.Qualification, checks []lanes.Surface) {
	root := p.spec.EvidenceDir
	if root == "" {
		return
	}
	// A root that is a link leads out of the directory the worker owns, and nothing here follows it.
	if info, err := os.Lstat(root); err == nil && !info.IsDir() || err != nil && !os.IsNotExist(err) {
		return
	}
	dir := filepath.Join(root, DriverName)
	_ = os.RemoveAll(dir)
	p.mu.Lock()
	kept := slices.DeleteFunc(slices.Clone(p.evidence), func(e evidence) bool {
		return !slices.ContainsFunc(e.surfaces, func(s lanes.Surface) bool {
			return slices.Contains(checks, s) && !q.Passed(s)
		})
	})
	p.mu.Unlock()
	if len(kept) == 0 {
		return
	}
	for _, path := range []string{root, dir} {
		if os.MkdirAll(path, 0o700) != nil {
			return
		}
		// What was made, or is there, is a directory and no link, which Chmod would follow.
		if info, err := os.Lstat(path); err != nil || !info.IsDir() || os.Chmod(path, 0o700) != nil {
			return
		}
	}
	for _, e := range kept {
		_ = writePrivate(filepath.Join(dir, e.file), bounded(e.body, e.cut))
	}
}

// writePrivate writes a file only its owner can read.
func writePrivate(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(body)
	if err == nil {
		err = f.Chmod(0o600)
	}
	if closed := f.Close(); err == nil {
		err = closed
	}
	return err
}

// bounded cuts a body that did not fit to the bound, and ends it with the line that says so.
func bounded(body []byte, cut bool) []byte {
	if !cut && len(body) <= evidenceLimit {
		return body
	}
	keep := min(max(evidenceLimit-len(evidenceCut), 0), len(body))
	return append(slices.Clone(body[:keep]), evidenceCut...)
}

// rawStream is what the session wrote to its stream, up to evidenceLimit bytes, and whether it wrote more.
func (s *probeSession) rawStream() (body []byte, cut bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.raw), s.rawCut
}

// keepRaw holds a line of the stream for the evidence, until the bound is reached.
func (s *probeSession) keepRaw(line []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rawCut {
		return
	}
	room := evidenceLimit - len(s.raw)
	chunk := append(slices.Clone(line), '\n')
	if len(chunk) > room {
		s.raw = append(s.raw, chunk[:max(room, 0)]...)
		s.rawCut = true
		return
	}
	s.raw = append(s.raw, chunk...)
}
