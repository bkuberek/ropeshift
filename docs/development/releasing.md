# Making a release

This page describes how a ropeshift release is built, tested, and published. [Releasing](../releasing.md) lists what a release holds and each install channel, [versioning](../versioning.md) states the policy a version follows, and [choosing a version](versioning.md) how this project applies it. Releases run on GitHub Actions: the one manual step that makes a release is merging the release pull request, after a CI run started on it by hand, and the first release is `v0.1.0-alpha.1`.

## The Homebrew formula

The project owns its formula template, `packaging/homebrew/ropeshift.rb.in`, and `scripts/homebrew-formula.sh` fills in the version, URL, and SHA-256 of each archive from `checksums.txt`. [Releasing](../releasing.md#homebrew) describes what the formula installs.

GoReleaser has its own Homebrew support, and it is not used here. Its [`brews` section is deprecated](https://goreleaser.com/resources/deprecations/#brews), softly since v2.10 and fully since v2.16, in favor of [`homebrew_casks`](https://goreleaser.com/customization/publish/homebrew_casks/). A cask cannot do what this project needs: [a formula's `service` block](https://docs.brew.sh/Formula-Cookbook) is what `brew services` runs, while the [Cask Cookbook](https://docs.brew.sh/Cask-Cookbook) has no launchd service stanza, and its `service` artifact only moves a file into `~/Library/Services`.

The release workflow commits the rendered formula to the tap, `bkuberek/homebrew-tap`, as `Formula/ropeshift.rb` and changes no other file there. The row for ropeshift in the tap's README is added once, by hand.

## Tools a maintainer needs

These run the checks on a workstation, and a release by hand. Continuous integration installs the same versions.

| Tool | Why |
|------|-----|
| Go, at the version `go.mod` names | Builds the binary |
| Node.js and npm | Builds the web UI, which every GoReleaser build does first. A released binary needs neither to run |
| GoReleaser v2, the free edition | Builds the release and uploads its artifacts, for example `brew install goreleaser` |
| The GitHub CLI, `gh` | Creates the release, and changes the release pull request's label, in a release by hand |
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

`make install-test` runs the install script on this machine and in Debian and Alpine containers, the `.deb` in Debian, the `.rpm` in Fedora, and the formula in a Homebrew container. Each container runs with no network. It also checks that the install script refuses a changed checksum, a changed archive, and an archive the checksums do not list, before it extracts anything. Then, in a scratch clone of the repository, it merges a simulated release pull request, checks what `scripts/release.sh` refuses, tags the merge commit with it, and builds the release from that tag with publishing skipped. The build must report the tag's version and the merge commit, embed the web UI in every archive, and write no release notes of its own.

`make exit-container` follows [onboarding](../onboarding.md) as a new user would, in a Debian container with git and tmux and no network: `ropeshift init` on a terminal, the sample contract committed to a repository of its own, `ropeshiftd solo` with that repository as its source, and the work order reviewed, claimed, run in a lane, and accepted. A fake `claude` stands for the agent CLI, and a canary that fails the run stands behind it. The run from the release archive shows that a new user completes a work order with no account, no Docker, and no network. The run on a build of the tree traces every process with `strace`, and every address a process connects to, binds, or sends to must be a local socket or a loopback address.

## The web UI in a release

`main` never holds the web UI's build, so a plain checkout builds with no Node.js and serves a placeholder page in place of the UI. GoReleaser builds the UI into `web/static` before every build, with `scripts/release-ui.sh`: a snapshot with the packages already installed in `web/node_modules`, and a release with `npm ci`, which installs exactly the packages `web/package-lock.json` pins, after it clears every file git ignores under `web/static`, so no earlier build's file is embedded. Every artifact therefore embeds the UI, and a release tag is a commit on `main` that holds no build output.

## Release notes

A release has one set of notes, from the [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) since the previous release. release-please writes them in the release pull request, as the version's section of `CHANGELOG.md`, with its default sections: Features, Bug Fixes, Performance Improvements, and Reverts. `docs:`, `test:`, `chore:`, `ci:`, `build:`, `refactor:`, and `style:` commits are left out, because they change nothing a user runs. A breaking change keeps its type, such as `feat!:`, so it appears in its section. The pull request shows the section before the release exists.

When the release pull request merges, release-please creates the GitHub release with that section as its notes. GoReleaser writes no notes of its own: its changelog is disabled, and its release mode `keep-existing` keeps the notes of the release it uploads to ([GoReleaser's release settings](https://goreleaser.com/customization/publish/scm/), [its changelog settings](https://goreleaser.com/customization/publish/changelog/)).

Every pull request merges into `main` with a merge commit. Squash and rebase merges are turned off in the repository settings. A merge commit keeps each of the pull request's commits, so each reaches the notes under its own type, and so every commit in a pull request must be a Conventional Commit.

A pull request's title may be a Conventional Commit too, but the merge commit's message must not hold one. GitHub's default merge commit message includes the pull request's title ([GitHub's commit merging settings](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/configuring-commit-merging-for-pull-requests)), and release-please reads a Conventional Commit message at the bottom of a commit as one more change ([release-please](https://github.com/googleapis/release-please#what-if-my-pr-contains-multiple-fixes-or-features)), so a title such as `fix: ...` in the merge commit counts its change twice in the notes. Merge with a plain description instead, which `gh pr merge` writes as the merge commit's body ([gh pr merge](https://cli.github.com/manual/gh_pr_merge)):

```sh
gh pr merge N --merge --body "Merges pull request #N."
```

On the pull request's web page, replace the merge commit's description with a plain sentence, such as `Merges pull request #N.`, before confirming the merge. A merge commit whose message holds no Conventional Commit is one release-please passes over, as it passes over any commit whose message is not a Conventional Commit.

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

## Branches and stability

`main` is the one long-lived branch. Every change reaches it through a pull request, and there is no `develop` branch. Stability comes from the version, not from the branch: a release's suffix, or the lack of one, says how stable it is.

- While `prerelease` is `true` in `release-please-config.json`, as it is now, every release from `main` is a pre-release: `v0.1.0-alpha.1`, `v0.1.0-alpha.2`, and so on.
- A `Release-As:` footer on a commit to `main` moves to another stage: `Release-As: 0.1.0-beta.1`, `Release-As: 0.1.0-rc.1`, or `Release-As: 0.1.0` for the stable release. [Choosing the next version](#choosing-the-next-version) describes the footers.
- After a stable release, the same setting decides what comes next. With `prerelease` set to `true`, the next release starts a new pre-release series, such as `v0.1.1-alpha.1` or `v0.2.0-alpha.1`. With it set to `false`, release-please drops the suffix from the version it computes, so the next release is stable, such as `v0.1.1` or `v0.2.0`. A `Release-As:` footer names its version either way.

Do not create a release branch yet. Cut one when the first stable release has people who depend on it, or the first time a released version needs a patch while `main` has moved on:

1. Create a short-lived branch, such as `release/0.1`, for patches to that release only. Start it from the release's tag, which is on `main`.
2. Before its first release, give it a release-please configuration of its own, and let the release machinery run on it: the release workflow runs only on pushes to `main`, `scripts/release.sh` tags on `main` unless `ROPESHIFT_RELEASE_BRANCH` names another branch, and the `production` environment allows deployments from `main` only.

Until then, every release comes from `main`.

The one manual step that makes a release is merging the release pull request, and the release pull request gets no CI run by itself, so start one on it by hand first. Never push a release tag by hand, except in [releasing by hand](#releasing-by-hand) when GitHub Actions is unavailable: release-please creates the tag itself.

## How a release happens

The workflow `.github/workflows/release.yml` runs on every push to `main`. Merging the release pull request is the one manual step that makes a release:

1. On each push to `main`, [release-please](https://github.com/googleapis/release-please) reads the Conventional Commits since the previous release and opens or updates a pull request titled `chore(main): release <version>`, from the branch `release-please--branches--main`. It adds the version's section to `CHANGELOG.md` and the version to `.release-please-manifest.json`.
2. The release pull request gets no CI run by itself, because the workflow's own token opened it. The maintainer starts CI on it by hand, as [one-time setup](#one-time-setup) describes, reads the pull request, and merges it once CI result passes.
3. The push of that merge runs the release workflow. release-please finds the merged pull request by its label, `autorelease: pending`, tags its merge commit `v<version>`, and creates the GitHub release with the version's section of `CHANGELOG.md` as its notes. Then it changes the pull request's label to `autorelease: tagged`. The tag is the only one the release has.
4. In the same run, because a tag created with the workflow's own token starts no other workflow, the publish job checks out the tag and checks that it is on `main`. GoReleaser builds the web UI and every artifact from the tag, and uploads the archives, packages, and checksums to the release. It keeps the release's notes, and marks a version with `-alpha`, `-beta`, or `-rc` as a pre-release and every other version as a release.
5. The job renders the formula from `checksums.txt` and commits it to the tap as `Formula/ropeshift.rb`. Without the `HOMEBREW_TAP_GITHUB_TOKEN` secret in the `production` environment, this step warns and the release still succeeds.
6. release-please opens the next release pull request once a commit that the notes would list reaches `main`.

Between steps 3 and 4, the release exists with no assets. The install script, when no version is named, can choose it then, and stops before it installs anything, because the release has no checksums file yet.

The publish job is the only job in the GitHub environment `production`, which holds the tap's token, and it runs only when release-please created a release, so a push with nothing to release deploys nothing. It runs for the push to `main`, which the environment's rule that deployments come from `main` requires. The release-please job and the publish job are the only jobs with write access: release-please to the repository's contents for the tag, the release, and its branch, and to pull requests and issues for its pull request and its labels, and the publish job to the contents for the release's assets. Every other job, in both workflows, reads only.

### Releases before the one-tag process

`v0.1.0-alpha.1` was released by an earlier process, which tagged a commit beside `main` holding the built web UI, and marked the merge commit on `main` with the tag `main-v0.1.0-alpha.1`. Neither is read by the release process now. From `v0.1.0-alpha.2` on, every release is one tag on `main`, and release-please finds the previous release at that tag.

### When a release stops partway

If release-please fails before it creates the release, the pull request keeps the label `autorelease: pending`, and release-please opens no other release pull request meanwhile. Re-run the failed job, or run the release workflow by hand from `main`, which also happens on the next push to `main`.

If the publish job fails, re-run it from the same workflow run. A re-run keeps the outputs of the release-please job and builds from the same tag, and GoReleaser keeps the release's notes and replaces any assets the earlier run uploaded.

If release-please created the release but its job failed afterwards, the publish job did not run, and release-please does not create that release again. Publish it from the tag on a workstation, with step 4 of [releasing by hand](#releasing-by-hand) but without `gh release create`, because the release exists, and then step 5.

## Choosing the next version

release-please is configured for the [versioning policy](../versioning.md) in `release-please-config.json`:

- The first release is `v0.1.0-alpha.1`. The manifest starts at `0.0.0`, which counts as no release, and `initial-version` names the first.
- Each release pull request stays in the pre-release series it is in. In a series whose patch number is 0, it counts the suffix up whatever its commits are: `v0.1.0-alpha.1`, then `v0.1.0-alpha.2`, and so on. In a series whose patch number is not 0, such as `v0.1.1-alpha.2`, a feature or a breaking change moves to the next minor version and keeps the suffix: `v0.2.0-alpha.2`.
- After a stable release, with `prerelease` set to `true`, the next is a pre-release of the version the policy names: a feature or a breaking change leads to `v0.2.0-alpha.1`, and fixes alone to `v0.1.1-alpha.1`. With `prerelease` set to `false`, the next is stable: `v0.2.0` or `v0.1.1`.

To move to another stage, put a `Release-As:` footer in the message of a commit that reaches `main`, such as one of a pull request's commits, or an empty commit in its own pull request:

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
2. Under Environments, keep an environment named `production` whose deployment branches are limited to `main`. The publish job is the only job that uses it.
3. Create a fine-grained personal access token whose resource owner is `bkuberek`, with access to the repository `bkuberek/homebrew-tap` only, and the repository permission Contents set to read and write. Save it as `HOMEBREW_TAP_GITHUB_TOKEN`, a secret of the `production` environment and not a repository secret, for example with `gh secret set HOMEBREW_TAP_GITHUB_TOKEN --env production --repo bkuberek/ropeshift`, and renew it before it expires.
4. Protect `main` and require the status check CI result.

GitHub starts no workflow for an event the workflow's token causes, so CI does not run on the release pull request by itself. To give it a CI result, run CI by hand on its branch, `release-please--branches--main`, or close and reopen the pull request.

## Releasing by hand

When GitHub Actions is unavailable, a maintainer runs the same steps on a workstation, after merging the release pull request. `scripts/release.sh` is this fallback, and nothing else runs it:

1. Bring `main` up to date, confirm that the tree is clean, and read the version release-please chose. Stay on the commit where the release pull request merged, which is `main`'s head unless something merged after it; otherwise check out that commit with `git switch --detach <commit>`:

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

3. Tag the merge commit:

   ```sh
   scripts/release.sh v0.1.0-alpha.2
   ```

   It refuses a version outside the [policy](../versioning.md), a tag that already exists, a tree with uncommitted changes, a commit that is not on `main`'s first-parent history, and every commit but the one where the release pull request for that version merged: the manifest must name the version at that commit and not at the commit before it. It tags the commit with an annotated tag, writes the version's section of `CHANGELOG.md` to a temporary file as the release notes, pushes nothing, and prints the commands for the next step.

4. Push the tag, create the release with those notes, and build and upload its artifacts from the tag, with the commands the script printed:

   ```sh
   git push origin v0.1.0-alpha.2
   gh release create v0.1.0-alpha.2 --verify-tag --title v0.1.0-alpha.2 --notes-file <notes file> --prerelease
   GITHUB_TOKEN=<token> GORELEASER_CURRENT_TAG=v0.1.0-alpha.2 goreleaser release --clean
   ```

   As in the release workflow, the release exists with release-please's notes before GoReleaser runs. GoReleaser builds the web UI and every artifact, uploads them to the release, keeps its notes, and marks a version with `-alpha`, `-beta`, or `-rc` as a pre-release.

5. Render the formula, and commit it to the tap as `Formula/ropeshift.rb`, with no other change there:

   ```sh
   sh scripts/homebrew-formula.sh dist/checksums.txt "" dist/homebrew/ropeshift.rb
   ```

6. Change the release pull request's label, as release-please does after it creates a release, so it does not try to create this one again:

   ```sh
   gh pr edit <number> --remove-label "autorelease: pending" --add-label "autorelease: tagged"
   ```

## Future work

This is open, and nothing in the repository implements it yet:

- Signing the checksums file would let the install script prove who made a release.
