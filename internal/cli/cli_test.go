package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/contract"
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
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr, cli.Options{Socket: s.daemon.Socket, Session: s.daemon.Session})
	return code, stdout.String(), stderr.String()
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

// completed leaves a work order in `acceptance`, as a lane reporting these checks would.
func (s *session) completed(id string, checks ...workorders.CheckResult) {
	s.t.Helper()
	s.daemon.Completed(s.t, id, workorders.LaneResult{
		Report: workorders.Report{Outcome: workorders.OutcomeCompleted, Checks: checks},
		Branch: "lane/" + id, Head: "abc123",
	})
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
		{"acceptance of completed work", func(s *session) { s.completed("wo") }, []string{"work", "acceptance", "wo"}, cli.ExitOK},
		{"acceptance of an absent work order", nil, []string{"work", "acceptance", "absent"}, cli.ExitNotFound},
		{"acceptance with no id", nil, []string{"work", "acceptance"}, cli.ExitUsage},
		{"accept completed work", func(s *session) { s.completed("wo") }, []string{"work", "accept", "wo"}, cli.ExitOK},
		{"accept work that is not complete", func(s *session) { s.create("wo") }, []string{"work", "accept", "wo"}, cli.ExitConflict},
		{"accept with no id", nil, []string{"work", "accept"}, cli.ExitUsage},
		{
			"accept over a failed check", func(s *session) { s.completed("wo", workorders.CheckResult{Name: "unit", Criterion: "c1"}) },
			[]string{"work", "accept", "wo"}, cli.ExitConflict,
		},
		{"rework completed work", func(s *session) { s.completed("wo") }, []string{"work", "rework", "wo", "--reason", "it fails"}, cli.ExitOK},
		{"rework with no reason", func(s *session) { s.completed("wo") }, []string{"work", "rework", "wo"}, cli.ExitUsage},
		{"rework with no id", nil, []string{"work", "rework", "--reason", "it fails"}, cli.ExitUsage},
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

// planned holds a goal, a plan, a requirement, and one contract in a session's ledger.
func (s *session) planned() {
	s.t.Helper()
	source := fake.NewWorkSource()
	source.Write("goal.md", []byte(contract.GoalText("a-goal", "The goal")))
	source.Write("plan.md", []byte(contract.PlanText("a-plan", "The plan", "a-goal")))
	source.Write("requirements.md", []byte(contract.RequirementsText("payments", "PAY-12", "It retries")))
	report, err := control.NewIntake(s.daemon.Store).Poll(s.t.Context(), apitest.Scope, source)
	if err != nil || len(report.Problems) != 0 {
		s.t.Fatalf("report %+v: %v", report, err)
	}
}

// TestPlanShowReturnsTheAPIRecord is the unit's check: the CLI prints what the API holds.
func TestPlanShowReturnsTheAPIRecord(t *testing.T) {
	s := newSession(t)
	s.planned()
	plans, err := s.daemon.Client.Plans(t.Context())
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans %+v: %v", plans, err)
	}
	want, err := s.daemon.Client.Plan(t.Context(), plans[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var got api.PlanView
	if err := json.Unmarshal([]byte(s.must("plan", "show", plans[0].ID, "--json")), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the CLI printed %+v, and the API holds %+v", got, want)
	}
	if text := s.must("plan", "show", plans[0].ID); !strings.Contains(text, "The plan") || !strings.Contains(text, "The goal") {
		t.Errorf("text %q", text)
	}
	if text := s.must("plan", "list"); !strings.Contains(text, plans[0].ID) {
		t.Errorf("list %q", text)
	}
	if code, _, _ := s.run("plan", "show", "absent"); code != cli.ExitNotFound {
		t.Errorf("exit %d", code)
	}
}

func TestRequirementGetReturnsTheAPIRecord(t *testing.T) {
	s := newSession(t)
	s.planned()
	want, err := s.daemon.Client.Requirement(t.Context(), "PAY-12")
	if err != nil {
		t.Fatal(err)
	}
	var got planning.Requirement
	if err := json.Unmarshal([]byte(s.must("requirement", "get", "PAY-12", "--json")), &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("the CLI printed %+v, and the API holds %+v", got, want)
	}
	if text := s.must("requirement", "get", "PAY-12"); !strings.Contains(text, "It retries") {
		t.Errorf("text %q", text)
	}
	if code, _, _ := s.run("requirement", "get", "ABSENT"); code != cli.ExitNotFound {
		t.Errorf("exit %d", code)
	}
}

func TestWorkSendBack(t *testing.T) {
	s := newSession(t)
	s.create("wo")
	if code, _, _ := s.run("work", "send-back", "wo"); code != cli.ExitUsage {
		t.Errorf("no reason: exit %d", code)
	}
	s.must("work", "send-back", "wo", "--reason", "The criteria are wrong")
	if text := s.must("work", "show", "wo"); !strings.Contains(text, "planning") {
		t.Errorf("show %q", text)
	}
	if code, _, _ := s.run("work", "send-back", "wo", "--reason", "Again"); code != cli.ExitConflict {
		t.Errorf("exit %d", code)
	}
}

// Each kind reads its own versions. A kind with no read of its own used to list the decision policy.
func TestPolicyListReadsTheKindItNames(t *testing.T) {
	s := newSession(t)
	for _, kind := range []authority.PolicyKind{authority.PolicyDecision, authority.PolicyLane, authority.PolicyDelegation, authority.PolicyAutonomy, authority.PolicyNotify} {
		var list api.PolicyList
		if err := json.Unmarshal([]byte(s.must("policy", "list", "--kind", string(kind), "--json")), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Versions) != 1 || list.Versions[0].Kind != kind {
			t.Errorf("--kind %s listed %+v", kind, list.Versions)
		}
		var one authority.PolicyVersion
		if err := json.Unmarshal([]byte(s.must("policy", "show", "1", "--kind", string(kind), "--json")), &one); err != nil {
			t.Fatal(err)
		}
		if one.Kind != kind {
			t.Errorf("--kind %s showed %+v", kind, one)
		}
	}
}

func TestANotifyPolicyPublishesAndHoldsNoSecret(t *testing.T) {
	s := newSession(t)
	path := filepath.Join(t.TempDir(), "notify.toml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("[notify_policy]\nversion = 2\nwebhook_url = \"https://hooks.example.com/r?token=hunter2\"\n")
	if code, _, _ := s.run("policy", "publish", "--file", path); code != cli.ExitUsage {
		t.Errorf("a secret in the address: exit %d", code)
	}
	write("[notify_policy]\nversion = 2\ndesktop = true\nwebhook_channel = \"loud\"\n")
	if code, _, _ := s.run("policy", "publish", "--file", path); code != cli.ExitUsage {
		t.Errorf("a setting nobody reads: exit %d", code)
	}
	write("[notify_policy]\nversion = 2\ndesktop = true\nwebhook_url = \"https://hooks.example.com/r\"\nwebhook_secret_env = \"ROPESHIFT_WEBHOOK_KEY\"\n")
	s.must("policy", "publish", "--file", path)
	if text := s.must("policy", "show", "2", "--kind", "notify", "--json"); !strings.Contains(text, "ROPESHIFT_WEBHOOK_KEY") {
		t.Errorf("show %q", text)
	}
	if text := s.must("notice", "list"); text != "" {
		t.Errorf("no tick ran, and the list reads %q", text)
	}
}

func TestHealthSaysWhereTheLedgerStands(t *testing.T) {
	s := newSession(t)
	if text := s.must("health"); !strings.HasPrefix(text, "ok\trevision ") {
		t.Errorf("text %q", text)
	}
	var health api.Health
	if err := json.Unmarshal([]byte(s.must("health", "--json")), &health); err != nil {
		t.Fatal(err)
	}
	if health.Status != "ok" || health.Revision == 0 {
		t.Errorf("health %+v", health)
	}
	if code, _, _ := s.run("health", "extra"); code != cli.ExitUsage {
		t.Errorf("an argument: exit %d", code)
	}
}

func TestWorkWriteGoesUnderAFence(t *testing.T) {
	s := newSession(t)
	s.claimed("wo")
	fence := s.fenceOf("wo")
	if code, _, _ := s.run("work", "write", "wo", "--claim-id", "claim-wo"); code != cli.ExitUsage {
		t.Errorf("no fence: exit %d", code)
	}
	args := []string{"work", "write", "wo", "--claim-id", "claim-wo", "--fence", fence, "--write-id", "write-1", "--payload", "a note"}
	var first api.WriteResponse
	if err := json.Unmarshal([]byte(s.must(append(args, "--json")...)), &first); err != nil {
		t.Fatal(err)
	}
	var again api.WriteResponse
	if err := json.Unmarshal([]byte(s.must(append(args, "--json")...)), &again); err != nil {
		t.Fatal(err)
	}
	if first.ID != "write-1" || first != again {
		t.Errorf("a retry wrote again: %+v, %+v", first, again)
	}
	stale := []string{"work", "write", "wo", "--claim-id", "claim-wo", "--fence", "99", "--write-id", "write-2", "--payload", "late"}
	if code, _, _ := s.run(stale...); code != cli.ExitFenced {
		t.Errorf("a stale fence: exit %d", code)
	}
}

func TestWorkCancel(t *testing.T) {
	s := newSession(t)
	s.ready("wo")
	if code, _, _ := s.run("work", "cancel", "wo"); code != cli.ExitUsage {
		t.Errorf("no reason: exit %d", code)
	}
	if text := s.must("work", "cancel", "wo", "--reason", "The plan changed"); text != "wo\tcanceled\n" {
		t.Errorf("text %q", text)
	}
	if text := s.must("work", "show", "wo"); !strings.Contains(text, "canceled") {
		t.Errorf("show %q", text)
	}
	var answer api.Accepted
	if err := json.Unmarshal([]byte(s.must("work", "cancel", "wo", "--reason", "A retry", "--json")), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.State != workorders.Canceled {
		t.Errorf("answer %+v", answer)
	}
	if code, _, _ := s.run("work", "cancel", "no-such-work", "--reason", "Gone"); code != cli.ExitNotFound {
		t.Errorf("unknown work: exit %d", code)
	}
	s.completed("finished")
	s.must("work", "accept", "finished")
	if code, _, stderr := s.run("work", "cancel", "finished", "--reason", "Too late"); code != cli.ExitConflict {
		t.Errorf("finished work: exit %d, %s", code, stderr)
	}
	s.ready("held")
	if code, _, _ := run(t, s.daemon.Socket, "work", "cancel", "held", "--reason", "No session"); code != cli.ExitForbidden {
		t.Errorf("no session: exit %d", code)
	}
}

// A work order in any other state refuses, and the route that refuses is what this test holds: the
// contract suite walks blocked work through both edges.
func TestWorkResolveAndReplanReachTheLedger(t *testing.T) {
	s := newSession(t)
	s.create("wo")
	for _, command := range []string{"resolve", "replan"} {
		if code, _, _ := s.run("work", command, "wo"); code != cli.ExitUsage {
			t.Errorf("%s with no reason: exit %d", command, code)
		}
		if code, _, stderr := s.run("work", command, "wo", "--reason", "It works now"); code != cli.ExitConflict {
			t.Errorf("%s of work that is not blocked: exit %d, %s", command, code, stderr)
		}
	}
}

func TestDocumentCommands(t *testing.T) {
	knowledge := fake.NewKnowledgeSource()
	knowledge.Write("design.md", "# One\n")
	knowledge.Write("design.md", "# Two\n")
	daemon := apitest.StartWith(t, apitest.Options{Knowledge: []ports.KnowledgeSource{knowledge}})
	s := &session{t: t, daemon: daemon}
	if text := s.must("document", "list"); !strings.Contains(text, "design.md") || !strings.Contains(text, "Two") {
		t.Errorf("list %q", text)
	}
	if text := s.must("document", "show", "design.md"); text != "# Two\n" {
		t.Errorf("show %q", text)
	}
	versions := strings.Fields(s.must("document", "versions", "design.md"))
	if len(versions) != 4 {
		t.Fatalf("versions %v", versions)
	}
	if text := s.must("document", "show", "design.md", "--version", versions[2]); text != "# One\n" {
		t.Errorf("earlier %q", text)
	}
	if code, _, _ := s.run("document", "show", "absent.md"); code != cli.ExitNotFound {
		t.Errorf("exit %d", code)
	}
}

func TestQuestionCommands(t *testing.T) {
	s := newSession(t)
	s.must("category", "add", "pricing")
	if text := s.must("category", "list"); !strings.Contains(text, "pricing") || !strings.Contains(text, "security") {
		t.Errorf("categories %q", text)
	}
	s.must("question", "record", "--id", "q1", "--text", "What is the retry budget?", "--category", "pricing", "--subject", "alpha")
	s.must("question", "record", "--id", "q2", "--text", "Where does audit data live?", "--category", "data")
	for _, tt := range []struct {
		args []string
		want string
		not  string
	}{
		{[]string{"--category", "pricing"}, "q1", "q2"},
		{[]string{"--status", "open", "--scope", "alpha"}, "q1", "q2"},
		{[]string{"--text", "audit"}, "q2", "q1"},
	} {
		text := s.must(append([]string{"question", "list"}, tt.args...)...)
		if !strings.Contains(text, tt.want) || strings.Contains(text, tt.not) {
			t.Errorf("%v: %q", tt.args, text)
		}
	}
	var want, got questions.Question
	want, err := s.daemon.Client.Question(t.Context(), "q1")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(s.must("question", "show", "q1", "--json")), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the CLI printed %+v, and the API holds %+v", got, want)
	}
	s.must("question", "dismiss", "q2")
	if code, _, _ := s.run("question", "record", "--id", "q3", "--text", "A", "--category", "unknown"); code != cli.ExitUsage {
		t.Errorf("an unknown category: exit %d", code)
	}
	if code, _, _ := s.run("question", "show", "absent"); code != cli.ExitNotFound {
		t.Errorf("exit %d", code)
	}
}

func TestDecisionAndSearchCommands(t *testing.T) {
	s := newSession(t)
	s.must("decision", "record", "--id", "d1", "--statement", "The retry budget is three calls.", "--rationale", "It follows.",
		"--scope", "checkout", "--category", "product", "--citation", "PAY-12", "--reversibility", "reversible")
	if text := s.must("decision", "list", "--scope", "checkout", "--text", "retry"); !strings.Contains(text, "d1") {
		t.Errorf("list %q", text)
	}
	if text := s.must("decision", "show", "d1"); !strings.Contains(text, "three calls") {
		t.Errorf("show %q", text)
	}
	if text := s.must("search", "retry", "budget", "--kind", "decision"); !strings.Contains(text, "d1") {
		t.Errorf("search %q", text)
	}
	if code, _, _ := s.run("decision", "record", "--id", "d2"); code != cli.ExitUsage {
		t.Errorf("no statement: exit %d", code)
	}
}

// asked counts each prompt a command shows, and answers with one passphrase.
type asked struct {
	passphrase string
	prompts    []string
}

func (a *asked) read(prompt string) (string, error) {
	a.prompts = append(a.prompts, prompt)
	return a.passphrase, nil
}

// runAs runs one command in a shell with this session and this terminal.
func (s *session) runAs(token string, terminal func(string) (string, error), args ...string) (int, string, string) {
	s.t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr, cli.Options{Socket: s.daemon.Socket, Session: token, ReadPassphrase: terminal})
	return code, stdout.String(), stderr.String()
}

func TestLoginPrintsOneExportForEval(t *testing.T) {
	s := newSession(t)
	terminal := &asked{passphrase: apitest.Passphrase}
	code, stdout, stderr := s.runAs("", terminal.read, "login")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	token, found := strings.CutPrefix(strings.TrimSpace(stdout), "export "+authority.EnvMemberToken+"=")
	if !found || strings.ContainsAny(token, " '\"\n") || token == "" {
		t.Fatalf("stdout %q cannot go through eval", stdout)
	}
	if code, _, stderr := s.runAs(token, nil, "category", "add", "pricing"); code != cli.ExitOK {
		t.Fatalf("the session it printed: exit %d, %s", code, stderr)
	}
}

func TestAChangeAsksOnceAndARefusalExplainsItself(t *testing.T) {
	s := newSession(t)
	terminal := &asked{passphrase: apitest.Passphrase}
	if code, _, stderr := s.runAs("", terminal.read, "category", "add", "pricing"); code != cli.ExitOK {
		t.Fatalf("exit %d, %s", code, stderr)
	}
	if len(terminal.prompts) != 1 {
		t.Fatalf("asked %v", terminal.prompts)
	}
	if code, _, _ := s.runAs("", terminal.read, "category", "list"); code != cli.ExitOK || len(terminal.prompts) != 1 {
		t.Fatalf("a read asked: exit %d, prompts %v", code, terminal.prompts)
	}
	wrong := &asked{passphrase: "not the passphrase"}
	if code, _, stderr := s.runAs("", wrong.read, "category", "add", "other"); code != cli.ExitForbidden || !strings.Contains(stderr, "does not match") {
		t.Fatalf("a wrong passphrase: exit %d, %s", code, stderr)
	}
	code, _, stderr := s.runAs("", nil, "category", "add", "other")
	if code != cli.ExitForbidden || !strings.Contains(stderr, "ropeshift login") {
		t.Fatalf("no terminal: exit %d, %s", code, stderr)
	}
	code, _, stderr = s.runAs("", func(string) (string, error) { return "", errors.New("no tty") }, "category", "add", "other")
	if code != cli.ExitForbidden || !strings.Contains(stderr, "no tty") {
		t.Fatalf("a terminal that fails: exit %d, %s", code, stderr)
	}
}

func TestAnExpiredSessionAsksAgainOnATerminal(t *testing.T) {
	s := newSession(t)
	stale, err := s.daemon.Client.SignIn(s.t.Context(), api.SignIn{Passphrase: apitest.Passphrase, TTLMs: 1})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if code, _, stderr := s.runAs(stale.Token, nil, "category", "add", "pricing"); code != cli.ExitForbidden {
		t.Fatalf("no terminal: exit %d, %s", code, stderr)
	}
	terminal := &asked{passphrase: apitest.Passphrase}
	if code, _, stderr := s.runAs(stale.Token, terminal.read, "category", "add", "pricing"); code != cli.ExitOK || len(terminal.prompts) != 1 {
		t.Fatalf("exit %d, %s, prompts %v", code, stderr, terminal.prompts)
	}
}

func TestPassphraseSetNeedsTwoThatMatch(t *testing.T) {
	daemon := apitest.StartWith(t, apitest.Options{NoMember: true})
	s := &session{t: t, daemon: daemon}
	answers := []string{apitest.Passphrase, "something else"}
	typed := func(string) (string, error) {
		answer := answers[0]
		answers = answers[1:]
		return answer, nil
	}
	if code, _, stderr := s.runAs("", typed, "passphrase", "set"); code != cli.ExitUsage || !strings.Contains(stderr, "differ") {
		t.Fatalf("exit %d, %s", code, stderr)
	}
	same := &asked{passphrase: apitest.Passphrase}
	if code, stdout, stderr := s.runAs("", same.read, "passphrase", "set"); code != cli.ExitOK || !strings.Contains(stdout, "set") {
		t.Fatalf("exit %d, %s", code, stderr)
	}
	if code, _, stderr := s.runAs("", same.read, "login"); code != cli.ExitOK {
		t.Fatalf("exit %d, %s", code, stderr)
	}
}

// deliveredBatch takes one question past the gate and delivers its batch, which is the
// ledger's own path, because a person's commands stop at the batch.
func (s *session) deliveredBatch(id, category string) {
	s.t.Helper()
	ctx := s.t.Context()
	s.must("question", "record", "--id", id, "--text", "What is the retry budget?", "--category", category)
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}
	use := control.NewQuestions(s.daemon.Store, questions.BatchPolicy{WindowMs: 1})
	if _, err := use.Take(ctx, apitest.Scope, id, 60_000, orchestrator); err != nil {
		s.t.Fatal(err)
	}
	for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
		step := questions.Step{Rung: rung, Candidates: []string{"decision-1"}, Outcome: "no match"}
		if _, err := use.Climb(ctx, apitest.Scope, id, step, orchestrator); err != nil {
			s.t.Fatal(err)
		}
	}
	escalation := questions.Escalation{Reason: questions.ReasonScopeChange, Options: []string{"Keep", "Change"}, Recommendation: "Keep"}
	if _, err := use.Escalate(ctx, apitest.Scope, id, escalation, orchestrator); err != nil {
		s.t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	delivered, err := use.DeliverBatches(ctx, apitest.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "test"})
	if err != nil || len(delivered) != 1 {
		s.t.Fatalf("delivered %v: %v", delivered, err)
	}
}

