#!/bin/sh
# Renders the Homebrew formula for one release from packaging/homebrew/ropeshift.rb.in, with the
# version, URL, and SHA-256 of each archive that the release's checksums file lists. GoReleaser
# deprecated its own formulas in favor of casks, and a cask cannot run under `brew services`, so the
# project owns this template (ADR-0001).
#
# Usage: scripts/homebrew-formula.sh CHECKSUMS BASE_URL OUTPUT
#
#   CHECKSUMS  The release's checksums.txt.
#   BASE_URL   Where the archives are downloaded from. Empty means the release's page on GitHub.
#   OUTPUT     The formula file to write.

set -eu

fail() {
	printf 'homebrew-formula: %s\n' "$*" >&2
	exit 1
}

[ $# -eq 3 ] || {
	printf 'usage: scripts/homebrew-formula.sh CHECKSUMS BASE_URL OUTPUT\n' >&2
	exit 2
}
checksums=$1
base_url=$2
output=$3
template=$(dirname "$0")/../packaging/homebrew/ropeshift.rb.in

[ -f "$checksums" ] || fail "no checksums file at $checksums"
[ -f "$template" ] || fail "no template at $template"

version=
for platform in darwin_arm64 darwin_amd64 linux_arm64 linux_amd64; do
	found=
	while read -r sum name; do
		case $name in
		ropeshift_*_"$platform".tar.gz)
			v=${name#ropeshift_}
			v=${v%_"$platform".tar.gz}
			[ -z "$version" ] || [ "$v" = "$version" ] || fail "$checksums lists archives of two versions, $version and $v"
			version=$v
			case $sum in *[!0-9a-f]* | "") fail "$name has no SHA-256 in $checksums" ;; esac
			[ ${#sum} -eq 64 ] || fail "$name has no SHA-256 in $checksums"
			found=$sum
			;;
		esac
	done <"$checksums"
	[ -n "$found" ] || fail "$checksums lists no archive for $platform"
	eval "sha256_$platform=\$found"
done

[ -n "$base_url" ] || base_url=https://github.com/bkuberek/ropeshift/releases/download/v$version
case $base_url in
*[!A-Za-z0-9:/._~%+-]*) fail "the base URL holds a character the formula cannot carry: $base_url" ;;
esac

url() { printf '%s/ropeshift_%s_%s.tar.gz' "$base_url" "$version" "$1"; }

mkdir -p "$(dirname "$output")"
sed \
	-e "s|@VERSION@|$version|g" \
	-e "s|@URL_DARWIN_ARM64@|$(url darwin_arm64)|g" \
	-e "s|@URL_DARWIN_AMD64@|$(url darwin_amd64)|g" \
	-e "s|@URL_LINUX_ARM64@|$(url linux_arm64)|g" \
	-e "s|@URL_LINUX_AMD64@|$(url linux_amd64)|g" \
	-e "s|@SHA256_DARWIN_ARM64@|$sha256_darwin_arm64|g" \
	-e "s|@SHA256_DARWIN_AMD64@|$sha256_darwin_amd64|g" \
	-e "s|@SHA256_LINUX_ARM64@|$sha256_linux_arm64|g" \
	-e "s|@SHA256_LINUX_AMD64@|$sha256_linux_amd64|g" \
	"$template" >"$output.tmp"
if grep -q '@[A-Z0-9_]*@' "$output.tmp"; then
	rm -f "$output.tmp"
	fail "the template holds a placeholder this script does not fill"
fi
mv -f "$output.tmp" "$output"
printf 'homebrew-formula: wrote %s for %s\n' "$output" "$version"
