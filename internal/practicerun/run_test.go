package practicerun_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practicerun"
	"ropeshift.com/ropeshift/internal/practices"
)

const (
	checkoutDir = "/checkouts/widgets"
	tipCommit   = "1111111111111111111111111111111111111111"
	oldCommit   = "2222222222222222222222222222222222222222"
)

var (
	checkedAt = time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
	binding   = ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}
)

// soundPaths hold every file practice and a Go module with tests, so a run finds nothing in the files.
var soundPaths = []string{
	".gitignore", ".pre-commit-config.yaml", ".github/workflows/ci.yml", ".github/dependabot.yml", "SECURITY.md",
	"CODEOWNERS", "LICENSE", "go.mod", "go.sum", "main.go", "main_test.go",
}

// soundSnapshot is a checkout at tipCommit that every file practice holds for.
func soundSnapshot() practices.Snapshot {
	ignored := map[string]bool{}
	for _, probe := range practices.Probes(soundPaths) {
		ignored[probe] = true
	}
	return practices.Snapshot{
		Commit: tipCommit,
		Paths:  soundPaths,
		Read: func(path string) (string, bool) {
			if path == "go.mod" {
				return "module acme.test/widgets\n\ngo 1.24\n\nrequire example.test/dep v1.0.0\n", true
			}
			return "", true
		},
		Ignored: ignored,
	}
}

type fixture struct {
	fake    *fake.Practices
	sources practicerun.Sources
}

func newFixture() *fixture {
	var f fake.Practices
	f.SetSnapshot(checkoutDir, soundSnapshot())
	return &fixture{fake: &f, sources: practicerun.Sources{
		Snapshots: &f, Secrets: &f, Forge: &f, Clock: fake.NewClock(checkedAt),
	}}
}

func request() practicerun.Request {
	return practicerun.Request{Repository: "widgets", Worker: "worker-1", Dir: checkoutDir, BaseBranch: "main"}
}

func statusOf(t *testing.T, result practices.Result, id practices.ID) practices.PracticeStatus {
	t.Helper()
	for _, p := range result.Practices {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("the result holds no status for %s", id)
	return practices.PracticeStatus{}
}

func findingIDs(result practices.Result) []string {
	var ids []string
	for _, f := range result.Findings {
		ids = append(ids, f.ID)
	}
	slices.Sort(ids)
	return ids
}

func requireStatus(t *testing.T, result practices.Result, id practices.ID, status practices.Status, reason string) {
	t.Helper()
	got := statusOf(t, result, id)
	if got.Status != status || got.Reason != reason {
		t.Errorf("%s is %s %q, want %s %q", id, got.Status, got.Reason, status, reason)
	}
}

// A run with no forge binding fills the result from the request, the snapshot, the clock, and the tables, reads
// no forge setting, and leaves the three forge-dependent practices not applicable (FR-PRAC-5).
func TestRunWithNoBindingLeavesTheForgePracticesNotApplicable(t *testing.T) {
	f := newFixture()
	result, err := practicerun.Run(t.Context(), f.sources, request())
	if err != nil {
		t.Fatal(err)
	}
	if result.Repository != "widgets" || result.Worker != "worker-1" || result.BaseBranch != "main" || result.Commit != tipCommit ||
		!result.CheckedAt.Equal(checkedAt) || result.TableVersion != practices.TableVersion ||
		result.RulesVersion != ledger.CredentialRulesVersion || result.ScannedFrom != "" || result.Skipped != 0 {
		t.Errorf("the result is %+v", result)
	}
	for _, id := range []practices.ID{practices.BranchProtection, practices.RequiredReview, practices.License} {
		requireStatus(t, result, id, practices.StatusNotApplicable, "no forge binding")
	}
	if len(result.Findings) != 0 {
		t.Errorf("a sound checkout has findings %v", findingIDs(result))
	}
	if len(f.fake.SettingsCalls) != 0 {
		t.Errorf("the forge was read %v with no binding", f.fake.SettingsCalls)
	}
}

// The run resolves the commit once, by the base branch's name, and scans with the commit the snapshot named as the
// tip, so the scan reads the commit the files came from.
func TestRunScansTheCommitTheSnapshotRead(t *testing.T) {
	f := newFixture()
	if _, err := practicerun.Run(t.Context(), f.sources, request()); err != nil {
		t.Fatal(err)
	}
	if want := [][2]string{{checkoutDir, "main"}}; !slices.Equal(f.fake.SnapshotCalls, want) {
		t.Errorf("snapshots were read as %v, want %v", f.fake.SnapshotCalls, want)
	}
	if want := [][3]string{{checkoutDir, tipCommit, ""}}; !slices.Equal(f.fake.ScanCalls, want) {
		t.Errorf("scans ran as %v, want %v", f.fake.ScanCalls, want)
	}
}

// An incremental scan passes the last scanned commit through, and the result names where the scan started and how
// many files it skipped, and holds what it matched as findings by ID, never the text.
func TestRunPassesTheLastScannedCommitAndReportsTheScan(t *testing.T) {
	f := newFixture()
	digest := "1a2b3c4d5e6f" + strings.Repeat("0", 52)
	f.fake.SetScan(checkoutDir, ports.SecretScan{
		Matches:     []practices.SecretMatch{{Rule: "github_token", Path: "config/dev.yaml", Line: 4, Commit: oldCommit, Digest: digest}},
		ScannedFrom: oldCommit, Skipped: 2,
	})
	req := request()
	req.ScannedFrom = oldCommit
	result, err := practicerun.Run(t.Context(), f.sources, req)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][3]string{{checkoutDir, tipCommit, oldCommit}}; !slices.Equal(f.fake.ScanCalls, want) {
		t.Errorf("scans ran as %v, want %v", f.fake.ScanCalls, want)
	}
	if result.ScannedFrom != oldCommit || result.Skipped != 2 {
		t.Errorf("the result scanned from %q and skipped %d", result.ScannedFrom, result.Skipped)
	}
	if want := []string{"secrets:github_token:config/dev.yaml:1a2b3c4d5e6f"}; !slices.Equal(findingIDs(result), want) {
		t.Errorf("findings are %v, want %v", findingIDs(result), want)
	}
	requireStatus(t, result, practices.Secrets, practices.StatusFinding, "")
}

