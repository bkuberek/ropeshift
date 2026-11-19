package practicegit_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// secretScanConfigure appends text to the git configuration the repository's commands read.
func secretScanConfigure(t *testing.T, repo *gittest.Repo, text string) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(repo.Home, "gitconfig"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func secretScanWrite(t *testing.T, repo *gittest.Repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo.Dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSecretScanReadsHunksThatGitFusesAcrossContext(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	secretScanConfigure(t, repo, "[diff]\n\tinterHunkContext = 10\n\tsuppressBlankEmpty = true\n")
	repo.Commit("a.txt", "one\n\ntwo\nthree\nfour\n")
	repo.Commit("a.txt", "first\n\ntwo\nthree\n"+secretScanTokenA+"\n")
	added := repo.Git("rev-parse", "HEAD")
	tip := repo.Commit("a.txt", "first\n\ntwo\nthree\nfour\n")
	secretScanExpect(t, secretScan(t, repo, tip, ""), secretScanWant("github_token", "a.txt", 5, added, secretScanTokenA))
}

func TestSecretScanReadsTheRootCommitWhenLogHidesIt(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	secretScanConfigure(t, repo, "[log]\n\tshowRoot = false\n")
	root := repo.Commit("a.txt", secretScanTokenA+"\n")
	tip := repo.Commit("a.txt", "clean\n")
	secretScanExpect(t, secretScan(t, repo, tip, ""), secretScanWant("github_token", "a.txt", 1, root, secretScanTokenA))
}

func TestSecretScanReadsFilesThatAttributesCallBinary(t *testing.T) {
	t.Parallel()
	for name, setup := range map[string]func(t *testing.T, repo *gittest.Repo) string{
		"a committed minus diff": func(t *testing.T, repo *gittest.Repo) string {
			return repo.Commit(".gitattributes", "*.txt -diff\n")
		},
		"a committed binary": func(t *testing.T, repo *gittest.Repo) string {
			return repo.Commit(".gitattributes", "*.txt binary\n")
		},
		"info attributes": func(t *testing.T, repo *gittest.Repo) string {
			secretScanWrite(t, repo, ".git/info/attributes", "*.txt -diff\n")
			return repo.Commit("other.md", "x\n")
		},
		"a global attributes file": func(t *testing.T, repo *gittest.Repo) string {
			secretScanWrite(t, repo, "../attributes", "*.txt -diff\n")
			secretScanConfigure(t, repo, "[core]\n\tattributesFile = "+filepath.Join(filepath.Dir(repo.Dir), "attributes")+"\n")
			return repo.Commit("other.md", "x\n")
		},
		"a binary diff driver": func(t *testing.T, repo *gittest.Repo) string {
			secretScanConfigure(t, repo, "[diff \"opaque\"]\n\tbinary = true\n")
			return repo.Commit(".gitattributes", "*.txt diff=opaque\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo := gittest.New(t)
			setup(t, repo)
			added := repo.Commit("a.txt", "one\n"+secretScanTokenA+"\n")
			tip := repo.Commit("a.txt", "one\n")
			got := secretScan(t, repo, tip, "")
			secretScanExpect(t, got, secretScanWant("github_token", "a.txt", 2, added, secretScanTokenA))
			if got.Skipped != 0 {
				t.Errorf("skipped %d files", got.Skipped)
			}
		})
	}
}

func TestSecretScanReadsWhatAMergeAdds(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("base.txt", "base\n")
	repo.Git("checkout", "--quiet", "-b", "side")
	repo.Commit("side.txt", "side\n")
	repo.Git("checkout", "--quiet", "main")
	repo.Commit("main.txt", "main\n")
	repo.Git("merge", "--quiet", "--no-ff", "--no-commit", "side")
	secretScanWrite(t, repo, "resolved.txt", "x\n"+secretScanTokenA+"\n")
	repo.Git("add", "resolved.txt")
	repo.Git("commit", "--quiet", "-m", "merge side")
	merge := repo.Git("rev-parse", "HEAD")
	tip := repo.Commit("resolved.txt", "x\n")
	secretScanExpect(t, secretScan(t, repo, tip, ""), secretScanWant("github_token", "resolved.txt", 2, merge, secretScanTokenA))
}

// A secret a side branch added shows again in the diff of the merge against its first parent, and is one match at
// the side branch's commit.
func TestSecretScanKeepsTheSideCommitOfAMergedSecret(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("base.txt", "base\n")
	repo.Git("checkout", "--quiet", "-b", "side")
	added := repo.Commit("side.txt", secretScanTokenA+"\n")
	repo.Git("checkout", "--quiet", "main")
	repo.Commit("main.txt", "main\n")
	repo.Merge("side")
	tip := repo.Commit("side.txt", "clean\n")
	secretScanExpect(t, secretScan(t, repo, tip, ""), secretScanWant("github_token", "side.txt", 1, added, secretScanTokenA))
}

func TestSecretScanReadsHistoryWhenAPathIsNamedLikeTheTip(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	added := repo.Commit("a.txt", secretScanTokenA+"\n")
	tip := repo.Commit("a.txt", "clean\n")
	secretScanWrite(t, repo, tip, "x\n")
	secretScanExpect(t, secretScan(t, repo, tip, ""), secretScanWant("github_token", "a.txt", 1, added, secretScanTokenA))
}

func TestSecretScanReadsPathsFromTheRootOfTheRepository(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	secretScanConfigure(t, repo, "[diff]\n\trelative = true\n")
	added := repo.Commit("top.txt", secretScanTokenA+"\n")
	repo.Commit("sub/inner.txt", "x\n")
	tip := repo.Commit("top.txt", "clean\n")
	got, err := practicegit.SecretScanner{Env: repo.Env()}.Scan(context.Background(), filepath.Join(repo.Dir, "sub"), tip, "")
	if err != nil {
		t.Fatal(err)
	}
	secretScanExpect(t, got, secretScanWant("github_token", "top.txt", 1, added, secretScanTokenA))
}

func TestSecretScanIgnoresReplacementRefs(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("a.txt", "clean\n")
	added := repo.Commit("a.txt", secretScanTokenA+"\n")
	repo.Commit("b.txt", "b\n")
	clean := repo.Git("rev-parse", "HEAD~2")
	repo.Git("replace", added, clean)
	tip := repo.Commit("a.txt", "clean\n")
	secretScanExpect(t, secretScan(t, repo, tip, ""), secretScanWant("github_token", "a.txt", 1, added, secretScanTokenA))
}

func TestSecretScanDoesNotCountADeletedBinaryFile(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	from := repo.Commit("blob.bin", "\x00\x01\x02\n")
	repo.Git("rm", "--quiet", "blob.bin")
	repo.Git("commit", "--quiet", "-m", "remove the binary")
	tip := repo.Git("rev-parse", "HEAD")
	got := secretScan(t, repo, tip, from)
	if got.Skipped != 0 || len(got.Matches) != 0 {
		t.Errorf("skipped %d, matches %+v", got.Skipped, got.Matches)
	}
}

// A scan stopped while git is still writing reports the stop, and not the signal that ended git.
func TestSecretScanStoppedInTheMiddleOfTheHistoryReportsTheContext(t *testing.T) {
	repo := gittest.New(t)
	tip := repo.Commit("a.txt", "one\n")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = log ]; then\n\techo " + tip + "\n\texec sleep 60\nfi\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	defer cancel()
	start := time.Now()
	_, err = practicegit.SecretScanner{Env: repo.Env()}.Scan(ctx, repo.Dir, tip, "")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the error is %v, want the context's", err)
	}
	if time.Since(start) > 30*time.Second {
		t.Error("the scan did not stop")
	}
}

