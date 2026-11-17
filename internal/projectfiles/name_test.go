package projectfiles_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/projectfiles"
)

// A folder's name makes a project name as `ropeshift new`, `adopt`, and a worker writing a folder for the web UI all make one.
func TestNameOfMakesAProjectNameOfAFolder(t *testing.T) {
	cases := map[string]string{
		"harbor":         "harbor",
		"Acme Shop":      "acme-shop",
		"__acme__shop__": "acme-shop",
		"api.v2":         "api-v2",
		"___":            "",
		"":               "",
	}
	for folder, want := range cases {
		if got := projectfiles.NameOf(folder); got != want {
			t.Errorf("NameOf(%q) = %q, want %q", folder, got, want)
		}
	}
	long := projectfiles.NameOf(strings.Repeat("a", 62) + " b")
	if len(long) > 63 || strings.HasSuffix(long, "-") {
		t.Errorf("a long name is %q, want at most 63 characters with no trailing hyphen", long)
	}
}
