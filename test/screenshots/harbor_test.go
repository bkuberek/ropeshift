//go:build screenshots

package screenshots

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

// guidesFolder is the one fixed, neutral folder the guide walk's clones live in. A field of the web UI shows
// a path under it, where the capture would otherwise show a temporary directory, so the capture's private-word
// check can stay on for every image. The kit makes it, removes it at the end of the run, and refuses to start
// while it exists, which a run that is still going or one that was killed leaves.
const guidesFolder = "/tmp/ropeshift-guides"

// subscription is the word `claude auth status` reports for a sign-in with a subscription.
const subscription = "claude.ai"

// harbor is the world the guides 1 to 7 walk: a fresh installation of Harbor, a project of two code
// repositories and a planning repository. It holds what the web UI cannot make: the daemon and its connected
// worker, the fake agent CLI signed in with a subscription, as on a person's own machine, so the worker starts
// at attended, three clones on disk that no project has registered, and the fake GitHub CLI signed in to a
// fake forge. Everything a guide tells a person to do in the web UI, the walk does there.
type harbor struct {
	in *install
	// src is the folder of clones, which a guide's Folder field names, and api and web are the clones in it.
	src      string
	api, web *clone
	// plan is the planning repository's clone, beside the folder of clones, which a guide registers as one
	// repository.
	plan *clone
	// gh is the directory of the fake GitHub CLI, which keeps the fake forge beside it.
	gh string
}

// harborBinding is where guide 7 tells ropeshift harbor-api lives on GitHub. The clone's remote is behind the
// neutral address, which names another host, so the guide names the binding.
const harborBinding = "github.com/harbor/harbor-api"

// harborRequiredChecks are the checks the fake forge's CI runs on each pull request, which the base branch's
// protection requires.
var harborRequiredChecks = []string{"build", "test"}

// newHarbor makes the folder of clones and starts the installation's daemon, and answers once its worker has
// connected. The installation has the first project and nothing else.
func newHarbor(in *install) *harbor {
	in.t.Helper()
	if _, err := os.Lstat(guidesFolder); err == nil {
		in.t.Fatalf("%s exists: another guide walk is running, or one was killed and left it; remove it and run again", guidesFolder)
	}
	if err := os.Mkdir(guidesFolder, 0o700); err != nil {
		in.t.Fatal(err)
	}
	// Registered before the daemon starts, so it runs after the daemon and the lanes have ended.
	in.t.Cleanup(func() {
		if err := os.RemoveAll(guidesFolder); err != nil {
			in.t.Errorf("removing %s: %v", guidesFolder, err)
		}
	})
	h := &harbor{in: in, src: filepath.Join(guidesFolder, "src")}
	in.gitSetup()
	h.api = in.newCloneAt(filepath.Join(h.src, "harbor-api"), harborRemoteBase, harborAPIFiles())
	h.web = in.newCloneAt(filepath.Join(h.src, "harbor-web"), harborRemoteBase, harborWebFiles())
	h.plan = in.newCloneAt(filepath.Join(guidesFolder, "harbor-plan"), harborRemoteBase, harborPlanFiles())
	state := in.paths().StateDir
	if err := os.MkdirAll(state, 0o700); err != nil {
		in.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "profile.toml"), []byte(userProfile), 0o600); err != nil {
		in.t.Fatal(err)
	}
	in.signIn = subscription
	in.scripts = harborLanes()
	in.writeFixture(harborTurns())
	h.fakeForge()
	// The beat is the world's three minutes: the worker uploads a forge report, such as guide 6's merge, as soon as
	// it records it, and a lane's state change publishes at once, so no step waits for a heartbeat to see either.
	// The forge tick is short, so guide 7's worker reads its sign-in and its pull request while the walk waits.
	in.start("--intake-tick", "200ms", "--lease-tick", "200ms", "--wake-tick", "200ms", "--batch-tick", "500ms",
		"--delivery-tick", "1s", "--forecast-tick", "1s", "--heartbeat", "3m", "--lease-ttl", "15m", "--forge-tick", "2s")
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	return h
}

// vars are the names a guide's {{name}} can stand for in this world.
func (h *harbor) vars() map[string]string {
	return map[string]string{"src": h.src, "api": h.api.Dir, "web": h.web.Dir, "plan": h.plan.Dir, "binding": harborBinding}
}