// A scan that fails leaves the secrets practice unknown with the reason and does not fail the run, and every other
// practice is still decided.
func TestRunLeavesSecretsUnknownWhenTheScanFails(t *testing.T) {
	f := newFixture()
	boom := errors.New("git log failed")
	failing := f.sources
	failing.Secrets = failingScans{err: boom}
	result, err := practicerun.Run(t.Context(), failing, request())
	if err != nil {
		t.Fatalf("a failed scan failed the run: %v", err)
	}
	got := statusOf(t, result, practices.Secrets)
	if got.Status != practices.StatusUnknown || !strings.Contains(got.Reason, "git log failed") {
		t.Errorf("secrets are %s %q, want unknown with the scan's error", got.Status, got.Reason)
	}
	requireStatus(t, result, practices.SecurityPolicy, practices.StatusMet, "")
	if result.ScannedFrom != "" || result.Skipped != 0 {
		t.Errorf("a failed scan names %q and %d", result.ScannedFrom, result.Skipped)
	}
}

// A scan that read only part of the history leaves the secrets practice unknown with the reason.
func TestRunLeavesSecretsUnknownWhenTheScanIsPartial(t *testing.T) {
	f := newFixture()
	f.fake.SetScan(checkoutDir, ports.SecretScan{Partial: "the repository is a shallow clone"})
	result, err := practicerun.Run(t.Context(), f.sources, request())
	if err != nil {
		t.Fatal(err)
	}
	got := statusOf(t, result, practices.Secrets)
	if got.Status != practices.StatusUnknown || !strings.Contains(got.Reason, "the repository is a shallow clone") {
		t.Errorf("secrets are %s %q, want unknown with the partial scan's reason", got.Status, got.Reason)
	}
}

// A scan or a settings read that ends with the caller's context is a failed run, not an unknown practice.
func TestRunFailsWhenTheContextEnds(t *testing.T) {
	t.Run("scan", func(t *testing.T) {
		f := newFixture()
		ctx, cancel := context.WithCancel(t.Context())
		sources := f.sources
		sources.Secrets = canceledScans{cancel: cancel}
		if _, err := practicerun.Run(ctx, sources, request()); !errors.Is(err, context.Canceled) {
			t.Errorf("the run ended with %v, want the context's error", err)
		}
	})
	t.Run("evaluation", func(t *testing.T) {
		f := newFixture()
		ctx, cancel := context.WithCancel(t.Context())
		snapshot := soundSnapshot()
		snapshot.Read = func(string) (string, bool) {
			cancel()
			return "", false
		}
		f.fake.SetSnapshot(checkoutDir, snapshot)
		result, err := practicerun.Run(ctx, f.sources, request())
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the run ended with %v and %d practices, want the context's error", err, len(result.Practices))
		}
	})
	t.Run("settings", func(t *testing.T) {
		f := newFixture()
		ctx, cancel := context.WithCancel(t.Context())
		sources := f.sources
		sources.Forge = canceledSettings{cancel: cancel}
		req := request()
		req.Binding = binding
		if _, err := practicerun.Run(ctx, sources, req); !errors.Is(err, context.Canceled) {
			t.Errorf("the run ended with %v, want the context's error", err)
		}
	})
}

