package practicegit

import (
	"context"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
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
