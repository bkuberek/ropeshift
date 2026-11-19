//go:build screenshots

package screenshots

import (
	"encoding/json"
	"flag"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

var guideFlag = flag.String("guide", "", "the prefix of the one guide to capture, after walking the guides before it; empty captures every guide")

// TestGuides walks the guides through the web UI and captures their images, in each theme on an installation
// of its own, so the acts of a walk happen once per theme. A guide's world is the installation it starts from:
// harbor is fresh, and sample is the sample project of TestCapture. The guides of a world run in order, each
// from the state the ones before it left, and with -guide only the named guide takes images, after the guides
// before it have run without them. A step that fails stops the run with the guide and the step named.
func TestGuides(t *testing.T) {
	if *binaryFlag == "" || *outFlag == "" {
		t.Fatal("set -binary and -out; scripts/screenshots.sh does")
	}
	binary, err := filepath.Abs(*binaryFlag)
	if err != nil {
		t.Fatal(err)
	}
	out, err := filepath.Abs(*outFlag)
	if err != nil {
		t.Fatal(err)
	}
	guides, err := loadGuides(guidesDir)
	if err != nil {
		t.Fatal(err)
	}
	walks, err := selectGuides(guides, *guideFlag)
	if err != nil {
		t.Fatal(err)
	}
	for _, world := range []string{worldHarbor, worldSample} {
		walk := walks[world]
		if len(walk.run) == 0 {
			continue
		}
		for _, theme := range []string{"light", "dark"} {
			t.Run(world+"/"+theme, func(t *testing.T) { walkWorld(t, binary, out, theme, world, walk) })
		}
	}
}

// walkWorld makes the world's installation and runs the browser over its guides in one theme.
func walkWorld(t *testing.T, binary, out, theme, name string, w walk) {
	in := newInstall(t, binary, *keepFlag)
	var vars map[string]string
	var sample *world
	switch name {
	case worldHarbor:
		h := newHarbor(in)
		vars = h.vars()
		maps.Copy(vars, h.sessions())
		maps.Copy(vars, h.practices())
	case worldSample:
		sample = newWorld(in)
		sample.guided = true
		sample.populate()
		sample.severalProjects()
	}
	// The troubleshooting guide reads the sample after its troubles are laid out, which the guides before it must
	// not see, so the walk runs them first and lays the troubles out between.
	run, troubled := w.run, []guide(nil)
	if sample != nil {
		if at := slices.IndexFunc(run, func(g guide) bool { return g.Guide == troubleGuide }); at >= 0 {
			run, troubled = run[:at], run[at:]
		}
	}
	if len(run) > 0 {
		play(t, in, theme, out, vars, w.capture, run)
	}
	if len(troubled) > 0 {
		sample.troubled()
		play(t, in, theme, out, vars, w.capture, troubled)
	}
}

// play runs the browser over some guides, against the web address the installation has now.
func play(t *testing.T, in *install, theme, out string, vars map[string]string, capture []string, guides []guide) {
	t.Helper()
	plan := map[string]any{
		"base":       in.web,
		"passphrase": passphrase(),
		"out":        out,
		"theme":      theme,
		"vars":       vars,
		"private":    private(in),
		"capture":    capture,
		"guides":     guides,
	}
	raw, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	listing := filepath.Join(in.root, "guides.json")
	if err := os.WriteFile(listing, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(moduleRoot(), "web")
	cmd := exec.Command("node", filepath.Join(web, "scripts", "guides.mjs"), listing)
	cmd.Dir = web
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the guide walk failed in the %s theme: %v", theme, err)
	}
}
