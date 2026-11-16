package screenshots

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The guide walk's step format. A guide is one file, guides/<guide>.json, whose steps web/scripts/guides.mjs
// walks through the web UI. Go reads the files, checks their shape here, and hands them to the script as they
// are, so a step that cannot run fails `make check` and not a capture run.

// The worlds a guide walks. A world is the installation a walk starts from.
const (
	worldHarbor = "harbor"
	worldSample = "sample"
)

// guide is one guide's walk.
type guide struct {
	// Guide is the guide's prefix, which names the file and begins each image's name.
	Guide string `json:"guide"`
	Title string `json:"title"`
	World string `json:"world"`
	// Order puts the guides of a world in the order a person reads them, because each starts from the state
	// the ones before it left.
	Order int `json:"order"`
	// SignedOut starts the guide with no session, so its first steps sign in through the page.
	SignedOut bool       `json:"signed_out,omitempty"`
	Steps     []walkStep `json:"steps"`
}

// walkStep is one step of a guide.
type walkStep struct {
	Step    string    `json:"step"`
	Route   string    `json:"route,omitempty"`
	Actions []action  `json:"actions,omitempty"`
	Capture *snapshot `json:"capture,omitempty"`
}

// action has exactly one verb.
type action struct {
	Click   *control `json:"click,omitempty"`
	Fill    *field   `json:"fill,omitempty"`
	Press   string   `json:"press,omitempty"`
	WaitFor *text    `json:"waitFor,omitempty"`
}

// element is a control or region by its role and accessible name.
type element struct {
	Role  string `json:"role"`
	Name  string `json:"name"`
	Exact *bool  `json:"exact,omitempty"`
}

type control struct {
	element
	In *element `json:"in,omitempty"`
}

type field struct {
	Label string   `json:"label"`
	Value string   `json:"value"`
	Exact *bool    `json:"exact,omitempty"`
	In    *element `json:"in,omitempty"`
}

type text struct {
	Text  string   `json:"text"`
	Exact *bool    `json:"exact,omitempty"`
	In    *element `json:"in,omitempty"`
}

// snapshot is an image a step takes: its name, and what it clips to.
type snapshot struct {
	Name string          `json:"name"`
	Clip json.RawMessage `json:"clip"`
}

// clipBox is a clip to one element: a selector, or a role and name, with pad pixels around it.
type clipBox struct {
	Selector string `json:"selector"`
	Role     string `json:"role"`
	Name     string `json:"name"`
	Exact    *bool  `json:"exact,omitempty"`
	Pad      int    `json:"pad,omitempty"`
}

// guidesDir holds the guides' files.
const guidesDir = "guides"

var (
	guidePrefix = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	imageNumber = regexp.MustCompile(`^[0-9]{2}$`)
)

// readGuide decodes one guide file, and refuses a field the format does not have.
func readGuide(file string) (guide, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return guide{}, err
	}
	var g guide
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&g); err != nil {
		return guide{}, fmt.Errorf("%s: %w", file, err)
	}
	return g, nil
}

// loadGuides reads every guide of the directory, in the order a person reads them: by world, then order.
func loadGuides(dir string) ([]guide, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var guides []guide
	for _, file := range files {
		g, err := readGuide(file)
		if err != nil {
			return nil, err
		}
		if want := strings.TrimSuffix(filepath.Base(file), ".json"); g.Guide != want {
			return nil, fmt.Errorf("%s: the guide is named %q, and a guide's file is named for it", file, g.Guide)
		}
		guides = append(guides, g)
	}
	slices.SortFunc(guides, func(a, b guide) int {
		if a.World != b.World {
			return strings.Compare(a.World, b.World)
		}
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.Guide, b.Guide)
	})
	if err := checkGuideSet(guides); err != nil {
		return nil, err
	}
	return guides, nil
}

// checkGuideSet holds the guides together: each is sound, no two share an order in a world, and no two
// images share a name.
func checkGuideSet(guides []guide) error {
	var problems []error
	names := map[string]string{}
	orders := map[string]string{}
	for _, g := range guides {
		if err := checkGuide(g); err != nil {
			problems = append(problems, err)
		}
		place := fmt.Sprintf("%s/%d", g.World, g.Order)
		if other, ok := orders[place]; ok {
			problems = append(problems, fmt.Errorf("%s: guides %s and %s share order %d in world %s", g.Guide, other, g.Guide, g.Order, g.World))
		}
		orders[place] = g.Guide
		for _, s := range g.Steps {
			if s.Capture == nil {
				continue
			}
			if other, ok := names[s.Capture.Name]; ok && other != g.Guide {
				problems = append(problems, fmt.Errorf("%s: image %s is also guide %s's", g.Guide, s.Capture.Name, other))
			}
			names[s.Capture.Name] = g.Guide
		}
	}
	return errors.Join(problems...)
}

