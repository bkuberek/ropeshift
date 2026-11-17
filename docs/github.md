# Connect a GitHub repository

This guide has ropeshift review a repository's changes through pull requests on GitHub. You sign in to the GitHub CLI yourself, in a terminal, then switch the repository to pull requests in the web UI, run a change through its pull request, and read the pull request, its checks, and its review. It follows Harbor, whose `harbor-api` merges at `person_approves` after [review and merge](review-and-merge.md), and its next work order, Serve the sailings of each berth. Put your own project and work in their place.

## Before you start

- Finish [review and merge](review-and-merge.md): the repository is registered, and a work order in it is ready.
- The repository is on GitHub, and your own GitHub account can push to it and open pull requests in it.

## What changes with pull requests

A repository's review target says where a finished change is reviewed and where it lands:

| Review target | What happens to a finished change |
|---------------|-----------------------------------|
| `branch`, the default | The review reads the lane's branch on this machine. Nothing is pushed before a merge, and no forge runs checks on it, so the review's verdicts are the only gate |
| `pull_request` | The worker pushes the lane's branch and opens a pull request against the work order's base. The review reads the pull request's head commit and posts its verdicts on the pull request, and the checks GitHub requires gate the merge |

A team that protects its base branch on GitHub uses `pull_request`: ropeshift merges a pull request only through GitHub, which applies the branch protection.

## In a terminal

### 1. Sign in to the GitHub CLI

