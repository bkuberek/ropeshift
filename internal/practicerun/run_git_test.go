package practicerun_test

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/practicegit"
	"ropeshift.com/ropeshift/internal/practicerun"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/test/gittest"
)

// gitSources reads a repository with the git adapters. No forge is read, so the fake serves only as a placeholder.
func gitSources(repo *gittest.Repo) practicerun.Sources {
	var forge fake.Practices
	return practicerun.Sources{
		Snapshots: practicegit.Snapshots{Env: repo.Env()},
		Secrets:   practicegit.SecretScanner{Env: repo.Env()},
		Forge:     &forge,
		Clock:     fake.NewClock(time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)),
	}
}

// A repository that holds exactly one finding of every file practice gives exactly those findings, by ID, and the
// text of the secret is in no finding. A later incremental run keeps them, because the tree is scanned every time.
func TestRunFindsExactlyOneFindingOfEveryFilePractice(t *testing.T) {
	repo := gittest.New(t)
	// The token is built at run time, so no credential sits in this file.
	token := "ghp_" + strings.Repeat("a", 36)
	sum := sha256.Sum256([]byte(token))
	digest := hex.EncodeToString(sum[:])[:practices.DigestIDLength]
	repo.Commit("go.mod", "module acme.test/widgets\n\ngo 1.24\n\nrequire example.test/dep v1.0.0\n")
	repo.Commit("main.go", "package main\n\nfunc main() {}\n")
	first := repo.Commit("config/dev.yaml", "token: "+token+"\n")
	want := []string{
		"ci",
		"codeowners",
		"dependency-updates",
		"env-ignored:ignore:.",
		"lockfile:go",
		"pre-commit",
		"secrets:github_token:config/dev.yaml:" + digest,
		"security-policy",
		"tests:go",
	}

	for _, tt := range []struct{ name, from string }{{"whole history", ""}, {"incremental", first}} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.from != "" {
				repo.Commit("README.md", "# widgets\n")
			}
			result, err := practicerun.Run(t.Context(), gitSources(repo), practicerun.Request{
				Repository: "widgets", Dir: repo.Dir, BaseBranch: "main", ScannedFrom: tt.from,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := findingIDs(result); !slices.Equal(got, want) {
				t.Errorf("findings are %v, want %v", got, want)
			}
			if head := repo.Git("rev-parse", "HEAD"); result.Commit != head {
				t.Errorf("the result read %s, want %s", result.Commit, head)
			}
			if result.ScannedFrom != tt.from {
				t.Errorf("the scan started from %q, want %q", result.ScannedFrom, tt.from)
			}
			for _, id := range []practices.ID{practices.BranchProtection, practices.RequiredReview, practices.License} {
				requireStatus(t, result, id, practices.StatusNotApplicable, "no forge binding")
			}
			for _, f := range result.Findings {
				if strings.Contains(f.Detail+f.ID+f.Digest, token) {
					t.Errorf("finding %s holds the secret's text", f.ID)
				}
			}
		})
	}
}