// checkGuide answers what is wrong with one guide: its prefix, its world, and each step.
func checkGuide(g guide) error {
	var problems []string
	bad := func(where, format string, args ...any) {
		problems = append(problems, fmt.Sprintf("guide %s%s: %s", g.Guide, where, fmt.Sprintf(format, args...)))
	}
	if !guidePrefix.MatchString(g.Guide) {
		bad("", "the prefix %q is not lowercase words joined by hyphens", g.Guide)
	}
	if g.World != worldHarbor && g.World != worldSample {
		bad("", "the world %q is neither %s nor %s", g.World, worldHarbor, worldSample)
	}
	if g.Order < 1 {
		bad("", "the order is %d, and the first guide of a world is 1", g.Order)
	}
	if len(g.Steps) == 0 {
		bad("", "has no steps")
	}
	seen := map[string]bool{}
	images := map[string]bool{}
	for i, s := range g.Steps {
		where := fmt.Sprintf(" step %d (%s)", i+1, s.Step)
		if s.Step == "" {
			bad(where, "has no name, which a failed run says")
		}
		if seen[s.Step] {
			bad(where, "repeats the name of an earlier step")
		}
		seen[s.Step] = true
		if s.Route != "" && !strings.HasPrefix(s.Route, "/") {
			bad(where, "the route %q does not start with /", s.Route)
		}
		if s.Route == "" && len(s.Actions) == 0 && s.Capture == nil {
			bad(where, "does nothing: it has no route, action, or capture")
		}
		for j, a := range s.Actions {
			if message := checkAction(a); message != "" {
				bad(where, "action %d %s", j+1, message)
			}
		}
		if s.Capture != nil {
			if message := checkCapture(g.Guide, *s.Capture); message != "" {
				bad(where, "%s", message)
			}
			if images[s.Capture.Name] {
				bad(where, "takes image %s again", s.Capture.Name)
			}
			images[s.Capture.Name] = true
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "\n"))
}

func checkAction(a action) string {
	verbs := 0
	var problem string
	if a.Click != nil {
		verbs++
		if a.Click.Role == "" || a.Click.Name == "" {
			problem = "click names a role and a name"
		}
		problem = cmpProblem(problem, checkElement(a.Click.In))
	}
	if a.Fill != nil {
		verbs++
		if a.Fill.Label == "" {
			problem = "fill names the field's label"
		}
		problem = cmpProblem(problem, checkElement(a.Fill.In))
	}
	if a.Press != "" {
		verbs++
	}
	if a.WaitFor != nil {
		verbs++
		if a.WaitFor.Text == "" {
			problem = "waitFor names the text to wait for"
		}
		problem = cmpProblem(problem, checkElement(a.WaitFor.In))
	}
	switch {
	case verbs != 1:
		return fmt.Sprintf("has %d verbs, and an action is one of click, fill, press, and waitFor", verbs)
	default:
		return problem
	}
}

// cmpProblem keeps the first problem found.
func cmpProblem(first, next string) string {
	if first != "" {
		return first
	}
	return next
}

func checkElement(e *element) string {
	if e != nil && (e.Role == "" || e.Name == "") {
		return "scopes to an element with no role or name in `in`"
	}
	return ""
}

// checkCapture holds an image to its name and its clip.
func checkCapture(prefix string, c snapshot) string {
	number, found := strings.CutPrefix(c.Name, prefix+"-")
	if !found || !imageNumber.MatchString(number) {
		return fmt.Sprintf("the image %q is not named <guide>-NN, such as %s-01", c.Name, prefix)
	}
	var word string
	if err := json.Unmarshal(c.Clip, &word); err == nil {
		if word != "window" {
			return fmt.Sprintf("image %s clips to %q, and a clip is \"window\" or an element", c.Name, word)
		}
		return ""
	}
	var box clipBox
	decoder := json.NewDecoder(bytes.NewReader(c.Clip))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&box); err != nil {
		return fmt.Sprintf("image %s has a clip that is neither \"window\" nor an element: %v", c.Name, err)
	}
	switch {
	case box.Selector != "" && (box.Role != "" || box.Name != ""):
		return fmt.Sprintf("image %s clips to a selector and a role at once", c.Name)
	case box.Selector == "" && (box.Role == "" || box.Name == ""):
		return fmt.Sprintf("image %s clips to an element with no selector, and no role and name", c.Name)
	case box.Pad < 0:
		return fmt.Sprintf("image %s pads its clip by %d pixels", c.Name, box.Pad)
	}
	return ""
}

