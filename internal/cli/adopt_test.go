package cli_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// The clones these tests place on the worker's machine: a shop under a directory whose name is not a
// project name as it stands, and two libraries in a folder of clones.
var (
	shopClone = ports.Clone{Path: "/work/Shop_App", ID: "5c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5", Remote: "https://example.invalid/acme/shop.git", DefaultBranch: "main"}
	apiClone  = ports.Clone{Path: "/work/libs/api", ID: "6d2e3f405162738495a6b7c8d9e0f1a2b3c4d5e6", DefaultBranch: "main"}
	webClone  = ports.Clone{Path: "/work/libs/web", ID: "7e3f405162738495a6b7c8d9e0f1a2b3c4d5e6f7", DefaultBranch: "main"}
)

// adoption is an installation whose worker's machine holds the shop and the libraries, with a member's
// session, and a terminal that runs in the shop.
type adoption struct {
	t  *testing.T
	in *apitest.Installation
}

func newAdoption(t *testing.T) *adoption {
	t.Helper()
	in := apitest.StartInstallation(t)
	for _, clone := range []ports.Clone{shopClone, apiClone, webClone} {
		in.Checkouts.Place(clone)
	}
	return &adoption{t: t, in: in}
}

// in runs a command in a clone, as a person at a terminal there would, with what they type.
func (a *adoption) runIn(clone ports.Clone, typed string, args ...string) (int, string, string) {
	a.t.Helper()
	opts := cli.Options{
		Socket: a.in.Socket, Session: a.in.Session,
		RepositoryRoot: func(context.Context) (string, error) { return clone.Path, nil },
		RepositoryHere: func(context.Context) (string, error) { return ledger.RepositoryIdentity(clone.Remote, clone.ID), nil },
	}
	if typed != "" {
		opts.Terminal = strings.NewReader(typed)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr, opts)
	return code, stdout.String(), stderr.String()
}

func (a *adoption) projects() map[string]api.Project {
	a.t.Helper()
	list, err := a.in.Client.Projects(a.t.Context())
	if err != nil {
		a.t.Fatal(err)
	}
	out := map[string]api.Project{}
	for _, p := range list {
		out[p.Name] = p
	}
	return out
}

func (a *adoption) repositories(project string) map[string]api.RepositoryView {
	a.t.Helper()
	list, err := a.in.Client.Project(project).Repositories(a.t.Context())
	if err != nil {
		a.t.Fatal(err)
	}
	out := map[string]api.RepositoryView{}
	for _, view := range list.Repositories {
		out[view.Name] = view
	}
	return out
}

func (a *adoption) workOrders(project string) []workorders.WorkOrder {
	a.t.Helper()
	orders, err := a.in.Client.Project(project).WorkOrders(a.t.Context(), "")
	if err != nil {
		a.t.Fatal(err)
	}
	return orders
}

// ADR-0023: adopt, in a repository no project holds, makes a project named after the repository's directory,
// registers the repository, plans in it as the project's only repository, and starts the adopt session: a
// planning work order that adopts, claimed and running, with the command that joins it.
func TestAdoptMakesTheProjectAndStartsTheAdoptSession(t *testing.T) {
	a := newAdoption(t)
	code, stdout, stderr := a.runIn(shopClone, "", "adopt", "--worker", "worker-a", "--no-attach")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, made := a.projects()["shop-app"]; !made {
		t.Fatalf("no project shop-app among %v", a.projects())
	}
	if shop, held := a.repositories("shop-app")["Shop_App"]; !held || shop.ID != shopClone.ID || !shop.PlanOfRecord {
		t.Errorf("the project's repositories are %+v", a.repositories("shop-app"))
	}
	orders := a.workOrders("shop-app")
	if len(orders) != 1 || !orders[0].Adopt || orders[0].Kind != workorders.KindPlan || orders[0].RepositoryID != shopClone.ID || orders[0].ClaimID == "" {
		t.Fatalf("the adopt session's work is %+v", orders)
	}
	for _, want := range []string{"Created project shop-app", "registered\tShop_App", "lane attach "} {
		if !strings.Contains(stdout, want) {
			t.Errorf("adopt printed no %q:\n%s", want, stdout)
		}
	}
}

