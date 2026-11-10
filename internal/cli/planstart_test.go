package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// installation is a control plane with one repository, one online worker, and a member who may do
// everything `plan start` asks of them, answering as the real one would.
type installation struct {
	*plane
	rows *lanes
	held *panes

	mu      sync.Mutex
	sources []planning.SourceBinding
	workers []api.WorkerLine
	review  api.ReviewResult
	order   workorders.State
	// laneAppears is how many reads of the new lane answer not found before its first report.
	laneAppears int
	laneReads   int
	// opens is whether the worker opens the lane's pane once it reports.
	opens bool
	// web is the web UI's address, as this machine names it.
	web string
}

func newInstallation(t *testing.T) *installation {
	t.Helper()
	i := &installation{
		plane: newPlane(t), rows: &lanes{}, held: &panes{},
		sources: []planning.SourceBinding{{ID: "repofiles:shop", Adapter: "repofiles", Instance: "shop", Health: "healthy"}},
		workers: []api.WorkerLine{{Worker: ledger.Worker{ID: "worker-a", Name: "mint"}, Freshness: ledger.FreshnessObserved}},
		review:  api.ReviewResult{State: workorders.Ready},
		order:   workorders.Running,
		opens:   true,
	}
	i.on("GET /v1/sources", func(*http.Request, []byte) (any, *api.Error) {
		i.mu.Lock()
		defer i.mu.Unlock()
		return api.SourceList{Sources: i.sources}, nil
	})
	i.on("GET /v1/workers", func(*http.Request, []byte) (any, *api.Error) {
		i.mu.Lock()
		defer i.mu.Unlock()
		list := api.WorkerList{Workers: []ledger.Worker{}}
		for _, line := range i.workers {
			list.Workers = append(list.Workers, line.Worker)
		}
		return list, nil
	})
	i.on("GET /v1/views/shop-floor", func(*http.Request, []byte) (any, *api.Error) {
		i.mu.Lock()
		defer i.mu.Unlock()
		return api.ShopFloor{Workers: i.workers}, nil
	})
	i.on("POST /v1/work-orders", func(_ *http.Request, body []byte) (any, *api.Error) {
		var in api.CreateWorkOrder
		_ = json.Unmarshal(body, &in)
		return workorders.WorkOrder{ID: in.ID, Kind: in.Kind, Title: in.Title, State: workorders.ContractReview}, nil
	})
	i.on("POST /v1/work-orders/{id}/review", func(*http.Request, []byte) (any, *api.Error) {
		i.mu.Lock()
		defer i.mu.Unlock()
		return i.review, nil
	})
	i.on("POST /v1/work-orders/{id}/claims", func(r *http.Request, body []byte) (any, *api.Error) {
		var in api.ClaimRequest
		_ = json.Unmarshal(body, &in)
		return api.Grant{WorkOrder: r.PathValue("id"), ClaimID: in.ClaimID, Fence: 1}, nil
	})
	i.on("POST /v1/work-orders/{id}/lanes", func(r *http.Request, body []byte) (any, *api.Error) {
		var in api.StartLane
		_ = json.Unmarshal(body, &in)
		return api.LaneDispatched{LaneID: in.LaneID, WorkOrder: r.PathValue("id"), Fence: 1}, nil
	})
	i.on("GET /v1/work-orders/{id}", func(r *http.Request, _ []byte) (any, *api.Error) {
		i.mu.Lock()
		defer i.mu.Unlock()
		return workorders.WorkOrder{ID: r.PathValue("id"), Kind: workorders.KindPlan, State: i.order}, nil
	})
	i.on("GET /v1/lanes/{id}", func(r *http.Request, _ []byte) (any, *api.Error) {
		i.mu.Lock()
		i.laneReads++
		reads, appears := i.laneReads, i.laneAppears
		i.mu.Unlock()
		if reads <= appears {
			return nil, notFound("lane " + r.PathValue("id"))
		}
		id := r.PathValue("id")
		i.rows.set(id, "working", 1)
		if i.opens {
			i.held.hold(id, 1, true)
		}
		i.rows.mu.Lock()
		defer i.rows.mu.Unlock()
		return i.rows.rows[id], nil
	})
	return i
}