// walk is the guides of one world to run, and the names of those that take images.
type walk struct {
	run     []guide
	capture []string
}

// selectGuides picks the guides to run for each world. With no prefix, every guide runs and takes images. With
// one, the guides of its world up to it run, and it alone takes images.
func selectGuides(guides []guide, prefix string) (map[string]walk, error) {
	walks := map[string]walk{}
	if prefix == "" {
		for _, g := range guides {
			w := walks[g.World]
			w.run = append(w.run, g)
			w.capture = append(w.capture, g.Guide)
			walks[g.World] = w
		}
		return walks, nil
	}
	index := slices.IndexFunc(guides, func(g guide) bool { return g.Guide == prefix })
	if index < 0 {
		var names []string
		for _, g := range guides {
			names = append(names, g.Guide)
		}
		return nil, fmt.Errorf("no guide %q; the guides are %s", prefix, strings.Join(names, ", "))
	}
	chosen := guides[index]
	for _, g := range guides {
		if g.World == chosen.World && g.Order <= chosen.Order {
			w := walks[g.World]
			w.run = append(w.run, g)
			walks[g.World] = w
		}
	}
	w := walks[chosen.World]
	w.capture = []string{prefix}
	walks[chosen.World] = w
	return walks, nil
}

func TestSelectingGuides(t *testing.T) {
	window := json.RawMessage(`"window"`)
	one := func(world, prefix string, order int) guide {
		return guide{Guide: prefix, World: world, Order: order, Steps: []walkStep{{Step: "open", Route: "/", Capture: &snapshot{Name: prefix + "-01", Clip: window}}}}
	}
	guides := []guide{one(worldHarbor, "get-started", 1), one(worldHarbor, "projects", 2), one(worldHarbor, "plan", 3), one(worldSample, "measures", 1)}
	names := func(list []guide) string {
		var out []string
		for _, g := range list {
			out = append(out, g.Guide)
		}
		return strings.Join(out, " ")
	}

	all, err := selectGuides(guides, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(all[worldHarbor].run); got != "get-started projects plan" {
		t.Errorf("every guide of harbor runs in order, got %q", got)
	}
	if got := strings.Join(all[worldHarbor].capture, " "); got != "get-started projects plan" {
		t.Errorf("every guide takes images, got %q", got)
	}
	if got := names(all[worldSample].run); got != "measures" {
		t.Errorf("the sample's guide runs, got %q", got)
	}

	one2, err := selectGuides(guides, "projects")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(one2[worldHarbor].run); got != "get-started projects" {
		t.Errorf("a guide runs after the ones before it and none after, got %q", got)
	}
	if got := strings.Join(one2[worldHarbor].capture, " "); got != "projects" {
		t.Errorf("only the named guide takes images, got %q", got)
	}
	if len(one2[worldSample].run) != 0 {
		t.Errorf("a guide of another world ran: %v", names(one2[worldSample].run))
	}

	if _, err := selectGuides(guides, "nowhere"); err == nil || !strings.Contains(err.Error(), "get-started, projects, plan, measures") {
		t.Errorf("an unknown guide: %v", err)
	}
}

func TestGuideFilesAreWellFormed(t *testing.T) {
	guides, err := loadGuides(guidesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(guides) == 0 {
		t.Fatal("no guide in " + guidesDir)
	}
}

// TestGuideFormatCanFail proves each rule of the format can fail: every case breaks one.
func TestGuideFormatCanFail(t *testing.T) {
	click := func(role, name string) action {
		return action{Click: &control{element: element{Role: role, Name: name}}}
	}
	window := json.RawMessage(`"window"`)
	sound := func() guide {
		return guide{Guide: "projects", World: worldHarbor, Order: 2, Steps: []walkStep{
			{Step: "open", Route: "/projects", Actions: []action{click("button", "Create project")}, Capture: &snapshot{Name: "projects-01", Clip: window}},
		}}
	}
	tests := []struct {
		name   string
		change func(g *guide)
		want   string
	}{
		{"an unknown world", func(g *guide) { g.World = "cove" }, `the world "cove"`},
		{"a prefix with capitals", func(g *guide) { g.Guide = "Projects" }, "is not lowercase words"},
		{"no order", func(g *guide) { g.Order = 0 }, "the order is 0"},
		{"no steps", func(g *guide) { g.Steps = nil }, "has no steps"},
		{"a step with no name", func(g *guide) { g.Steps[0].Step = "" }, "has no name"},
		{"a step that repeats a name", func(g *guide) { g.Steps = append(g.Steps, g.Steps[0]) }, "repeats the name"},
		{"a route with no slash", func(g *guide) { g.Steps[0].Route = "projects" }, "does not start with /"},
		{"a step that does nothing", func(g *guide) { g.Steps = append(g.Steps, walkStep{Step: "idle"}) }, "does nothing"},
		{"an action with no verb", func(g *guide) { g.Steps[0].Actions = []action{{}} }, "has 0 verbs"},
		{"an action with two verbs", func(g *guide) {
			g.Steps[0].Actions = []action{{Click: &control{element: element{Role: "button", Name: "Go"}}, Press: "Enter"}}
		}, "has 2 verbs"},
		{"a click with no name", func(g *guide) { g.Steps[0].Actions = []action{click("button", "")} }, "click names a role and a name"},
		{"a fill with no label", func(g *guide) { g.Steps[0].Actions = []action{{Fill: &field{Value: "x"}}} }, "fill names the field's label"},
		{"a waitFor with no text", func(g *guide) { g.Steps[0].Actions = []action{{WaitFor: &text{}}} }, "waitFor names the text"},
		{"a scope with no name", func(g *guide) {
			g.Steps[0].Actions = []action{{Click: &control{element: element{Role: "button", Name: "Go"}, In: &element{Role: "dialog"}}}}
		}, "scopes to an element"},
		{"an image with another guide's prefix", func(g *guide) { g.Steps[0].Capture.Name = "plan-01" }, "is not named <guide>-NN"},
		{"an image with no number", func(g *guide) { g.Steps[0].Capture.Name = "projects-open" }, "is not named <guide>-NN"},
		{"a clip that is another word", func(g *guide) { g.Steps[0].Capture.Clip = json.RawMessage(`"page"`) }, `clips to "page"`},
		{"a clip with no element", func(g *guide) { g.Steps[0].Capture.Clip = json.RawMessage(`{"pad": 4}`) }, "no selector, and no role and name"},
		{"a clip with a selector and a role", func(g *guide) {
			g.Steps[0].Capture.Clip = json.RawMessage(`{"selector": "#a", "role": "dialog", "name": "A"}`)
		}, "a selector and a role at once"},
		{"a clip with a field the format lacks", func(g *guide) { g.Steps[0].Capture.Clip = json.RawMessage(`{"selector": "#a", "margin": 4}`) }, "neither"},
		{"a clip with a negative pad", func(g *guide) { g.Steps[0].Capture.Clip = json.RawMessage(`{"selector": "#a", "pad": -1}`) }, "pads its clip"},
		{"a step that takes an image twice", func(g *guide) {
			g.Steps = append(g.Steps, walkStep{Step: "again", Capture: &snapshot{Name: "projects-01", Clip: window}})
		}, "takes image projects-01 again"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := sound()
			if err := checkGuide(g); err != nil {
				t.Fatalf("the sound guide has a problem: %v", err)
			}
			tt.change(&g)
			err := checkGuide(g)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want a problem with %q", err, tt.want)
			}
		})
	}
}

