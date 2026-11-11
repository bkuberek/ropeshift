package exit

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// TestTwoProjectsShareOneNeedsYouOnTheBinary is FR-PROJ-8 through the real daemon and the real command
// line: work that waits in two projects shows in one Needs you, each item with its project, in `needs`,
// `top`, and `project list`. Once the second project is archived it shows in none of them, because an
// archived project takes no change.
func TestTwoProjectsShareOneNeedsYouOnTheBinary(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	in.must("project", "create", "beta")
	in.must("work", "create", "--project", ledger.DefaultProjectName, "--id", "wo-home", "--title", "Add the retry budget", "--criterion", "It retries")
	in.must("work", "create", "--project", "beta", "--id", "wo-beta", "--title", "Ship the export", "--criterion", "It exports")

	var view api.NeedsYou
	if err := json.Unmarshal([]byte(in.must("needs", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, item := range view.Items {
		if item.Project != nil {
			labels[item.ID] = item.Project.Name
		}
	}
	if labels["wo-home"] != ledger.DefaultProjectName || labels["wo-beta"] != "beta" {
		t.Errorf("needs labels %v", labels)
	}
	// top shows one project's lanes, so it takes its project as any project command does, and lists what
	// waits in every project above them.
	frame := in.must("top", "--once", "--width", "200", "--project", ledger.DefaultProjectName)
	for _, want := range []string{"wo-home", "wo-beta", "beta "} {
		if !strings.Contains(frame, want) {
			t.Errorf("top lacks %q:\n%s", want, frame)
		}
	}
	list := in.must("project", "list")
	if strings.Count(list, "1 waiting") != 2 {
		t.Errorf("project list:\n%s", list)
	}

	in.must("project", "archive", "beta")
	if needs := in.must("needs"); strings.Contains(needs, "wo-beta") || !strings.Contains(needs, "wo-home") {
		t.Errorf("needs with beta archived:\n%s", needs)
	}
}
