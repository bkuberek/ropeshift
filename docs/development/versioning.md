# Choosing a version

This page states how this project chooses each release's version, and how a build is stamped with it. [Versioning](../versioning.md) states the policy a version follows and what a binary reports, and [making a release](releasing.md) describes how a release is made.

## The next version

While `prerelease` is `true` in `release-please-config.json`, as it is now, every release is a pre-release. The release pull request proposes each version by the rules of the [versioning policy](../versioning.md#release-versions) and continues the pre-release series it is in, and the maintainer moves to the next stage, or to a stable release, with a `Release-As:` footer. [Making a release](releasing.md#branches-and-stability) describes the branches, and [choosing the next version](releasing.md#choosing-the-next-version) the footers.

`scripts/release.sh` refuses any suffix but `-alpha.N`, `-beta.N`, and `-rc.N`, and any build metadata, because none of them is a release version.

## Snapshot builds

`make snapshot` builds every release artifact from a commit with no tag. Its version names the commit, such as `v0.0.1-snapshot-dd81c21`, so it is never mistaken for a release.

## How a build is stamped

A release build sets the version, commit, date, and modified flag with linker flags on one small package, `internal/version`. A build that sets none of them reads what the Go toolchain recorded.
