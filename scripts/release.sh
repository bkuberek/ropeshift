#!/bin/sh
# Makes a release's commit and tag, and pushes nothing (D28).
#
# The branch never holds the web UI's build, so a plain checkout builds with no Node.js. A release
# tag must hold it, because go install at the tag builds whatever the tag's tree holds. This script
# builds the UI, commits it with --force on top of the branch's head, tags that commit, and returns
# to the branch, which stays as it was. docs/releasing.md describes the steps around it.
#
# Usage: scripts/release.sh vMAJOR.MINOR.PATCH[-alpha.N|-beta.N|-rc.N]
#
# ROPESHIFT_RELEASE_BRANCH names the branch a release is cut from, main by default.

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
branch=${ROPESHIFT_RELEASE_BRANCH:-main}

# The versioning policy, as docs/versioning.md states it.
printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.[1-9][0-9]*)?$' ||
	fail "$version is not a release version: use vMAJOR.MINOR.PATCH, with -alpha.N, -beta.N, or -rc.N and N from 1 for a pre-release (docs/versioning.md)"

cd "$(git rev-parse --show-toplevel)"

current=$(git symbolic-ref --quiet --short HEAD || true)
[ "$current" = "$branch" ] || fail "a release is cut from $branch, and this checkout is on ${current:-a detached HEAD}"
[ -z "$(git status --porcelain)" ] || fail "the tree has changes; a release holds only committed work"
! git rev-parse --quiet --verify "refs/tags/$version" >/dev/null || fail "the tag $version already exists"

# The newest release tag before this one, which the release notes start from.
previous=$(git for-each-ref --sort=-creatordate --count=1 --format='%(refname:short)' 'refs/tags/v[0-9]*')
head=$(git rev-parse HEAD)

npm --prefix web ci
npm --prefix web run build
[ -f web/static/index.html ] || fail "the web build wrote no web/static/index.html"

# From here, any failure puts the checkout back on the branch.
trap 'git checkout --quiet --force "$branch" >/dev/null 2>&1 || true' EXIT
git checkout --quiet --detach
git add --force web/static
git commit --quiet --message "chore(release): $version" --message "The built web UI, which only a release tag holds (D28)."
git tag --annotate "$version" --message "ropeshift $version"
release=$(git rev-parse HEAD)
git checkout --quiet "$branch"
trap - EXIT

[ "$(git rev-parse HEAD)" = "$head" ] || fail "the branch moved during the release"

printf 'release: tagged %s at %s, a commit on top of %s (%s) that adds the built web UI.\n' "$version" "$release" "$branch" "$head"
printf 'release: nothing was pushed. To publish it:\n\n'
printf '  git push origin %s\n' "$version"
printf '  git checkout %s\n' "$version"
if [ -n "$previous" ]; then
	printf '  GORELEASER_PREVIOUS_TAG=%s GORELEASER_CURRENT_TAG=%s goreleaser release --clean\n' "$previous" "$version"
else
	printf '  GORELEASER_CURRENT_TAG=%s goreleaser release --clean\n' "$version"
fi
printf '  sh scripts/homebrew-formula.sh dist/checksums.txt "" dist/homebrew/ropeshift.rb\n'
printf '  git checkout %s\n' "$branch"
