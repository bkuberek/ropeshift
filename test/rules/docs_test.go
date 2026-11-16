package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUserDocsFollowTheImageAndLinkRules holds the README and the docs directly in docs/ to the rules of
// CheckUserDocs.
func TestUserDocsFollowTheImageAndLinkRules(t *testing.T) {
	found, err := CheckUserDocs(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range found {
		t.Error(violation)
	}
}

// docsTree writes a small repository: each file of files by its path, and each image of images, by its
// path, with that many bytes.
func docsTree(t *testing.T, files map[string]string, images map[string]int) string {
	t.Helper()
	root := t.TempDir()
	if _, ok := files["README.md"]; !ok {
		write(t, filepath.Join(root, "README.md"), []byte("# Ropeshift\n"))
	}
	for name, body := range files {
		write(t, filepath.Join(root, filepath.FromSlash(name)), []byte(body))
	}
	for name, size := range images {
		write(t, filepath.Join(root, filepath.FromSlash(name)), make([]byte, size))
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(screenshots)), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func write(t *testing.T, name string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	pair = "![A view](assets/screenshots/view-light.png#gh-light-mode-only)\n![A view](assets/screenshots/view-dark.png#gh-dark-mode-only)\n"
	// the two files of pair
	small = 1000
)

func pairFiles() map[string]int {
	return map[string]int{"docs/assets/screenshots/view-light.png": small, "docs/assets/screenshots/view-dark.png": small}
}

func TestUserDocsFixturePasses(t *testing.T) {
	root := docsTree(t, map[string]string{
		"README.md": "# Ropeshift\n\n" +
			"[![CI](https://example.com/badge.svg)](docs/guide.md#a-heading)\n" +
			"![Terminal](docs/assets/screenshots/top.png)\n" +
			"![Banner](docs/assets/ropeshift-banner.png)\n" +
			"[the license](LICENSE.md)\n" +
			"```md\n[broken](nowhere.md) ![x](assets/missing.png)\n```\n" +
			"Write `[broken](nowhere.md)` as an example.\n",
		"LICENSE.md": "terms\n",
		"docs/guide.md": "# Guide\n\n## A heading\n\n## A heading\n\n## Run `ropeshift top` [now](other.md)\n\n" + pair +
			"[again](#a-heading-1) [there](other.md#run-ropeshift-top-now) [dir](development)\n",
		"docs/other.md":                 "# Other\n\n## Run `ropeshift top` [now](guide.md)\n",
		"docs/development/internals.md": "[broken](nowhere.md) open source\n",
		"docs/development/screenshots.md": "| Capture | Published as | Shown in |\n| --- | --- | --- |\n" +
			"| `view-<theme>-1440.png` | `view-<theme>.png` | Guide |\n| `top.png` | `top.png` | README |\n",
	}, map[string]int{
		"docs/assets/screenshots/top.png":        small,
		"docs/assets/ropeshift-banner.png":       small,
		"docs/assets/screenshots/view-light.png": small,
		"docs/assets/screenshots/view-dark.png":  small,
	})
	if found, err := CheckUserDocs(root); err != nil || len(found) > 0 {
		t.Errorf("a sound tree has violations %v (%v)", found, err)
	}
}

// TestUserDocsRulesCanFail proves each rule can fail: every fixture is a small tree that breaks one rule.
func TestUserDocsRulesCanFail(t *testing.T) {
	tests := []struct {
		name   string
		files  map[string]string
		images map[string]int
		want   string
	}{
		{
			"an image that is missing",
			map[string]string{"README.md": "![A view](docs/assets/screenshots/gone-light.png#gh-light-mode-only)\n![A view](docs/assets/screenshots/gone-dark.png#gh-dark-mode-only)\n"},
			nil,
			"the image docs/assets/screenshots/gone-light.png does not exist",
		},
		{
			"an image over the size limit",
			map[string]string{"docs/guide.md": pair},
			map[string]int{"docs/assets/screenshots/view-light.png": MaxImageBytes + 1, "docs/assets/screenshots/view-dark.png": small},
			"docs/guide.md:1: docs/assets/screenshots/view-light.png is 250001 bytes, over the 250000 limit",
		},
		{
			"an image with capitals",
			map[string]string{"docs/guide.md": "![A view](assets/screenshots/View-light.png#gh-light-mode-only)\n![A view](assets/screenshots/View-dark.png#gh-dark-mode-only)\n"},
			map[string]int{"docs/assets/screenshots/View-light.png": small, "docs/assets/screenshots/View-dark.png": small},
			"is not a lowercase hyphenated .png under docs/assets",
		},
		{
			"an image that is not a png",
			map[string]string{"docs/guide.md": "![A view](assets/screenshots/view-light.jpg#gh-light-mode-only)\n![A view](assets/screenshots/view-dark.jpg#gh-dark-mode-only)\n"},
			map[string]int{"docs/assets/screenshots/view-light.jpg": small, "docs/assets/screenshots/view-dark.jpg": small},
			"is not a lowercase hyphenated .png under docs/assets",
		},
		{
			"an image outside docs/assets",
			map[string]string{"docs/guide.md": "![A view](pictures/view.png)\n"},
			map[string]int{"docs/pictures/view.png": small},
			"docs/pictures/view.png is not a lowercase hyphenated .png under docs/assets",
		},
		{
			"a light file with no dark file",
			map[string]string{"docs/guide.md": "![A view](assets/screenshots/view-light.png#gh-light-mode-only)\n\ntext\n"},
			pairFiles(),
			"view-light.png has no dark file after it",
		},
		{
			"a dark file with no light file",
			map[string]string{"docs/guide.md": "![A view](assets/screenshots/view-dark.png#gh-dark-mode-only)\n"},
			pairFiles(),
			"view-dark.png has no light file before it",
		},
		{
			"a dark file before its light file",
			map[string]string{"docs/guide.md": "![A view](assets/screenshots/view-dark.png#gh-dark-mode-only)\n![A view](assets/screenshots/view-light.png#gh-light-mode-only)\n"},
			pairFiles(),
			"view-dark.png has no light file before it",
		},
		{
			"a dark file that follows another view's light file",
			map[string]string{"docs/guide.md": "![A](assets/screenshots/a-light.png#gh-light-mode-only)\n![B](assets/screenshots/view-dark.png#gh-dark-mode-only)\n"},
			map[string]int{"docs/assets/screenshots/a-light.png": small, "docs/assets/screenshots/view-dark.png": small},
			"view-dark.png has no light file before it",
		},
		{
			"a light file without its fragment",
			map[string]string{"docs/guide.md": "![A view](assets/screenshots/view-light.png)\n![A view](assets/screenshots/view-dark.png#gh-dark-mode-only)\n"},
			pairFiles(),
			"is a light file with #gh-light-mode-only or a dark file with #gh-dark-mode-only",
		},
		{
			"a single file that takes a fragment",
			map[string]string{"README.md": "![Terminal](docs/assets/screenshots/top.png#gh-dark-mode-only)\n"},
			map[string]int{"docs/assets/screenshots/top.png": small},
			"docs/assets/screenshots/top.png has a single file and takes no #gh-dark-mode-only",
		},
		{
			"an image no doc shows",
			map[string]string{"README.md": "# Ropeshift\n"},
			map[string]int{"docs/assets/screenshots/orphan-light.png": small},
			"docs/assets/screenshots/orphan-light.png: no user doc shows this image",
		},
		{
			"an image the table of published images does not list",
			map[string]string{
				"README.md":                       "![A](docs/assets/screenshots/a-light.png#gh-light-mode-only)\n![A](docs/assets/screenshots/a-dark.png#gh-dark-mode-only)\n",
				"docs/development/screenshots.md": "| `a-<theme>-1440.png` | `b-<theme>.png` | README |\n",
			},
			map[string]int{"docs/assets/screenshots/a-light.png": small, "docs/assets/screenshots/a-dark.png": small},
			"docs/assets/screenshots/a-light.png: docs/development/screenshots.md has no row that publishes this image",
		},
		{
			"a row of the table that publishes an image that is missing",
			map[string]string{"docs/development/screenshots.md": "| `a-<theme>-1440.png` | `a-<theme>.png` | README |\n"},
			nil,
			"a row publishes a-light.png, which is not in docs/assets/screenshots",
		},
		{
			"a relative link to a file that is missing",
			map[string]string{"docs/guide.md": "See [the CLI](cli.md).\n"},
			nil,
			"docs/guide.md:1: cli.md does not resolve",
		},
		{
			"a relative link that leaves the repository",
			map[string]string{"docs/guide.md": "See [the CLI](../../cli.md).\n"},
			nil,
			"../../cli.md does not resolve",
		},
		{
			"a link that is an absolute path",
			map[string]string{"docs/guide.md": "See [the CLI](/docs/cli.md).\n"},
			nil,
			"/docs/cli.md is not relative",
		},
		{
			"an anchor in the same doc that is missing",
			map[string]string{"docs/guide.md": "# Guide\n\nSee [below](#nowhere).\n"},
			nil,
			"docs/guide.md has no heading #nowhere",
		},
		{
			"an anchor in another doc that is missing",
			map[string]string{"docs/guide.md": "See [it](other.md#nowhere).\n", "docs/other.md": "# Other\n"},
			nil,
			"docs/other.md has no heading #nowhere",
		},
		{
			"a link in the README to a doc that is missing",
			map[string]string{"README.md": "[![Platforms](https://example.com/b.svg)](docs/onboarding.md)\n"},
			nil,
			"README.md:1: docs/onboarding.md does not resolve",
		},
		{
			"a link to the product repository on the forge",
			map[string]string{"docs/guide.md": "See [the daemon](https://github.com/someone/ropeshift/blob/main/cmd/ropeshiftd/main.go).\n"},
			nil,
			"link the file by a relative path, not on the forge",
		},
		{
			"a link to a directory of the product repository on the forge",
			map[string]string{"docs/guide.md": "See [the tests](https://github.com/someone/ropeshift/tree/main/test).\n"},
			nil,
			"link the file by a relative path, not on the forge",
		},
		{
			"the product called open source",
			map[string]string{"docs/guide.md": "Ropeshift is an Open Source factory.\n"},
			nil,
			"the product is fair source, never open source",
		},
		{
			"the product called open-source",
			map[string]string{"docs/guide.md": "An open-source factory.\n"},
			nil,
			"the product is fair source, never open source",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, err := CheckUserDocs(docsTree(t, tt.files, tt.images))
			if err != nil {
				t.Fatal(err)
			}
			for _, violation := range found {
				if strings.Contains(violation, tt.want) {
					return
				}
			}
			t.Errorf("no violation holds %q in %v", tt.want, found)
		})
	}
}

func TestHeadingAnchors(t *testing.T) {
	text := "# Get started\n\n## What `init` does, and why\n\n## Step 2: sign in (once)\n\n## Step 2: sign in (once)\n\n" +
		"```sh\n# not a heading\n```\n\n### [A link](x.md) title ###\n"
	want := []string{"get-started", "what-init-does-and-why", "step-2-sign-in-once", "step-2-sign-in-once-1", "a-link-title"}
	got := anchorsOf(text)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v, want %v", got, want)
	}
}
