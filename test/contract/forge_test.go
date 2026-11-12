package contract

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/ghcli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
	"ropeshift.com/ropeshift/test/gittest"
)

func TestCheckSourceContract(t *testing.T) {
	t.Parallel()
	RunCheckSource(t, func(*testing.T) CheckSourceRig {
		forge := &fake.Forge{}
		return CheckSourceRig{Source: forge, Scope: "workspace-a", Set: forge.Set}
	})
}

var contractBinding = ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}

func TestForgeContractOnTheFake(t *testing.T) {
	t.Parallel()
	RunForge(t, func(t *testing.T) ForgeRig {
		forge := &fake.Forge{}
		var commits int
		return ForgeRig{
			Forge: forge, Repo: ports.ForgeRepo{Dir: t.TempDir(), Binding: contractBinding},
			Push: func(_ *testing.T, branch string) string {
				commits++
				commit := fmt.Sprintf("%040d", commits)
				forge.Push(contractBinding, branch, commit)
				return commit
			},
			Merge: func(_ *testing.T, number int64) string {
				merge := strings.Repeat("m", 40)
				forge.Merge(number, merge)
				return merge
			},
			SetChecks: func(_ *testing.T, number int64, checks ...ledger.PullRequestCheck) {
				forge.SetChecks(number, checks...)
			},
			Comments: func(_ *testing.T, number int64) []string { return forge.Comments(number) },
			SignOut:  func(_ *testing.T, detail string) { forge.SignOut(contractBinding.Host, detail) },
			Missing: func(*testing.T) ports.Forge {
				forge.Remove()
				return forge
			},
		}
	})
}

// TestForgeContractOnGH runs the GitHub adapter on the fake `gh`, in a checkout whose remote is a bare
// repository on this machine, so nothing reaches GitHub and a push moves a pull request's head.
func TestForgeContractOnGH(t *testing.T) {
	t.Parallel()
	RunForge(t, func(t *testing.T) ForgeRig {
		repo, gh := ghCheckout(t)
		forge := ghcli.Forge{Path: filepath.Join(gh, ghcli.Binary), Env: repo.Env(), TempDir: t.TempDir()}
		edit := func(t *testing.T, number int64, change func(*ghstate.PullRequest)) {
			doubles.EditFakeGH(t, gh, func(s *ghstate.State) {
				if pr := s.Find(number); pr != nil {
					change(pr)
				}
			})
		}
		return ForgeRig{
			Forge: forge, Repo: ports.ForgeRepo{Dir: repo.Dir, Binding: contractBinding},
			Push: func(t *testing.T, branch string) string {
				if _, err := exec.LookPath("git"); err != nil {
					t.Skip("no git")
				}
				if repo.Git("branch", "--list", branch) == "" {
					repo.Branch(branch, "main")
				}
				commit := repo.Advance(branch)
				repo.Git("push", "--quiet", "origin", "refs/heads/"+branch+":refs/heads/"+branch)
				return commit
			},
			Merge: func(t *testing.T, number int64) string {
				merge := strings.Repeat("e", 40)
				edit(t, number, func(pr *ghstate.PullRequest) {
					pr.Head = repo.Git("rev-parse", "refs/heads/"+pr.Branch)
					pr.State, pr.MergeCommit, pr.MergedAt = "MERGED", merge, "2026-01-02T03:04:05Z"
				})
				return merge
			},
			SetChecks: func(t *testing.T, number int64, checks ...ledger.PullRequestCheck) {
				edit(t, number, func(pr *ghstate.PullRequest) { pr.Checks = ghChecks(checks) })
			},
			Comments: func(t *testing.T, number int64) []string {
				state := doubles.FakeGH(t, gh)
				if pr := state.Find(number); pr != nil {
					return pr.Comments
				}
				return nil
			},
			SignOut: func(t *testing.T, detail string) {
				doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.SignedOut, s.AuthError = true, detail })
			},
			Missing: func(t *testing.T) ports.Forge {
				return ghcli.Forge{Path: filepath.Join(t.TempDir(), ghcli.Binary), Env: repo.Env()}
			},
		}
	})
}

// ghCheckout is a checkout with one commit on main, whose origin is a bare repository beside it, and a
// fake `gh` that reads the forge from it.
func ghCheckout(t *testing.T) (*gittest.Repo, string) {
	t.Helper()
	repo := gittest.New(t)
	repo.Commit("README.md", "widgets\n")
	bare := filepath.Join(t.TempDir(), "remote.git")
	repo.Git("init", "--quiet", "--bare", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "--quiet", "origin", "main")
	return repo, doubles.InstallFakeGH(t)
}

// ghChecks writes checks as statusCheckRollup does: a pass as a check run that succeeded, a failure as a
// commit status that failed, and a pending check as a check run still in progress.
func ghChecks(checks []ledger.PullRequestCheck) []ghstate.Check {
	out := make([]ghstate.Check, 0, len(checks))
	for _, check := range checks {
		switch check.State {
		case ledger.CheckPassed:
			out = append(out, ghstate.Check{Typename: "CheckRun", Name: check.Name, Status: "COMPLETED", Conclusion: "SUCCESS", DetailsURL: check.DetailsURI})
		case ledger.CheckFailed:
			out = append(out, ghstate.Check{Typename: "StatusContext", Context: check.Name, State: "FAILURE", TargetURL: check.DetailsURI})
		default:
			out = append(out, ghstate.Check{Typename: "CheckRun", Name: check.Name, Status: "IN_PROGRESS", DetailsURL: check.DetailsURI})
		}
	}
	return out
}
