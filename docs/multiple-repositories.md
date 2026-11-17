# Projects of several repositories

Many products are more than one repository: an API, a web client, a shared library, and a repository that holds the plan. Ropeshift treats them as one project with one plan. Each work order changes one repository, work that spans repositories is several work orders joined by dependencies, and changes merge in dependency order.

This guide assumes you have read [set up a project](set-up-a-project.md), which covers installing, the daemon, and the review and merge settings. It uses the layout from [project structure](project-structure.md): a workspace repository `acme-workspace` that is the planning repository, with the code repositories `acme-api` and `acme-web` cloned into its git-ignored `repos/` folder.

## How it fits together

- **One installation, one daemon.** One daemon per machine serves every project and every repository. You never run a daemon per repository.
- **One planning repository.** The plan of record lives in one repository, and intake reads plan files only from there.
- **One repository per work order.** A contract lives in the planning repository, and each of its work orders names the repository it changes with `repo`.
- **Read-only reads.** A lane reads the other repositories it builds on, at a recorded commit, and cannot write there.
- **Merge in dependency order.** Work that depends on work in another repository waits until that work has merged.

## 1. Lay out the workspace

Clone the code repositories into the workspace's ignored folder, the way your `make setup` does:

```text
acme-workspace/
├── AGENTS.md
├── Makefile
├── .gitignore        repos/
├── plan/
└── repos/
    ├── acme-api/
    └── acme-web/
```