func TestPolicyCommands(t *testing.T) {
	s := newSession(t)
	if text := s.must("policy", "list"); !strings.Contains(text, "1\tbuilt-in\tsha256:") {
		t.Fatalf("policy list %q", text)
	}
	if text := s.must("policy", "show", "1"); text != questions.BuiltInPolicyBody {
		t.Fatalf("policy show %q", text)
	}
	file := filepath.Join(t.TempDir(), "decision-policy.toml")
	body := strings.Replace(questions.BuiltInPolicyBody, "version = 1", "version = 2", 1)
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if text := s.must("policy", "publish", "--file", file); !strings.HasPrefix(text, "2\tmember") {
		t.Fatalf("policy publish %q", text)
	}
	if text := s.must("policy", "show", "2"); text != body {
		t.Fatalf("policy show %q", text)
	}
	if code, _, _ := s.run("policy", "show", "9"); code != cli.ExitNotFound {
		t.Fatalf("exit %d for a version nobody wrote", code)
	}
	if code, _, _ := s.run("policy", "publish"); code != cli.ExitUsage {
		t.Fatalf("exit %d with no file", code)
	}
}

// A decision says what it read, so a person cannot approve a request they have not looked at.
func TestADecisionNeedsItsDigest(t *testing.T) {
	s := newSession(t)
	for _, verb := range []string{"approve", "deny"} {
		code, _, stderr := s.run("approval", verb, "a1")
		if code != cli.ExitUsage || !strings.Contains(stderr, "--digest") {
			t.Errorf("%s: exit %d: %s", verb, code, stderr)
		}
	}
}

