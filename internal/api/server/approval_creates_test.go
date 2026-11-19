package server_test

import (
	"context"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// placingCreator is the fake creator on the daemon's worker's machine: a creation that writes leaves the clone it made
// there, with the origin the check gave, so the registration that follows reads it as it would read the real one.
type placingCreator struct {
	*fake.RepositoryCreator
	machine *fake.Checkouts
	// approval is the plan approval whose creations the creator answers, which its answers are keyed by.
	approval string
	urls     map[string]string
	// gate runs before each creation does, and fails it when it returns an error.
	gate func(context.Context, ledger.RepositoryCreation) error
}

func newPlacingCreator(machine *fake.Checkouts) *placingCreator {
	return &placingCreator{RepositoryCreator: fake.NewRepositoryCreator(), machine: machine, urls: map[string]string{}}
}

// key is the key of the creation of the repository named, which the creator answers by.
func (c *placingCreator) key(name string) string { return ledger.CreationKey(c.approval, name) }

func (c *placingCreator) Create(ctx context.Context, job ledger.RepositoryCreation) (ledger.TemplateWriteReport, error) {
	if c.gate != nil {
		if err := c.gate(ctx, job); err != nil {
			return ledger.TemplateWriteReport{}, err
		}
	}
	report, err := c.RepositoryCreator.Create(ctx, job)
	if err == nil && report.Outcome == ledger.TemplateOutcomeWritten {
		c.machine.Place(ports.Clone{Path: job.Path, ID: "id-" + job.Name, Remote: c.urls[job.Name], DefaultBranch: "main", Directory: path.Base(job.Path)})
	}
	return report, err
}

// passes makes the check of the repository named pass and its creation finish every step.
func (c *placingCreator) passes(name string) {
	url := "git@github.com:acme/" + name + ".git"
	c.urls[name] = url
	c.SetCheck(c.key(name), ledger.CreationCheck{URL: url, Login: "octo", Branch: "main"})
	c.SetReport(c.key(name), ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeWritten, Commit: "1a2b3c4", Clone: true,
		Repositories: []ledger.RepositoryReport{{Name: name, Path: ".", Commit: "1a2b3c4", Remote: url, Steps: []ledger.RemoteStep{
			{Step: ledger.RemoteStepCreate, Outcome: ledger.RemoteStepDone}, {Step: ledger.RemoteStepOrigin, Outcome: ledger.RemoteStepDone}, {Step: ledger.RemoteStepPush, Outcome: ledger.RemoteStepDone},
		}}},
	})
}

// failsPush makes the creation of the repository named stop at its push.
func (c *placingCreator) failsPush(name string) {
	c.passes(name)
	c.SetReport(c.key(name), ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeWritten, Commit: "5d6e7f8", Clone: true,
		Repositories: []ledger.RepositoryReport{{Name: name, Path: ".", Commit: "5d6e7f8", Remote: c.urls[name], Steps: []ledger.RemoteStep{
			{Step: ledger.RemoteStepCreate, Outcome: ledger.RemoteStepDone}, {Step: ledger.RemoteStepOrigin, Outcome: ledger.RemoteStepDone},
			{Step: ledger.RemoteStepPush, Outcome: ledger.RemoteStepFailed, Detail: "remote: Permission denied"},
		}}},
		Remaining: []string{"git -C /work/" + name + " push --set-upstream origin main"},
	})
}

// creatingPlan is a plan whose manifest lists billing, whose clone the machine keeps, and web and docs, which the plan's
// work orders name and approving the version creates.
const creatingManifest = `+++
kind = "repositories"
id = "platform"
plan = "platform-v1"

[[repository]]
name = "billing"
remote = "git@example.invalid:acme/billing.git"
path = "../billing"

[[repository]]
name = "web"
path = "../web"
create = "github:acme"

[[repository]]
name = "docs"
path = "../docs"
create = "github:acme"
visibility = "public"
default_branch = "trunk"
+++
`

