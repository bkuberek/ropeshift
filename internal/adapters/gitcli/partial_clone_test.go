package gitcli_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/test/gittest"
)

// A repository is a partial clone when the extension is set or a remote is a promisor, whatever the remote is named.
func TestPartialCloneReadsTheExtensionAndThePromisorRemotes(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		set  [][2]string
		want bool
	}{
		"neither":                                    {nil, false},
		"the partial clone extension":                {[][2]string{{"extensions.partialClone", "origin"}}, true},
		"an extension with no remote name":           {[][2]string{{"extensions.partialClone", ""}}, false},
		"a promisor remote":                          {[][2]string{{"remote.origin.promisor", "true"}}, true},
		"a promisor remote set as yes":               {[][2]string{{"remote.origin.promisor", "yes"}}, true},
		"a promisor remote set as 1":                 {[][2]string{{"remote.origin.promisor", "1"}}, true},
		"a remote that is no promisor":               {[][2]string{{"remote.origin.promisor", "false"}}, false},
		"a promisor remote with a space in its name": {[][2]string{{"remote.a b.promisor", "true"}}, true},
		"a promisor remote with a dot in its name":   {[][2]string{{"remote.a.b.promisor", "true"}}, true},
		"one promisor among remotes that are not":    {[][2]string{{"remote.one.promisor", "false"}, {"remote.two.promisor", "true"}}, true},
		"a setting that only ends like the key":      {[][2]string{{"remote.origin.notpromisor", "true"}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo := gittest.New(t)
			for _, setting := range tt.set {
				repo.Git("config", "--local", setting[0], setting[1])
			}
			got, err := gitcli.Git{Dir: repo.Dir, Env: repo.Env()}.PartialClone(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("PartialClone = %v, want %v", got, tt.want)
			}
		})
	}
}
