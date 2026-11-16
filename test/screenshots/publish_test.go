package screenshots

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// publishScript is scripts/screenshots-publish.sh, which make screenshots-publish runs.
func publishScript(t *testing.T) string {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "screenshots-publish.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return script
}

func writeFile(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// publishRun is one run of the publish step on a small tree: a table of the published images, a directory
// of captures, and a stand-in for pngquant that records how it was called and writes the size it is told.
type publishRun struct {
	t                 *testing.T
	dir               string
	shots, out, table string
	calls             string
}

// newPublishRun writes the table, which names two views of both themes and the terminal image, and the
// captures a run of the sample writes for them.
func newPublishRun(t *testing.T, outputBytes int) *publishRun {
	t.Helper()
	dir := t.TempDir()
	r := &publishRun{t: t, dir: dir, shots: filepath.Join(dir, "shots"), out: filepath.Join(dir, "published"),
		table: filepath.Join(dir, "screenshots.md"), calls: filepath.Join(dir, "calls.txt")}
	writeFile(t, r.table, "Prose.\n\n| Capture | Published as | Shown in |\n| --- | --- | --- |\n"+
		"| `floor-<theme>-1440.png` | `floor-<theme>.png` | README |\n"+
		"| `get-started-01-<theme>.png` | `get-started-01-<theme>.png` | Guide 1 |\n"+
		"| `top.png` | `top.png` | README |\n\nMore prose with `code` | in a line.\n")
	for _, name := range []string{"floor-light-1440.png", "floor-dark-1440.png", "get-started-01-light.png", "get-started-01-dark.png", "top.png", "floor-light-390.png"} {
		writeFile(t, filepath.Join(r.shots, name), "capture "+name)
	}
	// The stand-in writes a file of the size the test sets to the path after --output, as pngquant does.
	writeFile(t, filepath.Join(dir, "bin", "pngquant"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+r.calls+"'\n"+
		"while [ $# -gt 0 ]; do [ \"$1\" = --output ] && out=$2; shift; done\n"+
		"head -c "+strconv.Itoa(outputBytes)+" /dev/zero > \"$out\"\n")
	return r
}

// publish runs the step, with the stand-in as the optimizer unless optimizer says another.
func (r *publishRun) publish(optimizer string) (string, error) {
	r.t.Helper()
	if optimizer == "" {
		optimizer = filepath.Join(r.dir, "bin", "pngquant")
	}
	cmd := exec.Command("sh", publishScript(r.t), "-s", r.shots, "-t", r.table, "-o", r.out)
	cmd.Env = append(os.Environ(), "PNGQUANT="+optimizer)
	raw, err := cmd.CombinedOutput()
	return string(raw), err
}

func (r *publishRun) published() []string {
	r.t.Helper()
	entries, err := os.ReadDir(r.out)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		r.t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestPublishCopiesTheTableThroughTheOptimizer(t *testing.T) {
	r := newPublishRun(t, 2000)
	output, err := r.publish("")
	if err != nil {
		t.Fatalf("the step failed: %v\n%s", err, output)
	}
	want := "floor-dark.png floor-light.png get-started-01-dark.png get-started-01-light.png top.png"
	if got := strings.Join(r.published(), " "); got != want {
		t.Errorf("published %q, want %q", got, want)
	}
	calls, err := os.ReadFile(r.calls)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(calls), "\n"); lines != 5 {
		t.Errorf("the optimizer ran %d times, want 5:\n%s", lines, calls)
	}
	if !strings.Contains(string(calls), "--quality=70-90 --speed 1 --strip") {
		t.Errorf("the optimizer ran with other settings:\n%s", calls)
	}
	info, err := os.Stat(filepath.Join(r.out, "floor-light.png"))
	if err != nil || info.Size() != 2000 {
		t.Errorf("floor-light.png is not the optimizer's output: %v %v", info, err)
	}
	if !strings.Contains(output, "published 5 images") {
		t.Errorf("the step does not say what it published:\n%s", output)
	}
}

func TestPublishStopsOnACaptureThatIsMissing(t *testing.T) {
	r := newPublishRun(t, 2000)
	if err := os.Remove(filepath.Join(r.shots, "get-started-01-dark.png")); err != nil {
		t.Fatal(err)
	}
	output, err := r.publish("")
	if err == nil || !strings.Contains(output, "get-started-01-dark.png") {
		t.Errorf("a missing capture did not stop the step: %v\n%s", err, output)
	}
	if got := r.published(); len(got) > 0 {
		t.Errorf("the step published %v beside a missing capture", got)
	}
}

func TestPublishStopsOnAnImageOverTheLimit(t *testing.T) {
	r := newPublishRun(t, 250001)
	output, err := r.publish("")
	if err == nil || !strings.Contains(output, "over 250000 bytes") {
		t.Errorf("an image over the limit did not stop the step: %v\n%s", err, output)
	}
	if got := r.published(); len(got) > 0 {
		t.Errorf("the step published %v beside an oversized image", got)
	}
}

func TestPublishStopsWithTheInstallCommandWhenPngquantIsMissing(t *testing.T) {
	r := newPublishRun(t, 2000)
	output, err := r.publish(filepath.Join(r.dir, "none", "pngquant"))
	if err == nil || !strings.Contains(output, "brew install pngquant") {
		t.Errorf("a missing pngquant did not stop the step with the install command: %v\n%s", err, output)
	}
}

func TestPublishNeedsTheDirectoryOfACapture(t *testing.T) {
	r := newPublishRun(t, 2000)
	cmd := exec.Command("sh", publishScript(t), "-t", r.table, "-o", r.out)
	cmd.Env = append(os.Environ(), "PNGQUANT="+filepath.Join(r.dir, "bin", "pngquant"))
	if raw, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(raw), "SHOTS") {
		t.Errorf("a run with no capture directory did not stop and name SHOTS: %v\n%s", err, raw)
	}
}