func TestDelegationPolicyCommands(t *testing.T) {
	s := newSession(t)
	if text := s.must("policy", "show", "--kind", "delegation", "1"); text != authority.BuiltInDelegationBody {
		t.Fatalf("policy show %q", text)
	}
	file := filepath.Join(t.TempDir(), "delegation.toml")
	body := "[delegation_policy]\nversion = 2\n\n[[delegation_policy.classes]]\ntool = \"Bash\"\ncommand_prefix = \"go test\"\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if text := s.must("policy", "publish", "--file", file); !strings.HasPrefix(text, "2\tmember") {
		t.Fatalf("policy publish %q", text)
	}
	if text := s.must("policy", "list", "--kind", "delegation"); !strings.HasPrefix(text, "2\tmember") {
		t.Fatalf("policy list %q", text)
	}
	if text := s.must("policy", "show", "--kind", "delegation", "2"); text != body {
		t.Fatalf("policy show %q", text)
	}
}

func TestLanePolicyCommands(t *testing.T) {
	s := newSession(t)
	if text := s.must("policy", "list", "--kind", "lane"); !strings.Contains(text, "1\tbuilt-in\tsha256:") {
		t.Fatalf("policy list %q", text)
	}
	if text := s.must("policy", "show", "--kind", "lane", "1"); text != authority.BuiltInLanePolicyBody {
		t.Fatalf("policy show %q", text)
	}
	file := filepath.Join(t.TempDir(), "lane-policy.toml")
	body := strings.Replace(authority.BuiltInLanePolicyBody, "version = 1", "version = 2", 1)
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if text := s.must("policy", "publish", "--file", file); !strings.HasPrefix(text, "2\tmember") {
		t.Fatalf("policy publish %q", text)
	}
	if text := s.must("policy", "show", "--kind", "lane", "2"); text != body {
		t.Fatalf("policy show %q", text)
	}
	if text := s.must("policy", "list"); strings.Contains(text, "2\tmember") {
		t.Fatalf("the decision policy lists %q", text)
	}
	if text := s.must("policy", "list", "--kind", "autonomy"); !strings.Contains(text, "1\tbuilt-in\tsha256:") {
		t.Fatalf("autonomy lists %q", text)
	}
	if code, _, _ := s.run("policy", "list", "--kind", "takeover"); code != cli.ExitUsage {
		t.Fatalf("exit %d for a kind nobody named", code)
	}
}