func TestSecretScanOfAMissingObjectFailsWithNoText(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("a.txt", secretScanTokenA+"\n")
	from := repo.Commit("b.txt", "b\n")
	tip := repo.Commit("c.txt", "c\n")
	blob := repo.Git("rev-parse", from+":a.txt")
	if err := os.Chmod(filepath.Join(repo.Dir, ".git", "objects", blob[:2]), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo.Dir, ".git", "objects", blob[:2], blob[2:])); err != nil {
		t.Fatal(err)
	}
	_, err := practicegit.SecretScanner{Env: repo.Env()}.Scan(context.Background(), repo.Dir, tip, from)
	if err == nil {
		t.Fatal("a scan of a tree with a missing blob succeeded")
	}
	if strings.Contains(err.Error(), secretScanTokenA) || strings.Contains(err.Error(), strings.Repeat("a", 36)) {
		t.Errorf("the error holds a secret's text: %v", err)
	}
}

func TestSecretScanRecordsTheResolvedCommitItStartedFrom(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("a.txt", "clean\n")
	from := repo.Commit("a.txt", "still clean\n")
	tip := repo.Commit("b.txt", "clean\n")
	repo.Git("branch", "marker", from)
	for name, revision := range map[string]string{"an abbreviated ID": from[:12], "a branch": "marker", "a relative name": "HEAD~1"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := secretScan(t, repo, tip, revision); got.ScannedFrom != from {
				t.Errorf("the scan read from %q, want %q", got.ScannedFrom, from)
			}
		})
	}
}