const creatingContract = `+++
kind = "contract"
id = "alpha"
title = "Alpha"
plan = "platform-v1"

[[criteria]]
id = "c1"
text = "It works"

[[work_orders]]
id = "alpha-billing"
title = "The billing work"
repo = "billing"

[[work_orders]]
id = "alpha-web"
title = "The web work"
repo = "web"

[[work_orders]]
id = "alpha-docs"
title = "The docs work"
repo = "docs"
+++

The statement.
`

// aVersionThatCreates serves a plan whose first version waits for approval and creates web and docs, on a daemon that
// creates them with the creator the test sets up, and returns the plan's ID.
func aVersionThatCreates(t *testing.T, options apitest.Options, setup func(*placingCreator)) (*apitest.Daemon, *placingCreator, *fake.Checkouts, string) {
	t.Helper()
	var creator *placingCreator
	d, machine := planningMachineWith(t, options, func(machine *fake.Checkouts) ports.RepositoryCreator {
		creator = newPlacingCreator(machine)
		return creator
	})
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(manifestPlanText))
	source.Write("repositories.md", []byte(creatingManifest))
	source.Write("contract.md", []byte(creatingContract))
	if _, err := control.NewIntake(d.Store).Poll(t.Context(), apitest.Scope, source); err != nil {
		t.Fatal(err)
	}
	plans, err := d.Client.Plans(t.Context())
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans %+v: %v", plans, err)
	}
	// The creator answers each creation by its key, which names the plan approval, so it is set up once one is raised and
	// before any read of the plan asks it to check.
	waiting, err := d.Client.Approvals(t.Context(), authority.ApprovalPending)
	if err != nil || len(waiting) != 1 {
		t.Fatalf("approvals %+v: %v", waiting, err)
	}
	creator.approval = waiting[0].ID
	setup(creator)
	return d, creator, machine, plans[0].ID
}

// FR-REPO-16: the plan approval page previews a version that creates a repository with what approving would create,
// and the check that refused another, with the command that fixes it, and creates nothing.
func TestGoldenPlanWithACreatedRepository(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d, creator, _, plan := aVersionThatCreates(t, apitest.Options{Clock: clock.Dialect}, func(creator *placingCreator) {
		creator.passes("web")
		creator.SetCheck(creator.key("docs"), ledger.CreationCheck{Refusal: &ledger.TemplateWriteReport{
			Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedGHSignedOut,
			Detail: "Run `gh auth login --hostname github.com` on this machine", Remedy: "gh auth login --hostname github.com",
		}})
	})
	view, err := d.Client.Plan(t.Context(), plan)
	if err != nil || view.Approval == nil || view.Approval.Registration == nil {
		t.Fatalf("the plan reads %+v: %v", view.Approval, err)
	}
	registration := view.Approval.Registration
	if !registration.Preview || len(registration.Repositories) != 3 {
		t.Fatalf("the version previews %+v", registration)
	}
	byName := map[string]api.RepositoryAdded{}
	for _, one := range registration.Repositories {
		byName[one.Name] = one
	}
	web, docs := byName["web"], byName["docs"]
	if web.Outcome != ledger.Created || web.Remote != "git@github.com:acme/web.git" || web.Creation == nil || web.Creation.Visibility != "private" || web.Creation.Login != "octo" || web.Creation.DefaultBranch != "main" {
		t.Errorf("web previews as %+v with creation %+v", web, web.Creation)
	}
	if docs.Outcome != ledger.RegistrationRefused || docs.Creation == nil || docs.Creation.Remedy != "gh auth login --hostname github.com" || docs.Creation.Visibility != "public" {
		t.Errorf("docs previews as %+v with creation %+v", docs, docs.Creation)
	}
	if created := creator.Created(); len(created) != 0 {
		t.Errorf("reading the plan created %d repositories", len(created))
	}
	if checked := creator.Checked(); len(checked) != 2 || checked[0].Project != apitest.ProjectName {
		t.Errorf("reading the plan asked the creator to check %+v, want web and docs for the project %s", checked, apitest.ProjectName)
	}
	// The ledger lists work orders, and the estimates of them, by the IDs it generated, which differ between runs.
	slices.SortFunc(view.WorkOrders, func(a, b workorders.WorkOrder) int { return strings.Compare(a.Title, b.Title) })
	titles := map[string]string{}
	for _, order := range view.WorkOrders {
		titles[order.ID] = order.Title
	}
	if set := view.Approval.Forecast.Set; set != nil {
		slices.SortFunc(set.Estimates, func(a, b delivery.Estimate) int { return strings.Compare(titles[a.WorkOrder], titles[b.WorkOrder]) })
	}
	steadySet(&view)
	holdsGolden(t, "plan-created", steady(pretty(t, view)))
}