func TestDecisionOverturn(t *testing.T) {
	s := newSession(t)
	s.must("decision", "record", "--id", "d1", "--statement", "Retries log at info.", "--assumed", "--scope", "checkout")
	second := s.must("decision", "record", "--id", "d2", "--statement", "Retries emit a metric.", "--scope", "checkout",
		"--no-conflict", "d1", "--json")
	if !strings.Contains(second, `"overlaps":["d1"]`) || !strings.Contains(second, `"no_conflict":["d1"]`) {
		t.Fatalf("decision record %q", second)
	}
	text := s.must("decision", "overturn", "d1", "--id", "d3", "--statement", "Retries log at warn.")
	for _, want := range []string{"d1\toverturned", "d3\tdecided"} {
		if !strings.Contains(text, want) {
			t.Errorf("overturn %q lacks %q", text, want)
		}
	}
	if text := s.must("decision", "show", "d1"); !strings.Contains(text, "overturned\tcheckout\tRetries log at info.") {
		t.Errorf("the overturned decision reads %q", text)
	}
	if code, _, _ := s.run("decision", "overturn", "d1", "--statement", "Again."); code != cli.ExitConflict {
		t.Errorf("exit %d for a second overturn", code)
	}
	if code, _, _ := s.run("decision", "overturn", "d2"); code != cli.ExitUsage {
		t.Errorf("exit %d with no statement", code)
	}
	s.must("decision", "record", "--id", "d4", "--statement", "Replaced.", "--scope", "other")
	s.must("decision", "record", "--id", "d5", "--statement", "Replacing.", "--scope", "other", "--supersedes", "d4")
	if text := s.must("decision", "show", "d4"); !strings.Contains(text, "replaced") {
		t.Errorf("the replaced decision reads %q", text)
	}
}

