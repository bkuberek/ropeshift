#!/bin/sh
# Builds the web UI into web/static, where the binary embeds it. GoReleaser runs it before every build,
# because a commit holds only a placeholder page there.
#
#   release-ui.sh build     Builds with the packages web/node_modules holds, installing them first only
#                           when it holds none, as make web-build does. A snapshot uses it.
#   release-ui.sh release   Clears every file git ignores under web/static, installs exactly the packages
#                           web/package-lock.json pins, then builds, so a release built on a workstation
#                           embeds what one built in CI does, and no file an earlier build left there.

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
release)
	# The binary embeds everything under web/static, and only the placeholder page there is tracked.
	git clean -fdXq -- web/static
	npm --prefix web ci
	npm --prefix web run build
	;;
*)
	printf 'usage: release-ui.sh build|release\n' >&2
	exit 2
	;;
esac

[ -f web/static/index.html ] || fail "the web build wrote no web/static/index.html"
