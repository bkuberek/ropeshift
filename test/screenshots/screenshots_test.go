//go:build screenshots

// Package screenshots captures the web UI and `ropeshift top` on a sample project. It builds nothing:
// scripts/screenshots.sh builds the binary and runs this with the screenshots tag. The run makes an
// isolated installation, starts the daemon from it on a loopback port the kernel chooses, lays out the
// sample through the binary's own command line, with the fake agent CLI for every session, and drives
// web/scripts/screenshots.mjs to capture each view. It stops what it started and removes the installation.
package screenshots

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/doubles"
)

var (
	binaryFlag = flag.String("binary", "", "the ropeshift binary to capture")
	outFlag    = flag.String("out", "", "the directory the images go to")
	keepFlag   = flag.Bool("keep", false, "keep the installation, for debugging")
)

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(doubles.Main(m))
}

// moduleRoot is the checkout this package lives in, whose web directory holds Playwright. go test runs a
// package in its own directory.
func moduleRoot() string {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic(err)
	}
	return root
}

func TestCapture(t *testing.T) {
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
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	in := newInstall(t, binary, *keepFlag)
	w := newWorld(in)
	w.populate()

	top := in.must("top", "--once", "--width", "200")
	for _, word := range private(in) {
		if strings.Contains(top, word) {
			t.Errorf("top shows %q", word)
		}
	}
	if err := os.WriteFile(filepath.Join(out, "top.txt"), []byte(top), 0o644); err != nil {
		t.Fatal(err)
	}
	w.capture(out, filepath.Join(out, "top.txt"))
}

// private is what no image may show: the installation's own path, the fake CLI's, and this machine's home
// directory, user, and host. The capture fails a view whose text holds any of them.
func private(in *install) []string {
	words := []string{in.root, doubles.BinDir(in.path)}
	if home, err := os.UserHomeDir(); err == nil {
		words = append(words, home)
	}
	if name, err := os.Hostname(); err == nil {
		words = append(words, name)
	}
	if current, err := user.Current(); err == nil {
		words = append(words, current.Username)
	}
	// A word this short would match ordinary text.
	return slices.DeleteFunc(words, func(word string) bool { return len(word) < 4 })
}

// capture writes the list of views and runs the browser over them.
func (w *world) capture(out, top string) {
	in := w.in
	in.t.Helper()
	plan := map[string]any{
		"base":       in.web,
		"passphrase": passphrase(),
		"out":        out,
		"top":        top,
		"views":      w.views(),
		"private":    private(in),
	}
	raw, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		in.t.Fatal(err)
	}
	listing := filepath.Join(in.root, "views.json")
	if err := os.WriteFile(listing, raw, 0o600); err != nil {
		in.t.Fatal(err)
	}
	web := filepath.Join(moduleRoot(), "web")
	cmd := exec.Command("node", filepath.Join(web, "scripts", "screenshots.mjs"), listing)
	cmd.Dir = web
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		in.t.Fatalf("the capture failed: %v", err)
	}
}

// view is one page the capture opens, under the name its images take.
type view struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Dock opens the conversation panel on the page and switches it to the target whose name matches this
	// pattern, and DockReady names the pattern of the log that shows once the target is open. An empty
	// DockReady keeps the assistant.
	Dock      string `json:"dock,omitempty"`
	DockReady string `json:"dock_ready,omitempty"`
}

// projectPath is a view of the sample's project.
func (w *world) projectPath(view string, query ...string) string {
	path := "/projects/" + w.project + "/" + view
	if len(query) > 0 {
		path += "?" + strings.Join(query, "&")
	}
	return path
}

func q(name, value string) string { return name + "=" + urlEscape(value) }

func urlEscape(value string) string {
	return strings.NewReplacer("%", "%25", ":", "%3A", "/", "%2F", " ", "%20", "&", "%26", "?", "%3F", "=", "%3D").Replace(value)
}

// views is every view the capture takes, each with the records a person would open.
func (w *world) views() []view {
	p := w.projectPath
	views := []view{
		{Name: "needs-you", Path: "/needs-you"},
		{Name: "needs-you-question", Path: "/needs-you?" + q("item", w.questionItem)},
		{Name: "needs-you-card", Path: "/needs-you?kind=cards&" + q("item", w.cardItem)},
		{Name: "needs-you-plan-approval", Path: "/needs-you?" + q("item", w.planItem)},
		{Name: "roadmap", Path: p("roadmap")},
		{Name: "plans", Path: p("plans")},
		{Name: "plan-approval", Path: p("plan", q("id", w.plan), "version=2")},
		{Name: "plan-approved", Path: p("plan", q("id", w.plan), "version=1")},
		{Name: "planning", Path: p("planning")},
		{Name: "documents", Path: p("documents", "kind=requirements")},
		{Name: "documents-requirement", Path: p("documents", "kind=requirements", "key=ORD-3")},
		{Name: "documents-spec", Path: p("documents", q("id", "spec:orders-errors"))},
		{Name: "documents-spec-changes", Path: p("documents", q("id", "spec:orders-errors"), "version=2", "against=1")},
		{Name: "traceability", Path: p("traceability")},
		{Name: "traceability-requirement", Path: p("traceability", "key=ORD-3")},
		{Name: "decisions", Path: p("decisions")},
		{Name: "questions", Path: p("questions")},
		{Name: "work", Path: p("work", "group=all")},
		{Name: "work-order", Path: p("work-order", q("id", w.orders[orderRetries]))},
		{Name: "floor", Path: p("floor")},
		{Name: "lane", Path: p("lane", q("id", laneRateLimit))},
		{Name: "lane-parked", Path: p("lane", q("id", laneSigningKeys))},
		{Name: "measures", Path: p("measures")},
		{Name: "conversations", Path: p("conversations", q("conversation", w.conversation))},
		{Name: "assistant-dock", Path: p("roadmap"), Dock: "^Assistant"},
		{Name: "orchestrator-dock", Path: p("floor"), Dock: "^Orchestrator", DockReady: "^Event stream of the orchestrator"},
		{Name: "lane-dock", Path: p("floor"), Dock: "^Lane.*" + laneRateLimit, DockReady: "^Event stream of lane " + laneRateLimit},
		{Name: "projects", Path: "/projects"},
		{Name: "repositories", Path: p("repositories")},
		{Name: "repository", Path: p("repositories", q("repo", w.repository))},
		{Name: "agents", Path: "/agents"},
		{Name: "agents-project", Path: p("agents", q("agent", w.agent))},
		{Name: "agents-history", Path: p("agents", "tab=history")},
		{Name: "settings", Path: "/settings?section=admission"},
		{Name: "settings-assistant", Path: "/settings?section=assistant"},
		{Name: "settings-project", Path: p("settings", "section=fleet")},
		{Name: "settings-delegates", Path: p("settings", "section=delegate-policy")},
		{Name: "members", Path: "/members"},
		{Name: "workers", Path: "/workers"},
		{Name: "usage", Path: "/usage"},
		{Name: "import", Path: "/import"},
		{Name: "audit", Path: p("audit")},
		{Name: "audit-installation", Path: "/audit"},
	}
	return views
}

// passphrase is what the install's member signs in with, on the page as on the command line.
func passphrase() string { return apitest.Passphrase }

// requireJSON decodes one view the binary prints.
func (in *install) view(name string, out any) {
	in.t.Helper()
	raw := in.must("view", name)
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		in.t.Fatalf("view %s: %v", name, err)
	}
}