// The text of a failed read that goes into a result is one line of at most 2000 bytes, cut on a rune boundary, so a
// tool's whole message, with a path or an argument list in it, never reaches a result.
func TestRunBoundsTheReasonItRecordsForAFailedRead(t *testing.T) {
	// The euro sign is three bytes, so the cut falls inside a rune.
	long := errors.New("git log --format=%H failed\n  in /checkouts/widgets\n" + strings.Repeat("\u20ac", 3000))
	check := func(t *testing.T, reason, prefix string) {
		t.Helper()
		if len(reason) > 2000 {
			t.Errorf("the reason is %d bytes, want at most 2000", len(reason))
		}
		if len(reason) < 1900 {
			t.Errorf("the reason is %d bytes, want it cut near the bound", len(reason))
		}
		if strings.ContainsAny(reason, "\r\n") {
			t.Errorf("the reason spans lines: %q", reason[:80])
		}
		if !utf8.ValidString(reason) {
			t.Error("the reason is not valid UTF-8")
		}
		if !strings.HasPrefix(reason, prefix+"git log --format=%H failed in /checkouts/widgets") {
			t.Errorf("the reason starts %q", reason[:80])
		}
	}
	t.Run("scan", func(t *testing.T) {
		f := newFixture()
		sources := f.sources
		sources.Secrets = failingScans{err: long}
		result, err := practicerun.Run(t.Context(), sources, request())
		if err != nil {
			t.Fatal(err)
		}
		check(t, statusOf(t, result, practices.Secrets).Reason, "the secret scan failed: ")
	})
	t.Run("settings", func(t *testing.T) {
		f := newFixture()
		f.fake.SetSettingsError(binding.String(), long)
		req := request()
		req.Binding = binding
		result, err := practicerun.Run(t.Context(), f.sources, req)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []practices.ID{practices.License, practices.BranchProtection, practices.RequiredReview} {
			check(t, statusOf(t, result, id).Reason, "the forge settings could not be read: ")
		}
	})
}

// Clip is what a worker bounds the text of a failed run with: one line, at most MaxReason bytes, cut on a rune.
func TestClipWritesOneBoundedLineOnARuneBoundary(t *testing.T) {
	got := practicerun.Clip("  a\n\tb  " + strings.Repeat("\u20ac", 3000))
	if !strings.HasPrefix(got, "a b \u20ac") || strings.ContainsAny(got, "\r\n\t") {
		t.Errorf("the line is %q", got[:20])
	}
	if len(got) > practicerun.MaxReason || len(got) < practicerun.MaxReason-3 || !utf8.ValidString(got) {
		t.Errorf("the line is %d bytes, valid UTF-8 %v, want a cut near %d", len(got), utf8.ValidString(got), practicerun.MaxReason)
	}
	if short := practicerun.Clip("one  two"); short != "one two" {
		t.Errorf("a short text reads %q", short)
	}
}

// A snapshot that cannot be read fails the run, because no practice can be decided without it.
func TestRunFailsWhenTheSnapshotCannotBeRead(t *testing.T) {
	f := newFixture()
	boom := errors.New("no such commit")
	f.fake.Fail = boom
	if _, err := practicerun.Run(t.Context(), f.sources, request()); !errors.Is(err, boom) {
		t.Errorf("the run ended with %v, want the snapshot's error", err)
	}
}

