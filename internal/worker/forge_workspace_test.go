package worker_test

import (
	"encoding/json"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// healthReported reads the forge health reports the worker told the ledger, in order.
func (f *forgeRig) healthReported() []ledger.ForgeHealthReport {
	f.t.Helper()
	var reports []ledger.ForgeHealthReport
	err := f.store.Read(f.t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, event := range events {
			if event.Type != ledger.EventForgeHealth {
				continue
			}
			var report ledger.ForgeHealthReport
			if err := json.Unmarshal(event.Data, &report); err != nil {
				return err
			}
			reports = append(reports, report)
		}
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return reports
}

// A worker that keeps the checkout of a pull request repository at the project's workspace, a git clone, says in its
// health report that the workspace does not ignore the folder for lane worktrees, and says nothing of it when the
// workspace ignores the folder, when the project has no workspace, or when the workspace is another repository's
// checkout.
func TestAHealthReportCarriesAWorkspaceThatDoesNotIgnoreItsWorktrees(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		profile string
		setup   func(*fake.Checkouts)
		want    string
	}{
		{name: "the checkout is the workspace and does not ignore the folder", profile: "[projects.default]\nworkspace = \"/repository\"\n", want: "/repository"},
		{
			name: "the checkout is the workspace and ignores the folder", profile: "[projects.default]\nworkspace = \"/repository\"\n",
			setup: func(m *fake.Checkouts) { m.Ignore("/repository", planning.WorktreesFolder) },
		},
		{name: "the project has no workspace", profile: "[projects.default]\nworkspace = \"\"\n"},
		{name: "no profile names a workspace and the planning repository is not this one"},
		{name: "the workspace is another repository's checkout", profile: "[projects.default]\nworkspace = \"/work/ws\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			machine := fake.NewCheckouts()
			keeping(t, machine, scope, "repo-1", "/repository")
			keeping(t, machine, scope, "plan", "/work/ws")
			if tt.setup != nil {
				tt.setup(machine)
			}
			f := newForgeRigOn(t, func(*fake.Forge, *fake.Worktrees) {}, func(r *laneRig) {
				r.config.Provisioner.Checkouts = machine
				r.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser(tt.profile)
			})
			f.converge(2)
			reports := f.healthReported()
			if len(reports) != 1 || reports[0].State != ledger.ForgeHealthy || reports[0].UnignoredWorkspace != tt.want {
				t.Errorf("the worker reported %+v, want one healthy report with the unignored workspace %q", reports, tt.want)
			}
		})
	}
}

// A workspace that comes to ignore the folder is told again, so the report that named it stops.
func TestAHealthReportStopsNamingAWorkspaceThatIgnoresItsWorktrees(t *testing.T) {
	t.Parallel()
	machine := fake.NewCheckouts()
	keeping(t, machine, scope, "repo-1", "/repository")
	f := newForgeRigOn(t, func(*fake.Forge, *fake.Worktrees) {}, func(r *laneRig) {
		r.config.Provisioner.Checkouts = machine
		r.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser("[projects.default]\nworkspace = \"/repository\"\n")
	})
	f.converge(2)
	machine.Ignore("/repository", planning.WorktreesFolder)
	f.clock.Advance(6 * time.Minute)
	f.converge(2)
	reports := f.healthReported()
	if len(reports) != 2 || reports[0].UnignoredWorkspace != "/repository" || reports[1].UnignoredWorkspace != "" {
		t.Errorf("the worker reported %+v, want the workspace named and then not", reports)
	}
}
