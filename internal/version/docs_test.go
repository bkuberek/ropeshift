package version

import "testing"

func TestDocsLinkToTheTagOfAReleaseAndToMainOtherwise(t *testing.T) {
	const page = "docs/troubleshooting.md#database-refuses-to-start"
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{"a release", "v0.4.1", "https://github.com/bkuberek/ropeshift/blob/v0.4.1/" + page},
		{"a pre-release", "v0.1.0-alpha.17", "https://github.com/bkuberek/ropeshift/blob/v0.1.0-alpha.17/" + page},
		{"a release candidate", "v0.1.0-rc.1", "https://github.com/bkuberek/ropeshift/blob/v0.1.0-rc.1/" + page},
		{"a beta", "v0.2.0-beta.3", "https://github.com/bkuberek/ropeshift/blob/v0.2.0-beta.3/" + page},
		{"a snapshot", "v0.0.1-snapshot-1a2b3c4", "https://github.com/bkuberek/ropeshift/blob/main/" + page},
		{"a dirty pre-release", "v0.1.0-rc.1-dirty", "https://github.com/bkuberek/ropeshift/blob/main/" + page},
		{"a dev pre-release", "v0.1.0-dev", "https://github.com/bkuberek/ropeshift/blob/main/" + page},
		{"a development build", Development, "https://github.com/bkuberek/ropeshift/blob/main/" + page},
		{"no version", "", "https://github.com/bkuberek/ropeshift/blob/main/" + page},
		{"a pseudo-version", "v0.0.0-20261107230936-dd81c214342b", "https://github.com/bkuberek/ropeshift/blob/main/" + page},
		{"a tree with changes", "v0.4.1+dirty", "https://github.com/bkuberek/ropeshift/blob/main/" + page},
		{"a version that is not a release", "v0.4", "https://github.com/bkuberek/ropeshift/blob/main/" + page},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := docsURL(tt.version, page); got != tt.want {
				t.Errorf("docsURL(%q) is %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}

// TestADocsLinkFollowsTheVersionABuildReports holds the link to the version a build reports: a stamped
// release names its tag, and a build the toolchain recorded a pseudo-version for names main.
func TestADocsLinkFollowsTheVersionABuildReports(t *testing.T) {
	const page = "docs/cli.md"
	stamped := resolve("ropeshift", stamp{version: "v0.4.1"}, built("(devel)"), true)
	if got, want := docsURL(stamped.Version, page), "https://github.com/bkuberek/ropeshift/blob/v0.4.1/docs/cli.md"; got != want {
		t.Errorf("a stamped release links %q, want %q", got, want)
	}
	pseudo := resolve("ropeshift", stamp{}, built("v0.0.0-20261107230936-dd81c214342b", vcs(revision, "false")...), true)
	if got, want := docsURL(pseudo.Version, page), "https://github.com/bkuberek/ropeshift/blob/main/docs/cli.md"; got != want {
		t.Errorf("a development build links %q, want %q", got, want)
	}
}