func TestQuestionBatches(t *testing.T) {
	s := newSession(t)
	if text := s.must("question", "batches"); text != "" {
		t.Fatalf("nothing reached anyone, and it printed %q", text)
	}
	s.deliveredBatch("q1", "security")
	text := s.must("question", "batches")
	for _, want := range []string{"q1", "security", "What is the retry budget?", "Keep | Change", "recommendation: Keep", "decision-1"} {
		if !strings.Contains(text, want) {
			t.Errorf("batches %q lacks %q", text, want)
		}
	}
	if text := s.must("question", "batches", "--category", "product"); text != "" {
		t.Errorf("another category shows %q", text)
	}
	var list api.BatchList
	if err := json.Unmarshal([]byte(s.must("question", "batches", "--json", "--category", "security")), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Batches) != 1 || len(list.Batches[0].Items) != 1 || list.Batches[0].Items[0].Recommendation != "Keep" {
		t.Fatalf("list %+v", list)
	}
}

func TestQuestionAnswer(t *testing.T) {
	s := newSession(t)
	s.must("question", "record", "--id", "q1", "--text", "What is the retry budget?")
	if code, _, stderr := s.run("question", "answer", "q1"); code != cli.ExitUsage || !strings.Contains(stderr, "--answer") {
		t.Fatalf("no answer: exit %d, %s", code, stderr)
	}
	if code, _, _ := s.run("question", "answer", "--answer", "Two."); code != cli.ExitUsage {
		t.Fatalf("no question: exit %d", code)
	}
	if code, _, _ := s.run("question", "answer", "missing", "--answer", "Two."); code != cli.ExitNotFound {
		t.Fatalf("an unknown question: exit %d", code)
	}
	if text := s.must("question", "answer", "q1", "--answer", "Two retries.", "--id", "d1", "--citation", "PAY-12"); !strings.Contains(text, "d1") {
		t.Fatalf("printed %q", text)
	}
	if code, _, _ := s.run("question", "answer", "q1", "--answer", "Three."); code != cli.ExitConflict {
		t.Fatalf("a second answer: exit %d", code)
	}
	if text := s.must("question", "answer", "q1", "--answer", "Two retries.", "--id", "d1"); !strings.Contains(text, "d1") {
		t.Fatalf("a retry printed %q", text)
	}
	if code, _, _ := s.runAs("", nil, "question", "answer", "q1", "--answer", "Three."); code != cli.ExitForbidden {
		t.Fatalf("no session: exit %d", code)
	}
}
