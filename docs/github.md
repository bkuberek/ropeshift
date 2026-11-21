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

When the repository's checkout is the project's [workspace](project-structure.md#the-workspace-on-a-machine), a git clone, and it does not ignore `.ropeshift/worktrees/`, the cell also says "This workspace does not ignore .ropeshift/worktrees/; add this line to its .gitignore: /.ropeshift/worktrees/", under the worker's name, and `ropeshift repository show` prints it on a `workspace` line. Add the line and commit it; the next report, within a minute, clears the note. Until then the lanes of the project's other repositories run outside the workspace. Only a repository that reviews through pull requests is reported on, so a workspace that reviews through branches shows the note nowhere, and `ropeshift worker doctor` is where to look.

### 3. Run a change through its pull request

Start the next work order of the repository, as in [watch work run](watch-work-run.md#3-start-a-work-order-yourself), here Serve the sailings of each berth with the Lane ID `serve-sailings`. Inside a run window, the orchestrator claims it for you.

When the lane ends with a commit, the worker that ran it pushes the lane's branch to the repository's remote and opens a pull request against the work order's base, with the work order, its contract, and its criteria in the description. A push never forces and runs no repository hook. The review waits, ready but held, as `pull_request_pending`, until the pull request is open with the lane's commit as its head. A push or pull request that fails, such as one that branch protection refuses, is tried again a minute later.

Then start the review on the Floor, as in [review and merge](review-and-merge.md#1-start-the-review), here with the Lane ID `review-serve-sailings`. It reads the pull request's head commit and gives each criterion a verdict, and the worker posts the verdicts on the pull request as one comment: each criterion with its verdict, its reasons, and its citations, and then, under Security notes, the notes the review left on its [security points](review-and-merge.md#security-points-and-notes). The reasons and citations of each verdict and note are set as literal text in a fenced block, so a mention, a link, or markup in them is shown as written. The notes take at most 20,000 bytes of the comment, and a section that does not fit ends with how many notes it leaves for ropeshift to show. It is a comment, not an approving review, because the pull request is opened under your account, and [GitHub does not let the author approve their own pull request](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/reviewing-changes-in-pull-requests/approving-a-pull-request-with-required-reviews).

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

### When ropeshift reads the checks

The worker reads a pull request's checks on a schedule that follows how long the repository's CI takes. It reads once when the pull request opens, once at the expected p50 of a run, at half the remaining time to the p85 each time after that, and every `ci_steady_interval`, 5 minutes by default, once a run is past its p85, for as long as it runs. Time a run spends queued does not advance the schedule, because a run counts only once GitHub says it started. A new push to the branch reads again at once. With no required check left to finish, ropeshift reads every minute, to learn of a merge or a close you make yourself. The keys are set per repository, with `ropeshift repository set <repository> ci.<key>=<value>` ([how ropeshift reads CI](set-up-projects.md#how-ropeshift-reads-ci)). Each read is a `gh` call and starts no agent session, so a run of three hours costs the same in credits as a run of three minutes: nothing.

### How a read is kept cheap

Each read goes through `gh` and starts no agent session, and each is made to cost as little of GitHub's rate limit as it can:

- **One query for several pull requests.** When two or more pull requests of a repository are due, the worker reads all of them, with the checks on each head and whether the base branch requires each check, in one GraphQL query, where a read of each is two calls apiece. GraphQL's rate limit counts points, the nodes a query could reach divided by 100 and at least 1 ([rate limits and node limits](https://docs.github.com/en/graphql/overview/rate-limits-and-node-limits-for-the-graphql-api)).
- **A conditional probe for one.** When one pull request is due and a required check is still to finish, the worker first asks the pull request, and the check runs and the commit statuses of its head, each with the `ETag` of its last answer in `If-None-Match`. When all three answer `304 Not Modified`, nothing a read would report has changed, no read is made, and a `304` does not count against the primary rate limit ([use conditional requests](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api#use-conditional-requests)). The first probe of a pull request has no `ETag` to send and gives the one to use next. A push, a merge, or a close changes the pull request's answer, so the next read finds it.
- **Rate limits are honoured.** A read GitHub refuses for a rate limit holds that repository's reads for the longest of `retry-after`, the time `x-ratelimit-reset` gives when `x-ratelimit-remaining` is 0, and a back-off that starts at one minute and doubles with each refusal in a row, to an hour, and the daemon says so once for each stretch of refusals. A response whose `x-poll-interval` names a time sets the least gap before the next scheduled read. A hint reads through that gap, and through nothing else ([handle rate limit errors appropriately](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api), [rate limits for the REST API](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)).

GitHub does not document conditional requests for GraphQL, so the query is never conditional.

### Webhooks make a read come sooner

GitHub can tell ropeshift that a check changed, which reads the pull requests at that head at once instead of waiting for the schedule. A delivery is a hint: it carries no state, and the read decides what changed. Polling works alone, so a delivery that never comes costs only the delay, and there is nothing to set up for ropeshift to read CI.

A solo daemon on a laptop has no address GitHub can reach, and its route is on its control socket, which nothing exposes, so it takes no delivery unless you forward that socket to GitHub yourself. A [group server](group-server.md) takes them on its listener, at `/v1/webhooks/github`. That route is the one route of the listener that takes no session and no credential: the signature of each delivery is its credential.

To point a webhook at a repository on a group server:

1. Choose a variable name for the repository's key that starts with `ROPESHIFT_HOOK_`, such as `ROPESHIFT_HOOK_WIDGETS`, and put a long random value in that variable in the server's environment. The ledger never holds the key: it holds the variable's name. A value that is not a name is refused, and so is a name that does not start with `ROPESHIFT_HOOK_`, so no other variable of the server's environment, such as a database URL or a cloud credential, can be named as a key.
2. Name the variable on the repository with `ropeshift repository set <repository> ci.ci_webhook_secret_env=ROPESHIFT_HOOK_WIDGETS`. The repository must have a forge binding, and a restart of the server picks up a changed environment.
3. In the repository's settings on GitHub, under Webhooks, add a webhook ([creating webhooks](https://docs.github.com/en/webhooks/using-webhooks/creating-webhooks)). Set the payload URL to `https://<your server>/v1/webhooks/github`, the content type to `application/json`, and the secret to the same value as the variable. Select individual events, and choose Check runs, Check suites, and Workflow runs. Leave SSL verification on.
4. GitHub sends a ping when it saves the webhook, which the server answers.

A GitHub Enterprise Server names its own host in `X-GitHub-Enterprise-Host`, and the repository on that host is the one the delivery is for. The server first checks that the `X-Hub-Signature-256` header is `sha256=` and 64 hexadecimal characters, and refuses a delivery whose header is anything else before it reads the body. It bounds the body's size, then verifies the header, the HMAC-SHA256 of the body under the repository's key, before anyone acts on the body, and compares it in constant time ([validating webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries)). It computes one digest over the body whether or not the repository is registered, so the time a refusal takes says nothing of which repositories there are. Then:

| Delivery | What the server does |
|----------|----------------------|
| A `check_suite` or a `check_run` with the action `completed`, or a `workflow_run` with `requested`, `in_progress`, or `completed` | Makes a hint for the head it names, which each worker that serves the repository's project takes on its next heartbeat, and no other worker does |
| Any other event or action, and a `ping` | Answers, and makes no hint |
| A delivery ID or a body the server has seen in the last hour | Answers `200` and makes no second hint, so a redelivery, or a captured delivery sent again under a new ID, repeats nothing |
| A missing or wrong signature, a repository ropeshift does not hold, or a repository with no key | Answers `403 bad_signature` alike for every one, makes no hint, and counts it |
| A body of more than a megabyte, or a content type other than `application/json` | Refuses it, as `413` or `415`, and counts it |

The server counts each refusal by its reason and warns of the first of each and of every hundredth after it, in words that name no repository, no key, and no variable. Each refusal also counts against the address it came from, whatever its reason, so an address that sends very many deliveries it cannot sign is refused with `429` for a while. A delivery the server could not look up because its stores could not be read counts too, and the server waits a few seconds before it reads them again. The route has a limit of its own, apart from [the listener's limit on wrong passphrases](group-server.md): a delivery costs one lookup in memory and one HMAC, GitHub sends several at once from the same few addresses, and GitHub does not retry a delivery that is refused, so the route takes hundreds in flight from one address and refuses an address only after a thousand refusals within the lockout. A delivery that holds counts for nothing. The server keeps in memory which variable holds each repository's key, and reads the projects' records again when a repository record changes, so a delivery reads no project's store. Hints live in the server's memory for ten minutes, and a restart loses them, which the schedule makes good.

Prefer a webhook on each repository to one on the organization. An organization webhook sends the server a delivery for every repository of the organization, including the ones ropeshift does not hold, and the server refuses each of those as a repository it does not hold, which counts against GitHub's address like any other refusal. A webhook on a repository sends only that repository's events, so every delivery the server gets is one it can use, and the key of one repository is not the key of every repository of the organization.

### review_approves

At `review_approves`, the review's pass is the approval: once every criterion passes and every required check has passed on the reviewed commit, ropeshift merges with no person in between, and the record names the review lane as the approver. These still wait for you: a criterion the review could not judge, unless a passing check from the forge covers it, work the review still fails after three reviews, and a merge GitHub refuses. Keep a repository at `person_approves` until Agreement, in Reviews, shows how often you agree with the review there; the level never rises by itself.

### How ropeshift merges

Ropeshift merges in one step of its own, never through an agent, and only the commit the review judged. The worker that opened the pull request runs [`gh pr merge`](https://cli.github.com/manual/gh_pr_merge) with `--match-head-commit` set to the reviewed commit, and the flag of the repository's merge method, `--merge`, `--squash`, or `--rebase`. A squash also passes `--body` with the trailers `Ropeshift-Work-Order` and `Ropeshift-Reviewed-Commit`, so the commit it makes names the work order and the reviewed commit. One branch is the exception: an adoption branch that imports a design repository's history always merges with `--merge`, whatever the repository's merge method, because a squash or a rebase drops the merge's second parent and the design history with it ([consolidating a design repository](adopt.md#consolidating-a-design-repository)). That pull request's description says so, and at `person_merges` it tells you to merge it with a merge commit yourself. A base whose rules require a linear history, or a repository with merge commits turned off, refuses that merge, and the held merge says so. It never passes `--admin`, `--auto`, or `--delete-branch`, so it never bypasses branch protection and never deletes a branch. A pull request whose head moved past the reviewed commit goes back to review at the new head. A merge GitHub refuses waits in Needs you as a Held merge, with what GitHub said, and Retry merge tries it again.

A planning branch in a repository that reviews through pull requests gets a pull request too, and your plan approval is the only approval of its merge ([a planning branch](onboarding.md#a-planning-branch)). `ropeshift repository set` changes the same settings from a terminal ([the command line](cli.md)).

## Create a new project's repository on GitHub

`ropeshift new` makes a new project's repositories and gives each one a remote: a URL you name, added as `origin`, or a repository created on GitHub with `gh` under your own sign-in. It asks first whether the project lives in one repository or several, makes each repository's first commit, creates each repository private unless you ask for public, and pushes the first commit. Ropeshift creates repositories on GitHub only, on `github.com`; to host a repository elsewhere, create it there by hand and give its URL as the remote.

From a terminal, run `ropeshift new` and answer the questions, or name everything in flags:

```sh
ropeshift new monorepo ~/code/harbor --create-remote github:acme
ropeshift new workspace ~/code/harbor --create-remote github:acme \
  --new-repository harbor-api --new-repository harbor-web=none
ropeshift new monorepo ~/code/harbor --remote git@example.com:acme/harbor.git
```

`--create-remote github:<owner>` creates every repository that has no remote of its own, the folder's own repository included, and `--public` makes each of them public. A `--new-repository` value of `<name>=<url>`, `<name>=github:<owner>`, or `<name>=none` sets that repository's remote, which `--create-remote` and `--remote` do not. A remote makes the first commit, as `--first-commit` does.

A remote URL holds no credential: `new` refuses an `https` URL with a user, such as `https://token@host/...`, and any URL with a password, such as `ssh://user:secret@host/...`, before it writes anything, because the URL is stored in the installation, printed in reports, and written to the manifest. A user without a password stays allowed on `ssh` and `git` URLs, as in `git@host:path`. Let git's credential helper or an SSH key supply the credential.

A repository created on GitHub is named after its folder or its `--new-repository` name, which has 1 to 100 letters, digits, dots, underscores, and hyphens and is not `.` or `..`; `new` refuses any other name before it writes anything.

Before it writes anything, `new` checks `gh` on the machine that writes the folder, and the first failing check stops it with the command to run, with nothing written and nothing created:

| Check | Refusal | What to run |
|-------|---------|-------------|
| `gh` is on `PATH` | `gh_missing` | Install the GitHub CLI from <https://cli.github.com/>, then `gh auth login --hostname github.com` |
| `gh auth status --json hosts` answers | `gh_outdated` | Upgrade the GitHub CLI |
| The account for `github.com` is signed in | `gh_signed_out` | `gh auth login --hostname github.com` |
| A classic token holds `repo`, or `public_repo` when every repository is public | `gh_scope_missing` | `gh auth refresh --hostname github.com --scopes repo` |
| Each owner is your login or one of your organizations | `owner_not_available`, or `gh_scope_missing` with `read:org` when the token cannot list them | `gh auth refresh --hostname github.com --scopes read:org` |
| No repository exists at the owner and name | `remote_exists` | Choose another name, or give its URL as the remote |

A token that reports no scopes, as a fine-grained token does, is not checked for scopes, and GitHub's answer to the create decides. Ropeshift never runs `gh auth login` and never reads the token. A created repository's URL follows your `gh` git protocol, `git@github.com:<owner>/<name>.git` for `ssh` and `https://github.com/<owner>/<name>.git` otherwise, and an `https` push uses `gh` as git's credential helper for that one command.

Each repository takes three steps, each with a two-minute limit: `create` (`gh repo create <owner>/<name> --private`, or `--public`), `origin`, and `push`. A failed step marks the later steps of that repository skipped and goes on to the next repository. Every folder, file, and commit stays, and a created repository is never deleted. The command prints each step's outcome and, under "To finish what did not run", the commands that finish it, such as `gh repo create acme/harbor --private`, `git -C <folder> remote add origin <url>`, and `git -C <folder> push --set-upstream origin -- main`. A push that remains over `https` is preceded by [`gh auth setup-git`](https://cli.github.com/manual/gh_auth_setup-git).

In a workspace, each new code repository is written at `repos/<name>` from the repository shape's files and gets a first commit of its own. The manifest lists each one with its remote, or with no `remote` key when it has none, so approving the first plan registers the code repositories. The same creation runs on a worker for the web UI: the request is a member's alone, as `repository.create_remote` ([authorization](authorization.md)), and the worker runs the same checks and steps.

## On the command line

[The CLI for power users](cli-for-power-users.md#connect-a-github-repository) gives the command for each step of this guide.

## Next

[Run several projects at once](several-projects.md) moves between projects, reads what waits in all of them, and sets the caps they share.
