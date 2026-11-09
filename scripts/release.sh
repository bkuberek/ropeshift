#!/bin/sh
# Tags a release by hand, for when GitHub Actions is unavailable (docs/releasing.md#releasing-by-hand).
# The release workflow does this itself: release-please tags the commit where the release pull request
# merged into main and creates the release, and GoReleaser uploads the release's artifacts to it.
#
# Run it on that merge commit. It checks that the commit is where the release pull request for this
# version merged, tags it, writes the release notes, which are the version's section of CHANGELOG.md
# that release-please wrote, to a temporary file, pushes nothing, and prints the commands that publish
# the release from the tag.
#
# Usage: scripts/release.sh vMAJOR.MINOR.PATCH[-alpha.N|-beta.N|-rc.N]
#
# ROPESHIFT_RELEASE_BRANCH names the branch a release is tagged on, main by default.

set -eu

fail() {
	printf 'release: %s\n' "$*" >&2
	exit 1
}

[ $# -eq 1 ] || {
	printf 'usage: scripts/release.sh vMAJOR.MINOR.PATCH[-alpha.N|-beta.N|-rc.N]\n' >&2
	exit 2
}
version=$1
plain=${version#v}
branch=${ROPESHIFT_RELEASE_BRANCH:-main}

# The versioning policy, as docs/versioning.md states it.
printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.[1-9][0-9]*)?$' ||
	fail "$version is not a release version: use vMAJOR.MINOR.PATCH, with -alpha.N, -beta.N, or -rc.N and N from 1 for a pre-release (docs/versioning.md)"

cd "$(git rev-parse --show-toplevel)"

[ -z "$(git status --porcelain)" ] || fail "the tree has changes; a release holds only committed work"
! git rev-parse --quiet --verify "refs/tags/$version" >/dev/null || fail "the tag $version already exists"
git rev-parse --quiet --verify "refs/heads/$branch" >/dev/null || fail "there is no branch $branch"

head=$(git rev-parse HEAD)
short=$(git rev-parse --short HEAD)

# A pull request merges into the branch as a commit on its first-parent history. The pull request's own
# commits are reachable from the branch too, but none of them is where it merged.
git rev-list --first-parent "refs/heads/$branch" | grep -qxF "$head" ||
	fail "$short is not on $branch's first-parent history; check out the commit where the release pull request merged into $branch"

# The version .release-please-manifest.json names at a commit, or nothing.
manifest_version() {
	git show "$1:.release-please-manifest.json" 2>/dev/null | while IFS= read -r line; do
		case $line in
		*'"."'*:*)
			v=${line#*:}
			v=${v#*\"}
			printf '%s\n' "${v%%\"*}"
			;;
		esac
	done
}

# The release pull request is the change that sets the manifest to this version.
at=$(manifest_version "$head")
[ "$at" = "$plain" ] ||
	fail "$short is not where the release pull request for $plain merged: its manifest names ${at:-no version}"
[ "$(manifest_version "$head^1")" != "$plain" ] ||
	fail "$short is not where the release pull request for $plain merged: the commit before it on $branch already names that version"

# The release notes: the version's heading in CHANGELOG.md, and every line up to the next heading of
# that level. release-please puts the same section on the release it creates.
notes=$(mktemp "${TMPDIR:-/tmp}/ropeshift-$version-notes.XXXXXX")
trap 'rm -f "$notes"' EXIT
git show "$head:CHANGELOG.md" 2>/dev/null | {
	in=
	while IFS= read -r line || [ -n "$line" ]; do
		case $line in
		"## $plain "* | "## [$plain]"*) in=1 ;;
		"## "*) [ -z "$in" ] || break ;;
		esac
		[ -z "$in" ] || printf '%s\n' "$line"
	done
} >"$notes"
[ -s "$notes" ] || fail "CHANGELOG.md at $short holds no section for $plain, so the release would have no notes"

git tag --annotate "$version" --message "ropeshift $version"
trap - EXIT

prerelease=
case $plain in
*-*) prerelease=' --prerelease' ;;
esac

printf 'release: tagged %s at %s, where the release pull request merged into %s.\n' "$version" "$head" "$branch"
printf 'release: wrote the release notes, the section of CHANGELOG.md for %s, to %s.\n' "$plain" "$notes"
printf 'release: nothing was pushed. To publish the release, with GITHUB_TOKEN set to a token that can write releases:\n\n'
printf '  git push origin %s\n' "$version"
printf '  gh release create %s --verify-tag --title %s --notes-file %s%s\n' "$version" "$version" "$notes" "$prerelease"
printf '  GORELEASER_CURRENT_TAG=%s goreleaser release --clean\n' "$version"
printf '  sh scripts/homebrew-formula.sh dist/checksums.txt "" dist/homebrew/ropeshift.rb\n'
