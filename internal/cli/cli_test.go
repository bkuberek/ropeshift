package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/apitest"
)

type session struct {
	t      *testing.T
	daemon *apitest.Daemon
}

func run(t *testing.T, socket string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr, cli.Options{Socket: socket})
	return code, stdout.String(), stderr.String()
}

func newSession(t *testing.T) *session {
	t.Helper()
	return &session{t: t, daemon: apitest.Start(t)}
}

func (s *session) run(args ...string) (int, string, string) {
	s.t.Helper()
	return run(s.t, s.daemon.Socket, args...)
}

func (s *session) must(args ...string) string {
	s.t.Helper()
	code, stdout, stderr := s.run(args...)
	if code != cli.ExitOK {
		s.t.Fatalf("%v: exit %d: %s", args, code, stderr)
	}
	return stdout
}

func (s *session) create(id string) {
	s.t.Helper()
	s.must("work", "create", "--id", id, "--title", "Add the retry budget", "--criterion", "It retries")
}

func (s *session) ready(id string) {
	s.t.Helper()
	s.create(id)
	s.must("work", "review", id)
}

func (s *session) claimed(id string) {
	s.t.Helper()
	s.ready(id)
	s.must("work", "claim", id, "--worker", "worker-a", "--claim-id", "claim-"+id)
}

func (s *session) fenceOf(id string) string {
	s.t.Helper()
	var row workorders.WorkOrder
	if err := json.Unmarshal([]byte(s.must("work", "show", id, "--json")), &row); err != nil {
		s.t.Fatal(err)
	}
	return strconv.FormatInt(row.Fence, 10)
}

func (s *session) lapse(id string) {
	s.t.Helper()
	s.ready(id)
	s.must("work", "claim", id, "--worker", "worker-a", "--claim-id", "claim-"+id, "--ttl", "1ms")
	time.Sleep(5 * time.Millisecond)
	actor := ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}
	lapsed, err := control.NewLeases(s.daemon.Store).Sweep(s.t.Context(), apitest.Scope, actor)
	if err != nil || len(lapsed) != 1 {
		s.t.Fatalf("lapsed %v: %v", lapsed, err)
	}
}

// TestExitCodes is the documented table: every command, each way it can end.
func TestExitCodes(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*session)
		args  []string
		want  int
	}{
		{"no command", nil, nil, cli.ExitUsage},
		{"an unknown group", nil, []string{"nonsense"}, cli.ExitUsage},
		{"an unknown work command", nil, []string{"work", "nonsense"}, cli.ExitUsage},
		{"work with no command", nil, []string{"work"}, cli.ExitUsage},
		{"an unknown flag", nil, []string{"work", "list", "--nonsense"}, cli.ExitUsage},

		{"create", nil, []string{"work", "create", "--title", "T"}, cli.ExitOK},
		{"create with no title", nil, []string{"work", "create"}, cli.ExitUsage},
		{"create with a stray argument", nil, []string{"work", "create", "--title", "T", "stray"}, cli.ExitUsage},
		{"create with a bad size", nil, []string{"work", "create", "--title", "T", "--size", "enormous"}, cli.ExitUsage},

		{"list", nil, []string{"work", "list"}, cli.ExitOK},
		{"list with a stray argument", nil, []string{"work", "list", "stray"}, cli.ExitUsage},

		{"show", func(s *session) { s.create("wo") }, []string{"work", "show", "wo"}, cli.ExitOK},
		{"show an absent work order", nil, []string{"work", "show", "absent"}, cli.ExitNotFound},
		{"show with no id", nil, []string{"work", "show"}, cli.ExitUsage},

		{"review", func(s *session) { s.create("wo") }, []string{"work", "review", "wo"}, cli.ExitOK},
		{"review of an absent work order", nil, []string{"work", "review", "absent"}, cli.ExitNotFound},
		{"review with no id", nil, []string{"work", "review"}, cli.ExitUsage},
		{
			"review that the definition of ready refuses",
			func(s *session) { s.must("work", "create", "--id", "bare", "--title", "No criteria") },
			[]string{"work", "review", "bare"}, cli.ExitNotReady,
		},
		{
			"a repeated review",
			func(s *session) { s.claimed("wo") },
			[]string{"work", "review", "wo"}, cli.ExitOK,
		},

		{"claim", func(s *session) { s.ready("wo") }, []string{"work", "claim", "wo", "--worker", "w"}, cli.ExitOK},
		{"claim with no worker", nil, []string{"work", "claim", "wo"}, cli.ExitUsage},
		{"claim with no id", nil, []string{"work", "claim", "--worker", "w"}, cli.ExitUsage},
		{"claim of an absent work order", nil, []string{"work", "claim", "absent", "--worker", "w"}, cli.ExitNotFound},
		{"a lost claim", func(s *session) { s.claimed("wo") }, []string{"work", "claim", "wo", "--worker", "w"}, cli.ExitConflict},

		{"release", func(s *session) { s.claimed("wo") }, nil, cli.ExitOK},
		{"release with a stale fence", func(s *session) { s.claimed("wo") }, []string{"work", "release", "wo", "--claim-id", "claim-wo", "--fence", "99"}, cli.ExitFenced},
		{"release of a lapsed lease", func(s *session) { s.lapse("wo") }, nil, cli.ExitLapsed},
		{"release with no claim", nil, []string{"work", "release", "wo", "--fence", "1"}, cli.ExitUsage},
		{"release with no fence", nil, []string{"work", "release", "wo", "--claim-id", "c"}, cli.ExitUsage},

		{"reassign", func(s *session) { s.lapse("wo") }, []string{"work", "reassign", "wo", "--reason", "a closed laptop"}, cli.ExitOK},
		{"reassign of work that did not lapse", func(s *session) { s.claimed("wo") }, []string{"work", "reassign", "wo", "--reason", "r"}, cli.ExitConflict},
		{"reassign with no reason", nil, []string{"work", "reassign", "wo"}, cli.ExitUsage},

		{"worker list", nil, []string{"worker", "list"}, cli.ExitOK},
		{"worker token", nil, []string{"worker", "token"}, cli.ExitOK},
		{"worker list with a stray argument", nil, []string{"worker", "list", "stray"}, cli.ExitUsage},
		{"an unknown worker command", nil, []string{"worker", "nonsense"}, cli.ExitUsage},

		{"source list", nil, []string{"source", "list"}, cli.ExitOK},
		{"an unknown source command", nil, []string{"source", "nonsense"}, cli.ExitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSession(t)
			if tt.setup != nil {
				tt.setup(s)
			}
			args := tt.args
			if args == nil && tt.setup != nil {
				args = []string{"work", "release", "wo", "--claim-id", "claim-wo", "--fence", s.fenceOf("wo")}
			}
			code, stdout, stderr := s.run(args...)
			if code != tt.want {
				t.Errorf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tt.want, stdout, stderr)
			}
			if code != cli.ExitOK && stderr == "" {
				t.Error("a failure printed nothing")
			}
		})
	}
}

