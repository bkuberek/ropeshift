# Set up a project

This guide takes you from a fresh machine to a project with one repository, a first approved plan, and its work accepted and merged. It explains each choice on the way, so you know what you chose and how to change it later.

For the fastest path through one sample work order, follow [get started](getting-started.md). For the same planning steps with every screen shown, follow [your first plan](first-plan.md). For a project that spans several repositories, read this guide first, then [projects of several repositories](multiple-repositories.md). To bring in a project that already has documents and branches, see [adopt an existing project](adopt.md).

## Before you start

### What you need

- macOS or Linux, on arm64 or amd64.
- git, and tmux. Planning sessions always run in a tmux pane, and lanes on a Claude subscription run in one by default.
- The Claude Code CLI, installed and signed in through Anthropic's own flow. Ropeshift relies on a version only after a check of it passed: `ropeshift worker doctor` says whether yours is qualified, and [qualification](qualification.md) lists the versions.
- A git repository with at least one commit.

### Steps that need you at a terminal

Ropeshift reads its passphrase only from a terminal, never from a pipe, a flag, or a variable, so an agent cannot sign in as you. These steps need a person at a terminal or a browser:

| Step | How often |
|------|-----------|
| `ropeshift init` asks for the first passphrase, twice | Once per installation |
| A command that changes something asks for the passphrase | Each such command, unless your shell holds a session |
| `eval "$(ropeshift login)"` holds a session in one shell | It lasts an hour, then sign in again |
| Signing in to the web UI | Per browser session |
| A planning session talks with you | While you plan; with `--no-attach`, its questions reach Needs you instead |

