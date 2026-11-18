package practicegit_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/practicegit"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/test/gittest"
)

// The tokens are built at run time, so no credential sits in this file.
var (
	secretScanTokenA = "ghp_" + strings.Repeat("a", 36)
	secretScanTokenB = "ghp_" + strings.Repeat("b", 36)
	secretScanTokenC = "gl" + "pat-" + strings.Repeat("c", 24)
)

func secretScanDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func secretScan(t *testing.T, repo *gittest.Repo, tip, from string) ports.SecretScan {
	t.Helper()
	got, err := practicegit.SecretScanner{Env: repo.Env()}.Scan(context.Background(), repo.Dir, tip, from)
	if err != nil {
		t.Fatal(err)
	}
	// The result is the same for any reader of it: no text of a secret is in it.
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{secretScanTokenA, secretScanTokenB, secretScanTokenC} {
		if strings.Contains(string(out), token) || strings.Contains(string(out), token[6:]) {
			t.Fatalf("the result holds a secret's text: %s", out)
		}
	}
	return got
}

func secretScanWant(rule, path string, line int, commit, token string) practices.SecretMatch {
	return practices.SecretMatch{Rule: rule, Path: path, Line: line, Commit: commit, Digest: secretScanDigest(token)}
}

func secretScanExpect(t *testing.T, got ports.SecretScan, want ...practices.SecretMatch) {
	t.Helper()
	if len(got.Matches) != len(want) {
		t.Fatalf("matches are %+v, want %+v", got.Matches, want)
	}
	for i := range want {
		if got.Matches[i] != want[i] {
			t.Errorf("match %d is %+v, want %+v", i, got.Matches[i], want[i])
		}
	}
}

func TestSecretScanFindsATokenAddedAndDeletedInHistoryOnly(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("config.txt", "name = demo\ntoken = "+secretScanTokenA+"\n")
	added := repo.Git("rev-parse", "HEAD")
	tip := repo.Commit("config.txt", "name = demo\n")
	got := secretScan(t, repo, tip, "")
	secretScanExpect(t, got, secretScanWant("github_token", "config.txt", 2, added, secretScanTokenA))
	if got.ScannedFrom != "" || got.Skipped != 0 {
		t.Errorf("the scan reads from %q and skipped %d", got.ScannedFrom, got.Skipped)
	}
}

func TestSecretScanFindsATrackedTokenOnce(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("README.md", "hello\n")
	tip := repo.Commit("deploy/env.txt", "a\nb\nkey "+secretScanTokenA+"\n")
	got := secretScan(t, repo, tip, "")
	secretScanExpect(t, got, secretScanWant("github_token", "deploy/env.txt", 3, tip, secretScanTokenA))
}

func TestSecretScanKeepsTheEarliestCommitOfATokenInTheTreeAndTheHistory(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("a.txt", "token "+secretScanTokenA+"\n")
	first := repo.Git("rev-parse", "HEAD")
	repo.Commit("a.txt", "none\n")
	tip := repo.Commit("a.txt", "token "+secretScanTokenA+"\n")
	secretScanExpect(t, secretScan(t, repo, tip, ""), secretScanWant("github_token", "a.txt", 1, first, secretScanTokenA))
}

func TestSecretScanFindsAPrivateKeyBlock(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	block := "-----BEGIN " + "RSA PRIVATE KEY-----\nMIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu\nKUpRKfFLfRYC9AIKjbJTWit+CqvjWYzvQwECAwEAAQ==\n-----END " + "RSA PRIVATE KEY-----\n"
	tip := repo.Commit("keys/id_rsa", "# key\n"+block)
	got := secretScan(t, repo, tip, "")
	if len(got.Matches) != 1 || got.Matches[0].Rule != "private_key" || got.Matches[0].Line != 2 || got.Matches[0].Path != "keys/id_rsa" || got.Matches[0].Commit != tip {
		t.Errorf("matches are %+v", got.Matches)
	}
	if got.Matches[0].Digest != secretScanDigest(strings.TrimSuffix(block, "\n")) {
		t.Errorf("the digest is %s", got.Matches[0].Digest)
	}
}

func TestSecretScanFindsNoGenericAssignment(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("settings.env", "password=hunter2\nDEPLOY_TOKEN=s3cr3tvalue123\n")
	tip := repo.Commit("notes.md", "Authorization: Bearer abcdef0123456789.xyz\n")
	got := secretScan(t, repo, tip, "")
	if len(got.Matches) != 0 {
		t.Errorf("matches are %+v", got.Matches)
	}
}

func TestSecretScanFindsATokenOfAPathWithSpaces(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	tip := repo.Commit("my docs/the key.txt", secretScanTokenA+"\n")
	secretScanExpect(t, secretScan(t, repo, tip, ""), secretScanWant("github_token", "my docs/the key.txt", 1, tip, secretScanTokenA))
}