Ropeshift opens, reads, and merges pull requests with the [GitHub CLI](https://cli.github.com/), `gh`, under your own sign-in, so each pull request is yours, opened by your account. It never signs in for you, never reads or stores the token, and offers no sign-in of its own. A terminal step is the only way to give `gh` your sign-in.

Install `gh` on the machine whose worker keeps the repository's clone, which in solo mode is the machine the service runs on, in a folder on the service's `PATH`: a service that `ropeshift service install` wrote keeps the `PATH` of the shell that ran it. Then sign in to GitHub yourself, and check the sign-in:

```sh
gh auth login
gh auth status
```

[`gh auth status`](https://cli.github.com/manual/gh_auth_status) says which host and account `gh` is signed in to.

No lane gets this sign-in. Each agent session starts without `GH_TOKEN`, `GITHUB_TOKEN`, `GH_ENTERPRISE_TOKEN`, or `GITHUB_ENTERPRISE_TOKEN`, and with `GH_CONFIG_DIR` pointed at an empty directory of its own, so a `gh` that an agent runs finds no sign-in. Pushes, pull requests, comments, and merges are steps of the worker process, never of an agent.

## In the web UI

### 1. Review through pull requests

In the sidebar, select Repositories, then the repository, here `harbor-api`. In its record, under Review target, choose `pull_request`.

Forge binding names the repository on GitHub as `HOST/OWNER/REPO`, the form `gh --repo` takes. Leave it empty when the clone's remote is the repository's address on GitHub, such as `git@github.com:acme/widgets.git` or `https://github.com/acme/widgets.git`: ropeshift takes `github.com/acme/widgets` from it. Enter it when the remote names another host, such as a mirror or an SSH host alias. Harbor's remote is a mirror, so enter `github.com/harbor/harbor-api`. The field refuses a value that is not three parts, such as `acme/widgets`, or whose parts hold a space or one of `\ : @ ? #`.

![The record of harbor-api, with pull_request chosen, the forge binding, and Save for the unsaved changes](assets/screenshots/github-01-light.png#gh-light-mode-only)
![The record of harbor-api, with pull_request chosen, the forge binding, and Save for the unsaved changes](assets/screenshots/github-01-dark.png#gh-dark-mode-only)

Select Save. A record that reviews through pull requests with no binding, and a remote that names no repository on a forge, is refused with the reason.

### 2. Check the worker's sign-in

The worker that keeps the repository's clone runs `gh auth status --hostname github.com` for it once a minute, and reports what it found. The Forge column of Repositories shows it:

| Forge | Meaning | What to do |
|-------|---------|------------|
| gh signed in | `gh` is on the worker's `PATH` and signed in to the host | Nothing |
| gh signed out on `<worker>` | `gh` is there and not signed in to the host; the cell quotes what `gh` said | Run `gh auth login` on that machine |
| No gh on `<worker>` | The worker found no `gh` on its `PATH` | Install `gh`, or fix the service's `PATH` |

While `gh` is signed out or missing, the review of each change waits, as `forge_unavailable`, until it is fixed.

### 3. Run a change through its pull request

Start the next work order of the repository, as in [watch work run](watch-work-run.md#3-start-a-work-order-yourself), here Serve the sailings of each berth with the Lane ID `serve-sailings`. Inside a run window, the orchestrator claims it for you.

When the lane ends with a commit, the worker that ran it pushes the lane's branch to the repository's remote and opens a pull request against the work order's base, with the work order, its contract, and its criteria in the description. A push never forces and runs no repository hook. The review waits, ready but held, as `pull_request_pending`, until the pull request is open with the lane's commit as its head. A push or pull request that fails, such as one that branch protection refuses, is tried again a minute later.

Then start the review on the Floor, as in [review and merge](review-and-merge.md#1-start-the-review), here with the Lane ID `review-serve-sailings`. It reads the pull request's head commit and gives each criterion a verdict, and the worker posts the verdicts on the pull request as one comment: each criterion with its verdict, its reasons, and its citations. It is a comment, not an approving review, because the pull request is opened under your account, and [GitHub does not let the author approve their own pull request](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/reviewing-changes-in-pull-requests/approving-a-pull-request-with-required-reviews).

A failed criterion sends the work to rework on the same branch. The next attempt's commit updates the same pull request, the new head is reviewed again, and verdicts on the old head no longer count.

### 4. Read the open pull requests

In the sidebar, under Work, select Reviews, then Pull requests. It lists each open pull request ropeshift opened for work that waits for acceptance or a merge: its number, its address, and its branch into its base, the work order, its head commit, whether a review read that commit, its checks on GitHub, whether GitHub says it merges cleanly, and how long it has been in review. The work order's Pull request and checks tab shows the same pull request and its checks as soon as the worker reports it.

![Reviews, Pull requests, with the sailings pull request read by the review, its two checks passed, and merging cleanly](assets/screenshots/github-02-light.png#gh-light-mode-only)
![Reviews, Pull requests, with the sailings pull request read by the review, its two checks passed, and merging cleanly](assets/screenshots/github-02-dark.png#gh-dark-mode-only)

### 5. Approve the merge

Once the review passes, the work moves on at the repository's merge authority. At `person_approves`, `harbor-api`'s level, it waits in Needs you as a merge request, `Approve the merge of <work order's title>?`, whose card names the pull request and its address beside the reviewed commit. Select Approve merge, as in [approve the merge](review-and-merge.md#7-approve-the-merge). The worker that opened the pull request merges it on GitHub at the reviewed commit, and once ropeshift reads the merge, the work reads `done`.

### 6. Read the work order's pull request and merge

In the sidebar, select Work orders, then Completed, and open the work order. Select the Pull request and checks tab. The pull request gives its number, its state, its address, its branch into its base, its head commit, and the commit its merge made. Checks lists the checks on record for the work's commit, here the lane's own test, beside the pull request's checks on GitHub, each marked required where the base branch requires it. Merge gives each approval of its merge: the commit approved, who approved it, at which merge authority, the commit the merge made, and the pull request it went through. Artifacts names the branch.

![The Pull request and checks tab of the merged work, with the lane's check and the merge through pull request #1](assets/screenshots/github-03-light.png#gh-light-mode-only)
![The Pull request and checks tab of the merged work, with the lane's check and the merge through pull request #1](assets/screenshots/github-03-dark.png#gh-dark-mode-only)

## Merging through pull requests

What happens once the review passes every criterion:

| Level | What waits for you | What ropeshift merges |
|-------|--------------------|-----------------------|
| `person_merges`, the default | The work waits in `integrating` until you merge the pull request on GitHub, and reads `done` once ropeshift reads your merge | Nothing |
| `person_approves` | Approve merge in Needs you | The pull request at the reviewed commit, once you approve and every required check is green |
| `review_approves` | Nothing, unless the merge cannot land | The pull request at the reviewed commit, once the review passes and every required check is green |

### Required checks

Ropeshift waits only for the checks the base branch requires, and the base branch's protection on GitHub decides which those are. The worker reads them each time it reads the pull request, with [`gh pr checks --required`](https://cli.github.com/manual/gh_pr_checks):

| A required check | Effect at `person_approves` and `review_approves` |
|------------------|---------------------------------------------------|
| Still running | Holds the merge, and the work says which check it waits on |
| Failed on the reviewed commit | Sends the work to rework, naming the check |
| Passed | Lets the merge go ahead, once every other required check has passed too |

A check the base branch does not require counts for nothing, and a base branch that requires no checks has none to wait for. To make a check gate ropeshift's merges, make it a required status check in the base branch's protection on GitHub ([about protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches#require-status-checks-before-merging)). At `person_merges`, ropeshift merges nothing, and GitHub applies the branch protection when you merge.

### review_approves

At `review_approves`, the review's pass is the approval: once every criterion passes and every required check has passed on the reviewed commit, ropeshift merges with no person in between, and the record names the review lane as the approver. These still wait for you: a criterion the review could not judge, unless a passing check from the forge covers it, work the review still fails after three reviews, and a merge GitHub refuses. Keep a repository at `person_approves` until Agreement, in Reviews, shows how often you agree with the review there; the level never rises by itself.

### How ropeshift merges

Ropeshift merges in one step of its own, never through an agent, and only the commit the review judged. The worker that opened the pull request runs [`gh pr merge`](https://cli.github.com/manual/gh_pr_merge) with `--match-head-commit` set to the reviewed commit, and the flag of the repository's merge method, `--merge`, `--squash`, or `--rebase`. It never passes `--admin`, `--auto`, or `--delete-branch`, so it never bypasses branch protection and never deletes a branch. A pull request whose head moved past the reviewed commit goes back to review at the new head. A merge GitHub refuses waits in Needs you as a Held merge, with what GitHub said, and Retry merge tries it again.

A planning branch in a repository that reviews through pull requests gets a pull request too, and your plan approval is the only approval of its merge ([a planning branch](onboarding.md#a-planning-branch)). `ropeshift repository set` changes the same settings from a terminal ([the command line](cli.md)).

## Next

[Run several projects at once](several-projects.md) moves between projects, reads what waits in all of them, and sets the caps they share.