// start runs `plan start` as a person at a terminal, or with none.
func (i *installation) start(t *testing.T, terminal bool, args ...string) (int, string, string) {
	t.Helper()
	t.Cleanup(cli.PaneTiming(2*time.Millisecond, 400*time.Millisecond))
	t.Cleanup(cli.FollowTiming(2*time.Millisecond, 100*time.Millisecond))
	var stdout, stderr bytes.Buffer
	opts := cli.Options{Socket: i.Socket, Panes: i.held, WebAddress: i.web}
	if terminal {
		opts.Terminal = strings.NewReader("")
	}
	code := cli.Run(append([]string{"plan", "start"}, args...), &stdout, &stderr, opts)
	return code, stdout.String(), stderr.String()
}

func (i *installation) created(t *testing.T) api.CreateWorkOrder {
	t.Helper()
	bodies := i.bodies("POST", "/v1/work-orders")
	if len(bodies) != 1 {
		t.Fatalf("%d work orders created: %v", len(bodies), i.reads())
	}
	var in api.CreateWorkOrder
	if err := json.Unmarshal([]byte(bodies[0]), &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func (i *installation) wrote() []string {
	var out []string
	for _, call := range i.reads() {
		if strings.HasPrefix(call, "POST ") {
			out = append(out, call)
		}
	}
	return out
}

// Every step is one the member could take by hand, in the order the state machine allows, so the
// ledger keeps who did what.
func TestPlanStartChainsTheMemberCallsAndAttaches(t *testing.T) {
	i := newInstallation(t)
	code, stdout, stderr := i.start(t, true, "--goal", "Build a billing portal\nwith invoices and refunds")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	order := i.created(t)
	if order.Kind != workorders.KindPlan || order.Title != "Build a billing portal" || order.RepositoryID != "shop" ||
		order.Statement != "Build a billing portal\nwith invoices and refunds" || len(order.Criteria) != 1 || order.ID == "" {
		t.Errorf("work order %+v", order)
	}
	wrote := i.wrote()
	want := []string{
		"POST /v1/work-orders", "POST /v1/work-orders/" + order.ID + "/review",
		"POST /v1/work-orders/" + order.ID + "/claims", "POST /v1/work-orders/" + order.ID + "/lanes",
	}
	if !slices.Equal(wrote, want) {
		t.Errorf("writes\n got %v\nwant %v", wrote, want)
	}
	var claim api.ClaimRequest
	if err := json.Unmarshal([]byte(i.bodies("POST", "/v1/work-orders/"+order.ID+"/claims")[0]), &claim); err != nil || claim.WorkerID != "worker-a" || claim.ClaimID == "" {
		t.Errorf("claim %+v: %v", claim, err)
	}
	var lane api.StartLane
	if err := json.Unmarshal([]byte(i.bodies("POST", "/v1/work-orders/"+order.ID+"/lanes")[0]), &lane); err != nil || lane.LaneID == "" {
		t.Fatalf("lane %+v: %v", lane, err)
	}
	if got := i.held.attachments(); !slices.Equal(got, []string{lane.LaneID + "-g1"}) {
		t.Errorf("attached to %v, want the new lane's first pane", got)
	}
	for _, step := range []string{"Work order " + order.ID, "review", "worker-a", "lane " + lane.LaneID} {
		if !strings.Contains(stdout, step) {
			t.Errorf("no word of %q in the steps:\n%s", step, stdout)
		}
	}
}

func TestPlanStartWaitsForThePaneBeforeAttaching(t *testing.T) {
	i := newInstallation(t)
	i.laneAppears = 3
	code, _, stderr := i.start(t, true, "--goal", "Build a billing portal")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if i.laneReads <= 3 {
		t.Errorf("the lane was read %d times, and attached to before it reported", i.laneReads)
	}
	if got := i.held.attachments(); len(got) != 1 {
		t.Errorf("attached to %v", got)
	}
}

// Without a terminal there is nothing to attach, and with --no-attach the person chose none. Either
// way the session runs, and the command says how to join it.
func TestPlanStartWithNothingToAttachNamesTheCommand(t *testing.T) {
	tests := []struct {
		name     string
		terminal bool
		args     []string
	}{
		{"no terminal", false, nil},
		{"--no-attach", true, []string{"--no-attach"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := newInstallation(t)
			code, stdout, stderr := i.start(t, tt.terminal, append([]string{"--goal", "Build a billing portal"}, tt.args...)...)
			if code != cli.ExitOK {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			var lane api.StartLane
			order := i.created(t)
			if err := json.Unmarshal([]byte(i.bodies("POST", "/v1/work-orders/"+order.ID+"/lanes")[0]), &lane); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stdout, "ropeshift lane attach "+lane.LaneID) {
				t.Errorf("no command to join the session:\n%s", stdout)
			}
			if got := i.held.attachments(); len(got) != 0 {
				t.Errorf("attached to %v", got)
			}
			if reads := i.bodies("GET", "/v1/lanes/"+lane.LaneID); len(reads) != 0 {
				t.Errorf("waited for a pane nobody will attach to")
			}
		})
	}
}

// The web UI shows the same pane, so a session nobody attached to names its page there too, and a
// machine whose web UI is off names none.
func TestPlanStartNamesTheSessionsPageInTheWebUI(t *testing.T) {
	i := newInstallation(t)
	i.web = "127.0.0.1:7317"
	code, stdout, stderr := i.start(t, false, "--goal", "Build a billing portal")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var lane api.StartLane
	order := i.created(t)
	if err := json.Unmarshal([]byte(i.bodies("POST", "/v1/work-orders/"+order.ID+"/lanes")[0]), &lane); err != nil {
		t.Fatal(err)
	}
	if want := "http://127.0.0.1:7317/lane?id=" + url.QueryEscape(lane.LaneID); !strings.Contains(stdout, want) {
		t.Errorf("no %s in:\n%s", want, stdout)
	}
	code, stdout, stderr = i.start(t, false, "--goal", "Build a billing portal", "--json")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var out struct {
		Lane string `json:"lane"`
		Web  string `json:"web"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("%v: %q", err, stdout)
	}
	if out.Web != "http://127.0.0.1:7317/lane?id="+url.QueryEscape(out.Lane) {
		t.Errorf("document %+v", out)
	}

	off := newInstallation(t)
	code, stdout, stderr = off.start(t, false, "--goal", "Build a billing portal")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "http://") {
		t.Errorf("a machine with no web UI named a page:\n%s", stdout)
	}
}

func TestPlanStartPrintsOneDocumentWithJSON(t *testing.T) {
	i := newInstallation(t)
	code, stdout, stderr := i.start(t, true, "--goal", "Build a billing portal", "--json")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var out struct {
		WorkOrder string `json:"work_order"`
		Lane      string `json:"lane"`
		Worker    string `json:"worker"`
		Attach    string `json:"attach"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("%v: %q", err, stdout)
	}
	if out.WorkOrder == "" || out.Lane == "" || out.Worker != "worker-a" || out.Attach != "ropeshift lane attach "+out.Lane {
		t.Errorf("document %+v", out)
	}
	if got := i.held.attachments(); len(got) != 0 {
		t.Errorf("a JSON run attached to %v", got)
	}
}

func TestPlanStartReadsTheGoalFromAFile(t *testing.T) {
	i := newInstallation(t)
	path := filepath.Join(t.TempDir(), "goal.md")
	if err := os.WriteFile(path, []byte("\n  Replace the checkout\n\nIt is slow and the team dreads it.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := i.start(t, false, "--goal-file", path); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	order := i.created(t)
	if order.Title != "Replace the checkout" || !strings.Contains(order.Statement, "It is slow and the team dreads it.") {
		t.Errorf("work order %+v", order)
	}
}

func TestPlanStartWithNoGoalLetsThePlannerAsk(t *testing.T) {
	i := newInstallation(t)
	if code, _, stderr := i.start(t, false); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if order := i.created(t); order.Title != "Planning session" || order.Statement != "" {
		t.Errorf("work order %+v", order)
	}
}

func TestPlanStartRefusesAGoalItCannotRead(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"both a goal and a file", []string{"--goal", "x", "--goal-file", "y"}},
		{"a file that is not there", []string{"--goal-file", filepath.Join(t.TempDir(), "nothing.md")}},
		{"a stray argument", []string{"stray"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := newInstallation(t)
			code, _, _ := i.start(t, true, tt.args...)
			if code != cli.ExitUsage {
				t.Errorf("exit %d", code)
			}
			if wrote := i.wrote(); len(wrote) != 0 {
				t.Errorf("wrote %v before the command line was whole", wrote)
			}
		})
	}
}

// The worker is the one the person names, the only one registered, or the only one of several whose
// word is fresh. An idle worker reads as last reported once its session is old, so a lone worker is
// chosen however stale it reads, and a choice the command would have to guess is the person's.
func TestPlanStartChoosesTheWorker(t *testing.T) {
	line := func(id, name, freshness string) api.WorkerLine {
		return api.WorkerLine{Worker: ledger.Worker{ID: id, Name: name}, Freshness: freshness}
	}
	observed, reported := ledger.FreshnessObserved, ledger.FreshnessLastReported
	tests := []struct {
		name    string
		workers []api.WorkerLine
		args    []string
		want    int
		worker  string
		stderr  string
	}{
		{"the only worker, which an idle one reads as last reported", []api.WorkerLine{line("worker-a", "mint", reported)}, nil, cli.ExitOK, "worker-a", ""},
		{"the only worker, fresh", []api.WorkerLine{line("worker-a", "mint", observed)}, nil, cli.ExitOK, "worker-a", ""},
		{"one of two that is fresh", []api.WorkerLine{line("worker-a", "mint", reported), line("worker-b", "dev", observed)}, nil, cli.ExitOK, "worker-b", ""},
		{"a worker the person names", []api.WorkerLine{line("worker-a", "mint", observed), line("worker-b", "dev", observed)}, []string{"--worker", "worker-b"}, cli.ExitOK, "worker-b", ""},
		{"a worker named that nothing lists", nil, []string{"--worker", "worker-x"}, cli.ExitOK, "worker-x", ""},
		{"two fresh", []api.WorkerLine{line("worker-a", "mint", observed), line("worker-b", "dev", observed)}, nil, cli.ExitUsage, "", "worker-b (dev, observed)"},
		{"two, neither fresh", []api.WorkerLine{line("worker-a", "mint", reported), line("worker-b", "dev", reported)}, nil, cli.ExitUsage, "", "worker-a (mint, last_reported)"},
		{"no workers", nil, nil, cli.ExitConflict, "", "no worker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := newInstallation(t)
			i.workers = tt.workers
			code, _, stderr := i.start(t, false, append([]string{"--goal", "x"}, tt.args...)...)
			if code != tt.want || !strings.Contains(stderr, tt.stderr) {
				t.Fatalf("exit %d (want %d): %s", code, tt.want, stderr)
			}
			if tt.want != cli.ExitOK {
				if wrote := i.wrote(); len(wrote) != 0 {
					t.Errorf("wrote %v with no worker to give the work", wrote)
				}
				return
			}
			var claim api.ClaimRequest
			order := i.created(t)
			if err := json.Unmarshal([]byte(i.bodies("POST", "/v1/work-orders/"+order.ID+"/claims")[0]), &claim); err != nil || claim.WorkerID != tt.worker {
				t.Errorf("claimed on %q (%v), want %q", claim.WorkerID, err, tt.worker)
			}
		})
	}
}

func TestPlanStartChoosesTheRepository(t *testing.T) {
	shop := planning.SourceBinding{ID: "repofiles:shop", Adapter: "repofiles", Instance: "shop", Health: "healthy"}
	blog := planning.SourceBinding{ID: "repofiles:blog", Adapter: "repofiles", Instance: "blog", Health: "healthy"}
	tests := []struct {
		name    string
		sources []planning.SourceBinding
		args    []string
		want    int
		repo    string
		stderr  string
	}{
		{"the only source", []planning.SourceBinding{shop}, nil, cli.ExitOK, "shop", ""},
		{"a repository named by its instance", []planning.SourceBinding{shop, blog}, []string{"--repository", "blog"}, cli.ExitOK, "blog", ""},
		{"a repository named by its source ID", []planning.SourceBinding{shop, blog}, []string{"--repository", "repofiles:blog"}, cli.ExitOK, "blog", ""},
		{"two sources and no choice", []planning.SourceBinding{shop, blog}, nil, cli.ExitUsage, "", "blog"},
		{"a repository no source has", []planning.SourceBinding{shop}, []string{"--repository", "nowhere"}, cli.ExitUsage, "", "nowhere"},
		{"no source", nil, nil, cli.ExitConflict, "", "source"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := newInstallation(t)
			i.sources = tt.sources
			code, _, stderr := i.start(t, false, append([]string{"--goal", "x"}, tt.args...)...)
			if code != tt.want || !strings.Contains(stderr, tt.stderr) {
				t.Fatalf("exit %d (want %d): %s", code, tt.want, stderr)
			}
			if tt.want != cli.ExitOK {
				if wrote := i.wrote(); len(wrote) != 0 {
					t.Errorf("wrote %v with no repository to plan in", wrote)
				}
				return
			}
			if got := i.created(t).RepositoryID; got != tt.repo {
				t.Errorf("repository %q, want %q", got, tt.repo)
			}
		})
	}
}