// fakeForge puts the fake GitHub CLI on the daemon's PATH, after the fake agent CLI and ahead of the canary, so
// the pull requests of guide 7 open on a fake forge and nothing reaches GitHub. Its sign-in is the fake's own,
// and no credential is read. It also stands for the forge's CI: each pull request a worker opens soon has the
// checks its base branch requires, passed.
func (h *harbor) fakeForge() {
	in := h.in
	in.t.Helper()
	h.gh = doubles.InstallFakeGH(in.t)
	claude, rest, _ := strings.Cut(in.path, string(os.PathListSeparator))
	in.path = strings.Join([]string{claude, h.gh, rest}, string(os.PathListSeparator))
	stop, done := make(chan struct{}), make(chan struct{})
	// Registered after the fake's directory, so the CI stops before the directory goes.
	in.t.Cleanup(func() {
		close(stop)
		<-done
	})
	go func() {
		defer close(done)
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			if err := h.runChecks(); err != nil {
				in.t.Errorf("the fake forge's CI: %v", err)
				return
			}
		}
	}()
}

// runChecks gives each open pull request with no checks yet the required checks, passed on its head.
func (h *harbor) runChecks() error {
	state, err := ghstate.Read(h.gh)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(state.PullRequests, unchecked) {
		return nil
	}
	return ghstate.Edit(h.gh, func(s *ghstate.State) error {
		for i := range s.PullRequests {
			pr := &s.PullRequests[i]
			if !unchecked(*pr) {
				continue
			}
			for _, name := range harborRequiredChecks {
				pr.Checks = append(pr.Checks, ghstate.Check{Typename: "CheckRun", Name: name, Status: "COMPLETED", Conclusion: "SUCCESS"})
			}
			pr.Required = slices.Clone(harborRequiredChecks)
		}
		return nil
	})
}

// unchecked is an open pull request that no check has run on yet.
func unchecked(pr ghstate.PullRequest) bool { return pr.State == "OPEN" && len(pr.Checks) == 0 }

// harborAPIFiles is the API repository of Harbor, which tracks the berths and sailings of a small port.
func harborAPIFiles() map[string]string {
	return map[string]string{
		"README.md": lines("# harbor-api", "", "The berths and the sailings of a small port, and the API the port's web pages call."),
		"go.mod":    "module example.com/harbor-api\n\ngo 1.24\n",
		"cmd/harbor/main.go": lines("package main", "", "import \"example.com/harbor-api/internal/berths\"", "",
			"func main() { berths.Serve() }"),
		"internal/berths/server.go": lines("package berths", "", "// Serve answers the berths API.", "func Serve() {}"),
		"docs/architecture.md": lines("# Architecture", "",
			"The API is one service in front of a SQLite file. A request passes through a router and the berth",
			"handlers, which read and change the berths."),
	}
}

// harborWebFiles is the web repository of Harbor, whose pages call the API.
func harborWebFiles() map[string]string {
	return map[string]string{
		"README.md":       lines("# harbor-web", "", "The port's public pages, which show the berths and the sailings."),
		"package.json":    lines("{", "  \"name\": \"harbor-web\",", "  \"private\": true", "}"),
		"src/berths.ts":   lines("// listBerths reads the berths from the API.", "export async function listBerths(): Promise<void> {}"),
		"src/sailings.ts": lines("// listSailings reads the sailings from the API.", "export async function listSailings(): Promise<void> {}"),
	}
}

// harborPlanFiles is the planning repository of Harbor, which holds the instructions its agents read and,
// once a planning session writes it, the plan.
func harborPlanFiles() map[string]string {
	return map[string]string{
		"README.md": lines("# harbor-plan", "", "The plan of Harbor: its goals, documents, milestones, and contracts."),
		"AGENTS.md": lines("# Harbor", "",
			"Harbor tracks the berths and the sailings of a small port. Its code is in two repositories:",
			"harbor-api, the API, and harbor-web, the port's public pages.", "",
			"Plan files go in plan/, in ropeshift's file format."),
	}
}

// harborTurns are the turns of every session the fake agent CLI runs in Harbor that no lane script names: the
// session's opening prompt, which it answers with nothing to show, and then a reply to each message of a
// conversation with the assistant.
func harborTurns() [][]step {
	return [][]step{
		{{"init": true}, result(0.01, 20)},
		reply("msg-1", "Harbor has two repositories, harbor-api and harbor-web, and no plan yet."),
		reply("msg-2", "Start a planning session, and describe what the port's pages should show."),
	}
}
