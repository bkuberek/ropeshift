# Releasing

This page describes how a ropeshift release is built, tested, and published, and how each install channel uses it. [Versioning](versioning.md) states how a version is chosen. No release has been published yet, and the maintainer decides when the first pre-release tag is cut from `main`.

## What a release holds

A release is one binary for each platform, built from a tag by [GoReleaser](https://goreleaser.com/) with the web UI embedded. The binary is named `ropeshift`, and `ropeshiftd` is a link to it, because the name a process starts under picks the command.

| Artifact | What it is |
|----------|------------|
| `ropeshift_<version>_<os>_<arch>.tar.gz` | The binary, `LICENSE.md`, and `README.md`, for macOS and Linux on arm64 and amd64 |
| `checksums.txt` | The SHA-256 of every archive and package |
| `.deb` and `.rpm` packages | For Linux on arm64 and amd64. They install `/usr/bin/ropeshift`, the `/usr/bin/ropeshiftd` link, and the license and readme under `/usr/share/doc/ropeshift`. They recommend git and tmux |
| The Homebrew formula | Rendered from the checksums for the project's tap |
| The tag itself | `go install` at the tag builds the same binary from source, with the UI |

No package installs a service. `ropeshift service install` writes a launchd agent or a systemd user unit for the person who runs it, and on a Homebrew install `brew services` does that job instead. [Onboarding](onboarding.md#run-the-daemon-as-a-service) describes both.

## Install channels

| Channel | Command | Service |
|---------|---------|---------|
| Install script | `curl -fsSL https://raw.githubusercontent.com/bkuberek/ropeshift/main/install.sh \| sh -s -- --version v0.1.0` | `ropeshift service install` |
| Homebrew | `brew install bkuberek/tap/ropeshift` | `brew services start ropeshift` |
| Debian and Ubuntu | `sudo dpkg -i ropeshift_<version>_<arch>.deb` | `ropeshift service install` |
| Fedora and RHEL | `sudo rpm -i ropeshift-<version>-1.<arch>.rpm` | `ropeshift service install` |
| Go | `go install ropeshift.dev/ropeshift/cmd/...@v0.1.0` | `ropeshift service install` |

`go install` builds `ropeshift` and `ropeshiftd` as two copies of the same program, which behaves the same as the link.

Under `brew services`, the daemon starts with no repository source. To name sources on a Homebrew install, use `ropeshift service install -- --source /path/to/repository` instead. Use one or the other, because two daemons for one installation conflict.

### The install script

`install.sh` is a POSIX shell script that needs no root unless the chosen prefix does:

1. It detects the operating system and architecture, and refuses any platform with no release.
2. It downloads `checksums.txt` and the archive for this machine, with curl or wget, from the release page.
3. It computes the archive's SHA-256 and compares it with the line in `checksums.txt`. On a mismatch, or an archive the file does not list, it stops with a non-zero exit before it extracts anything.
4. It installs the binary to `PREFIX/bin`, links `ropeshiftd` to it, and puts the license and readme in `PREFIX/share/doc/ropeshift`. The binary is copied beside the old one and renamed over it, so a running daemon keeps its file.

| Option | Default | Meaning |
|--------|---------|---------|
| `--version` | The latest release | The release to install, such as `v0.1.0` |
| `--prefix` | `$HOME/.local` | Where to install. The script uses sudo only when it cannot write there |
| `--from` | None | A directory that holds the archive and `checksums.txt`, read in place of a download |
| `--base-url` | The project's GitHub releases | Where releases are downloaded from |

The checksum proves that the download matches the release's own list. It does not prove who made the release, because releases are not signed yet.

### Homebrew

The project owns its formula template, `packaging/homebrew/ropeshift.rb.in`, and `scripts/homebrew-formula.sh` fills in the version, URL, and SHA-256 of each archive from `checksums.txt`. The formula installs the binary, links `ropeshiftd`, and holds a `service` block that runs `ropeshiftd solo` with `process_type :interactive`, because launchd throttles a job with no process class.

GoReleaser has its own Homebrew support, and it is not used here. Its [`brews` section is deprecated](https://goreleaser.com/resources/deprecations/#brews), softly since v2.10 and fully since v2.16, in favor of [`homebrew_casks`](https://goreleaser.com/customization/publish/homebrew_casks/). A cask cannot do what this project needs: [a formula's `service` block](https://docs.brew.sh/Formula-Cookbook) is what `brew services` runs, while the [Cask Cookbook](https://docs.brew.sh/Cask-Cookbook) has no launchd service stanza, and its `service` artifact only moves a file into `~/Library/Services`.

The tap is the repository `bkuberek/homebrew-tap`, which other tools share. Publishing the formula means committing the rendered file to that repository as `Formula/ropeshift.rb`, and adding a row for ropeshift to the tools table in its README.

## Tools a maintainer needs

| Tool | Why |
|------|-----|
| Go, at the version `go.mod` names | Builds the binary |
| Node.js and npm | Builds the web UI. Running a release needs neither |
| GoReleaser v2, the free edition | Builds and publishes the release, for example `brew install goreleaser` |
| Docker | Runs the Linux channels of `make install-test`, and `make exit-container` |
| A GitHub token that can write the repository's releases | Uploads the draft release, as `GITHUB_TOKEN` |

## Make targets

| Target | What it does |
|--------|--------------|
| `make release-check` | Runs `goreleaser check` on `.goreleaser.yaml`, and checks the release scripts' syntax. `make check` runs it, and skips the GoReleaser part when GoReleaser is not installed |
| `make snapshot` | Builds every artifact from the current commit into `dist/`, with no tag and no publishing, and renders `dist/homebrew/ropeshift.rb`. `FORMULA_URL` sets where that formula downloads from |
| `make install-test` | Builds a snapshot and installs it through every channel, each in a clean environment, then asks each install for its version. `INSTALL_DIST` names a snapshot built earlier |
| `make exit-container` | Builds a snapshot, installs its Linux archive with the install script for a new account in a container with no network, and runs a first work order there on the fake agent CLI. It also runs that work order on a build of the current tree under `strace`, and fails on any connection off loopback. `EXIT_DIST` names a snapshot built earlier |

`make install-test` runs the install script on this machine and in Debian and Alpine containers, the `.deb` in Debian, the `.rpm` in Fedora, and the formula in a Homebrew container. Each container runs with no network. It also checks that the install script refuses a changed checksum, a changed archive, and an archive the checksums do not list, before it extracts anything. Then, in a scratch clone of the repository, it cuts two releases with `scripts/release.sh`, builds each from its tag with publishing skipped, reads their release notes, and runs `go install` at a tag through a module proxy made of local files.

`make exit-container` follows [onboarding](onboarding.md) as a new user would, in a Debian container with git and tmux and no network: `ropeshift init` on a terminal, the sample contract committed to a repository of its own, `ropeshiftd solo` with that repository as its source, and the work order reviewed, claimed, run in a lane, and accepted. A fake `claude` stands for the agent CLI, and a canary that fails the run stands behind it. The run from the release archive shows that a new user completes a work order with no account, no Docker, and no network. The run on a build of the tree traces every process with `strace`, and every address a process connects to, binds, or sends to must be a local socket or a loopback address.

## The release commit

`main` never holds the web UI's build, so a plain checkout builds with no Node.js and serves a placeholder page in place of the UI. `go install` at a tag builds whatever the tag's tree holds, so a release tag must hold the build.

`scripts/release.sh` solves this with a release commit:

1. It refuses a version outside the [policy](versioning.md), a tag that already exists, a checkout on another branch, and a tree with uncommitted changes.
2. It runs `npm ci` and the web build.
3. It commits the build with `git add --force web/static` on top of the branch's head, as `chore(release): <version>`.
4. It tags that commit with an annotated tag, and returns to the branch, which stays as it was.
5. It pushes nothing, and prints the commands that publish the release.

The release commit is not on `main`. Its parent is the commit `main` pointed at when the release was cut. GoReleaser's build of a tag checks that the commit holds the built UI and nothing else under `web/static`, and refuses a tag that does not.

Because each release commit sits beside `main`, git cannot find the previous release from `main`'s history. `scripts/release.sh` therefore prints `GORELEASER_PREVIOUS_TAG`, which names the newest release tag before this one, so the release notes start there.

## Release notes

The notes come from the [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) since the previous release, grouped as Features (`feat:`), Fixes (`fix:`), and Others. `docs:`, `test:`, `chore:`, `ci:`, and `style:` commits are left out, because they change nothing a user runs. A breaking change keeps its type, such as `feat!:`, so it appears in its group.

GoReleaser writes no notes for a snapshot. The draft release shows them before anyone else can see them.

## Cutting a pre-release

The maintainer runs every step. Nothing runs on a schedule or on a push.

1. Bring `main` up to date, and confirm that the tree is clean:

   ```sh
   git switch main
   git pull --ff-only
   ```

2. Run the checks, the install matrix, and the first work order with no network:

   ```sh
   make check
   make install-test
   make exit-container EXIT_DIST=dist
   ```

3. Cut the release commit and its tag:

   ```sh
   scripts/release.sh v0.1.0-alpha.1
   ```

4. Push the tag, and build the release from it with the commands the script printed:

   ```sh
   git push origin v0.1.0-alpha.1
   git checkout v0.1.0-alpha.1
   GITHUB_TOKEN=<token> GORELEASER_PREVIOUS_TAG=<previous tag> GORELEASER_CURRENT_TAG=v0.1.0-alpha.1 goreleaser release --clean
   ```

   GoReleaser builds every artifact, checks the embedded UI, and uploads a draft release. A version with `-alpha`, `-beta`, or `-rc` is marked as a pre-release.

5. Render the formula, and commit it to the tap as `Formula/ropeshift.rb`:

   ```sh
   sh scripts/homebrew-formula.sh dist/checksums.txt "" dist/homebrew/ropeshift.rb
   ```

6. Read the draft's notes and assets on GitHub, and publish it.

7. Return to `main` with `git checkout main`.

A stable release follows the same steps with a version such as `v0.1.0`.

## Future work

These are open, and nothing in the repository implements them yet:

- Continuous integration could run steps 4 and 5 from a pushed tag.
- Signing the checksums file would let the install script prove who made a release.
