//go:build screenshots

package screenshots

import (
	"encoding/json"
	"flag"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
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
func walkWorld(t *testing.T, binary, out, theme, world string, w walk) {
	in := newInstall(t, binary, *keepFlag)
	var vars map[string]string
	switch world {
	case worldHarbor:
		h := newHarbor(in)
		vars = h.vars()
		maps.Copy(vars, h.sessions())
	case worldSample:
		w := newWorld(in)
		w.guided = true
		w.populate()
		w.severalProjects()
	}
	plan := map[string]any{
		"base":       in.web,
		"passphrase": passphrase(),
		"out":        out,
		"theme":      theme,
		"vars":       vars,
		"private":    private(in),
		"capture":    w.capture,
		"guides":     w.run,
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
