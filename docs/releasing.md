# Releasing

This page describes how a ropeshift release is built, tested, and published, and how each install channel uses it. [Versioning](versioning.md) states how a version is chosen. Releases run on GitHub Actions: the one manual step is merging the release pull request, and the first release is `v0.1.0-alpha.1`.

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

The tap is the repository `bkuberek/homebrew-tap`, which other tools share. The release workflow commits the rendered formula to it as `Formula/ropeshift.rb` and changes no other file there. Pre-releases update the formula too, because every release is a pre-release until `1.0.0`. The row for ropeshift in the tap's README is added once, by hand.

## Tools a maintainer needs

These run the checks on a workstation, and a release by hand. Continuous integration installs the same versions.

| Tool | Why |
|------|-----|
| Go, at the version `go.mod` names | Builds the binary |
| Node.js and npm | Builds the web UI. Running a release needs neither |
| GoReleaser v2, the free edition | Builds and publishes the release, for example `brew install goreleaser` |
| Docker | Runs the Linux channels of `make install-test`, `make exit-container`, and `make service-container` |
| A GitHub token that can write the repository's releases | Publishes a release by hand, as `GITHUB_TOKEN`. The release workflow uses its own token |

## Make targets

| Target | What it does |
|--------|--------------|
| `make release-check` | Runs `goreleaser check` on `.goreleaser.yaml`, and checks the release scripts' syntax. `make check` runs it, and skips the GoReleaser part when GoReleaser is not installed |
| `make snapshot` | Builds every artifact from the current commit into `dist/`, with no tag and no publishing, and renders `dist/homebrew/ropeshift.rb`. `FORMULA_URL` sets where that formula downloads from |
| `make install-test` | Builds a snapshot and installs it through every channel, each in a clean environment, then asks each install for its version. `INSTALL_DIST` names a snapshot built earlier |
| `make exit-container` | Builds a snapshot, installs its Linux archive with the install script for a new account in a container with no network, and runs a first work order there on the fake agent CLI. It also runs that work order on a build of the current tree under `strace`, and fails on any connection off loopback. `EXIT_DIST` names a snapshot built earlier |
| `make service-container` | Writes `make linux-bundle` for the container engine's architecture, and runs its scope and service tests in a container whose init is systemd, as a user whose manager runs through lingering. It fails if a test skips. `SERVICE_OUT` names the directory for the bundle, the results, and each step's log |

`make install-test` runs the install script on this machine and in Debian and Alpine containers, the `.deb` in Debian, the `.rpm` in Fedora, and the formula in a Homebrew container. Each container runs with no network. It also checks that the install script refuses a changed checksum, a changed archive, and an archive the checksums do not list, before it extracts anything. Then, in a scratch clone of the repository, it cuts two releases with `scripts/release.sh`, builds each from its tag with publishing skipped, reads their release notes, and runs `go install` at a tag through a module proxy made of local files.

`make exit-container` follows [onboarding](onboarding.md) as a new user would, in a Debian container with git and tmux and no network: `ropeshift init` on a terminal, the sample contract committed to a repository of its own, `ropeshiftd solo` with that repository as its source, and the work order reviewed, claimed, run in a lane, and accepted. A fake `claude` stands for the agent CLI, and a canary that fails the run stands behind it. The run from the release archive shows that a new user completes a work order with no account, no Docker, and no network. The run on a build of the tree traces every process with `strace`, and every address a process connects to, binds, or sends to must be a local socket or a loopback address.

## The release commit

`main` never holds the web UI's build, so a plain checkout builds with no Node.js and serves a placeholder page in place of the UI. `go install` at a tag builds whatever the tag's tree holds, so a release tag must hold the build.

`scripts/release.sh` solves this with a release commit:

1. It refuses a version outside the [policy](versioning.md), a tag that already exists, a checkout on another branch, and a tree with uncommitted changes.
2. It runs `npm ci` and the web build.
3. It commits the build with `git add --force web/static` on top of the branch's head, as `chore(release): <version>`.
4. It tags that commit with an annotated tag, and returns to the branch, which stays as it was.
5. It pushes nothing, and prints the commands that publish the release. When `ROPESHIFT_RELEASE_OUTPUT` names a file, it also appends `tag=`, `commit=`, and `previous=` lines to it, which is how the release workflow reads them.

The release commit is not on `main`. Its parent is the commit `main` pointed at when the release was cut. GoReleaser's build of a tag checks that the commit holds the built UI and nothing else under `web/static`, and refuses a tag that does not.