func TestGuideSetCanFail(t *testing.T) {
	window := json.RawMessage(`"window"`)
	one := func(prefix string, order int) guide {
		return guide{Guide: prefix, World: worldHarbor, Order: order, Steps: []walkStep{{Step: "open", Route: "/", Capture: &snapshot{Name: prefix + "-01", Clip: window}}}}
	}
	if err := checkGuideSet([]guide{one("get-started", 1), one("projects", 2)}); err != nil {
		t.Fatalf("a sound set has a problem: %v", err)
	}
	if err := checkGuideSet([]guide{one("get-started", 1), one("projects", 1)}); err == nil || !strings.Contains(err.Error(), "share order 1 in world harbor") {
		t.Errorf("two guides at one order: %v", err)
	}
	other := one("projects", 2)
	other.Steps[0].Capture.Name = "get-started-01"
	if err := checkGuideSet([]guide{one("get-started", 1), other}); err == nil || !strings.Contains(err.Error(), "is also guide get-started's") {
		t.Errorf("two guides with one image: %v", err)
	}
}

func TestGuideFilesRefuseWhatTheFormatLacks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "projects.json"), `{"guide": "projects", "world": "harbor", "order": 2, "steps": [{"step": "open", "route": "/", "clicks": []}]}`)
	if _, err := loadGuides(dir); err == nil || !strings.Contains(err.Error(), "clicks") {
		t.Errorf("a field the format lacks: %v", err)
	}
	writeFile(t, filepath.Join(dir, "projects.json"), `{"guide": "plan", "world": "harbor", "order": 2, "steps": [{"step": "open", "route": "/"}]}`)
	if _, err := loadGuides(dir); err == nil || !strings.Contains(err.Error(), "a guide's file is named for it") {
		t.Errorf("a file named for another guide: %v", err)
	}
}