// A repository with a binding has its settings read for the base branch, and each outcome decides the forge
// practices as the forge's facts say (FR-PRAC-5).
func TestRunDecidesTheForgePracticesFromTheForgeFacts(t *testing.T) {
	protected := practices.ForgeFacts{Visibility: practices.VisibilityPublic, Protected: true, ReviewCount: 1}
	tests := []struct {
		name                        string
		facts                       practices.ForgeFacts
		paths                       []string
		license, protection, review practices.Status
		reasons                     [3]string
	}{
		{name: "protected and reviewed", facts: protected, paths: soundPaths,
			license: practices.StatusMet, protection: practices.StatusMet, review: practices.StatusMet},
		{name: "public with no license file", facts: protected, paths: slices.DeleteFunc(slices.Clone(soundPaths), func(p string) bool { return p == "LICENSE" }),
			license: practices.StatusFinding, protection: practices.StatusMet, review: practices.StatusMet},
		{name: "private", facts: practices.ForgeFacts{Visibility: practices.VisibilityPrivate, Protected: true, ReviewCount: 2}, paths: soundPaths,
			license: practices.StatusNotApplicable, protection: practices.StatusMet, review: practices.StatusMet,
			reasons: [3]string{"the repository is not public"}},
		{name: "unprotected", facts: practices.ForgeFacts{Visibility: practices.VisibilityPublic}, paths: soundPaths,
			license: practices.StatusMet, protection: practices.StatusFinding, review: practices.StatusFinding},
		{name: "protected with no review", facts: practices.ForgeFacts{Visibility: practices.VisibilityPublic, Protected: true}, paths: soundPaths,
			license: practices.StatusMet, protection: practices.StatusMet, review: practices.StatusFinding},
		{name: "unreadable settings", paths: soundPaths,
			facts:   practices.ForgeFacts{VisibilityUnknown: "visibility refused", ProtectedUnknown: "protection refused", ReviewUnknown: "review refused"},
			license: practices.StatusUnknown, protection: practices.StatusUnknown, review: practices.StatusUnknown,
			reasons: [3]string{"visibility refused", "protection refused", "review refused"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			snapshot := soundSnapshot()
			snapshot.Paths = tt.paths
			f.fake.SetSnapshot(checkoutDir, snapshot)
			f.fake.SetSettings(binding.String(), tt.facts)
			req := request()
			req.Binding = binding
			result, err := practicerun.Run(t.Context(), f.sources, req)
			if err != nil {
				t.Fatal(err)
			}
			if want := [][2]string{{binding.String(), "main"}}; !slices.Equal(f.fake.SettingsCalls, want) {
				t.Errorf("settings were read as %v, want %v", f.fake.SettingsCalls, want)
			}
			requireStatus(t, result, practices.License, tt.license, tt.reasons[0])
			requireStatus(t, result, practices.BranchProtection, tt.protection, tt.reasons[1])
			requireStatus(t, result, practices.RequiredReview, tt.review, tt.reasons[2])
		})
	}
}

// A rate-limited read returns the result the facts it did settle give, and an error that wraps the rate limit, so a
// caller records the result and holds its reads of the repository.
func TestRunReturnsTheResultWithTheRateLimitError(t *testing.T) {
	f := newFixture()
	f.fake.SetSettings(binding.String(), practices.ForgeFacts{
		Visibility: practices.VisibilityPublic, Protected: true, ReviewUnknown: "rate limited until 10:00",
	})
	limited := &ports.RateLimitedError{Detail: "secondary limit"}
	f.fake.SetSettingsError(binding.String(), limited)
	req := request()
	req.Binding = binding
	result, err := practicerun.Run(t.Context(), f.sources, req)
	var got *ports.RateLimitedError
	if !errors.As(err, &got) || got != limited || !errors.Is(err, ports.ErrForgeRateLimited) {
		t.Fatalf("the run ended with %v, want the rate limit", err)
	}
	requireStatus(t, result, practices.License, practices.StatusMet, "")
	requireStatus(t, result, practices.BranchProtection, practices.StatusMet, "")
	requireStatus(t, result, practices.RequiredReview, practices.StatusUnknown, "rate limited until 10:00")
	if result.Commit != tipCommit || len(result.Practices) == 0 {
		t.Errorf("the result is %+v", result)
	}
}