// FR-REPO-16: deciding a version that creates two repositories answers with what became of each: one created and
// registered, one whose push failed, kept, with the steps and the commands that remain, and the approval stands.
func TestGoldenApprovalDecidedThatCreates(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d, creator, machine, plan := aVersionThatCreates(t, apitest.Options{Clock: clock.Dialect}, func(creator *placingCreator) {
		creator.passes("web")
		creator.failsPush("docs")
	})
	view, err := d.Client.Plan(t.Context(), plan)
	if err != nil || view.Approval == nil {
		t.Fatalf("the plan reads %+v: %v", view.Approval, err)
	}
	decided, err := d.Client.DecideApproval(t.Context(), view.Approval.Approval.ID, api.DecideApproval{Digest: view.Approval.Approval.SubjectDigest, Allow: true})
	if err != nil || decided.RepositoriesProblem != "" || len(decided.Repositories) != 3 {
		t.Fatalf("deciding answers %+v: %v", decided, err)
	}
	byName := map[string]api.RepositoryAdded{}
	for _, one := range decided.Repositories {
		byName[one.Name] = one
	}
	web, docs := byName["web"], byName["docs"]
	if web.Outcome != ledger.Created || web.Repository == nil || web.Repository.Name != "web" || web.Creation == nil || web.Creation.Makefile != "web=git@github.com:acme/web.git=../web" {
		t.Errorf("web is %+v with creation %+v", web, web.Creation)
	}
	if docs.Outcome != ledger.CreateFailed || docs.Repository != nil || docs.Creation == nil || !slices.Equal(docs.Creation.Remaining, []string{"git -C /work/docs push --set-upstream origin main"}) ||
		docs.Creation.Steps[2].Outcome != ledger.RemoteStepFailed || docs.Creation.Makefile != "" {
		t.Errorf("docs is %+v with creation %+v", docs, docs.Creation)
	}
	if held, _ := machine.Of(t.Context(), apitest.Scope); !slices.Contains(mapValues(held), "/work/web") || slices.Contains(mapValues(held), "/work/docs") {
		t.Errorf("the worker keeps the checkouts %v, want web's and not docs'", held)
	}
	if created := creator.Created(); len(created) != 2 || created[0].Project != apitest.ProjectName {
		t.Errorf("approving created %+v", created)
	}
	// The approval stands and keeps what it did: the plan reads it, and deciding again answers the same and creates nothing.
	read, err := d.Client.PlanVersion(t.Context(), plan, 1)
	if err != nil || read.Approval == nil || read.Approval.Registration == nil || read.Approval.Registration.Preview || len(read.Approval.Registration.Repositories) != 3 {
		t.Fatalf("the decided version reads %+v: %v", read.Approval, err)
	}
	again, err := d.Client.DecideApproval(t.Context(), view.Approval.Approval.ID, api.DecideApproval{Digest: view.Approval.Approval.SubjectDigest, Allow: true})
	if err != nil || len(again.Repositories) != 3 || len(creator.Created()) != 2 {
		t.Errorf("deciding again answers %+v (%v) and creates %d", again.Repositories, err, len(creator.Created()))
	}
	holdsGolden(t, "approval-decided-created", steady(pretty(t, decided)))
}

