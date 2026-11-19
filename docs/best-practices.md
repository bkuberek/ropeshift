# Best practices

The practices check reads each repository of a project for twelve practices that keep a project sound and secure: env files ignored, no secrets in tracked files or history, CI or the no-CI recipe, pre-commit hooks, lockfiles, dependency updates, a security policy, code owners, a license where the repository is public, tests, and, on GitHub, branch protection and required review. This guide says what each practice checks, why it matters, and how to meet it; how to run the check and read its findings; and how to keep a finding open with a reason. It follows Harbor, with its code repositories `harbor-api` and `harbor-web`, as [set up your projects](set-up-projects.md) registers them. Put your own names in their place.

`ropeshift practices` prints the live result of each repository. This guide describes the rules; it does not restate any result.

## What the check is

The check is deterministic: no model takes part, and the same commit gives the same findings. It runs on the worker that keeps a checkout of the repository, at the tip of the repository's base branch, `origin/<base>` when the clone has it and the local branch otherwise. It runs only read-only git commands against the checkout, writes nothing in the repository, starts no lane, and sends nothing but its result off the worker. No secret's text is recorded, printed, or sent: a `secrets` finding holds the rule, the file, the line, the commit, and a digest of the match.

Each practice comes to one status in a result:

| Status | What it means |
|--------|---------------|
| `met` | The practice holds |
| `finding` | The practice has at least one finding, each with an ID, a severity, a detail, why it matters, and a fix |
| `unknown` | The check could not read what the practice needs, and the reason says why. An unknown is never a finding |
| `not_applicable` | The practice does not apply to the repository, and the reason says why |

A result also names the commit it read, the base branch, the worker, when it was checked, and the version of the practice table and of the secret rules it used.

The check runs when a repository is registered, by `repository add`, an adoption, or a plan approval that registers a manifest's repositories; when a planning session starts, for each active repository of the project; and when a member or a delegate asks for it. Nothing runs it on a schedule or on a new commit of the base branch, so every view shows the commit a result read and its age.

### A finding never blocks

No claim, lane start, review, merge, plan approval, or adoption reads a finding. A repository with a `high` finding runs work exactly as one with none, and `ropeshift practices` exits 0 whatever it finds, so a script that runs it never fails on a finding. A finding is advice that a member reads and a planning session plans work for; acting on it is your decision.

## The practices

