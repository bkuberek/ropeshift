package ledger_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

func TestParseForgeOwnerReadsGitHubAndAnOwner(t *testing.T) {
	create, err := ledger.ParseForgeOwner("github:acme")
	if err != nil {
		t.Fatal(err)
	}
	if want := (ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: "acme", Visibility: ledger.VisibilityPrivate}); *create != want {
		t.Errorf("read %+v, want %+v", *create, want)
	}
}

func TestParseForgeOwnerRefuses(t *testing.T) {
	cases := map[string]string{
		"":                  `"" is not github:<owner>`,
		"github":            `"github" is not github:<owner>`,
		"github:":           `"github:" is not github:<owner>`,
		":acme":             `":acme" is not github:<owner>`,
		"gitlab:acme":       "creating a repository on gitlab is not supported yet; ropeshift creates repositories on GitHub",
		"github:-acme":      "is no GitHub account name",
		"github:acme--corp": "is no GitHub account name",
	}
	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			create, err := ledger.ParseForgeOwner(value)
			if err == nil || create != nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("read %+v, %v, want an error holding %q", create, err, want)
			}
		})
	}
}

func TestParseForgeOwnerNamesAnotherForgeInAnErrorACallerCanRead(t *testing.T) {
	_, err := ledger.ParseForgeOwner("gitlab:acme")
	var unsupported *ledger.UnsupportedForgeError
	if !errors.As(err, &unsupported) || unsupported.Forge != "gitlab" {
		t.Fatalf("the error is %v, want an unsupported forge naming gitlab", err)
	}
}

func TestCheckForgeRepositoryName(t *testing.T) {
	for _, name := range []string{"api", "a", "My.Repo_2-x", "...", strings.Repeat("a", 100)} {
		if err := ledger.CheckForgeRepositoryName(name); err != nil {
			t.Errorf("%q is refused: %v", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "my repo", "api/v2", "naïve", strings.Repeat("a", 101)} {
		err := ledger.CheckForgeRepositoryName(name)
		want := "a GitHub repository name has 1 to 100 letters, digits, dots, underscores, and hyphens and is not . or .., and " + `"` + name + `"` + " is not one"
		if err == nil || err.Error() != want {
			t.Errorf("%q reads %v, want %q", name, err, want)
		}
	}
}
