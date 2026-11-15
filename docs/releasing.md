# Releasing

This page describes what a ropeshift release holds, and how each install channel uses it. [Versioning](versioning.md) states what a version promises. How a release is built, tested, and published is in the [developer documentation](https://github.com/bkuberek/ropeshift/blob/main/docs/development/releasing.md).

## What a release holds

A release is one binary for each platform, built from a tag on `main` by [GoReleaser](https://goreleaser.com/) with the web UI embedded. The binary is named `ropeshift`, and `ropeshiftd` is a link to it, because the name a process starts under picks the command.

| Artifact | What it is |
|----------|------------|
| `ropeshift_<version>_<os>_<arch>.tar.gz` | The binary, `LICENSE.md`, and `README.md`, for macOS and Linux on arm64 and amd64 |
| `checksums.txt` | The SHA-256 of every archive and package |
| `.deb` and `.rpm` packages | For Linux on arm64 and amd64. They install `/usr/bin/ropeshift`, the `/usr/bin/ropeshiftd` link, and the license and readme under `/usr/share/doc/ropeshift`. They recommend git and tmux |
| The Homebrew formula | Rendered from the checksums for the project's tap |
| The tag | `v<version>`, on the commit where the release pull request merged into `main`. Every artifact is built from it |

No package installs a service. `ropeshift service install` writes a launchd agent or a systemd user unit for the person who runs it, and on a Homebrew install `brew services` does that job instead. [Onboarding](onboarding.md#run-the-daemon-as-a-service) describes both.

## Install channels

| Channel | Command | Service |
|---------|---------|---------|
| Install script | `curl -fsSL https://raw.githubusercontent.com/bkuberek/ropeshift/main/install.sh \| sh` | `ropeshift service install` |
| Homebrew | `brew install bkuberek/tap/ropeshift` | `brew services start ropeshift` |
| Debian and Ubuntu | `sudo dpkg -i ropeshift_<version>_<arch>.deb` | `ropeshift service install` |
| Fedora and RHEL | `sudo rpm -i ropeshift-<version>-1.<arch>.rpm` | `ropeshift service install` |

The packages are on the release's page. Their names carry the version in the packaging system's form, such as `ropeshift_0.1.0~alpha.1_arm64.deb` and `ropeshift-0.1.0~alpha.1-1.aarch64.rpm`, where an `.rpm` names the architecture `aarch64` or `x86_64`.

Every channel installs a binary that GoReleaser built, with the web UI embedded. `go install` is not an install channel for now: it builds from a tag's source, and no commit holds the built web UI, so the binary it builds serves the placeholder page. It becomes a channel when a release carries the built UI on `main`.

Under `brew services`, the daemon starts with no repository source. To name sources on a Homebrew install, use `ropeshift service install -- --source /path/to/repository` instead. Use one or the other, because two daemons for one installation conflict.

### The install script

`install.sh` is a POSIX shell script that needs no root unless the chosen prefix does:

1. It detects the operating system and architecture, and refuses any platform with no release.
2. With no version named, it reads the first page of the project's releases from GitHub's API, with no token, and chooses the newest stable release by version precedence, or the newest pre-release when no release is stable yet. It says which it chose. A draft, and a tag outside the [versioning policy](versioning.md), is passed over. With `--from`, it reads no list.
3. It downloads `checksums.txt` and the archive for this machine, with curl or wget, from the release page.
4. It computes the archive's SHA-256 and compares it with the line in `checksums.txt`. On a mismatch, or an archive the file does not list, it stops with a non-zero exit before it extracts anything.
5. It installs the binary to `PREFIX/bin`, links `ropeshiftd` to it, and puts the license and readme in `PREFIX/share/doc/ropeshift`. The binary is copied beside the old one and renamed over it, so a running daemon keeps its file.

| Option | Environment | Default | Meaning |
|--------|-------------|---------|---------|
| `--version` | `ROPESHIFT_VERSION` | The newest stable release, or the newest pre-release when no release is stable | The release to install, such as `v0.1.0` or `v0.1.0-alpha.1` |
| `--prefix` | `ROPESHIFT_PREFIX` | `$HOME/.local` | Where to install. The script uses sudo only when it cannot write there |
| `--from` | `ROPESHIFT_FROM` | None | A directory that holds the archive and `checksums.txt`, read in place of a download. With no version named, the archive it lists for this machine is installed |
| `--base-url` | `ROPESHIFT_BASE_URL` | The project's GitHub releases | Where releases are downloaded from. The release is still chosen from GitHub's list when no version is named |

Options go after `sh -s --` when the script is piped:

```sh
curl -fsSL https://raw.githubusercontent.com/bkuberek/ropeshift/main/install.sh | sh -s -- --version v0.1.0-alpha.2
```

GitHub's API serves [a limited number of requests an hour](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api) to a client with no token. When the list cannot be read, the script stops and says to name a release with `--version`, which reads no list.

The checksum proves that the download matches the release's own list. It does not prove who made the release, because releases are not signed yet.

### Homebrew

The formula installs the binary, links `ropeshiftd`, and holds a `service` block that runs `ropeshiftd solo` with `process_type :interactive`, because launchd throttles a job with no process class.

The tap is the repository `bkuberek/homebrew-tap`, which other tools share. Pre-releases update the formula too, because every release is a pre-release until `1.0.0`.

Homebrew asks a person to trust a third-party tap before some commands read it. `brew trust bkuberek/tap` does that once.
