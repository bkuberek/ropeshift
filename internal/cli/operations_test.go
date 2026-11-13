package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
)

// workerPlane are the routes only a worker calls, each with who proves itself there. A person has no
// worker's credential, and a command for one would be a command that always fails (FR-API-3).
var workerPlane = map[string]string{
	"POST /v1/workers/register":                      "a join token, which a worker spends once",
	"POST /v1/workers/{id}/sessions":                 "a worker's credential",
	"POST /v1/workers/{id}/heartbeat":                "a worker's credential",
	"GET /v1/workers/{id}/desired":                   "a worker's credential",
	"GET /v1/workers/{id}/watch":                     "a worker's credential",
	"POST /v1/workers/{id}/events":                   "a worker's credential",
	"POST /v1/workers/{id}/claims":                   "a worker's credential",
	"POST /v1/workers/{id}/ask":                      "a worker's credential",
	"POST /v1/workers/{id}/lane-facts":               "a worker's credential",
	"POST /v1/workers/{id}/lane-context":             "a worker's credential",
	"POST /v1/workers/{id}/rulings":                  "a worker's credential",
	"POST /v1/workers/{id}/approvals/use":            "a worker's credential",
	"POST /v1/workers/{id}/approvals/decide":         "a worker's credential",
	"POST /v1/workers/{id}/orchestrator/digest":      "a worker's credential",
	"POST /v1/workers/{id}/orchestrator/acts/{verb}": "a worker's credential",
	"POST /v1/workers/{id}/assistant-credentials":    "a worker's credential",
	"POST /v1/workers/{id}/assistant-entries":        "a worker's credential",
}

