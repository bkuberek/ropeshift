#!/bin/sh
# Puts the web UI where the binary embeds it, for a GoReleaser build (D28). GoReleaser runs it before
# it builds.
#
#   release-ui.sh build    Builds the UI into web/static, as a snapshot does.
#   release-ui.sh verify   Requires the commit being built to hold the built UI and nothing else under
#                          web/static, as a release does. A release tag points at the release commit
#                          that scripts/release.sh makes.

set -eu

cd "$(git rev-parse --show-toplevel)"

fail() {
	printf 'release-ui: %s\n' "$*" >&2
	exit 1
}

case ${1:-} in
build)
	make --no-print-directory web-build
	;;
verify)
	git ls-files --error-unmatch web/static/index.html >/dev/null 2>&1 ||
		fail "this commit holds no built web UI, so go install at its tag would serve the placeholder; tag the commit scripts/release.sh makes"
	# A file the commit does not hold, such as an earlier build's output, would be embedded too.
	[ -z "$(git status --porcelain --ignored --untracked-files=all -- web/static)" ] ||
		fail "web/static differs from the commit; build the release from a clean checkout of its tag"
	;;
*)
	printf 'usage: release-ui.sh build|verify\n' >&2
	exit 2
	;;
esac