// A rate-limited read with no facts settled, as a fake or an adapter that sets none returns it, leaves each forge
// practice unknown with the rate limit as the reason, and never a finding or a met practice, and the facts the read
// did settle stay.
func TestRunLeavesTheFactsARateLimitedReadDidNotSettleUnknown(t *testing.T) {
	reset := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	limited := &ports.RateLimitedError{Rate: ports.ForgeRate{Exhausted: true, Reset: reset}, Detail: "secondary limit"}
	tests := []struct {
		name  string
		facts practices.ForgeFacts
		want  [3]practices.Status
	}{
		{"none settled", practices.ForgeFacts{}, [3]practices.Status{practices.StatusUnknown, practices.StatusUnknown, practices.StatusUnknown}},
		{"visibility settled", practices.ForgeFacts{Visibility: practices.VisibilityPrivate},
			[3]practices.Status{practices.StatusNotApplicable, practices.StatusUnknown, practices.StatusUnknown}},
		{"protection settled", practices.ForgeFacts{Protected: true, ReviewCount: 1},
			[3]practices.Status{practices.StatusUnknown, practices.StatusMet, practices.StatusMet}},
		// A count below zero is no settled count, as zero is none.
		{"a negative review count", practices.ForgeFacts{Protected: true, ReviewCount: -1},
			[3]practices.Status{practices.StatusUnknown, practices.StatusMet, practices.StatusUnknown}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.fake.SetSettings(binding.String(), tt.facts)
			f.fake.SetSettingsError(binding.String(), limited)
			req := request()
			req.Binding = binding
			result, err := practicerun.Run(t.Context(), f.sources, req)
			if !errors.Is(err, ports.ErrForgeRateLimited) {
				t.Fatalf("the run ended with %v, want the rate limit", err)
			}
			for i, id := range []practices.ID{practices.License, practices.BranchProtection, practices.RequiredReview} {
				got := statusOf(t, result, id)
				if got.Status != tt.want[i] {
					t.Errorf("%s is %s %q, want %s", id, got.Status, got.Reason, tt.want[i])
				}
				if got.Status == practices.StatusUnknown && !strings.Contains(got.Reason, "rate limiting") {
					t.Errorf("%s is unknown with %q, want the rate limit as the reason", id, got.Reason)
				}
			}
		})
	}
}

// A settings read that fails with any other error leaves the three forge practices unknown with the error, and does
// not fail the run (FR-PRAC-1).
func TestRunLeavesTheForgePracticesUnknownWhenTheReadFails(t *testing.T) {
	f := newFixture()
	f.fake.SetSettingsError(binding.String(), errors.New("gh is not installed"))
	req := request()
	req.Binding = binding
	result, err := practicerun.Run(t.Context(), f.sources, req)
	if err != nil {
		t.Fatalf("a failed forge read failed the run: %v", err)
	}
	for _, id := range []practices.ID{practices.License, practices.BranchProtection, practices.RequiredReview} {
		got := statusOf(t, result, id)
		if got.Status != practices.StatusUnknown || !strings.Contains(got.Reason, "gh is not installed") {
			t.Errorf("%s is %s %q, want unknown with the forge's error", id, got.Status, got.Reason)
		}
	}
}

// The merge authority reaches the `ci` practice, and an empty one counts as the default.
func TestRunPassesTheMergeAuthorityToTheCIPractice(t *testing.T) {
	noCI := soundSnapshot()
	noCI.Paths = slices.DeleteFunc(slices.Clone(soundPaths), func(p string) bool { return p == ".github/workflows/ci.yml" })
	tests := []struct {
		authority ledger.MergeAuthority
		want      practices.Status
	}{
		{"", practices.StatusMet},
		{ledger.PersonMerges, practices.StatusMet},
		{ledger.PersonApproves, practices.StatusMet},
		{ledger.ReviewApproves, practices.StatusFinding},
	}
	for _, tt := range tests {
		t.Run(string(tt.authority), func(t *testing.T) {
			f := newFixture()
			f.fake.SetSnapshot(checkoutDir, noCI)
			req := request()
			req.MergeAuthority = tt.authority
			result, err := practicerun.Run(t.Context(), f.sources, req)
			if err != nil {
				t.Fatal(err)
			}
			requireStatus(t, result, practices.CI, tt.want, "")
		})
	}
}

// failingScans is a secret scan that always fails.
type failingScans struct{ err error }

func (s failingScans) Scan(context.Context, string, string, string) (ports.SecretScan, error) {
	return ports.SecretScan{}, s.err
}

// canceledScans is a secret scan that ends its caller's context.
type canceledScans struct{ cancel context.CancelFunc }

func (s canceledScans) Scan(ctx context.Context, _, _, _ string) (ports.SecretScan, error) {
	s.cancel()
	return ports.SecretScan{}, ctx.Err()
}

// canceledSettings is a settings read that ends its caller's context.
type canceledSettings struct{ cancel context.CancelFunc }

func (s canceledSettings) Settings(ctx context.Context, _ ports.ForgeRepo, _ string) (practices.ForgeFacts, error) {
	s.cancel()
	return practices.ForgeFacts{}, ctx.Err()
}