func TestUnreachableDaemon(t *testing.T) {
	socket := filepath.Join(apitest.ShortDir(t), api.ControlSocketName)
	for _, args := range [][]string{
		{"work", "list"},
		{"work", "show", "wo"},
		{"work", "create", "--title", "T"},
		{"work", "review", "wo"},
		{"work", "claim", "wo", "--worker", "w"},
		{"work", "release", "wo", "--claim-id", "c", "--fence", "1"},
		{"work", "reassign", "wo", "--reason", "r"},
		{"source", "list"},
		{"worker", "list"},
		{"worker", "token"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, _, stderr := run(t, socket, args...)
			if code != cli.ExitUnreachable {
				t.Errorf("exit %d, want %d: %s", code, cli.ExitUnreachable, stderr)
			}
		})
	}
}

func TestStoreFailureIsAnError(t *testing.T) {
	s := newSession(t)
	if err := s.daemon.Store.Close(); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := s.run("work", "list")
	if code != cli.ExitError {
		t.Errorf("exit %d, want %d: %s", code, cli.ExitError, stderr)
	}
}

func TestJSONOutput(t *testing.T) {
	s := newSession(t)
	var created workorders.WorkOrder
	if err := json.Unmarshal([]byte(s.must("work", "create", "--id", "wo", "--title", "T", "--criterion", "It works", "--json")), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "wo" || created.State != workorders.ContractReview {
		t.Errorf("created %+v", created)
	}
	var review api.ReviewResult
	if err := json.Unmarshal([]byte(s.must("work", "review", "wo", "--json")), &review); err != nil {
		t.Fatal(err)
	}
	if review.State != workorders.Ready {
		t.Errorf("review %+v", review)
	}
	var grant api.Grant
	if err := json.Unmarshal([]byte(s.must("work", "claim", "wo", "--worker", "w", "--claim-id", "c", "--json")), &grant); err != nil {
		t.Fatal(err)
	}
	if grant.ClaimID != "c" || grant.Fence != 1 || grant.Contract == nil {
		t.Errorf("grant %+v", grant)
	}
	var list api.WorkOrderList
	if err := json.Unmarshal([]byte(s.must("work", "list", "--json")), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.WorkOrders) != 1 || list.WorkOrders[0].State != workorders.Claimed {
		t.Errorf("list %+v", list)
	}
	code, _, stderr := s.run("work", "show", "absent", "--json")
	if code != cli.ExitNotFound {
		t.Fatalf("exit %d", code)
	}
	var body api.ErrorBody
	if err := json.Unmarshal([]byte(stderr), &body); err != nil {
		t.Fatalf("stderr is not JSON: %v: %q", err, stderr)
	}
	if body.Error.Code != api.CodeNotFound {
		t.Errorf("error %+v", body)
	}
}

func TestListFilterByState(t *testing.T) {
	s := newSession(t)
	s.create("waiting")
	s.ready("ready")
	stdout := s.must("work", "list", "--state", "ready")
	if !strings.Contains(stdout, "ready") || strings.Contains(stdout, "waiting") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestDependsOnHoldsWorkBack(t *testing.T) {
	s := newSession(t)
	s.create("first")
	s.must("work", "create", "--id", "second", "--title", "T", "--criterion", "C", "--depends-on", "first")
	code, stdout, _ := s.run("work", "review", "second")
	if code != cli.ExitNotReady {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "dependencies") {
		t.Errorf("the failed check is not named: %q", stdout)
	}
}

// TestExitCodesAreDocumented holds the reference to the constants, so a code cannot
// exist without its line.
func TestExitCodesAreDocumented(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`(?m)^\| (\d+) \|`)
	var documented []string
	for _, match := range row.FindAllStringSubmatch(string(raw), -1) {
		documented = append(documented, match[1])
	}
	want := fmt.Sprint([]int{cli.ExitOK, cli.ExitError, cli.ExitUsage, cli.ExitNotFound, cli.ExitConflict,
		cli.ExitFenced, cli.ExitLapsed, cli.ExitNotReady, cli.ExitForbidden, cli.ExitUnreachable})
	if got := "[" + strings.Join(documented, " ") + "]"; got != want {
		t.Errorf("documented %s, want %s", got, want)
	}
}