Because each release commit sits beside `main`, git cannot find the previous release from `main`'s history. `scripts/release.sh` therefore prints `GORELEASER_PREVIOUS_TAG`, which names the newest release tag before this one, so the release notes start there.

## Release notes

Two sets of notes come from the [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) since the previous release:

- The GitHub release's notes, which GoReleaser writes from git, grouped as Features (`feat:`), Fixes (`fix:`), and Others. `docs:`, `test:`, `chore:`, `ci:`, and `style:` commits are left out, because they change nothing a user runs. A breaking change keeps its type, such as `feat!:`, so it appears in its group.
- `CHANGELOG.md`, which release-please writes in the release pull request, with its default sections: Features, Bug Fixes, Performance Improvements, and Reverts. The pull request shows the section before the release exists.

GoReleaser writes no notes for a snapshot.

## Continuous integration

The workflow `.github/workflows/ci.yml` runs on every pull request to `main`, on every push to `main`, and by hand. It uses GitHub's standard runners only. No job starts an agent CLI session, so `make real` and `make qualify` never run there, and no workflow holds an agent credential.

| Job | What it runs | When |
|-----|--------------|------|
| `make check` | Formatting, vet, lint, the license check, the release configuration check, the race-enabled tests, and the web tests and typecheck, on Linux and on macOS | Every run |
| `make cross` | The build for every release platform | Every run |
| `make chaos` | The crash tests at their default size | Every run |
| `make snapshot` | One snapshot, which the next two jobs share | Every run |
| `make install-test` | An install of that snapshot through every channel | Every run |
| `make exit-container` | The first work order with no network, from that snapshot's archive and under `strace`. It fails if a test skips | Every run |
| Service tests under systemd | `make service-container` | Every run |
| `make chaos KILLS=1500` | The long crash run | A push to `main`, or by hand with `long-chaos` |
| `make bench` | The bench, whose timings on shared runners are informational. It never fails the run, and its numbers are in the job's summary | A push to `main`, or by hand with `bench` |
| CI result | Passes when every job above but the bench passed or was skipped | Every run |

A run that changes only Markdown and images runs no checks: the first job finds that, every check is skipped, and CI result still reports. Branch protection on `main` therefore requires CI result alone. A newer commit on a pull request cancels the run for the older one, while runs on `main` finish. A failed job uploads its log, and the service job also its results and the container's journal, as artifacts.

Toolchain versions live in `.github/actions/setup/action.yml`. Every third-party action is pinned to a full commit SHA, with its version in a comment.

## How a release happens

The workflow `.github/workflows/release.yml` runs on every push to `main`. Merging the release pull request is the one manual step:

