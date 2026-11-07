#!/bin/sh
# Installs a ropeshift release: one binary, named ropeshift, with ropeshiftd as a link to it.
#
# It downloads the archive for this machine and the release's checksums file, checks the archive's
# SHA-256 against that file before it extracts anything, and refuses on any mismatch. It needs no
# root unless the chosen prefix does. docs/releasing.md describes how a release is made.
#
# Usage: install.sh [--version vX.Y.Z] [--prefix DIR] [--from DIR] [--base-url URL]
#
#   --version   The release to install, such as v0.1.0 or v0.2.0-rc.1. Default: the latest release.
#   --prefix    Where to install: the binary goes in PREFIX/bin. Default: $HOME/.local.
#   --from      A directory that holds the archive and checksums.txt, read in place of a download.
#   --base-url  Where releases are downloaded from. Default: the project's GitHub releases.
#
# Each option can also come from the environment: ROPESHIFT_VERSION, ROPESHIFT_PREFIX,
# ROPESHIFT_FROM, and ROPESHIFT_BASE_URL.

set -eu

base_url=${ROPESHIFT_BASE_URL:-https://github.com/bkuberek/ropeshift/releases}
version=${ROPESHIFT_VERSION:-latest}
prefix=${ROPESHIFT_PREFIX:-}
from=${ROPESHIFT_FROM:-}

say() { printf 'ropeshift install: %s\n' "$*"; }
fail() {
	printf 'ropeshift install: %s\n' "$*" >&2
	exit 1
}

usage() {
	printf 'usage: install.sh [--version vX.Y.Z] [--prefix DIR] [--from DIR] [--base-url URL]\n' >&2
	exit 2
}

while [ $# -gt 0 ]; do
	case $1 in
	--version | --prefix | --from | --base-url)
		[ $# -ge 2 ] || usage
		case $1 in
		--version) version=$2 ;;
		--prefix) prefix=$2 ;;
		--from) from=$2 ;;
		--base-url) base_url=$2 ;;
		esac
		shift 2
		;;
	-h | --help) usage ;;
	*) usage ;;
	esac
done

if [ -z "$prefix" ]; then
	[ -n "${HOME:-}" ] || fail "HOME is not set, so name a prefix with --prefix"
	prefix=$HOME/.local
fi

case $version in
latest) ;;
v[0-9]*) ;;
[0-9]*) version=v$version ;;
*) fail "the version must look like v0.1.0, not $version" ;;
esac

case $(uname -s) in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) fail "no release is built for $(uname -s); releases cover macOS and Linux" ;;
esac

case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "no release is built for $(uname -m); releases cover arm64 and amd64" ;;
esac

work=$(mktemp -d "${TMPDIR:-/tmp}/ropeshift-install.XXXXXX")
trap 'rm -rf "$work"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# fetch copies one release file into the work directory, from --from or from the release page.
fetch() {
	if [ -n "$from" ]; then
		[ -f "$from/$1" ] || fail "$from holds no $1"
		cp "$from/$1" "$work/$1"
		return
	fi
	if [ "$version" = latest ]; then
		url=$base_url/latest/download/$1
	else
		url=$base_url/download/$version/$1
	fi
	if command -v curl >/dev/null 2>&1; then
		curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 --output "$work/$1" "$url" ||
			fail "could not download $url"
	elif command -v wget >/dev/null 2>&1; then
		wget --quiet --https-only --output-document="$work/$1" "$url" || fail "could not download $url"
	else
		fail "neither curl nor wget is installed, so nothing can be downloaded; use --from with a directory that holds the release"
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 -r "$1" | cut -d ' ' -f 1
	else
		fail "no SHA-256 tool is installed (sha256sum, shasum, or openssl), so the archive cannot be checked"
	fi
}

fetch checksums.txt

# The checksums file names every archive. With no version given, it is also where the latest
# release's version is read from.
suffix=_${os}_${arch}.tar.gz
archive=
expected=
while read -r sum name; do
	case $name in
	*"$suffix")
		if [ "$version" = latest ] || [ "$name" = "ropeshift_${version#v}$suffix" ]; then
			archive=$name
			expected=$sum
		fi
		;;
	esac
done <"$work/checksums.txt"

if [ -z "$archive" ]; then
	[ "$version" != latest ] || fail "no archive for $os/$arch is listed in the release's checksums file; nothing was installed"
	fail "ropeshift_${version#v}$suffix is not listed in the release's checksums file; nothing was installed"
fi
case $expected in
*[!0-9a-f]* | "") fail "the checksums file holds no SHA-256 for $archive" ;;
esac
[ ${#expected} -eq 64 ] || fail "the checksums file holds no SHA-256 for $archive"

fetch "$archive"
actual=$(sha256 "$work/$archive")
if [ "$actual" != "$expected" ]; then
	fail "checksum mismatch for $archive: the release lists $expected, the download is $actual. Nothing was extracted or installed."
fi
say "verified $archive (sha256 $actual)"

mkdir "$work/extract"
tar -xzf "$work/$archive" -C "$work/extract"
[ -f "$work/extract/ropeshift" ] || fail "$archive holds no ropeshift binary"

# The prefix needs root only when the user cannot write to it.
run=
target=$prefix
while [ ! -d "$target" ]; do
	target=$(dirname "$target")
done
if [ ! -w "$target" ] || { [ -d "$prefix/bin" ] && [ ! -w "$prefix/bin" ]; }; then
	command -v sudo >/dev/null 2>&1 || fail "$prefix is not writable and sudo is not installed; choose another prefix with --prefix"
	say "$prefix is not writable, so installing with sudo"
	run=sudo
fi

bin=$prefix/bin
doc=$prefix/share/doc/ropeshift
$run mkdir -p "$bin" "$doc"
# A copy beside the binary, then a rename, so a running daemon keeps its old file.
$run cp "$work/extract/ropeshift" "$bin/.ropeshift.new"
$run chmod 0755 "$bin/.ropeshift.new"
$run mv -f "$bin/.ropeshift.new" "$bin/ropeshift"
$run ln -sf ropeshift "$bin/ropeshiftd"
for file in LICENSE.md README.md; do
	if [ -f "$work/extract/$file" ]; then
		$run cp "$work/extract/$file" "$doc/$file"
		$run chmod 0644 "$doc/$file"
	fi
done

say "installed $("$bin/ropeshift" version)"
say "ropeshift and ropeshiftd are in $bin"
case :${PATH:-}: in
*:"$bin":*) ;;
*) say "$bin is not on PATH; add it to run ropeshift by name" ;;
esac