func TestSecretScanOfAFastForwardReadsOnlyTheNewCommits(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("a.txt", "token "+secretScanTokenA+"\n")
	from := repo.Commit("a.txt", "clean\n")
	repo.Commit("b.txt", "one\ntwo\n"+secretScanTokenB+"\n")
	added := repo.Git("rev-parse", "HEAD")
	repo.Commit("b.txt", "one\n")
	tip := repo.Commit("c.txt", secretScanTokenC+"\n")
	got := secretScan(t, repo, tip, from)
	secretScanExpect(t, got,
		secretScanWant("github_token", "b.txt", 3, added, secretScanTokenB),
		secretScanWant("gitlab_token", "c.txt", 1, tip, secretScanTokenC))
	if got.ScannedFrom != from {
		t.Errorf("the scan read from %q, want %q", got.ScannedFrom, from)
	}
}

// The tree scan runs every time, so a token in a file that no new commit touched is still found, at the tip.
func TestSecretScanOfAFastForwardStillReadsTheTree(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	from := repo.Commit("a.txt", "token "+secretScanTokenA+"\n")
	tip := repo.Commit("b.txt", "clean\n")
	got := secretScan(t, repo, tip, from)
	secretScanExpect(t, got, secretScanWant("github_token", "a.txt", 1, tip, secretScanTokenA))
	if got.ScannedFrom != from {
		t.Errorf("the scan read from %q, want %q", got.ScannedFrom, from)
	}
}

func TestSecretScanOfARewrittenBaseReadsEverything(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("a.txt", "clean\n")
	repo.Git("checkout", "--quiet", "-b", "old")
	repo.Commit("old.txt", "x\n")
	from := repo.Git("rev-parse", "HEAD")
	repo.Git("checkout", "--quiet", "main")
	repo.Commit("a.txt", "token "+secretScanTokenA+"\n")
	added := repo.Git("rev-parse", "HEAD")
	tip := repo.Commit("a.txt", "clean\n")
	got := secretScan(t, repo, tip, from)
	secretScanExpect(t, got, secretScanWant("github_token", "a.txt", 1, added, secretScanTokenA))
	if got.ScannedFrom != "" {
		t.Errorf("the scan read from %q, want the whole history", got.ScannedFrom)
	}
	for _, unknown := range []string{strings.Repeat("0", 40), "-x", "no-such-ref"} {
		if got := secretScan(t, repo, tip, unknown); got.ScannedFrom != "" || len(got.Matches) != 1 {
			t.Errorf("a scan from %q reads from %q and finds %+v", unknown, got.ScannedFrom, got.Matches)
		}
	}
}

func TestSecretScanSkipsAndCountsALargeFileAndABinaryFile(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("big.txt", secretScanTokenA+"\n"+strings.Repeat("a", 1<<20))
	repo.Commit("blob.bin", "\x00\x01"+secretScanTokenB+"\n")
	repo.Commit("edge.txt", strings.Repeat("a", 1<<20-len(secretScanTokenC)-2)+"\n"+secretScanTokenC+"\n")
	tip := repo.Commit("small.txt", "clean\n")
	got := secretScan(t, repo, tip, "")
	if got.Skipped != 2 {
		t.Errorf("skipped %d files, want 2", got.Skipped)
	}
	if len(got.Matches) != 1 || got.Matches[0].Path != "edge.txt" {
		t.Errorf("a file of exactly 1 MiB is scanned and the others are not: %+v", got.Matches)
	}
}

func TestSecretScanSkipsABinaryFileOnlyInHistory(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("blob.bin", "\x00"+secretScanTokenA+"\n")
	tip := repo.Commit("blob.bin", "text\n")
	got := secretScan(t, repo, tip, "")
	if got.Skipped != 1 || len(got.Matches) != 0 {
		t.Errorf("skipped %d, matches %+v", got.Skipped, got.Matches)
	}
}

func TestSecretScanErrorsHoldNoText(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("a.txt", "token "+secretScanTokenA+"\n")
	scanner := practicegit.SecretScanner{Env: repo.Env()}
	for _, tip := range []string{"no-such-ref", "-x", ""} {
		_, err := scanner.Scan(context.Background(), repo.Dir, tip, "")
		if err == nil {
			t.Fatalf("a scan of %q succeeded", tip)
		}
		if strings.Contains(err.Error(), secretScanTokenA) || strings.Contains(err.Error(), strings.Repeat("a", 36)) {
			t.Errorf("the error holds a secret's text: %v", err)
		}
	}
	if _, err := scanner.Scan(context.Background(), t.TempDir(), "HEAD", ""); err == nil {
		t.Error("a scan of a directory with no repository succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanner.Scan(ctx, repo.Dir, "HEAD", ""); err == nil {
		t.Error("a scan with an ended context succeeded")
	}
}

func TestSecretScanReportsEachPathThatHoldsTheSameBlob(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	first := repo.Commit("one/key.txt", secretScanTokenA+"\n")
	tip := repo.Commit("two/key.txt", secretScanTokenA+"\n")
	secretScanExpect(t, secretScan(t, repo, tip, ""),
		secretScanWant("github_token", "one/key.txt", 1, first, secretScanTokenA),
		secretScanWant("github_token", "two/key.txt", 1, tip, secretScanTokenA))
}

func TestSecretScanReadsPathsGitQuotes(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	tip := repo.Commit("café/\"key\"\t.txt", secretScanTokenA+"\n")
	secretScanExpect(t, secretScan(t, repo, tip, ""), secretScanWant("github_token", "café/\"key\"\t.txt", 1, tip, secretScanTokenA))
}
