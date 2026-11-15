# Connect a GitHub repository

This guide registers a repository on GitHub, has ropeshift review each change through a pull request, and chooses how much of each merge ropeshift does by itself. Every setting here is a member's, and a new repository starts at the safest one: nothing is pushed, and you merge every change yourself.

## Quick path

1. Install the GitHub CLI and sign in to it yourself: `gh auth login`.
2. Register the clone: `ropeshift repository add /path/to/widgets`.
3. Review through pull requests: `ropeshift repository set widgets --review-target pull_request`.
4. Check that the worker can use `gh`: `ropeshift repository show widgets` prints `ok` on its `forge` line.
5. Choose a merge authority: `ropeshift repository set widgets --merge-authority person_approves`.

The sections below explain each step and what you see after it.

## 1. Sign in to the GitHub CLI

Ropeshift opens, reads, and merges pull requests with the [GitHub CLI](https://cli.github.com/), `gh`, under your own sign-in. It never signs in for you, never reads or stores the token, and never offers a sign-in of its own.

1. Install `gh` on the machine whose worker keeps the repository's clone. In solo mode, that is the machine the daemon runs on.
2. Make sure `gh` is on the daemon's `PATH`. A daemon run as a service keeps the `PATH` of the shell that ran `ropeshift service install`.
3. Sign in to the repository's host yourself:

```sh
gh auth login
gh auth status
```

`gh auth status` says which host and account `gh` is signed in to.

No lane gets this sign-in. Each agent session starts without `GH_TOKEN`, `GITHUB_TOKEN`, `GH_ENTERPRISE_TOKEN`, or `GITHUB_ENTERPRISE_TOKEN`, and with `GH_CONFIG_DIR` pointed at an empty directory, so a `gh` that an agent runs finds no sign-in. Pushes, pull requests, comments, and merges are steps of the worker process, never of an agent ([what a lane does without asking](onboarding.md#what-a-lane-does-without-asking)).

## 2. Register the repository

Register the clone in the project, from the machine that keeps it:

```sh
ropeshift repository add /path/to/widgets
```

It reads the remote and the default branch from git, names the repository after its clone's directory, and prints what it registered. `--dry-run` prints what it would do and records nothing, and a folder of clones registers each clone one level down. A repository another project holds is refused, naming that project.

In the web UI, open Repositories and select Register repositories, then give the path to the clone or to a folder of clones.

A new repository takes the safe settings:

| Setting | Value | Meaning |
|---------|-------|---------|
| Review target | `branch` | The review reads the lane's branch on this machine, and nothing is pushed |
| Merge authority | `person_merges` | You merge every change yourself |
| Merge method | `merge` | How ropeshift lands a change when it merges one: `merge`, `squash`, or `rebase` |
| Start from | `merged` | Dependent work starts once the work it depends on is in the base branch |

```sh
ropeshift repository list
```

`repository list` prints each repository with its name, its identity, its base branch, its merge method, its start, its review target, its merge authority, and its remote. Commands take the name or a prefix of the identity of seven characters or more.

## 3. Choose where work is reviewed

The review target says where a finished change is reviewed and where it lands.

| Review target | What happens to a finished change |
|---------------|-----------------------------------|
| `branch` | The review reads the lane's branch on this machine. Nothing is pushed before a merge, and no forge runs checks on it, so the review's verdicts are the only gate |
| `pull_request` | The worker pushes the lane's branch and opens a pull request against the work order's base. The review reads the pull request's head commit, and posts its verdicts on the pull request |

A team that protects its base branch uses `pull_request`, because a merge through a pull request honors the branch protection.

Switch the repository to pull requests:

```sh
ropeshift repository set widgets --review-target pull_request
```

In the web UI, open Repositories, select the repository, choose `pull_request` under Review target, and select Save.

### The forge binding

`pull_request` needs a forge binding: the repository on its forge as `HOST/OWNER/REPO`, the form `gh --repo` takes. Ropeshift takes it from the remote, so a remote such as `git@github.com:acme/widgets.git` or `https://github.com/acme/widgets.git` binds to `github.com/acme/widgets` with nothing to set. For any other remote, name the binding:

```sh
ropeshift repository set widgets --forge-binding github.com/acme/widgets
```

In the web UI, enter it in Forge binding on the repository, in the same `HOST/OWNER/REPO` form, such as `github.com/acme/widgets`. The field checks the binding before it saves: a value that is not three parts, such as `acme/widgets`, or whose parts hold a space or one of `\ : @ ? #`, is refused beside the field, and so is a refusal from the server. `--forge-binding ""` takes the binding from the remote again. A remote that names no repository on a forge, with no binding set, makes `--review-target pull_request` exit 2.

### Check the worker's sign-in

The setting takes effect even when no worker has `gh` yet, because the control plane cannot see a worker's `gh`, so `repository set` prints a warning instead. Each worker that keeps a clone of the repository then runs `gh auth status --hostname <host>` once a minute, and reports what it found:

```sh
ropeshift repository show widgets
```

You see a `forge` line for each such worker:

| State | Meaning | What to do |
|-------|---------|------------|
| `ok` | `gh` is present and signed in to the host | Nothing |
| `signed_out` | `gh` is present, and not signed in to the host; the line quotes what `gh` said | Run `gh auth login` on that machine |
| `missing` | The worker found no `gh` on its `PATH` | Install `gh`, or fix the daemon's `PATH` |

The Forge column of Repositories and the Measures page show the same. The daemon's `--forge-tick` changes how often the worker reads it.

## 4. What a pull request goes through

With review target `pull_request`, each `change` or `rework` lane that ends with a commit goes through these steps:

1. The worker that ran the lane pushes the lane's branch to the remote and opens a pull request against the work order's base, with the work order, its contract, and its criteria in the description. A push never forces and runs no repository hook.
2. The review waits as `pull_request_pending` until the pull request is open with the lane's commit as its head, and as `forge_unavailable` while that worker's `gh` is missing or signed out. A push or pull request that fails, such as one that branch protection refuses, is tried again after a minute.
3. The review lane reads the pull request's head commit and gives each criterion a verdict.
4. The worker posts the verdicts on the pull request as one comment: each criterion with its verdict, reasons, and citations. It is a comment, not an approving review, because the pull request is yours, and [GitHub does not let the author approve their own pull request](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/reviewing-changes-in-pull-requests/approving-a-pull-request-with-required-reviews).
5. A failed criterion sends the work to rework on the same branch. The next attempt's commit updates the same pull request, the new head is reviewed again, and verdicts on the old head no longer count.

`ropeshift work show <work-order-id>` prints the pull request, its state, and its head commit. In the web UI, the work order's Pull request and checks tab shows the same, with what a merge still waits on.

## 5. Choose the merge authority

The merge authority says how much of each change you decide after the review agent judged it. Only a member sets it:

```sh
ropeshift repository set widgets --merge-authority person_approves
```

In the web UI, it is the Merge authority choice on the repository in Repositories.

![A repository's settings in Repositories: base branch, start from, review target, forge binding, and the three merge authority levels](assets/screenshots/repository-light.png#gh-light-mode-only)
![A repository's settings in Repositories: base branch, start from, review target, forge binding, and the three merge authority levels](assets/screenshots/repository-dark.png#gh-dark-mode-only)

What happens once the review passes every criterion:

| Level | What waits for a person | What ropeshift merges itself |
|-------|-------------------------|------------------------------|
| `person_merges`, the default | Through a pull request, the work waits in `integrating` until you merge the pull request on GitHub. With review target `branch`, it waits in Needs you, with the verdict for each criterion beside Accept and Send back for rework | Nothing. Ropeshift records your merge, or your acceptance, and the work reaches `done` |
| `person_approves` | A merge request, bound to the reviewed commit, waits in Needs you and in `ropeshift approval list` | The reviewed commit, once you approve the request, with every required check green on it |
| `review_approves` | Nothing, unless the merge cannot land | The reviewed commit, once the review passes, with every required check green on it. The audit names the review lane as the approval |

At every level, these still wait for a person:

- A criterion the review could not judge, unless a passing check from the forge covers it. A check the lane reported about its own work never does.
- Work the review still fails after three reviews of one work order.
- A merge the forge or git refuses, such as one that branch protection blocks, a closed pull request, or a base branch checked out under you. The work stays in `integrating` with a merge request that says why: approving it tries the merge again, and denying it sends the work to rework with your reason.

To approve a merge request from the command line:

```sh
ropeshift approval list
ropeshift approval show <approval-id>
ropeshift approval approve <approval-id> --digest <reviewed-commit>
```

`approval show` prints the work, the commit, the pull request, and the level, and the digest of a merge request is the reviewed commit. `ropeshift work accept <work-order-id>` on work in `integrating` approves the same merge. A new commit on the branch voids the request.

### How ropeshift merges

Ropeshift merges in one deterministic step of the control plane, never through an agent, and only the commit the review judged:

- Through a pull request, the worker that opened it runs `gh pr merge <number> --match-head-commit <commit>` with `--merge`, `--squash`, or `--rebase`, as the repository's merge method names ([gh pr merge](https://cli.github.com/manual/gh_pr_merge)). It never passes `--admin`, never forces, and never bypasses branch protection. A pull request whose head moved past the reviewed commit goes back to review at the new head.
- With review target `branch`, the worker merges the lane's branch into the base with git, in a temporary worktree, never in your checkout, and only while the branch is still at the reviewed commit. With a remote, it pushes the result to the base branch without forcing, and a push the remote refuses, such as to a protected branch, holds the work for you.

Choose the merge method with `--merge-method merge|squash|rebase`. Every approval and every merge is recorded, with who approved, the level in force, the approved commit, and the merged commit; `ropeshift work show` prints each as an `approved` line.

## 6. Required checks

Ropeshift waits only for the checks the base branch requires, and the base branch's protection on GitHub decides which those are. Each time it reads the pull request, the worker reads them with `gh pr checks --required` ([gh pr checks](https://cli.github.com/manual/gh_pr_checks)):

| A required check | Effect at `person_approves` and `review_approves` |
|------------------|---------------------------------------------------|
| Still running | Holds the merge; `ropeshift work show` says which check it waits on |
| Failed on the reviewed commit | Sends the work to rework, naming the check |
| Green | Lets the merge go ahead, once every other required check is green too |

A check the base branch does not require counts for nothing, and a base branch that requires no checks has none to wait for. To make a check gate ropeshift's merges, make it a required status check in the base branch's protection on GitHub. With review target `branch`, nothing is pushed before the merge, so no forge runs checks and the verdicts are the gate.

At `person_merges`, ropeshift merges nothing, and GitHub applies the branch protection when you merge the pull request.

## Planning branches

A planning session's branch follows the same repository settings. With review target `pull_request`, the worker pushes the planning branch and opens its pull request when the session ends, so you can read the plan there beside its approval. Your plan approval is the only approval of that branch's merge: at `person_approves` and `review_approves`, approving the plan merges the branch at the approved commit, with every required check green, and at `person_merges` you merge it yourself. [A planning branch](onboarding.md#a-planning-branch) covers the rest.

## Where to go next

- The whole review and merge model, including dependencies across repositories: [onboarding](onboarding.md#who-reviews-a-lanes-work).
- Every flag of `repository set`: [the command line](cli.md).
- How far you agreed with the review in each repository, as evidence for choosing a level: `ropeshift repository show <name>` and [review agreement](measures.md#review-agreement).