// FR-REPO-16: a member who closes the page, or interrupts the command, while a creation runs does not stop it: the
// creation goes on to its end and the repository is registered.
func TestAClientThatDisconnectsDoesNotStopACreation(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}, 1), make(chan struct{})
	d, _, _, plan := aVersionThatCreates(t, apitest.Options{}, func(creator *placingCreator) {
		creator.passes("web")
		creator.passes("docs")
		creator.gate = func(ctx context.Context, job ledger.RepositoryCreation) error {
			if job.Name != "web" {
				return nil
			}
			entered <- struct{}{}
			<-release
			// A creation whose context ended with its request is stopped here, as a worker's step is.
			return ctx.Err()
		}
	})
	view, err := d.Client.Plan(t.Context(), plan)
	if err != nil || view.Approval == nil {
		t.Fatalf("the plan reads %+v: %v", view.Approval, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	called := make(chan error, 1)
	go func() {
		_, err := d.Client.DecideApproval(ctx, view.Approval.Approval.ID, api.DecideApproval{Digest: view.Approval.Approval.SubjectDigest, Allow: true})
		called <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the creation of web never began")
	}
	cancel()
	if err := <-called; err == nil {
		t.Fatal("a call its client abandoned answered")
	}
	// The server notices the closed connection, and would end a request context with it.
	time.Sleep(300 * time.Millisecond)
	close(release)
	deadline := time.Now().Add(10 * time.Second)
	for {
		read, err := d.Client.PlanVersion(t.Context(), plan, 1)
		if err == nil && read.Approval != nil && read.Approval.Registration != nil && !read.Approval.Registration.Preview {
			for _, one := range read.Approval.Registration.Repositories {
				if one.Name == "web" && (one.Outcome != ledger.Created || one.Repository == nil || one.Repository.Name != "web") {
					t.Fatalf("a creation its client abandoned reads %+v", one)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the creation its client abandoned never finished: %+v (%v)", read.Approval, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// FR-REPO-16: only a member's read of a version that waits asks the creator to check, and a delegate's read, which
// reads no preview, asks it nothing.
func TestOnlyAMembersReadAsksTheCreatorToCheck(t *testing.T) {
	t.Parallel()
	d, creator, _, plan := aVersionThatCreates(t, apitest.Options{}, func(creator *placingCreator) {
		creator.passes("web")
		creator.passes("docs")
	})
	delegate := asDelegate(t, d.Socket, aDelegate(t, d.Client.Client, apitest.ProjectName).Token, apitest.ProjectName)
	view, err := delegate.Plan(t.Context(), plan)
	if err != nil || view.Approval == nil || view.Approval.Registration == nil || len(view.Approval.Registration.Repositories) != 0 || view.Approval.Registration.Problem == "" {
		t.Fatalf("a delegate reads the preview %+v: %v", view.Approval, err)
	}
	var shown api.ApprovalDetail
	if err := delegate.Do(t.Context(), "GET", api.InProject(apitest.ProjectName, "/v1/approvals/"+view.Approval.Approval.ID), nil, &shown); err != nil {
		t.Fatal(err)
	}
	if checked := creator.Checked(); len(checked) != 0 {
		t.Fatalf("a delegate's reads asked the creator to check %d repositories", len(checked))
	}
	if _, err := d.Client.Plan(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if checked := creator.Checked(); len(checked) != 2 {
		t.Errorf("a member's read asked the creator to check %d repositories, want web and docs", len(checked))
	}
}

// mapValues lists the values of a map, in order.
func mapValues(held map[string]string) []string {
	out := make([]string, 0, len(held))
	for _, value := range held {
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}
