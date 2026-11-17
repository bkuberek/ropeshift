package exit

import (
	"bytes"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/test/apitest"
)

// gateCommands runs a binary with the gate set to a value, and returns what it prints.
func gateCommand(t *testing.T, in *install, binary, gate string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = append(in.env(), "ROPESHIFT_GROUP_MODE="+gate)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v with the gate %q: %v: %s", binary, args, gate, err, stderr.String())
	}
	// A flag's default names the socket of the install that ran it, which differs between two installs.
	return strings.ReplaceAll(stdout.String(), in.home, "<home>")
}

var routeWildcard = regexp.MustCompile(`\{[^}]*\}`)

// routesProbed is every route the daemon's two surfaces name, and the routes of the API no list names.
func routesProbed() []api.WebRoute {
	routes := append([]api.WebRoute{}, api.WebRoutes()...)
	for _, other := range []string{
		"GET /v1/passphrase", "PUT /v1/passphrase", "POST /v1/sessions", "GET /v1/health", "GET /v1/lanes", "GET /v1/fleet",
		"POST /v1/workers/register", "POST /v1/workers/nothing/heartbeat", "GET /v1/workers/nothing/desired", "GET /v1/nothing",
		"GET /", "GET /assets/nothing.js", "POST /",
	} {
		method, path, _ := strings.Cut(other, " ")
		routes = append(routes, api.WebRoute{Method: method, Pattern: path})
	}
	return routes
}

// statuses is what a surface answers to each probed route with no credential.
func statuses(t *testing.T, client *http.Client, base string) map[string]int {
	t.Helper()
	answers := map[string]int{}
	for _, route := range routesProbed() {
		path := routeWildcard.ReplaceAllStringFunc(route.Pattern, func(name string) string {
			if name == "{project}" {
				return "default"
			}
			return "nothing"
		})
		request, err := http.NewRequestWithContext(t.Context(), route.Method, base+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", route.Method, path, err)
		}
		// A stream answers its status and then goes on, so the answer is read from the status alone.
		response.Body.Close()
		answers[route.Method+" "+route.Pattern] = response.StatusCode
	}
	return answers
}

// observed is what a solo daemon shows of itself.
type observed struct {
	help map[string]string
	api  map[string]int
	web  map[string]int
}

func observeSolo(t *testing.T, gate string) observed {
	t.Helper()
	in := newInstall(t)
	in.daemonEnv = []string{"ROPESHIFT_GROUP_MODE=" + gate}
	d := in.start("--web", "127.0.0.1:0")
	in.waitReady(d)
	shown := observed{help: map[string]string{}}
	for name, run := range map[string][]string{
		"cli help": {binaries.cli, "help"}, "cli work help": {binaries.cli, "work", "list", "--help"},
		"daemon help solo": {binaries.daemon, "help", "solo"}, "daemon solo --help": {binaries.daemon, "solo", "--help"},
		"daemon version help": {binaries.daemon, "help", "version"},
	} {
		shown.help[name] = gateCommand(t, in, run[0], gate, run[1:]...)
	}
	socket := apitest.RawClient(in.paths().ControlSocket())
	shown.api = statuses(t, socket, "http://ropeshift")
	shown.web = statuses(t, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, d.webURL(t))
	return shown
}

// Solo mode ignores the group gate: with it set or not, the daemon and the CLI print the same help for what
// solo mode does, and the daemon answers the same routes on its socket and on its web listener.
func TestSoloIgnoresTheGroupGate(t *testing.T) {
	off, on := observeSolo(t, ""), observeSolo(t, "preview")
	for name, want := range off.help {
		if got := on.help[name]; got != want {
			t.Errorf("%s differs with the gate on:\n%s\nagainst\n%s", name, got, want)
		}
	}
	for surface, pair := range map[string][2]map[string]int{"the socket": {off.api, on.api}, "the web listener": {off.web, on.web}} {
		if len(pair[0]) < 100 {
			t.Errorf("%s was probed on %d routes", surface, len(pair[0]))
		}
		for route, want := range pair[0] {
			if got := pair[1][route]; got != want {
				t.Errorf("%s answers %s with %d under the gate, and %d without it", surface, route, got, want)
			}
		}
	}
	// What the gate does change is the daemon's list of commands, and it changes nothing else.
	in := newInstall(t)
	if gateCommand(t, in, binaries.daemon, "", "help") == gateCommand(t, in, binaries.daemon, "preview", "help") {
		t.Error("the daemon's list of commands ignores the gate")
	}
}
