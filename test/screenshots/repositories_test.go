//go:build screenshots

package screenshots

import (
	"os"
	"path/filepath"
	"strings"

	"ropeshift.com/ropeshift/test/gittest"
)

// remoteBase is where every sample repository's remote appears to live. Git rewrites it to a bare
// repository inside the installation, so the pages show a neutral address and nothing leaves the machine.
const remoteBase = "https://git.example.com/orders/"

// The sample's author, as git and every commit name it.
const (
	authorName  = "Sample Author"
	authorEmail = "author@example.com"
)

// gitSetup writes the home's git configuration: a neutral author, and the rewrite of the remote address to
// the bare repositories. The daemon, its lanes, and the kit's own git commands all read it.
func (in *install) gitSetup() {
	in.t.Helper()
	remotes := filepath.Join(in.root, "remotes")
	if err := os.MkdirAll(remotes, 0o700); err != nil {
		in.t.Fatal(err)
	}
	config := "[user]\n\tname = " + authorName + "\n\temail = " + authorEmail + "\n" +
		"[init]\n\tdefaultBranch = main\n" +
		"[protocol \"file\"]\n\tallow = always\n" +
		"[url \"" + remotes + "/\"]\n\tinsteadOf = " + remoteBase + "\n"
	// The daemon's git reads the home's .gitconfig, and gittest's commands read the file it names.
	for _, name := range []string{".gitconfig", "gitconfig"} {
		if err := os.WriteFile(filepath.Join(in.home, name), []byte(config), 0o600); err != nil {
			in.t.Fatal(err)
		}
	}
}

// clone is one sample repository: a clone in the installation's source folder whose origin is a bare
// repository behind the neutral address.
type clone struct {
	*gittest.Repo
	name string
}

// newClone makes a bare remote and a clone of it with one commit of files.
func (in *install) newClone(folder, name string, files map[string]string) *clone {
	in.t.Helper()
	dir := filepath.Join(in.root, folder, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		in.t.Fatal(err)
	}
	repo := &gittest.Repo{T: in.t, Dir: dir, Home: in.home}
	c := &clone{Repo: repo, name: name}
	c.Git("init", "--quiet")
	c.write(files)
	c.Git("add", "-A")
	c.Git("commit", "--quiet", "-m", "Start "+name)
	bare := filepath.Join(in.root, "remotes", name+".git")
	c.Git("clone", "--quiet", "--bare", dir, bare)
	c.Git("remote", "add", "origin", remoteBase+name+".git")
	c.Git("fetch", "--quiet", "origin")
	c.Git("branch", "--quiet", "--set-upstream-to", "origin/main", "main")
	c.Git("remote", "set-head", "origin", "main")
	return c
}

func (c *clone) write(files map[string]string) {
	c.T.Helper()
	for name, body := range files {
		path := filepath.Join(c.Dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			c.T.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			c.T.Fatal(err)
		}
	}
}

// commit writes files, commits them, and pushes the commit, as a person on another machine would.
func (c *clone) commit(message string, files map[string]string) {
	c.T.Helper()
	c.write(files)
	c.Git("add", "-A")
	c.Git("commit", "--quiet", "-m", message)
	c.Git("push", "--quiet", "origin", "main")
}

// merge merges a lane's branch into main and pushes it, as a person merging the lane's work does.
func (c *clone) merge(branch, message string) {
	c.T.Helper()
	c.Git("merge", "--quiet", "--no-ff", "-m", message, branch)
	c.Git("push", "--quiet", "origin", "main")
}

// lines joins lines with a newline after each.
func lines(text ...string) string { return strings.Join(text, "\n") + "\n" }

// The sample's repository profile: a lane parks a moment after it asks, a request for approval waits past
// the whole capture, and nothing restarts on its own.
const repositoryProfile = "[lifecycle]\nask_wait = \"1s\"\nhandoff_timeout = \"10s\"\napproval_wait = \"30m\"\nmax_restarts = 0\n"

// The installation's own layer: questions reach a person in a batch that closes after two seconds, and an
// orchestrator wakes at once.
const userProfile = "[lifecycle]\nhuman_batch_window = \"2s\"\nhuman_batch_max_wait = \"2s\"\nwake_debounce = \"100ms\"\nwake_min_interval = \"200ms\"\n"

// ordersAPIFiles is the main repository as it starts: a small service with its documents.
func ordersAPIFiles() map[string]string {
	return map[string]string{
		".ropeshift/profile.toml": repositoryProfile,
		"README.md": lines("# orders-api", "",
			"The orders API takes orders from the storefront and from partner integrations, and hands them to fulfillment."),
		"go.mod": "module example.com/orders-api\n\ngo 1.24\n",
		"cmd/orders/main.go": lines("package main", "", "import \"example.com/orders-api/internal/orders\"", "",
			"func main() { orders.Serve() }"),
		"internal/orders/server.go": lines("package orders", "", "// Serve answers the orders API.", "func Serve() {}"),
		"internal/orders/errors.go": lines("package orders", "", "// Problem is the body of every error the API returns.",
			"type Problem struct {", "\tCode   string `json:\"code\"`", "\tDetail string `json:\"detail\"`", "}"),
		"docs/architecture.md": lines("# Architecture", "",
			"The orders API is one service in front of a Postgres database. Requests pass through a router, a",
			"rate limiter, and the order handlers. Every outbound call to fulfillment and payments goes through one",
			"client with a timeout and a retry budget.", "",
			"## Components", "",
			"- The router assigns a request ID and writes one structured log line per request.",
			"- The handlers validate an order, store it, and publish an event.",
			"- The payments client retries an idempotent call at most three times."),
		"docs/runbook.md": lines("# Runbook", "",
			"## A partner reports throttling", "",
			"Read the `rate_limited` log lines for the partner's client ID, and compare them with the partner's tier.", "",
			"## Fulfillment is slow", "",
			"Upstream timeouts surface as `upstream_timeout` errors. Check the fulfillment dashboard before retrying."),
		"docs/decisions/0001-postgres.md": lines("# Store orders in Postgres", "",
			"Orders are relational and need transactions across an order and its lines, so the service stores them",
			"in Postgres."),
	}
}

// ordersWebFiles is the storefront's checkout pages, the second repository of the project.
func ordersWebFiles() map[string]string {
	return map[string]string{
		".ropeshift/profile.toml": repositoryProfile,
		"README.md":               lines("# orders-web", "", "The checkout pages of the storefront, which call the orders API."),
		"package.json":            lines("{", "  \"name\": \"orders-web\",", "  \"private\": true", "}"),
		"src/checkout.ts":         lines("// placeOrder sends the cart to the orders API.", "export async function placeOrder(): Promise<void> {}"),
	}
}

// catalogFiles is the repository of the installation's second project.
func catalogFiles() map[string]string {
	return map[string]string{
		".ropeshift/profile.toml": repositoryProfile,
		"README.md":               lines("# catalog-service", "", "The product catalog the storefront reads."),
		"go.mod":                  "module example.com/catalog-service\n\ngo 1.24\n",
	}
}