// A review that the definition of ready refuses stops the session before anything claims the work, and
// says what holds it back, as `work review` does.
func TestPlanStartStopsWhenTheReviewIsNotReady(t *testing.T) {
	i := newInstallation(t)
	i.review = api.ReviewResult{State: workorders.ContractReview, Failures: []workorders.ReadyFailure{{Check: workorders.CheckQuestions, Detail: "1 blocking questions are open"}}}
	code, stdout, stderr := i.start(t, true, "--goal", "x")
	if code != cli.ExitNotReady {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout+stderr, "blocking questions") {
		t.Errorf("the failure is not named: %q %q", stdout, stderr)
	}
	for _, call := range i.wrote() {
		if strings.HasSuffix(call, "/claims") || strings.HasSuffix(call, "/lanes") {
			t.Errorf("went on past a review that was not ready: %v", i.wrote())
		}
	}
}

// A worker that cannot open the pane parks the work order in a state a person has to read, and waiting
// out the clock on that would hide it.
func TestPlanStartSaysWhenTheWorkOrderStopsBeforeThePane(t *testing.T) {
	i := newInstallation(t)
	i.laneAppears = 1 << 30
	i.order = workorders.Blocked
	code, _, stderr := i.start(t, true, "--goal", "x")
	if code != cli.ExitConflict || !strings.Contains(stderr, "blocked") || !strings.Contains(stderr, "work show") {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

func TestPlanStartGivesUpOnAPaneThatNeverOpens(t *testing.T) {
	i := newInstallation(t)
	i.opens = false
	code, _, stderr := i.start(t, true, "--goal", "x")
	if code != cli.ExitConflict || !strings.Contains(stderr, "lane attach") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	if got := i.held.attachments(); len(got) != 0 {
		t.Errorf("attached to %v", got)
	}
}
