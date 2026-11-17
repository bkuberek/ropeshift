#!/bin/sh
# Optimizes the captures of a screenshot run and copies the published ones into docs/assets/screenshots
# (docs/development/screenshots.md). make screenshots-publish runs it.
#
#   screenshots-publish.sh -s SHOTS [-t TABLE] [-o DIR] [-g GUIDE]
#
#   -s SHOTS  the directory a make screenshots or make guide-shots run wrote
#   -t TABLE  the Markdown file whose table names what is published, by default docs/development/screenshots.md
#   -o DIR    where the images go, by default docs/assets/screenshots
#   -g GUIDE  publish only the rows of the guide with this prefix, whose captures are named GUIDE-NN-<theme>.png
#
# Each row of the table has a capture and the name it is published as, both in backticks, in its first two
# cells. A <theme> in a name stands for light and for dark. Every capture goes through pngquant, and the step
# stops, with nothing copied, when a capture is missing or an optimized image weighs more than MAX bytes.
# PNGQUANT names another optimizer for a test.

set -eu

top=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)

fail() {
	printf 'screenshots-publish: %s\n' "$*" >&2
	exit 1
}

# The most an image weighs, the same limit test/rules holds the docs to.
max=250000

shots=""
table="$top/docs/development/screenshots.md"
out="$top/docs/assets/screenshots"
guide=""
while getopts s:t:o:g: flag; do
	case $flag in
	s) shots=$OPTARG ;;
	t) table=$OPTARG ;;
	o) out=$OPTARG ;;
	g) guide=$OPTARG ;;
	*)
		printf 'usage: screenshots-publish.sh -s SHOTS [-t TABLE] [-o DIR] [-g GUIDE]\n' >&2
		exit 2
		;;
	esac
done

[ -n "$shots" ] || fail "set SHOTS to the directory a capture run wrote, such as make screenshots-publish SHOTS=.scratch/shots/<version>"
[ -d "$shots" ] || fail "SHOTS is $shots, which is not a directory"
[ -f "$table" ] || fail "no table at $table"
case $guide in
*[!a-z0-9-]*) fail "GUIDE is $guide, which is not a guide's prefix: lowercase letters, digits, and hyphens" ;;
esac

optimizer=${PNGQUANT:-pngquant}
command -v "$optimizer" >/dev/null 2>&1 ||
	fail "pngquant is not installed: brew install pngquant, or your distribution's package, such as apt install pngquant"

work=$(mktemp -d "${TMPDIR:-/tmp}/rs-publish.XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM

# One line per image to publish: its capture, then its published name.
# shellcheck disable=SC2016
sed -n 's/^| `\([^`]*\)` | `\([^`]*\)` |.*$/\1 \2/p' "$table" |
	while read -r capture published; do
		case $capture in
		*"<theme>"*)
			for theme in light dark; do
				printf '%s %s\n' "$(printf '%s' "$capture" | sed "s/<theme>/$theme/")" "$(printf '%s' "$published" | sed "s/<theme>/$theme/")"
			done
			;;
		*) printf '%s %s\n' "$capture" "$published" ;;
		esac
	done >"$work/all"
[ -s "$work/all" ] || fail "$table has no row naming a capture and the name it is published as"

# A guide publishes its own rows alone, both themes of each, from a run that may have captured nothing else.
if [ -n "$guide" ]; then
	while read -r capture published; do
		case $capture in
		"$guide"-[0-9][0-9]-*) printf '%s %s\n' "$capture" "$published" ;;
		esac
	done <"$work/all" >"$work/list"
	[ -s "$work/list" ] || fail "$table has no row publishing a capture of the guide $guide, named $guide-NN-<theme>.png"
else
	mv "$work/all" "$work/list"
fi

missing=""
while read -r capture published; do
	[ -f "$shots/$capture" ] || missing="$missing $capture"
done <"$work/list"
[ -z "$missing" ] || fail "$shots holds no capture of:$missing"

mkdir -p "$work/out"
count=0
total=0
over=""
while read -r capture published; do
	# pngquant stops without writing when it cannot reach the lowest quality, which is the setting's point.
	"$optimizer" --quality=70-90 --speed 1 --strip --force --output "$work/out/$published" -- "$shots/$capture" ||
		fail "pngquant did not optimize $capture; look at the capture, which may be too noisy to publish"
	size=$(wc -c <"$work/out/$published" | tr -d ' ')
	[ "$size" -le "$max" ] || over="$over $published($size)"
	count=$((count + 1))
	total=$((total + size))
done <"$work/list"
[ -z "$over" ] || fail "an image is over $max bytes; clip its capture to the element:$over"

mkdir -p "$out"
cp "$work"/out/* "$out"/
printf 'screenshots-publish: published %s images, %s bytes, into %s\n' "$count" "$total" "$out"
