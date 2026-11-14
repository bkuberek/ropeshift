#!/bin/sh
# Captures every view of the web UI and `ropeshift top` on a sample project, for the README and the
# website (docs/screenshots.md). make screenshots runs it.
#
#   screenshots.sh [-o DIR] [-r REF] [-k]
#
#   -o DIR   where the images go, by default .scratch/shots/<version>
#   -r REF   a commit or tag to build, by default this checkout as it stands
#   -k       keep the isolated installation the run makes, for debugging
#
# It builds the web UI and the binary, then runs test/screenshots with the screenshots tag, which makes an
# isolated installation, starts the daemon on a loopback port the kernel chooses, lays out the sample with
# the fake agent CLI, and captures with Playwright's Chromium from web/. The run stops what it started.

set -eu

cd "$(git rev-parse --show-toplevel)"
top=$(pwd)

fail() {
	printf 'screenshots: %s\n' "$*" >&2
	exit 1
}

out=""
ref=""
keep=""
while getopts o:r:k flag; do
	case $flag in
	o) out=$OPTARG ;;
	r) ref=$OPTARG ;;
	k) keep=1 ;;
	*)
		printf 'usage: screenshots.sh [-o DIR] [-r REF] [-k]\n' >&2
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
	version=$(git describe --tags --always "$commit")
	source="$build/source"
	mkdir "$source"
	git archive --format=tar "$commit" | tar -x -C "$source"
	npm --prefix "$source/web" ci --no-audit --no-fund
	npm --prefix "$source/web" run build
else
	commit=$(git rev-parse HEAD)
	version=$(git describe --tags --always --dirty)
	source=$top
	make --no-print-directory web-build
fi
[ -f "$source/web/static/index.html" ] || fail "the web build wrote no web/static/index.html"
date=$(git show -s --format=%cI "$commit")

stamp="ropeshift.com/ropeshift/internal/version"
(cd "$source" && CGO_ENABLED=0 go build -trimpath \
	-ldflags "-X $stamp.version=$version -X $stamp.commit=$commit -X $stamp.date=$date" \
	-o "$build/ropeshift" ./cmd/ropeshift)

out=${out:-.scratch/shots/$version}
mkdir -p "$out"
out=$(cd "$out" && pwd)

# The capture runs from this checkout's web/, whatever was built.
[ -d web/node_modules ] || npm --prefix web ci --no-audit --no-fund
if ! (cd web && node --input-type=module -e "import { chromium } from 'playwright-core'; await (await chromium.launch()).close();") >/dev/null 2>&1; then
	(cd web && npx --no-install playwright-core install chromium-headless-shell)
fi

printf 'screenshots: capturing %s into %s\n' "$version" "$out"
go test -tags screenshots -count=1 -timeout 20m -run '^TestCapture$' ./test/screenshots \
	-args -binary "$build/ropeshift" -out "$out" ${keep:+-keep}
printf 'screenshots: wrote %s\n' "$out"