| ID | Severity | Applies when | Template |
|----|----------|--------------|----------|
| [`env-ignored`](#env-ignored) | `high` | Always | `repository/gitignore` |
| [`secrets`](#secrets) | `high` | Always | None |
| [`ci`](#ci) | `medium` | Always | `pre-commit-config.yaml` |
| [`pre-commit`](#pre-commit) | `low` | Always | `pre-commit-config.yaml` |
| [`lockfile`](#lockfile) | `low` | A package manager is detected and its manager has a lockfile | None |
| [`dependency-updates`](#dependency-updates) | `low` | A package manager is detected | `dependabot.yml` |
| [`security-policy`](#security-policy) | `low` | Always | `security.md` |
| [`codeowners`](#codeowners) | `low` | Always | `codeowners` |
| [`license`](#license) | `low` | The forge reports the repository public | None |
| [`tests`](#tests) | `low` | A package manager is detected | None |
| [`branch-protection`](#branch-protection) | `medium` | The repository has a forge binding | None |
| [`required-review`](#required-review) | `medium` | The repository has a forge binding | None |

A template is a file the binary embeds. [Templates](web-ui.md#templates) in the web UI shows each one at `/templates?name=<template>`, with Copy, and `ropeshift template show <template>` prints the same text. Each finding's fix names its template with that command, and a practice with no template says so.

### `env-ignored`

- Severity: `high`
- Applies when: Always
- Met when: the committed `.gitignore` files ignore each of `.env`, `.env.local`, `.env.development`, `.env.production`, and `.env.test`, in the root and in each folder that holds a detected manifest, and no tracked file at any depth is named `.env` or `.env.*` other than `.env.example`. Only the `.gitignore` files committed at the commit count, as [git check-ignore](https://git-scm.com/docs/git-check-ignore) reads them; your global excludes file and `.git/info/exclude` do not.
- Why it matters: An env file holds credentials and local settings. Tracked, it reaches every clone and stays in the history for good; a lane environment's written files must be ignored too.
- Fix: Add `.env`, `.env.*`, and `!.env.example` to `.gitignore`. For a tracked env file, run `git rm --cached <path>` and rotate every credential it held.
- Template: `repository/gitignore`, at `/templates?name=repository/gitignore` in [Templates](web-ui.md#templates), printed by `ropeshift template show repository/gitignore`
- Finding IDs: `env-ignored:ignore:<folder>` for a folder whose probes are not ignored, and `env-ignored:tracked:<path>` for a tracked env file

### `secrets`

- Severity: `high`
- Applies when: Always
- Met when: no rule of the secret rule set matches a tracked file at the commit or a line added anywhere in the history. The rules each match one kind of credential: `private_key`, `github_pat`, `github_token`, `gitlab_token`, `npm_token`, `aws_access_key`, `aws_secret_key`, `stripe_key`, `google_api_key`, `slack_token`, `api_key`, and `jwt`. The first check reads the whole history, and each later one reads only the commits added since, unless the base branch was rewritten, when it reads the whole history again. A file over 1 MiB, or with a NUL byte in its first 8000 bytes, is skipped and counted in the result. A result keeps at most 200 `secrets` findings and says when it left some out.
- Why it matters: A credential in a tracked file or in history can be read by anyone who can read the repository, every lane included, and deleting the file does not remove it from history.
- Fix: Revoke and rotate the credential at its provider first, then remove it from the file. Removing it from history needs a history rewrite, which ropeshift does not do. Acknowledge a match that is a test fixture or an example.
- Template: none
- Finding IDs: `secrets:<rule>:<path>:<digest>`, where the digest is the first 12 hexadecimal characters of the SHA-256 of the matched text

### `ci`

- Severity: `medium`
- Applies when: Always
- Met when: a file matches `.github/workflows/*.yml` or `.github/workflows/*.yaml`; or the no-CI recipe holds: the `pre-commit` practice is met and the repository's merge authority is `person_merges` or `person_approves`. A repository not registered yet counts at the default, `person_merges`.
- Why it matters: Checks on every change catch what a review misses, and with review target `pull_request` the base branch's required checks gate the merge.
- Fix: Add a GitHub Actions workflow that runs the repository's checks and make its check required on the base branch (https://docs.github.com/en/actions/concepts/workflows-and-actions/workflows). With no CI, follow the no-CI recipe: a pre-commit hook and merge authority `person_merges` or `person_approves`.
- Template: `pre-commit-config.yaml`, at `/templates?name=pre-commit-config.yaml` in [Templates](web-ui.md#templates), printed by `ropeshift template show pre-commit-config.yaml`
- Finding ID: `ci`

[Required checks](github.md#required-checks) says how ropeshift reads a base branch's required checks.

### `pre-commit`

- Severity: `low`
- Applies when: Always
- Met when: a `.pre-commit-config.yaml` is committed in the root, or the root `Makefile` has a `hooks` target, as the workspace shape's `Makefile` does.
- Why it matters: A quick check before every commit, each lane's commit included, catches errors before they reach a review.
- Fix: Commit the pre-commit configuration, or in a workspace keep the `Makefile`'s `hooks` target and run `make hooks`.
- Template: `pre-commit-config.yaml`, at `/templates?name=pre-commit-config.yaml` in [Templates](web-ui.md#templates), printed by `ropeshift template show pre-commit-config.yaml`
- Finding ID: `pre-commit`

### `lockfile`

- Severity: `low`
- Applies when: A package manager is detected and its manager has a lockfile
- Met when: for each folder that holds a manifest, one of its manager's lockfiles sits in that folder or a folder above it, up to the root. A `requirements.txt` counts as its own lockfile when every requirement line pins a version with `==`. [Package managers](#package-managers) lists each manager's manifests, lockfiles, and lock command, and when the practice does not apply to a manifest.
- Why it matters: Without a lockfile each install can resolve different versions, so a build cannot be repeated and a compromised release can enter unseen.
- Fix: Run the manager's lock command and commit the file it writes.
- Template: none
- Finding IDs: `lockfile:<manager>` for a manifest in the root, and `lockfile:<manager>:<folder>` for one below it

Each finding's detail names the lock command to run.

### `dependency-updates`

- Severity: `low`
- Applies when: A package manager is detected
- Met when: the repository holds `.github/dependabot.yml` or `.github/dependabot.yaml`, or a Renovate configuration file in one of the 13 file locations Renovate documents: `renovate.json`, `renovate.jsonc`, `renovate.json5`, the same three in `.github/` and in `.gitlab/`, `.renovaterc`, `.renovaterc.json`, `.renovaterc.jsonc`, and `.renovaterc.json5` ([Renovate configuration options](https://docs.renovatebot.com/configuration-options/)). The `renovate` key in `package.json` does not count, because Renovate documents storing its configuration there as deprecated (same source).
- Why it matters: A dependency with a known vulnerability stays until someone updates it; an update configuration proposes each update as a pull request.
- Fix: Copy the template to `.github/dependabot.yml` with one entry per package manager.
- Template: `dependabot.yml`, at `/templates?name=dependabot.yml` in [Templates](web-ui.md#templates), printed by `ropeshift template show dependabot.yml`
- Finding ID: `dependency-updates`

The Dependabot ecosystem column of [the package manager table](#package-managers) gives the `package-ecosystem` value of each entry ([Dependabot options reference](https://docs.github.com/en/code-security/dependabot/working-with-dependabot/dependabot-options-reference#package-ecosystem-)).

### `security-policy`

- Severity: `low`
- Applies when: Always
- Met when: a file named `SECURITY.md`, compared without case, sits in the root, `.github/`, or `docs/`. The check reads only that the file exists, not what it says.
- Why it matters: It tells a person who finds a vulnerability how to report it privately instead of in a public issue.
- Fix: Commit `SECURITY.md` from the template and name a private reporting channel.
- Template: `security.md`, at `/templates?name=security.md` in [Templates](web-ui.md#templates), printed by `ropeshift template show security.md`
- Finding ID: `security-policy`

### `codeowners`

- Severity: `low`
- Applies when: Always
- Met when: a file named exactly `CODEOWNERS` sits in the root, `.github/`, or `docs/`. The check reads only that the file exists, not the owners it names.
- Why it matters: It names who reviews each path; with a rule that requires code owner review, a change to a sensitive path needs its owner.
- Fix: Commit `CODEOWNERS` from the template and name the owners.
- Template: `codeowners`, at `/templates?name=codeowners` in [Templates](web-ui.md#templates), printed by `ropeshift template show codeowners`
- Finding ID: `codeowners`

### `license`

- Severity: `low`
- Applies when: The forge reports the repository public
- Met when: a file in the root is named `LICENSE`, `LICENCE`, or `COPYING`, compared without case, with or without an extension. A private or internal repository, and a repository with no forge binding, is `not_applicable`.
- Why it matters: A public repository with no license grants nobody the right to use, change, or share it.
- Fix: Choose a license and commit it as `LICENSE` (https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/licensing-a-repository).
- Template: none. Choosing a license is the owner's decision, so ropeshift ships no license template.
- Finding ID: `license`

### `tests`

- Severity: `low`
- Applies when: A package manager is detected
- Met when: for each detected manager, a tracked file outside the skipped folders matches the test convention of its language, as the Test files column of [the package manager table](#package-managers) gives it.
- Why it matters: Tests are the evidence review and acceptance rest on; with none, a change can be judged only by reading it.
- Fix: Add tests by the language's convention.
- Template: none
- Finding IDs: `tests:<manager>`

### `branch-protection`

- Severity: `medium`
- Applies when: The repository has a forge binding
- Met when: the forge says the base branch is protected by branch protection, or a ruleset rule applies to it. [The forge reads](#the-forge-reads) lists what the check asks GitHub.
- Why it matters: An unprotected base branch takes direct pushes, force pushes, and deletion, which bypass review.
- Fix: Protect the base branch with branch protection or a ruleset (https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches, https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets).
- Template: none
- Finding ID: `branch-protection`

### `required-review`

- Severity: `medium`
- Applies when: The repository has a forge binding
- Met when: branch protection or a ruleset rule requires at least one approving review before a merge into the base branch.
- Why it matters: Without a required approving review, a change can merge with nobody but its author having read it.
- Fix: Require at least one approving review on the base branch, by branch protection or a ruleset.
- Template: none
- Finding ID: `required-review`

A lane holds no forge credential, so only a person changes a forge setting: these two findings, like rotating a leaked credential and choosing a license, are steps for you.

## Package managers

The check detects a package manager by its manifest, at any depth of the tracked paths. It skips every path with a folder named `node_modules`, `vendor`, `third_party`, `testdata`, or `.git`. Each folder that holds a manager's manifest is one project of that manager, with its own `lockfile` finding, and its own env probes for `env-ignored`.

| Manager | Manifests | Lockfiles | Lock command | Test files | Dependabot ecosystem | Documentation |
|---------|-----------|-----------|--------------|------------|----------------------|---------------|
| `go` | `go.mod` | `go.sum` | `go mod tidy` | `*_test.go` | `gomod` | [go mod tidy](https://go.dev/ref/mod#go-mod-tidy), [go.sum files](https://go.dev/ref/mod#go-sum-files) |
| `npm` | `package.json` | `package-lock.json`, `npm-shrinkwrap.json`, `yarn.lock`, `pnpm-lock.yaml`, `bun.lock`, `bun.lockb` | `npm install` | `*.test.*` or `*.spec.*` with the extension `js`, `jsx`, `ts`, `tsx`, `mjs`, `cjs`, `mts`, or `cts`, a file named `test` or `spec` with one of those extensions, or any file under a `__tests__` folder | `npm` | [package-lock.json](https://docs.npmjs.com/cli/v11/configuring-npm/package-lock-json), [npm install](https://docs.npmjs.com/cli/v11/commands/npm-install) |
| `python` | `pyproject.toml`, `Pipfile`, `requirements.txt` | `poetry.lock`, `uv.lock`, `pdm.lock`, `Pipfile.lock`, or a `requirements.txt` whose every requirement pins a version with `==` | `uv lock` for `pyproject.toml`, `pipenv lock` for `Pipfile`, `uv pip compile requirements.txt -o requirements.txt` for `requirements.txt` | `test_*.py` or `*_test.py` | `pip` | [uv lock](https://docs.astral.sh/uv/concepts/projects/sync/), [pipenv lock](https://pipenv.pypa.io/en/latest/cli.html), [uv pip compile](https://docs.astral.sh/uv/pip/compile/), [requirements files](https://pip.pypa.io/en/stable/reference/requirements-file-format/) |
| `cargo` | `Cargo.toml` | `Cargo.lock` | `cargo generate-lockfile` | Any `.rs` file under a `tests` folder, or a `.rs` file that holds `#[test]` | `cargo` | [cargo generate-lockfile](https://doc.rust-lang.org/cargo/commands/cargo-generate-lockfile.html) |
| `bundler` | `Gemfile` | `Gemfile.lock` | `bundle lock` | `*_test.rb` or `*_spec.rb` | `bundler` | [bundle lock](https://guides.rubygems.org/command-reference/bundle-lock/) |
| `composer` | `composer.json` | `composer.lock` | `composer update` | `*Test.php` | `composer` | [composer update](https://getcomposer.org/doc/03-cli.md#update-u-upgrade) |
| `maven` | `pom.xml` | None | None | `Test*.java`, `*Test.java`, `*Tests.java`, `*TestCase.java`, `IT*.java`, `*IT.java`, or `*ITCase.java` under `src/test/` | `maven` | [dependency mechanism](https://maven.apache.org/guides/introduction/introduction-to-dependency-mechanism.html) |
| `gradle` | `build.gradle`, `build.gradle.kts` | `gradle.lockfile` | `gradle dependencies --write-locks` | Any file under `src/test/` | `gradle` | [dependency locking](https://docs.gradle.org/current/userguide/dependency_locking.html) |
| `swift` | `Package.swift` | `Package.resolved` | `swift package resolve` | Any `.swift` file under a `Tests` folder | `swift` | [swift package resolve](https://docs.swift.org/latest/documentation/packagemanagerdocs/packageresolve) |

The `lockfile` practice is `not_applicable` for a manifest that declares nothing to lock:

| Manager | Not applicable when |
|---------|---------------------|
| `go` | `go.mod` has no `require` directive |
| `npm` | `package.json` has no non-empty `dependencies`, `devDependencies`, or `optionalDependencies` |
| `python` | `pyproject.toml` declares no dependency under `[project] dependencies` or `[tool.poetry.dependencies]` other than `python`, and no other manifest exists in its folder |
| `composer` | `composer.json` has no non-empty `require` or `require-dev` |
| `maven` | Always, with the reason "Maven has no lockfile": Maven's dependency mechanism pins versions in the POM and documents no lockfile ([dependency mechanism](https://maven.apache.org/guides/introduction/introduction-to-dependency-mechanism.html)) |

A manifest the check cannot read, or a `package.json` or `composer.json` that is not valid JSON, makes `lockfile` `unknown` with the reason.

### What the check does not follow

- A `requirements.txt` include line (`-r other.txt`, or `--requirement`) is not followed. The check reads only the `requirements.txt` in the manifest's folder, and treats every line that starts with `-` as an option, not a requirement. The requirements of an included file are not checked for `==` pins, so a `requirements.txt` that holds only include lines counts as pinned. pip documents include lines in [referring to other requirements files](https://pip.pypa.io/en/stable/reference/requirements-file-format/#referring-to-other-requirements-files).
- The deprecated `renovate` key in `package.json` does not count as a dependency update configuration. Move the configuration to one of Renovate's configuration files, as [`dependency-updates`](#dependency-updates) lists them.

## Finding IDs

A finding's ID is its practice's ID, or the practice's ID and a subject joined by `:` where one practice can find several things. An ID stays the same from one check to the next while what it names is unchanged, so an acknowledgement keeps holding.

| Practice | ID | Example |
|----------|----|---------|
| `env-ignored`, a folder whose env probes are not ignored | `env-ignored:ignore:<folder>`, with `.` for the root | `env-ignored:ignore:.` |
| `env-ignored`, a tracked env file | `env-ignored:tracked:<path>` | `env-ignored:tracked:apps/web/.env` |
| `secrets` | `secrets:<rule>:<path>:<first 12 hex characters of the digest>` | `secrets:github_token:config/dev.yaml:1a2b3c4d5e6f` |
| `lockfile`, a manifest in the root | `lockfile:<manager>` | `lockfile:go` |
| `lockfile`, a manifest below the root | `lockfile:<manager>:<folder>` | `lockfile:npm:web` |
| `tests` | `tests:<manager>` | `tests:go` |
| Every other practice | The practice's ID | `codeowners` |

A `secrets` ID changes when the matched text changes, because the digest is of the text, so a different credential in the same file is a finding of its own, with its own acknowledgement.

## Run the check

### On the command line

```bash
ropeshift practices
ropeshift practices --repository harbor-api
ropeshift practices --latest
ropeshift practices --latest --json
```

Without `--latest`, `ropeshift practices` asks the worker that keeps each repository's checkout to run the check, waits until each worker reports or its request expires, and prints the latest result of each repository. `--repository` names one repository. `--latest` asks for nothing and prints what the project holds, and `--json` prints the views the API answers. Each repository prints its open findings by severity, each with its ID, severity, detail, why it matters, and its fix with the command that prints the template; then the findings a member acknowledged, with each reason; then the `unknown` and `not_applicable` practices with their reasons; and one line naming the practices met. [Command line](cli.md#commands) gives the command's whole output, and [API](api.md#routes) its routes.

A request goes to a connected worker that keeps a checkout of the repository, the daemon's own worker first. A repository with no such worker prints `not checked yet` with the reason, such as "no connected worker keeps a checkout of harbor-api", and a worker on a plane protocol older than the one that carries the practices check is asked to upgrade. A request expires 30 minutes after it is made, because a first scan of a long history can take minutes; a report that comes later still becomes the latest result. A repository holds one open request at a time, and asking again while one is open answers that one.

### In the web UI

The Overview's side column has a Practices panel after Needs you. It has one row for each repository: its name, which opens the repository's Practices tab; its open findings as `high`, `medium`, and `low` badges, each with its count, or No open findings; the count of findings acknowledged and of practices the check could not read; the short commit the result read; and its age. A repository no check has reported reads Not checked yet. Run check, in the panel's heading, asks every repository's worker to run the check, and a repository that cannot be asked says why on its row, such as that no connected worker keeps a checkout of it.

[Screenshot placeholder best-practices-01]: assets/screenshots/best-practices-01-light.png "The Practices panel of Harbor's Overview, with harbor-api, harbor-web, and harbor-plan and their open findings by severity"

On Repositories, the Findings column shows each repository's open findings with the same badges. The record below the table has two tabs, Settings and Practices, and the Practices tab's address is `/projects/<project>/repositories?repo=<id>&tab=practices`. The tab gives the result's Commit, Base branch, Worker, and when it was Checked, with Run check for this repository. Its Practices table lists every practice in table order with its Status, Why it matters, and Fix. The status is Met, the severity of the most serious open finding, Acknowledged with the severity beside it in a muted word, Unknown with the reason, or Not applicable with the reason. A fix that names a template links to the template in [Templates](web-ui.md#templates) and shows the command that prints it, with Copy. Its Findings table lists each finding with its ID and detail, its State, and its Acknowledgement. A `secrets` finding shows its rule, file, line, and commit, and never the text the rule matched.

[Screenshot placeholder best-practices-02]: assets/screenshots/best-practices-02-light.png "The Practices tab of harbor-api, with its result, the Practices table, and the Findings table"

While a request is open, Run check shows Checking on and the worker that carries it out, and the page reads the request once a second until it is reported or expired. An expired request reads "The check did not finish within 30 minutes. A later result still appears here."

The command palette lists Run the practices check, which moves to Run check on the Overview.

## Acknowledge a finding

A finding you decide to leave open, such as a `secrets` match in a test fixture, or a `codeowners` finding in a repository with one maintainer, can be acknowledged with a reason. An acknowledged finding stays visible: every view keeps it in the list, marked with its reason, the member who gave it, and when, and its practice keeps the status `finding`. Nothing filters it out, and a planning session proposes no work for it.

```bash
ropeshift practices acknowledge harbor-api secrets:jwt:test/fixtures/session.json:1a2b3c4d5e6f --reason "A test fixture signed with a throwaway key"
ropeshift practices withdraw harbor-api secrets:jwt:test/fixtures/session.json:1a2b3c4d5e6f
```

The repository is named by its name, identity, or a prefix of it, and the finding by its ID, as `ropeshift practices` prints it. The reason is required, at most 2 KiB, and kept on one line with the shapes of credentials taken out. A finding that already has an acknowledgement is refused with exit 4; withdraw it first to give a new reason. A finding no result holds may be acknowledged, because a check may not have run since it appeared. Withdrawing lists the finding among the open ones again.

In the web UI, Acknowledge on an open finding of the Practices tab's Findings table opens Acknowledge finding: the finding's ID, its detail, why its practice matters, and a required Reason of at most 2 KiB, with Cancel and Acknowledge. An acknowledged finding stays in the table and in the count of what was acknowledged, with its reason, the member who gave it, and when, and its Withdraw opens Withdraw acknowledgement, whose Withdraw acknowledgement button restores the plain finding. The palette lists Acknowledge a practices finding and Withdraw an acknowledgement, which open the same dialogs with a Repository picker and a Finding picker over the open findings or the acknowledged ones.

[Screenshot placeholder best-practices-03]: assets/screenshots/best-practices-03-light.png "Acknowledge finding, with harbor-api's env-ignored finding, why it matters, and a reason filled in"

Acknowledging and withdrawing are consent operations that no delegate policy exempts. A delegate's call, the assistant's included, prepares a confirmation card that names the finding, why its practice matters, and the reason, and a member confirms it, in Needs you or with `ropeshift confirm` ([delegates](authorization.md#delegates)). Running the check is an operational operation, so a delegate runs it as itself.

## Findings in an adoption dry run

`ropeshift adopt --dry-run` lists, under each repository it would register, a Practices block in the format of `ropeshift practices`, with no acknowledgements. The web adoption's Dry run step has Practices found, with one closed row for each repository registering would add, which opens to its open findings by severity, each with its fix ([adopt an existing project](adopt.md#2-read-the-dry-run)).

[Screenshot placeholder best-practices-04]: assets/screenshots/best-practices-04-light.png "Practices found in the dry run of importing lighthouse-api, with its eight findings counted by severity"

The dry run runs the check on the daemon's machine, at the base branch registration would record, with a scan of the whole history and no forge binding, so `branch-protection`, `required-review`, and `license` are `not_applicable`. It records nothing. A finding is no part of the plan's digest and never blocks the adoption. Adopting registers the repositories, and registration asks for the check that records a result.

## Findings in a planning session

A planning session, and an adopt session, begins with what the check last found in each active repository: the commit it read and how long ago, each open finding with its ID, severity, detail, and fix, each acknowledged finding with its reason, and each `unknown` practice with its reason. A repository no check has reported reads as not checked yet.

The planning agent proposes foundation work for each open finding: a contract in the plan's first milestone whose statement names the finding IDs it fixes, with a criterion that a practices check at the merged commit no longer reports them. It proposes none for an acknowledged finding. A finding that needs a person, such as `branch-protection`, `required-review`, rotating a leaked credential, or choosing a license, becomes a step for you in the plan's goal, naming its finding ID, and a work order covers only the part a lane can do. A finding never blocks the plan or its approval. [How ropeshift works](how-it-works.md#the-plan) describes the planning session.

## Security points in a review

Whatever its criteria say, every review also judges four security points in the diff: `secrets`, credentials added to files or logs; `injection`, untrusted input reaching a shell, a query, a path, or a template; `dependencies`, dependencies added or changed, and whether each is pinned and maintained; and `authority`, changes to permissions, CI workflows, `CODEOWNERS`, forge settings, and ropeshift's profiles and policies. The review records each concern as a security note, with its point, reasons, and citations.

A note is not a verdict: it fails no criterion and never changes a verdict, a rework, an acceptance, or a merge. Notes appear under Security notes after the verdicts, in the web UI, in `ropeshift work show`, and in the pull request comment of a repository that reviews through pull requests. [Security points and notes](review-and-merge.md#security-points-and-notes) gives their limits and every place they appear.

The practices check reads a repository at its base branch, and the security points read each change before it merges, so the two cover one another: a credential the review misses in a diff is a `secrets` finding at the next check after it merges.

## The forge reads

For a repository with a forge binding, the check reads GitHub through `gh api`, under the sign-in the worker's GitHub CLI already has ([sign in to the GitHub CLI](github.md#1-sign-in-to-the-github-cli)). It changes no forge setting.

| Read | What it gives |
|------|---------------|
| `repos/{owner}/{repo}` | The repository's `visibility`, for `license` |
| `repos/{owner}/{repo}/branches/{branch}` | Whether the base branch is `protected` by branch protection |
| `repos/{owner}/{repo}/rules/branches/{branch}` | The ruleset rules that apply to the branch ([rules for a branch](https://docs.github.com/en/rest/repos/rules#get-rules-for-a-branch)): any rule protects it, and a `pull_request` rule with `required_approving_review_count` of at least 1 requires a review |
| `repos/{owner}/{repo}/branches/{branch}/protection/required_pull_request_reviews` | Read only when branch protection applies and no ruleset rule requires a review: the `required_approving_review_count` ([branch protection](https://docs.github.com/en/rest/branches/branch-protection)) |

| Answer | Status |
|--------|--------|
| The repository is `public` | `license` applies |
| The repository is `private` or `internal` | `license` is `not_applicable`, "the repository is not public" |
| The branch read or the rules read says protected | `branch-protection` is `met` |
| Both reads answer, and neither says protected | `branch-protection` and `required-review` are each a finding |
| A ruleset rule, or branch protection, requires at least one approving review | `required-review` is `met` |
| Branch protection answers a count of 0, or a 404 whose message says reviews are not enabled **(unverified)** | `required-review` is a finding |
| A refused sign-in (401 or 403), a 404 the rows above do not settle, a rate limit, or `gh` not available | The practice the read decides is `unknown`, with the forge's message |

A sign-in that may read the repository may still be refused the protection reads, and a refusal is never a finding: `branch-protection` can be `met` while `required-review` is `unknown`. A rate limit stops the reads that remain, and their practices read "rate limited until" a time. A repository with no forge binding, and every repository in an adoption dry run, reads none of this: the three forge practices are `not_applicable`, "no forge binding".

### Why a practice is unknown

A practice is `unknown` when the check could not read what it needs. It is never a finding, so a read that failed never reports a problem the repository may not have. The reason says what failed:

| Practice | Reason |
|----------|--------|
| `env-ignored` | The check got no ignore answer for a probe path from the committed `.gitignore` files |
| `secrets` | The secret scan could not run, or the commits since the last scan were read by no scan; earlier `secrets` findings are kept |
| `ci`, `pre-commit` | The root `Makefile` cannot be read |
| `lockfile` | A manifest cannot be read, or is not valid JSON |
| `tests` | A source file the convention reads, such as a `.rs` file for `#[test]`, cannot be read |
| `license`, `branch-protection`, `required-review` | A forge read was refused, rate limited, or not settled, as the table above gives |

Run the check again once the cause is fixed, such as after `gh auth login` on the worker; `ropeshift practices` prints the new result.

## Next

[Set up your projects](set-up-projects.md) registers the repositories the check reads, [adopt an existing project](adopt.md) shows the findings in its dry run, and [plan your first change](plan-your-first-change.md) starts the planning session that proposes the work that fixes them.