func TestSecretScanOfAShallowCloneIsPartial(t *testing.T) {
	t.Parallel()
	origin := gittest.New(t)
	origin.Commit("a.txt", secretScanTokenA+"\n")
	origin.Commit("a.txt", "clean\n")
	origin.Commit("b.txt", "clean\n")
	shallow := origin.ShallowClone("shallow")
	got := secretScan(t, shallow, "HEAD", "")
	if got.Partial == "" {
		t.Error("the scan of a shallow clone is not partial")
	}
	if full := secretScan(t, origin, "HEAD", ""); full.Partial != "" {
		t.Errorf("the scan of a full clone is partial: %q", full.Partial)
	}
}

func TestSecretScanOfALargeFileReportsEachMatchAtItsLine(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	var content strings.Builder
	var tokens []string
	for line := 1; line <= 12000; line++ {
		if line%3 == 0 {
			token := fmt.Sprintf("ghp_%036d", line)
			tokens = append(tokens, token)
			content.WriteString("key " + token + "\n")
			continue
		}
		fmt.Fprintf(&content, "line %d\n", line)
	}
	tip := repo.Commit("big.txt", content.String())
	got := secretScan(t, repo, tip, "")
	if len(got.Matches) != len(tokens) {
		t.Fatalf("found %d matches, want %d", len(got.Matches), len(tokens))
	}
	for i, token := range tokens {
		if want := secretScanWant("github_token", "big.txt", 3*(i+1), tip, token); got.Matches[i] != want {
			t.Fatalf("match %d is %+v, want %+v", i, got.Matches[i], want)
		}
	}
}

// secretScanKey builds a private key block of body lines, with its BEGIN and END lines, and no newline at the end.
func secretScanKey(body ...string) string {
	return "-----BEGIN " + "RSA PRIVATE KEY-----\n" + strings.Join(body, "\n") + "\n-----END " + "RSA PRIVATE KEY-----"
}

// A commit that changes lines inside a key block adds a key whose BEGIN and END lines it does not show.
func TestSecretScanFindsAPrivateKeyReplacedInPlace(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	before := secretScanKey("MIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu", "KUpRKfFLfRYC9AIKjbJTWit+CqvjWYzvQwECAwEAAQ==")
	after := secretScanKey("MIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu", "ZZpRKfFLfRYC9AIKjbJTWit+CqvjWYzvQwECAwEAAQ==")
	first := repo.Commit("keys/id_rsa", "# key\n"+before+"\n")
	replaced := repo.Commit("keys/id_rsa", "# key\n"+after+"\n")
	tip := repo.Commit("keys/id_rsa", "gone\n")
	want := []practices.SecretMatch{
		{Rule: "private_key", Path: "keys/id_rsa", Line: 2, Commit: first, Digest: secretScanDigest(before)},
		{Rule: "private_key", Path: "keys/id_rsa", Line: 2, Commit: replaced, Digest: secretScanDigest(after)},
	}
	// Matches at one line are ordered by digest.
	slices.SortFunc(want, func(a, b practices.SecretMatch) int { return strings.Compare(a.Digest, b.Digest) })
	secretScanExpect(t, secretScan(t, repo, tip, ""), want...)
}

// A key block a commit did not touch is no match of that commit.
func TestSecretScanDoesNotBlameACommitForAKeyItLeftAlone(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	key := secretScanKey("MIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu", "KUpRKfFLfRYC9AIKjbJTWit+CqvjWYzvQwECAwEAAQ==")
	from := repo.Commit("keys/id_rsa", key+"\nnotes one\n")
	repo.Commit("keys/id_rsa", key+"\nnotes two\n")
	tip := repo.Commit("keys/id_rsa", "gone\n")
	if got := secretScan(t, repo, tip, from); len(got.Matches) != 0 {
		t.Errorf("matches are %+v", got.Matches)
	}
}