Reads, such as `ropeshift work list` or `ropeshift top`, need no session. An agent outside ropeshift that should act for you takes a delegate credential, which you make with `ropeshift delegate create`, for up to thirty days. What it may run at once, and what becomes a card that waits for your confirmation, is in [delegates](authorization.md#delegates).

### Where ropeshift keeps its state

| | macOS | Linux |
|-|-------|-------|
| State: the ledgers, the installation database, your `profile.toml`, telemetry | `~/Library/Application Support/ropeshift` | `$XDG_STATE_HOME/ropeshift`, or `~/.local/state/ropeshift` |
| Sockets | `~/.ropeshift/run` | `$XDG_RUNTIME_DIR/ropeshift`, or `~/.ropeshift/run` |
| Lane worktrees | `~/.ropeshift/worktrees/<project>/<repository>/`, until you set a root | `$XDG_DATA_HOME/ropeshift/worktrees/<project>/<repository>/`, or `~/.local/share/ropeshift/worktrees/<project>/<repository>/` |

Inside your repositories, ropeshift adds only the lanes' branches, refs under `refs/ropeshift/`, and git's record of each lane worktree. [Remove ropeshift from a machine](#remove-ropeshift-from-a-machine) undoes all of it.

### What ropeshift pushes

Nothing, by default. Lanes commit on branches of their own on this machine, and their work stays here. Ropeshift pushes only when you choose it: with review target `pull_request`, the worker pushes each change's branch and opens a pull request under your own `gh` sign-in; at `person_approves` or `review_approves`, ropeshift pushes the merge it makes; and `push_on_stop` in your profile pushes each lane's branch when the lane stops ([work stays on this machine](onboarding.md#work-stays-on-this-machine)).

## 1. Install ropeshift

```sh
brew install bkuberek/tap/ropeshift
```

Or use the install script, which installs to `~/.local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/bkuberek/ropeshift/main/install.sh | sh
```

Check it:

```sh
ropeshift version
```

You see the release, its commit, and the platform. [Onboarding](onboarding.md#install-ropeshift) lists the `.deb` and `.rpm` packages.

## 2. Set up the machine

```sh
ropeshift init
```

1. It asks for a passphrase, twice. You use it to sign in to the web UI and to approve changes.
2. It registers this machine's worker, and prints `Worker: registered as <worker-id>`.
3. It checks each agent CLI, and says what to do for any that is missing or signed out. Do it, and run `ropeshift init` again; a second run says only what is left.
4. It writes a sample contract, and prints where.

A worker on a Claude subscription starts at `attended`, and step 10 explains what that means for your day. Read [Claude subscriptions and automated use](onboarding.md#claude-subscriptions-and-automated-use) before the first lane.

## 3. Start the daemon

One daemon per machine serves every project and every repository of the installation. Run it in a terminal of its own:

```sh
ropeshiftd solo
```

It serves the web UI at `http://127.0.0.1:7317`, on loopback only. Open it, and sign in with your passphrase. You see Needs you.

To keep the daemon running in the background and across restarts, install it as a service instead: `ropeshift service install`, or `brew services start ropeshift` for a Homebrew install. Use one or the other, never both ([run the daemon as a service](onboarding.md#run-the-daemon-as-a-service)).

## 4. Create the project and register the repository

A project holds one product: its repositories, its plan, and its own ledger. Create one, then register your clone in it:

```sh
ropeshift project create acme
cd /path/to/acme-app
ropeshift repository add . --project acme
```

`repository add` reads the remote and the default branch from git, names the repository after its folder, and prints what it registered. Add `--dry-run` first to see what it would do. From now on, a command run inside this clone acts on `acme` with no `--project`.

```sh
ropeshift repository list
```

You see `acme-app` with its identity, its base branch, and the safe settings every new repository starts with: review target `branch`, merge method `merge`, start from `merged`, and merge authority `person_merges`.

In the web UI, Projects has Create project, and Repositories has Register repositories, which takes the path to a clone or to a folder of clones.

![Repositories, with two registered repositories, their base branch, review target, merge method, merge authority, and forge](assets/screenshots/repositories-light.png#gh-light-mode-only)
![Repositories, with two registered repositories, their base branch, review target, merge method, merge authority, and forge](assets/screenshots/repositories-dark.png#gh-dark-mode-only)

## 5. Choose where lanes work

Each lane works in a git worktree of its own, never in your checkout. By default the worktree goes outside every clone, in the worktree directory in the table above, in `<project>/acme-app/<lane>`. If you want lanes somewhere else, set a root of your own in your profile layer, `profile.toml` in the state directory above:

```toml
[worktree]
root = "/path/to/lanes/{project}/{repo}"
```

`{repo}` becomes the repository's folder name, and `{project}` the project's ID. Write an absolute path, because the root expands no `~` and no variable. A root that is a registered clone, or lies inside one, is refused: the lane fails with the reason `worktree_inside_clone` before any worktree exists, and `ropeshift repository add` warns when a root would do so. `ROPESHIFT_WORKTREE_DIR` moves the worktree directory itself. [Project structure](project-structure.md#where-lane-worktrees-go) explains the places and the refusal.

## 6. Choose the branch lanes start from

A lane starts from the repository's base branch as the remote holds it, never from whatever your checkout has out. The base branch is the default branch until you name another. A team that works on a long-lived branch names that branch:

```sh
ropeshift repository set acme-app --base develop
```

`--base ""` follows the default branch again. One work order can start from its own branch with `base = "release/1.2"` in its contract file. If the base cannot be fetched, the work waits as `base_unavailable` and tries again on its own ([where a lane starts](onboarding.md#where-a-lane-starts)).

## 7. Choose how work is reviewed and merged

Two settings decide what happens after a lane finishes. Start with the defaults, and move up once the review has earned your trust.

**Review target**: where a finished change is reviewed and lands.

| Review target | What happens |
|---------------|--------------|
| `branch`, the default | The review reads the lane's branch on this machine. Nothing is pushed before a merge |
| `pull_request` | The worker pushes the lane's branch and opens a pull request on GitHub, and the review posts its verdicts there. A team with branch protection uses this |

**Merge authority**: how much of each change you decide after the review passed it.

| Level | What waits for you | What ropeshift merges |
|-------|--------------------|-----------------------|
| `person_merges`, the default | Accepting the work, and merging it | Nothing |
| `person_approves` | Approving each merge | The reviewed commit, once you approve |
| `review_approves` | Nothing, unless the merge cannot land | The reviewed commit, once the review passes and every required check is green |

```sh
ropeshift repository set acme-app --merge-authority person_approves
```

`ropeshift repository show acme-app` ends with how often you agreed with the review in this repository, which is the evidence for choosing a level. [Connect a GitHub repository](github.md) sets up pull requests step by step.

## 8. Plan the work

Start a planning session with a goal:

```sh
ropeshift plan start --goal "Let customers export their invoices as CSV"
```

Your terminal joins the session. The planning agent reads the code, asks you what it needs to know, and writes the plan as files on a branch of its own: requirements, milestones, contracts, and work orders. Answer in the conversation. Detach with `C-b d`, which leaves the session running, and rejoin with `ropeshift lane attach <lane>`. In the web UI, Roadmap has Start planning session, and the Planning page shows the plan as the session commits it.

Tell the agent where your plan files go, such as `plan/`, in your `AGENTS.md` or `CLAUDE.md`, so every session writes them to the same place.

You can also write a contract by hand. Commit a file like this, and intake reads it:

```markdown
+++
kind = "contract"
id = "invoice-csv"
title = "Export invoices as CSV"

[[criteria]]
id = "c1"
text = "GET /invoices.csv returns every invoice of the signed-in customer, one per row"

[[work_orders]]
id = "invoice-csv-order"
title = "Add the CSV export"
repo = "."
size = "small"
+++

Customers need their invoices in a spreadsheet.
```

A contract outside an approved plan waits in contract review: pass it in Needs you, or with `ropeshift work review <id>`. [Project structure](project-structure.md#the-repository-file-format) describes the format.

## 9. Approve the plan

When the session finishes, the plan waits in Needs you as a plan approval.

```sh
ropeshift plan list
ropeshift plan show <plan>
ropeshift plan approve <plan>
```

`plan show` prints what the version holds, how it differs from the last one you approved, and each answer you gave that it records. `plan approve` approves that exact version: a version that changed since you read it is refused. Approving records your answers as decisions, passes the contract review of the plan's work, and makes its work orders appear. On the web, the plan's card in Needs you, or Open the full plan approval, does the same, and Decline sends a reason back to the session.

The planning branch then lands like any change, at the repository's merge authority: at `person_merges` you merge it yourself, and at the other levels ropeshift merges it at the approved commit ([a planning branch](onboarding.md#a-planning-branch)).

## 10. Run the work

Ready work needs something to claim it. Let the orchestrator do it:

```sh
ropeshift worker list
ropeshift orchestrator start <worker-id>
```

A worker on an API key starts at `unattended`, and its orchestrator claims as soon as work is ready. A worker on a Claude subscription starts at `attended`, which in practice means:

- The orchestrator claims work only inside a run window you open, such as when you sit down to work: `ropeshift worker window-open <worker-id>`, or Open window under Workers in Settings.
- A window lasts 4 hours unless `--for` says otherwise, 12 hours at most, and closes by itself. Closing it stops no lane: running lanes finish, and claimed work that has not started waits for the next window.
- You can still claim and start any work order by hand, at any time: open it in Work orders and select Start, or run `ropeshift work claim` and `ropeshift lane start`.

Watch the lanes on Floor, or with `ropeshift top`. Work runs in milestone order, and a lane that needs you shows in Needs you.

On a small machine, the worker caps how many lanes run at once from its cores and memory. The default estimate is 1 core and 2 GiB per lane, with 1 core and 4 GiB kept back, so a machine with 4 cores and 11 GiB runs at most 3 lanes. With no swap, lower the cap with `ropeshift worker caps <worker-id> --max-lanes 2` ([how many lanes run at once](onboarding.md#how-many-lanes-run-at-once)).

## 11. Review, accept, and merge

Every work order goes through the same steps. The steps marked "you" are the only ones that need a person, and which of them apply depends on the merge authority.

| Step | Who | What happens |
|------|-----|--------------|
| Created | Intake | Plan approval, or a contract you committed, records the work order |
| Ready | Ropeshift, or you | Plan approval passes its review; a hand-written contract needs your `work review` |
| Claimed and started | The orchestrator, or you | A lane starts in its own worktree, on branch `lane/<lane>` |
| Reviewed | A review lane | Each criterion gets `pass`, `fail`, or `cannot_judge`; a failure sends the work back by itself |
| Accepted | You, at `person_merges` with review target `branch` | `ropeshift work accept <id>`, or Accept in Needs you |
| Merge approved | You, at `person_approves` | `ropeshift approval approve <id> --digest <commit>`, or the merge request in Needs you |
| Merged | You at `person_merges`; ropeshift otherwise | At `person_merges`, merge the branch yourself; `ropeshift work acceptance <id>` names it and its commit |
| Archived | You | `ropeshift lane archive <lane>` removes the ended lane's worktree |

At `person_merges` with review target `branch`, accepting does not merge. Merge the branch the way you merge any branch:

```sh
ropeshift work acceptance <work-order-id>
git switch main
git merge lane/<lane>
```

### Check gates without CI

A change can go through three kinds of checks:

- **Your commit hooks.** A lane commits with git, and git runs the repository's commit hooks, so a quick check in a pre-commit hook runs on every lane commit. Ropeshift's own merge runs no hook.
- **Checks a lane reports.** A lane reports the checks it ran with its outcome. A failed one blocks acceptance. A passing one stands behind no criterion, because no agent stands behind a lane's own checks.
- **Required checks on GitHub.** With review target `pull_request` at `person_approves` or `review_approves`, the checks the base branch requires gate ropeshift's merge, and a failure sends the work back.

With no forge, nothing runs checks on a branch that was never pushed, so the review's verdicts and your acceptance are the gate. Write the check into the contract as a criterion, such as "`make verify` passes", so the review judges it, and keep `person_merges` or `person_approves` until you trust it.

### Clean up

Ropeshift never deletes a branch. After work merges:

1. `ropeshift lane archive <lane>` removes the lane's worktree and keeps its branch. A lane whose commits no remote holds reads `unpushed_work`, and `--force` removes it anyway.
2. Delete the merged branch with git: `git branch -d lane/<lane>`.
3. Uncommitted work a lane left when it stopped is saved under `refs/ropeshift/`. List those refs with `git for-each-ref refs/ropeshift/`, and delete one you no longer need with `git update-ref -d <ref>`.

## Remove ropeshift from a machine

1. Let every lane end, or cancel its work: `ropeshift lane list` shows none running.
2. Stop the daemon: `ropeshift service uninstall`, `brew services stop ropeshift`, or Ctrl-C in its terminal.
3. Delete the state and socket directories in the table above. This deletes every project's ledger, so run `ropeshift backup` first for a project you want to keep.
4. In each repository, remove the lane worktrees with `git worktree remove <path>`, then `git worktree prune`, and delete the `lane/` branches and the `refs/ropeshift/` refs as in [clean up](#clean-up).
5. Uninstall the binary: `brew uninstall ropeshift`, or delete `ropeshift` and `ropeshiftd` from `~/.local/bin`.

## Next steps

- Grow the project to several repositories: [projects of several repositories](multiple-repositories.md).
- Review through pull requests and let ropeshift merge: [connect a GitHub repository](github.md).
- Read what a lane may do without asking: [before the first lane](onboarding.md#before-the-first-lane).
