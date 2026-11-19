package practicegit

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/gittest"
)

func secretScanPatch(t *testing.T, stream string) *secretFindings {
	t.Helper()
	found := newSecretFindings()
	if err := found.readPatches(strings.NewReader(stream)); err != nil {
		t.Fatal(err)
	}
	return found
}

const secretScanHash = "0123456789abcdef0123456789abcdef01234567"

// A hunk that holds context lines, one with a space and a blank one, reads on to the lines it still owes, and the
// lines it adds keep their numbers in the file.
func TestSecretScanPatchReaderReadsContextLines(t *testing.T) {
	t.Parallel()
	token := "ghp_" + strings.Repeat("a", 36)
	stream := secretScanHash + "\n\ndiff --git a/f.txt b/f.txt\nindex 1..2 100644\n--- a/f.txt\n+++ b/f.txt\n" +
		"@@ -1,5 +1,5 @@\n-one\n+first\n two\n\n three\n-four\n+" + token + "\n"
	got := secretScanPatch(t, stream).result("")
	if len(got.Matches) != 1 || got.Matches[0].Line != 5 || got.Matches[0].Path != "f.txt" || got.Matches[0].Commit != secretScanHash {
		t.Errorf("matches are %+v", got.Matches)
	}
}

func TestSecretScanPatchReaderCountsOnlyAChangedBinaryFile(t *testing.T) {
	t.Parallel()
	stream := secretScanHash + "\n\ndiff --git a/gone.bin b/gone.bin\ndeleted file mode 100644\nindex 1..0\nBinary files a/gone.bin and /dev/null differ\n" +
		"diff --git a/kept.bin b/kept.bin\nindex 1..2 100644\nBinary files a/kept.bin and b/kept.bin differ\n" +
		"diff --git a/new.bin b/new.bin\nnew file mode 100644\nindex 0..2\nBinary files /dev/null and b/new.bin differ\n"
	got := secretScanPatch(t, stream).result("")
	if got.Skipped != 2 {
		t.Errorf("skipped %d files, want the two that still exist", got.Skipped)
	}
}

func TestSecretScanBlobReaderErrorsHoldNoText(t *testing.T) {
	t.Parallel()
	text := "ghp_" + strings.Repeat("a", 36)
	for name, stream := range map[string]string{
		"a header made of blob bytes": text + " " + text + " " + text + "\n",
		"a missing object":            text + " missing\n",
		"a size that is no number":    "abc blob " + text + "\n",
		"a short blob":                "abc blob 50\n" + text,
		"no answer":                   "",
	} {
		err := readBlobs(strings.NewReader(stream), []string{"abc"}, func(string, []byte) {})
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if strings.Contains(err.Error(), "aaaaaaaa") {
			t.Errorf("%s: the error holds text: %v", name, err)
		}
	}
	var seen []string
	err := readBlobs(strings.NewReader("abc blob 3\nxyz\n"), []string{"abc"}, func(object string, content []byte) { seen = append(seen, object+string(content)) })
	if err != nil || len(seen) != 1 || seen[0] != "abcxyz" {
		t.Errorf("a blob reads %v, %v", seen, err)
	}
}

// The settings every command of the scan runs under keep the repository's own configuration from changing what it
// reads.
func TestSecretScanEnvironmentOverridesRepositoryConfiguration(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Git("config", "core.fsmonitor", "/bin/true")
	repo.Git("config", "core.quotePath", "true")
	git := gitcli.Git{Dir: repo.Dir, Env: repo.Env(), Extra: secretScanEnvironment}
	for key, want := range map[string]string{"core.fsmonitor": "false", "core.quotePath": "false"} {
		got, err := git.Text(context.Background(), "config", "--get", key)
		if err != nil || got != want {
			t.Errorf("%s reads %q, %v; want %q", key, got, err, want)
		}
	}
	if !strings.Contains(strings.Join(secretScanEnvironment, " "), "GIT_NO_REPLACE_OBJECTS=1") {
		t.Errorf("the environment is %v", secretScanEnvironment)
	}
}