1. On each push to `main`, [release-please](https://github.com/googleapis/release-please) reads the Conventional Commits since the previous release and opens or updates a pull request titled `chore(main): release <version>`. It adds the version's section to `CHANGELOG.md` and the version to `.release-please-manifest.json`. It creates no tag and no release.
2. The maintainer reads the pull request and merges it.
3. The push of that merge runs the release workflow. Its first job finds the merged pull request by its label, `autorelease: pending`, and reads the version from the manifest at its merge commit. With one found, the release job runs.
4. The release job runs `scripts/release.sh` at that merge commit, which makes the release commit with the built UI and its annotated tag, `v<version>`, and pushes the tag.
5. In the same job, because a tag pushed with the workflow's own token starts no other workflow, GoReleaser builds every artifact from the tag, checks the embedded UI, and publishes the GitHub release. A version with `-alpha`, `-beta`, or `-rc` is marked as a pre-release.
6. The job renders the formula from `checksums.txt` and commits it to the tap as `Formula/ropeshift.rb`. Without the `HOMEBREW_TAP_GITHUB_TOKEN` secret in the `production` environment, this step warns and the release still succeeds.
7. It tags the merge commit `main-v<version>`, and changes the pull request's label to `autorelease: tagged`. The last job runs release-please, which opens the next release pull request once new commits arrive.

The release job is the only job in the GitHub environment `production`, which holds the tap's token, and it runs only when a merged release pull request is waiting, so a push with nothing to release deploys nothing. It and the job that runs release-please after it are the only jobs with write access: to the repository's contents for the tags, the release, and release-please's branch, and to pull requests and issues for release-please's pull request and its labels. Every other job, in both workflows, reads only.

### Why main carries a tag for each release

release-please finds the previous release by its tag, and lists the commits on `main` after that tag's commit. A release tag points at the release commit beside `main`, which is never in `main`'s history, so release-please would never reach it. The `main-v<version>` tag marks the commit on `main` that the release was cut from. release-please is configured with the component `main`, so it reads those tags and passes over the release tags. They are not versions: `go install` reads only `v` tags, the release workflow names both tags to GoReleaser, and a snapshot's version reads no tag.

### When a release stops partway

The pull request keeps the label `autorelease: pending` until the release is complete, and release-please opens no other release pull request meanwhile. Re-run the failed jobs, or run the release workflow by hand from `main`, which also happens on the next push to `main`. Either completes the same release: it uses the tag an earlier run pushed, after a check that the tag was cut from the merge commit, and GoReleaser replaces any assets the earlier run uploaded.

## Choosing the next version

release-please is configured for the [versioning policy](versioning.md) in `release-please-config.json`:

- The first release is `v0.1.0-alpha.1`. The manifest starts at `0.0.0`, which counts as no release, and `initial-version` names the first.
- Each release pull request continues the pre-release series it is in, whatever its commits are: `v0.1.0-alpha.1`, then `v0.1.0-alpha.2`, and so on.
- After a stable release, the next is a pre-release of the version the policy names: a feature or a breaking change leads to `v0.2.0-alpha.1`, and fixes alone to `v0.1.1-alpha.1`.

To move to another stage, put a `Release-As:` footer in a commit message on `main`, such as the squash message of a pull request, or an empty commit:

```sh
git commit --allow-empty -m "chore: release the first beta" -m "Release-As: 0.1.0-beta.1"
```

| To release | Footer |
|------------|--------|
| The first beta | `Release-As: 0.1.0-beta.1` |
| The first release candidate | `Release-As: 0.1.0-rc.1` |
| The stable release | `Release-As: 0.1.0` |
| The next minor's first alpha | `Release-As: 0.2.0-alpha.1` |

The next release pull request then names that version, and the ones after it continue its series.

## One-time setup

These are set once in the repository's settings on GitHub:

1. Under Actions, General, Workflow permissions, allow GitHub Actions to create and approve pull requests. release-please opens its pull request with the workflow's token.
2. Under Environments, keep an environment named `production` whose deployment branches are limited to `main`. The release job is the only job that uses it.
3. Create a fine-grained personal access token whose resource owner is `bkuberek`, with access to the repository `bkuberek/homebrew-tap` only, and the repository permission Contents set to read and write. Save it as `HOMEBREW_TAP_GITHUB_TOKEN`, a secret of the `production` environment and not a repository secret, for example with `gh secret set HOMEBREW_TAP_GITHUB_TOKEN --env production --repo bkuberek/ropeshift`, and renew it before it expires.
4. Protect `main` and require the status check CI result.

GitHub starts no workflow for an event the workflow's token causes, so CI does not run on the release pull request by itself. To give it a CI result, run CI by hand on its branch, `release-please--branches--main--components--main`, or close and reopen the pull request.

## Releasing by hand

When GitHub Actions is unavailable, a maintainer runs the same steps on a workstation, after merging the release pull request:

1. Bring `main` up to date, confirm that the tree is clean, and read the version release-please chose:

   ```sh
   git switch main
   git pull --ff-only
   jq -r '."."' .release-please-manifest.json
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

4. Push the tag, and publish the release from it with the commands the script printed:

   ```sh
   git push origin v0.1.0-alpha.1
   git checkout v0.1.0-alpha.1
   GITHUB_TOKEN=<token> GORELEASER_PREVIOUS_TAG=<previous tag> GORELEASER_CURRENT_TAG=v0.1.0-alpha.1 goreleaser release --clean
   ```

   GoReleaser builds every artifact, checks the embedded UI, and publishes the release. A version with `-alpha`, `-beta`, or `-rc` is marked as a pre-release.

5. Render the formula, and commit it to the tap as `Formula/ropeshift.rb`, with no other change there:

   ```sh
   sh scripts/homebrew-formula.sh dist/checksums.txt "" dist/homebrew/ropeshift.rb
   ```

6. Tag the merge commit for release-please, and change the pull request's label:

   ```sh
   git tag main-v0.1.0-alpha.1 main
   git push origin main-v0.1.0-alpha.1
   gh pr edit <number> --remove-label "autorelease: pending" --add-label "autorelease: tagged"
   ```

7. Return to `main` with `git checkout main`.

## Future work

This is open, and nothing in the repository implements it yet:

- Signing the checksums file would let the install script prove who made a release.
