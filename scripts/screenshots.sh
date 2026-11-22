#!/bin/sh
# Captures every view of the web UI and `ropeshift top` on a sample project, for the README and the
# website (docs/development/screenshots.md). make screenshots runs it.
#
#   screenshots.sh [-o DIR] [-r REF] [-k] [-g GUIDE | -G | -c | -s]
#
#   -o DIR   where the images go, by default .scratch/shots/<version>
#   -r REF   a commit or tag to build, by default this checkout as it stands
#   -k       keep the isolated installation the run makes, for debugging
#   -G       walk the guides (test/screenshots/guides) in place of the views, and capture every guide
#   -g GUIDE like -G, but capture only the guide with this prefix, after walking the guides before it
#   -c       run the product gap checks of the guide walk (PG6 and PG9) in place of a capture
#   -s       run the group scenario in place of a capture: a group server and two workers, each in an isolated
#            installation, joined with a join string. It needs initdb and postgres on PATH or in
#            ROPESHIFT_TEST_PG_BIN, as make check-postgres does.
#
# It builds the web UI and the binary with the screenshots tag, which reports a fixed machine and worker ID,
# then runs test/screenshots with the same tag, which makes an isolated installation, starts the daemon on a
# loopback port the kernel chooses, lays out the sample with the fake agent CLI, and captures with
# Playwright's Chromium from web/. Playwright's browsers live in .scratch/playwright unless
# PLAYWRIGHT_BROWSERS_PATH names another directory. The run stops what it started.
# A guide walk makes one installation for each theme and world, and the guides' neutral clone folder, which
# it removes at its end (docs/development/screenshots.md).

set -eu

cd "$(git rev-parse --show-toplevel)"
top=$(pwd)

# Playwright's browsers go in a directory of this checkout, which git ignores, and never in the cache that
# every other project on the machine shares. Every npm and node step below inherits it.
PLAYWRIGHT_BROWSERS_PATH=${PLAYWRIGHT_BROWSERS_PATH:-$top/.scratch/playwright}
export PLAYWRIGHT_BROWSERS_PATH

fail() {
	printf 'screenshots: %s\n' "$*" >&2
	exit 1
}

out=""
ref=""
keep=""
walk=""
guide=""
checks=""
scenario=""
while getopts o:r:kGg:cs flag; do
	case $flag in
	c) checks=1 ;;
	s) scenario=1 ;;
	o) out=$OPTARG ;;
	r) ref=$OPTARG ;;
	k) keep=1 ;;
	G) walk=1 ;;
	g)
		walk=1
		guide=$OPTARG
		;;
	*)
		printf 'usage: screenshots.sh [-o DIR] [-r REF] [-k] [-g GUIDE | -G | -c | -s]\n' >&2
		exit 2
		;;
	esac
done

command -v node >/dev/null 2>&1 || fail "node is not on PATH"
command -v tmux >/dev/null 2>&1 || fail "tmux is not on PATH; the planning session runs in a pane"

build=$(mktemp -d "${TMPDIR:-/tmp}/rs-shots-build.XXXXXX")
trap 'rm -rf "$build"' EXIT INT TERM

if [ -n "$ref" ]; then
	commit=$(git rev-parse --verify --quiet "$ref^{commit}") || fail "no commit $ref here; fetch it first"
	version=$(git describe --tags --abbrev=0 "$commit" 2>/dev/null || git rev-parse --short "$commit")
	source="$build/source"
	mkdir "$source"
	git archive --format=tar "$commit" | tar -x -C "$source"
	npm --prefix "$source/web" ci --no-audit --no-fund
	npm --prefix "$source/web" run build
else
	commit=$(git rev-parse HEAD)
	version=$(git describe --tags --abbrev=0 2>/dev/null || git rev-parse --short HEAD)
	source=$top
	make --no-print-directory web-build
fi
[ -f "$source/web/static/index.html" ] || fail "the web build wrote no web/static/index.html"
date=$(git show -s --format=%cI "$commit")

stamp="ropeshift.com/ropeshift/internal/version"
# The screenshots tag reports a fixed machine and worker ID, so the images show nothing of this machine. A
# ref from before the tag reports the machine it runs on.
(cd "$source" && CGO_ENABLED=0 go build -trimpath -tags screenshots \
	-ldflags "-X $stamp.version=$version -X $stamp.commit=$commit -X $stamp.date=$date" \
	-o "$build/ropeshift" ./cmd/ropeshift)

out=${out:-.scratch/shots/$version}
mkdir -p "$out"
out=$(cd "$out" && pwd)

# The capture runs from this checkout's web/, whatever was built.
[ -d web/node_modules ] || npm --prefix web ci --no-audit --no-fund
if ! (cd web && node --input-type=module -e "import { chromium } from 'playwright-core'; await (await chromium.launch({ executablePath: process.env.ROPESHIFT_TEST_CHROME || undefined })).close();") >/dev/null 2>&1; then
	(cd web && npx --no-install playwright-core install chromium-headless-shell)
fi

printf 'screenshots: capturing %s into %s\n' "$version" "$out"
if [ -n "$scenario" ]; then
	go test -tags 'screenshots postgres' -count=1 -timeout 20m -v -run '^TestGroupScenario$' ./test/screenshots \
		-args -binary "$build/ropeshift" -out "$out" ${keep:+-keep}
elif [ -n "$checks" ]; then
	go test -tags screenshots -count=1 -timeout 30m -v -run '^(TestNoCommandSpendsAJoinToken|TestAssistantAnswersInALaterProject)$' ./test/screenshots \
		-args -binary "$build/ropeshift" -out "$out" ${keep:+-keep}
elif [ -n "$walk" ]; then
	go test -tags screenshots -count=1 -timeout 60m -run '^TestGuides$' ./test/screenshots \
		-args -binary "$build/ropeshift" -out "$out" ${keep:+-keep} ${guide:+-guide "$guide"}
else
	go test -tags screenshots -count=1 -timeout 20m -run '^TestCapture$' ./test/screenshots \
		-args -binary "$build/ropeshift" -out "$out" ${keep:+-keep}
fi
printf 'screenshots: wrote %s\n' "$out"