// A blob that several paths hold is scanned once, and its matches are reported for each path.
func TestSecretScanTreeScansABlobOnceForAllItsPaths(t *testing.T) {
	t.Parallel()
	token := "ghp_" + strings.Repeat("a", 36)
	repo := gittest.New(t)
	repo.Commit("one/key.txt", "key "+token+"\n")
	repo.Commit("two/key.txt", "key "+token+"\n")
	tip := repo.Commit("three/key.txt", "key "+token+"\n")
	found := newSecretFindings()
	scans := 0
	found.find = func(text string) []ledger.SecretMatch {
		scans++
		return ledger.FindSecrets(text)
	}
	git := gitcli.Git{Dir: repo.Dir, Env: repo.Env(), Extra: secretScanEnvironment}
	if err := found.scanTree(context.Background(), git, tip); err != nil {
		t.Fatal(err)
	}
	if scans != 1 {
		t.Errorf("the blob was scanned %d times, want once", scans)
	}
	if got := found.result("").Matches; len(got) != 3 || got[0].Path != "one/key.txt" || got[1].Path != "three/key.txt" || got[2].Path != "two/key.txt" {
		t.Errorf("matches are %+v", got)
	}
}

// The changed files a history scan has to read again are handed on in runs of flushAfter, and are no longer held.
func TestSecretScanPatchReaderHandsOnTheChangedFilesInRuns(t *testing.T) {
	t.Parallel()
	var stream strings.Builder
	stream.WriteString(secretScanHash + "\n\n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&stream, "diff --git a/f%d b/f%d\nindex %040x..%040x 100644\n--- a/f%d\n+++ b/f%d\n@@ -1 +1 @@\n-x\n+y\n", i, i, i, i+100, i, i)
	}
	found := newSecretFindings()
	found.flushAfter = 2
	var runs []int
	found.flush = func(touched map[string][]secretTouch) error {
		runs = append(runs, len(touched))
		return nil
	}
	if err := found.readPatches(strings.NewReader(stream.String())); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(runs) != "[2 2]" || len(found.touched) != 1 {
		t.Errorf("runs are %v and %d files are held, want [2 2] and 1", runs, len(found.touched))
	}
}

// Reading the changed files in runs finds what reading them at once does.
func TestSecretScanInRunsFindsWhatOneReadFinds(t *testing.T) {
	t.Parallel()
	key := func(last string) string {
		return "-----BEGIN " + "RSA PRIVATE KEY-----\nMIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu\n" + last + "\n-----END " + "RSA PRIVATE KEY-----\n"
	}
	repo := gittest.New(t)
	repo.Commit("keys/one", key("AAAAAAAAAAAAAAAA"))
	repo.Commit("keys/two", key("BBBBBBBBBBBBBBBB"))
	for i := range 4 {
		repo.Commit("keys/one", key(fmt.Sprintf("CCCCCCCCCCCCCC%02d", i)))
		repo.Commit("keys/two", key(fmt.Sprintf("DDDDDDDDDDDDDD%02d", i)))
	}
	tip := repo.Commit("keys/one", "gone\n")
	whole, err := SecretScanner{Env: repo.Env()}.Scan(context.Background(), repo.Dir, tip, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(whole.Matches) != 10 {
		t.Fatalf("one read found %d matches, want 10: %+v", len(whole.Matches), whole.Matches)
	}
	for _, runLength := range []int{1, 3} {
		runs, err := SecretScanner{Env: repo.Env(), flushAfter: runLength}.Scan(context.Background(), repo.Dir, tip, "")
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(runs.Matches) != fmt.Sprint(whole.Matches) || runs.Skipped != whole.Skipped {
			t.Errorf("runs of %d find %+v, want %+v", runLength, runs.Matches, whole.Matches)
		}
	}
}