// operations names the command of every other route, so a new route fails here until it has one.
var operations = map[string]string{
	"GET /v1/health":                                   "health",
	"GET /v1/projects":                                 "project list",
	"GET /v1/projects/{project}":                       "project show",
	"POST /v1/projects":                                "project create",
	"POST /v1/projects/import":                         "project import",
	"POST /v1/projects/{project}/rename":               "project rename",
	"POST /v1/projects/{project}/archive":              "project archive",
	"POST /v1/projects/{project}/restore":              "project restore",
	"POST /v1/projects/{project}/remove":               "project remove",
	"GET /v1/sources":                                  "source list",
	"POST /v1/work-orders":                             "work create",
	"GET /v1/work-orders":                              "work list",
	"GET /v1/work-orders/{id}":                         "work show",
	"POST /v1/work-orders/{id}/review":                 "work review",
	"GET /v1/work-orders/{id}/acceptance":              "work acceptance",
	"POST /v1/work-orders/{id}/accept":                 "work accept",
	"POST /v1/work-orders/{id}/rework":                 "work rework",
	"POST /v1/work-orders/{id}/claims":                 "work claim",
	"POST /v1/work-orders/{id}/claims/{claim}/release": "work release",
	"POST /v1/work-orders/{id}/reassign":               "work reassign",
	"POST /v1/work-orders/{id}/cancel":                 "work cancel",
	"POST /v1/work-orders/{id}/lanes":                  "lane start",
	"POST /v1/work-orders/{id}/writes":                 "work write",
	"POST /v1/work-orders/{id}/send-back":              "work send-back",
	"POST /v1/work-orders/{id}/resolve":                "work resolve",
	"POST /v1/work-orders/{id}/replan":                 "work replan",
	"POST /v1/work-orders/{id}/retry":                  "work retry",
	"POST /v1/work-orders/{id}/lane-resume":            "lane resume",
	"POST /v1/work-orders/{id}/lane-reset":             "lane reset",
	"POST /v1/work-orders/{id}/lane-respawn":           "lane respawn",
	"POST /v1/work-orders/{id}/lane-archive":           "lane archive",
	"POST /v1/workers/tokens":                          "worker token",
	"GET /v1/workers":                                  "worker list",
	"GET /v1/lanes":                                    "lane list",
	"GET /v1/lanes/{id}":                               "lane show",
	"GET /v1/lanes/{id}/outline":                       "lane outline",
	"GET /v1/fleet":                                    "fleet show",
	"PUT /v1/fleet/mode":                               "fleet run",
	"PUT /v1/fleet/caps":                               "fleet caps",
	"GET /v1/plans":                                    "plan list",
	"GET /v1/plans/{id}":                               "plan show",
	"POST /v1/plans/{id}/reviews":                      "plan review",
	"GET /v1/requirements":                             "requirement list",
	"GET /v1/requirements/{key}":                       "requirement get",
	"GET /v1/documents":                                "document list",
	"GET /v1/documents/read":                           "document show",
	"GET /v1/documents/versions":                       "document versions",
	"GET /v1/documents/recorded":                       "document recorded",
	"POST /v1/messages":                                "message send",
	"GET /v1/messages/{id}":                            "message show",
	"GET /v1/lanes/{id}/messages":                      "message list",
	"GET /v1/workers/{id}/autonomy":                    "worker autonomy",
	"PUT /v1/workers/{id}/autonomy":                    "worker level",
	"GET /v1/workers/{id}/caps":                        "worker caps",
	"PUT /v1/workers/{id}/caps":                        "worker caps",
	"POST /v1/workers/{id}/autonomy/acknowledgement":   "worker acknowledge-risk",
	"POST /v1/workers/{id}/windows":                    "worker window-open",
	"DELETE /v1/workers/{id}/windows":                  "worker window-close",
	"GET /v1/questions":                                "question list",
	"POST /v1/questions":                               "question record",
	"GET /v1/questions/{id}":                           "question show",
	"POST /v1/questions/{id}/dismiss":                  "question dismiss",
	"POST /v1/questions/{id}/answer":                   "question answer",
	"GET /v1/question-batches":                         "question batches",
	"GET /v1/decisions":                                "decision list",
	"POST /v1/decisions":                               "decision record",
	"GET /v1/decisions/{id}":                           "decision show",
	"POST /v1/decisions/{id}/overturn":                 "decision overturn",
	"GET /v1/policies":                                 "policy list",
	"POST /v1/policies":                                "policy publish",
	"GET /v1/policies/{version}":                       "policy show",
	"GET /v1/categories":                               "category list",
	"POST /v1/categories":                              "category add",
	"GET /v1/candidates":                               "search",
	"GET /v1/effects":                                  "effect list",
	"GET /v1/effects/{id}":                             "effect show",
	"POST /v1/effects/{id}/decide":                     "effect decide",
	"POST /v1/sessions":                                "login",
	"POST /v1/sessions/end":                            "sign-out",
	"POST /v1/sessions/end-all":                        "sign-out",
	"GET /v1/delegates":                                "delegate list",
	"POST /v1/delegates":                               "delegate create",
	"POST /v1/delegates/{id}/revoke":                   "delegate revoke",
	"POST /v1/delegates/{id}/remove":                   "delegate remove",
	"PUT /v1/passphrase":                               "passphrase set",
	"GET /v1/passphrase":                               "init",
	"POST /v1/backup":                                  "backup",
	"POST /v1/installation/backup":                     "backup",
	"GET /v1/workers/{id}/orchestrator":                "orchestrator show",
	"PUT /v1/workers/{id}/orchestrator":                "orchestrator start",
	"GET /v1/notices":                                  "notice list",
	"GET /v1/telemetry/records":                        "telemetry query",
	"GET /v1/telemetry/lanes":                          "telemetry lanes",
	"GET /v1/measures":                                 "measures",
	"GET /v1/approvals":                                "approval list",
	"GET /v1/approvals/{id}":                           "approval show",
	"POST /v1/approvals/{id}/decide":                   "approval approve",
	"GET /v1/repositories":                             "repository list",
	"GET /v1/repositories/{id}":                        "repository show",
	"PATCH /v1/repositories/{id}":                      "repository set",
	"POST /v1/repositories":                            "repository add",
	"POST /v1/repositories/{id}/remove":                "repository remove",
	"GET /v1/roster":                                   "agent list",
	"GET /v1/roster/history":                           "agent history",
	"POST /v1/roster/changes":                          "agent set",
	"POST /v1/roster/revert":                           "agent revert",
	"GET /v1/conversations":                            "chat list",
	"POST /v1/conversations":                           "chat new",
	"GET /v1/conversations/{id}":                       "chat show",
	"POST /v1/conversations/{id}/open":                 "chat open",
	"POST /v1/conversations/{id}/stop":                 "chat stop",
	"POST /v1/conversations/{id}/rename":               "chat rename",
	"POST /v1/conversations/{id}/delete":               "chat delete",
	"POST /v1/conversations/{id}/compact":              "chat compact",
	"POST /v1/conversations/{id}/clear":                "chat clear",
	"GET /v1/conversations/{id}/entries":               "chat show",
	"POST /v1/conversations/{id}/messages":             "chat send",
	"GET /v1/lanes/{id}/stream":                        "lane attach",
	"GET /v1/orchestrators/{worker}/stream":            "orchestrator attach",
}

