//go:build screenshots

package screenshots

import (
	"os"
	"path/filepath"
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
// at attended, and three clones on disk that no project has registered. Everything a guide tells a person to do
// in the web UI, the walk does there.
type harbor struct {
	in *install
	// src is the folder of clones, which a guide's Folder field names, and api and web are the clones in it.
	src      string
	api, web *clone
	// plan is the planning repository's clone, beside the folder of clones, which a guide registers as one
	// repository.
	plan *clone
}

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
	in.writeFixture(harborTurns())
	in.start("--intake-tick", "200ms", "--lease-tick", "200ms", "--wake-tick", "200ms", "--batch-tick", "500ms",
		"--heartbeat", "3m", "--lease-ttl", "15m")
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	return h
}

// vars are the names a guide's {{name}} can stand for in this world.
func (h *harbor) vars() map[string]string {
	return map[string]string{"src": h.src, "api": h.api.Dir, "web": h.web.Dir, "plan": h.plan.Dir}
}

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