// A project named with --project takes the repository and every clone under the given paths. With several
// repositories, --planning names the one that holds the plan.
func TestAdoptRegistersTheClonesUnderItsPathsInTheProjectItIsNamed(t *testing.T) {
	a := newAdoption(t)
	code, _, stderr := a.runIn(shopClone, "", "adopt", "/work/libs", "--project", "acme", "--planning", "api", "--worker", "worker-a", "--no-attach")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	held := a.repositories("acme")
	var names []string
	for name := range held {
		names = append(names, name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Shop_App", "api", "web"}) || !held["api"].Planning {
		t.Errorf("acme holds %+v", held)
	}
	if orders := a.workOrders("acme"); len(orders) != 1 || orders[0].RepositoryID != apiClone.ID || !orders[0].Adopt {
		t.Errorf("the adopt session's work is %+v", orders)
	}
}

// A project of several repositories that names none asks the person on the terminal.
func TestAdoptAsksWhichRepositoryHoldsThePlan(t *testing.T) {
	a := newAdoption(t)
	code, stdout, stderr := a.runIn(shopClone, "web\n", "adopt", "/work/libs", "--worker", "worker-a")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Which repository holds the plan?") || !strings.Contains(stdout, "api, Shop_App, web") {
		t.Errorf("adopt asked:\n%s", stdout)
	}
	if !a.repositories("shop-app")["web"].Planning {
		t.Errorf("web is not the planning repository: %+v", a.repositories("shop-app"))
	}
	if orders := a.workOrders("shop-app"); len(orders) != 1 || orders[0].RepositoryID != webClone.ID {
		t.Errorf("the adopt session's work is %+v", orders)
	}
}

// With nobody at a terminal to ask, a project of several repositories that names none stops before any work,
// and says how to name one. Running it again with the name goes on from there.
func TestAdoptWithNobodyToAskNamesTheChoice(t *testing.T) {
	a := newAdoption(t)
	code, _, stderr := a.runIn(shopClone, "", "adopt", "/work/libs", "--worker", "worker-a", "--no-attach")
	if code != cli.ExitUsage || !strings.Contains(stderr, "--planning") || !strings.Contains(stderr, "api, Shop_App, web") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if orders := a.workOrders("shop-app"); len(orders) != 0 {
		t.Fatalf("work was recorded with no planning repository: %+v", orders)
	}
	code, stdout, stderr := a.runIn(shopClone, "", "adopt", "/work/libs", "--planning", "Shop_App", "--worker", "worker-a", "--no-attach")
	if code != cli.ExitOK || !strings.Contains(stdout, "already registered") {
		t.Fatalf("again with --planning: exit %d: %s%s", code, stdout, stderr)
	}
	if orders := a.workOrders("shop-app"); len(orders) != 1 || orders[0].RepositoryID != shopClone.ID {
		t.Errorf("the adopt session's work is %+v", orders)
	}
}

// A repository another project holds is adopted in that project, and naming a different one is refused.
func TestAdoptStaysInTheProjectThatHoldsTheRepository(t *testing.T) {
	a := newAdoption(t)
	if code, _, stderr := a.runIn(shopClone, "", "adopt", "--project", "acme", "--worker", "worker-a", "--no-attach"); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	code, _, stderr := a.runIn(shopClone, "", "adopt", "--project", "other", "--worker", "worker-a", "--no-attach")
	if code != cli.ExitConflict || !strings.Contains(stderr, "acme") {
		t.Errorf("adopting in another project: exit %d: %s", code, stderr)
	}
	if _, made := a.projects()["other"]; made {
		t.Error("a refused adoption made a project")
	}
}

// adopt runs in a repository with a commit, and says so anywhere else.
func TestAdoptOutsideARepositoryIsAUsageError(t *testing.T) {
	a := newAdoption(t)
	code, _, stderr := a.runIn(ports.Clone{}, "", "adopt", "--worker", "worker-a", "--no-attach")
	if code != cli.ExitUsage || !strings.Contains(stderr, "git repository") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	if projects := a.projects(); len(projects) != 1 {
		t.Errorf("adopt outside a repository made projects: %v", projects)
	}
}