Lane worktrees go outside the workspace and every clone by default, so none lands in `repos/`. A worktree root of your own that lies inside a registered clone is refused ([where lane worktrees go](project-structure.md#where-lane-worktrees-go)).

## 2. Register every repository in one step

`repository add` takes a clone, or a folder whose children are clones, and several paths at once:

```sh
ropeshift project create acme
cd /path/to/acme-workspace
ropeshift repository add . repos --project acme --dry-run
```

`.` is the workspace's own clone, and `repos` is the folder of clones. The dry run prints each repository it would register and each it would refuse, with why, and records nothing. Run it again without `--dry-run` to register them:

```sh
ropeshift repository add . repos --project acme
ropeshift repository list
```

You see `acme-workspace`, `acme-api`, and `acme-web`, each named after its folder and each with the safe settings. A clone inside another clone is never found by searching the outer one, which is why `repos` is named on its own. `--depth N` searches more levels below a folder. A repository another project holds is refused, naming that project, and the others still register.

In the web UI, Repositories has Register repositories. Register the workspace's clone, then the `repos` folder.

### How a repository is identified

A repository is identified by its remote, or by its first commit when it has none. The first commit is the root reached by following first parents from the tip of its default branch. When a workspace takes in another repository's history with an unrelated merge, the workspace gains a second root and keeps the identity it had.

A repository registered before this rule was named by the lowest root it had, and it keeps that ID. Registering its clone again matches the record this worker keeps at the clone's path when that record's ID is one of the clone's first commits, or else the record whose ID is the clone's first-parent root, or else a record that is not removed whose ID is the clone's lowest root, and the line for the clone says it was matched by its earlier identity when the record's ID is not the first-parent root. A repository the daemon reads from `--source` with no checkout kept keeps the ID of the record found in the same order. A record is not matched by the lowest root when it has a remote, the clone has a remote, and the two differ, unless this worker keeps a clone of that record at another path, which is refused for sharing its first commit: a workspace with a remote of its own that merged another repository's history is a repository of its own, and that repository's record is not changed. A repository you removed is never matched this way, so a workspace that took in a removed repository's history registers as a repository of its own, and the removed record stays removed.

### Clones that share a first commit

Two clones cut from one template, or two forks of one repository, start at the same first commit, and a first commit alone cannot tell them apart. A clone is refused when this worker keeps another clone with that first commit at another path that still exists, and the two have different remotes or neither has one:

```text
shares its first commit `5ca1ab1e` with repository `template-a` at `/work/template-a`; ropeshift identifies a repository by that commit, so give this clone a remote of its own or a history of its own
```

To register it, give the clone a remote of its own, or a history of its own. A second clone of one remote is the same repository and is not refused, and a clone at the same path with a new remote refreshes its record, because a remote can move. Within one run, the second such clone gets the same refusal. A clone with no remote that shares any first commit it holds with a repository another project holds is refused too, and the reason names that commit and that project.

## 3. Name the planning repository

A project of several repositories needs to know where its plan lives:

```sh
ropeshift repository set acme-workspace --planning
```

From now on intake reads goals, plans, documents, milestones, contracts, and the manifest only from `acme-workspace`, and `ropeshift source list` reports a plan file found in any other repository. Planning sessions run in `acme-workspace`. In Repositories, the same is the Planning repository box on the repository.

## 4. Set the code repositories' review and merge settings at once

`repository set` takes several names and applies the same change to each:

```sh
ropeshift repository set acme-api acme-web --review-target pull_request
ropeshift repository set acme-api acme-web --merge-authority person_approves
```

You see one line per repository. Each repository keeps its own settings, so the workspace can stay at `branch` and `person_merges` while the code repositories review through pull requests. [Connect a GitHub repository](github.md) covers the `gh` sign-in and the forge binding that pull requests need.

## 5. Keep a manifest in the planning repository

A manifest lists the project's repositories in the plan itself, so the set of repositories is reviewed like any other part of the plan, and a new machine can register them from it. It is a file of kind `repositories` in the planning repository:

```markdown
+++
kind = "repositories"
id = "acme"
title = "Acme repositories"
plan = "acme-v1"

[[repository]]
name = "acme-api"
remote = "git@github.com:acme/acme-api.git"
path = "repos/acme-api"
description = "The HTTP API"

[[repository]]
name = "acme-web"
remote = "git@github.com:acme/acme-web.git"
path = "repos/acme-web"
description = "The web client"
+++

The repositories of the Acme product.
```

- `path` is where the clone sits relative to the planning repository's clone. It may lead into a folder inside it, as here, or beside it, such as `../acme-api`. It must stay inside the folder that holds the planning repository's clone, and must not be that clone itself.
- The plan lists the manifest in its `documents` as `repositories:acme`, so approving the plan covers it.
- A manifest names repositories and nothing else. It never sets a review target, a merge method, a base branch, a forge binding, or a merge authority: those stay your settings, so a plan can never grant ropeshift authority. A key the format does not have is a problem `source list` reports, with no effect.
- A repository of the project the manifest does not list is reported, and never removed. Remove one yourself with `ropeshift repository remove <name>`.

Planning sessions are not shown the manifest format, so write the manifest yourself, or give the agent this example.

## 6. Let plan approval register the manifest's repositories

When you approve a plan version whose manifest names a repository the project lacks, approval registers it, as you, from the clone at its path, with the safe settings and the manifest's name for it. Before you approve, `ropeshift plan show` and `ropeshift plan approve` print a line for each repository approving would register: its path, where that path leads on this machine, its remote, and what approving does with it. The plan approval page and its card in Needs you show the same.

- Ropeshift clones nothing. A repository with no clone at its path stays unregistered until a later approval finds it, or until you run `ropeshift repository add --manifest plan/repositories.md`.
- A clone whose remote differs from the manifest's is refused.
- A repository you removed stays removed. Only `ropeshift repository add` brings it back.

`ropeshift repository add --manifest <file>` registers what a manifest lists with no plan approval, which is how you set up a second machine from the same workspace.

## 7. Name the repository in each work order

The contracts live in the planning repository, and each work order names the repository it changes by its name in the project. A design decision written down in the workspace, which the web client then implements, looks like this:

```markdown
+++
kind = "contract"
id = "accounts-web"
title = "Account settings page"
plan = "acme-v1"
milestone = "m1-accounts"
documents = ["architecture:acme-accounts"]
uses = ["accounts-api"]

[[criteria]]
id = "c1"
text = "A signed-in user changes their display name on the settings page"
requirements = ["ACC-3"]

[[work_orders]]
id = "settings-page"
title = "Build the account settings page"
repo = "acme-web"
size = "medium"
+++

The page calls the accounts API that accounts-api delivers.
```

- `documents` cites the architecture document in the workspace that records the design. Intake pins the citation to the version at this commit, and the lane reads it.
- `repo = "acme-web"` sends the work to the web client. A name the project does not hold keeps the work from `ready`, and `source list` names it.
- `uses = ["accounts-api"]` builds on the shared contract `accounts-api`, which is marked `shared = true` and whose work order has `repo = "acme-api"`. Every work order here waits for that contract's work, so the API lands first.

## 8. Dependencies across repositories

Work that depends on work in another repository becomes ready only once that work is `done`:

- Once its merge landed.
- Or, in a repository at `person_merges` with review target `branch`, once you accepted it, because ropeshift cannot see a merge you make yourself.

Within one repository, `repository set <name> --start-from accepted` lets dependent work start from accepted work before it merges. That never applies across repositories, because a build in one repository cannot use an unmerged branch of another.

## 9. What a lane reads in other repositories

A lane writes only in its own repository. It also reads, read-only:

- Each repository its work order's dependencies target, at the commit ropeshift's merge of that dependency made, or at the base branch tip when ropeshift recorded no merge.
- Each repository its contract names in `reads`, such as `reads = ["acme-api"]`, at the base branch tip.

The worker checks each one out beside the lane's worktree, in a folder named after the lane with `.reads` after it, and the lane's brief names them. The lane may read them and may not edit, commit, push, or change branch there, and the worker removes them when the lane ends. The review of that lane reads the same repositories at the same commits, so it judges the work against the code the writer saw. A planning session reads every repository of the project at its base branch tip.

## 10. Merge order

Changes merge one by one, in dependency order, within and across repositories. Approved work whose dependency is not yet `done` waits in `integrating`, and `ropeshift work show` prints a `waiting` line naming each dependency with its repository and state. At `person_merges` with pull requests, the same line tells you which pull request to merge first.

Merges across repositories are not atomic: each lands on its own. A milestone whose work spans repositories is done once its work has merged in every one. In a repository at `person_merges` with review target `branch`, the roadmap shows such a milestone as accepted, not merged.

## Next steps

- Bring an existing set of repositories into ropeshift: [adopt an existing project](adopt.md).
- The rules behind each step: [where a lane starts](onboarding.md#where-a-lane-starts) and [merging accepted work](onboarding.md#merging-accepted-work).