// namedRoutes are the routes the source spells through a constant.
var namedRoutes = map[string]string{
	"api.EventsPath": "events", "api.RiskPath": "worker risk", "api.NeedsYouPath": "needs", "api.PanePath": "lane attach", "api.PaneInputPath": "lane attach",
	"api.AuditPath": "audit", "api.MembersPath": "member list", "api.OperationsPath": "operation list", "api.ImportUploadPath": "project import", "api.BackupDownloadPath": "backup", "api.InstallationBackupDownloadPath": "backup",
	"api.ConversationTargetsPath": "chat targets",
}

var (
	literalRoute = regexp.MustCompile(`mux\.HandleFunc\("((?:GET|POST|PUT|DELETE|PATCH) /[^"]+)"`)
	namedRoute   = regexp.MustCompile(`mux\.HandleFunc\("(?:GET|POST|PUT|DELETE|PATCH) "\+(api\.\w+),`)
)

// TestEveryOperationHasACommand reads the routes from the source, so none can be left out (FR-API-1).
func TestEveryOperationHasACommand(t *testing.T) {
	files, err := filepath.Glob("../api/server/*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(raw)) {
			if found := literalRoute.FindStringSubmatch(line); found != nil {
				seen[found[1]] = true
				_, plane := workerPlane[found[1]]
				command, named := operations[found[1]]
				switch {
				case !plane && !named:
					t.Errorf("%s has no command, and is no worker's route", found[1])
				case named && !cli.HasCommand(command):
					t.Errorf("%s names %q, which does not exist", found[1], command)
				}
			}
			if found := namedRoute.FindStringSubmatch(line); found != nil {
				command, named := namedRoutes[found[1]]
				if !named || !cli.HasCommand(command) {
					t.Errorf("%s has no command", found[1])
				}
			}
		}
	}
	if len(seen) < 60 {
		t.Fatalf("read %d routes, so the source scan found too few", len(seen))
	}
	for route := range operations {
		if !seen[route] {
			t.Errorf("%s is listed and nothing serves it", route)
		}
	}
	for _, name := range api.Views {
		if !cli.HasCommand("view " + name) {
			t.Errorf("view %s has no command", name)
		}
	}
}

func TestEveryCommandTakesJSON(t *testing.T) {
	for _, command := range cli.Commands() {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(strings.Fields(command), "--json", "--socket", filepath.Join(t.TempDir(), "nothing"))
			cli.Run(args, &stdout, &stderr, cli.Options{})
			if strings.Contains(stderr.String(), "flag provided but not defined") {
				t.Errorf("--json refused: %s", stderr.String())
			}
		})
	}
}

// With --json a failure is one document on stderr, so a script reads a refusal and its code alike.
func TestAFailureInJSONIsOneDocument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	socket := filepath.Join(t.TempDir(), "nothing")
	code := cli.Run([]string{"work", "show", "wo", "--json", "--socket", socket}, &stdout, &stderr, cli.Options{})
	if code != cli.ExitUnreachable {
		t.Fatalf("exit %d", code)
	}
	var body api.ErrorBody
	if err := json.Unmarshal(stderr.Bytes(), &body); err != nil || body.Error.Message == "" {
		t.Errorf("stderr %q: %v", stderr.String(), err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout %q", stdout.String())
	}
}

func TestEveryCommandIsDocumented(t *testing.T) {
	raw, err := os.ReadFile("../../docs/cli.md")
	if err != nil {
		t.Fatal(err)
	}
	docs := string(raw)
	var stderr bytes.Buffer
	cli.Run(nil, &bytes.Buffer{}, &stderr, cli.Options{})
	usage := stderr.String()
	for _, command := range cli.Commands() {
		words := strings.Fields(command)
		if words[0] == "view" {
			if !strings.Contains(docs, "`"+words[1]+"`") {
				t.Errorf("view %s is not in the document", words[1])
			}
			continue
		}
		if !strings.Contains(usage, "  "+command+" ") && !strings.Contains(usage, "  "+command+"\n") {
			t.Errorf("%s is not in the usage", command)
		}
		if words[0] == "fleet" && slices.Contains([]string{"run", "pause", "drain"}, words[1]) {
			if !strings.Contains(docs, "`ropeshift fleet run`, `pause`, `drain`") {
				t.Errorf("%s is not in docs/cli.md", command)
			}
			continue
		}
		if !strings.Contains(docs, "ropeshift "+command) {
			t.Errorf("%s is not in docs/cli.md", command)
		}
	}
	if !strings.Contains(usage, "  view <name> [--category C]") {
		t.Error("the views are not in the usage")
	}
	if strings.Contains(usage, "plan, or planning work") {
		t.Error("the usage says one thing twice")
	}
}
