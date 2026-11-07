# Versioning

Ropeshift versions follow [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html). This page states how a version is chosen, and how a binary reports the version it is. [Releasing](releasing.md) describes how a release is made.

## Release versions

A release is a git tag of the form `vMAJOR.MINOR.PATCH`, such as `v0.4.1`. Each number is a decimal with no leading zero.

The project stays at `0.y.z` until the maintainer declares it stable and releases `v1.0.0`. Semantic Versioning lets anything change at `0.y.z`, and this project narrows that:

| Change | While at `0.y.z` | From `v1.0.0` |
|--------|------------------|---------------|
| A change that breaks what an earlier release accepted or wrote: a command, a flag, an exit code, an API route, an MCP tool, a file format, or stored data | MINOR | MAJOR |
| A new feature that breaks nothing | MINOR | MINOR |
| A fix, or any other change that breaks nothing | PATCH | PATCH |

A MINOR release resets PATCH to 0, and a MAJOR release resets both MINOR and PATCH.

## Pre-releases

A pre-release adds a suffix to the version it leads to, with N counting from 1:

| Suffix | Meaning |
|--------|---------|
| `-alpha.N` | Incomplete. Features may still change or disappear before the release |
| `-beta.N` | Every feature of the release is present. Fixes continue |
| `-rc.N` | A candidate. It becomes the release unless a defect is found |

Precedence follows Semantic Versioning, so `v0.5.0-alpha.2` < `v0.5.0-beta.1` < `v0.5.0-rc.1` < `v0.5.0`. No other suffix and no build metadata is a release version, and `scripts/release.sh` refuses them.

## Development builds

Only a release build reports a release version. Every other build reports `dev`:

- `go build`, `make`, or any build from a checkout. The Go toolchain records a pseudo-version for such a build, such as `v0.0.0-20261107230936-dd81c214342b`, and ropeshift does not report it as its version.
- A build from a tree with uncommitted changes, which the toolchain marks `+dirty`.
- `go install` at a commit or a branch, which also records a pseudo-version.

`go install` at a release tag, such as `go install ropeshift.dev/ropeshift/cmd/...@v0.4.1`, records that exact version, and the binary reports it.

`make snapshot` builds every release artifact from a commit with no tag. Its version names the commit, such as `v0.0.1-snapshot-dd81c21`, so it is never mistaken for a release.

## What a binary reports

`ropeshift version` and `ropeshiftd version` print one line:

```text
ropeshift v0.4.1 (commit dd81c214342b, built 2026-11-08T10:00:00Z, go1.27.3, linux/arm64)
```

`--json` prints one object. Every key is present on every build, in this order, and `commit` and `date` are empty when the build does not know them:

```json
{"command":"ropeshift","version":"v0.4.1","commit":"dd81c214342bad4bb98b4907f65871cf6607be59","date":"2026-11-08T10:00:00Z","modified":false,"go":"go1.27.3","os":"linux","arch":"arm64"}
```

| Key | Value |
|-----|-------|
| `command` | The name the process started under, `ropeshift` or `ropeshiftd` |
| `version` | The release version, or `dev` |
| `commit` | The full commit the build came from |
| `date` | When a release build was built, in RFC 3339. Empty for every other build |
| `modified` | Whether the tree held uncommitted changes |
| `go`, `os`, `arch` | The Go version, and the platform the binary runs on |

The MCP server reports the same version in its `serverInfo`, and telemetry reports it as `service.version`.

A release build sets the version, commit, date, and modified flag with linker flags on one small package, `internal/version`. A build that sets none of them reads what the Go toolchain recorded.
