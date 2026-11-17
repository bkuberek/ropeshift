package version

// repository is where the project's source and docs are published.
const repository = "https://github.com/bkuberek/ropeshift"

// DocsURL is the link to a page of the repository, such as "docs/cli.md#commands", at the revision this
// build was made from: the tag of its release for a release build, which names the page as that release
// shipped it, and main for any other build.
func DocsURL(page string) string {
	return docsURL(Read("").Version, page)
}

// docsURL links page at the tag of a build that reports the release v, and at main for any other build. A
// release's tag is its version.
func docsURL(v, page string) string {
	ref := "main"
	if isRelease(v) {
		ref = v
	}
	return repository + "/blob/" + ref + "/" + page
}
