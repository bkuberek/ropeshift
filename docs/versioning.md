# Versioning

Ropeshift versions follow [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html). This page states what a version promises, and how a binary reports the version it is. [Releasing](releasing.md) describes what a release holds and each install channel.

## Release versions

A release is a git tag of the form `vMAJOR.MINOR.PATCH`, such as `v0.4.1`, on the commit where its release pull request merged into `main`. Each number is a decimal with no leading zero.

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

Precedence follows Semantic Versioning, so `v0.5.0-alpha.2` < `v0.5.0-beta.1` < `v0.5.0-rc.1` < `v0.5.0`. No other suffix and no build metadata is a release version.

Stability comes from the version, not from a branch: every release is tagged on `main`, and its suffix, or the lack of one, says how stable it is. For now, every release is a pre-release.

## Development builds

Only a release build reports a release version. Every other build reports `dev`:

- `go build`, `make`, or any build from a checkout. The Go toolchain records a pseudo-version for such a build, such as `v0.0.0-20261107230936-dd81c214342b`, and ropeshift does not report it as its version.
- A build from a tree with uncommitted changes, which the toolchain marks `+dirty`.
- `go install` at a commit or a branch, which also records a pseudo-version.

`go install` at a release tag, such as `go install ropeshift.com/ropeshift/cmd/...@v0.4.1`, records that exact version, and the binary reports it. It is not an install channel for now, because no commit holds the built web UI, so that binary serves the placeholder page ([releasing](releasing.md#install-channels)). Every channel installs a release build, which embeds the UI.

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

## What a daemon reports

A daemon keeps running the build it started from after a package manager replaces the binary on disk. `GET /v1/health` reports the build the daemon runs, as `version`, `commit`, and `date`, and the release of the binary installed at the daemon's own executable path, as `installed`. The daemon reads that binary once a minute by starting it with `version --json`, and never while answering a request. It says nothing of the installed build when it cannot read it. The same answer carries `mode`, `solo` for a daemon that runs its own worker, which is how the web UI knows whether a worker on another machine can join.

When the installed release is later than the running one, `warning` says so and names the fix: `brew services restart ropeshift` when the daemon runs through `brew services`, which names the binary by its Homebrew `opt` link, and otherwise restarting the service, including one `ropeshift service install` wrote. A development build and an unreadable one are not releases, so neither is compared.

`ropeshift version` prints the daemon's version under its own line, and the same warning. With `--json`, a `daemon` object holds the daemon's `version`, `commit`, `date`, and `installed`, and `warning` holds the warning. Neither key is present when no daemon answers. The [web UI](web-ui.md#version) shows both.
