# Set up your projects

This guide creates a project in the web UI for each product you work on, and registers each project's repositories: the clones on your machine, the planning repository where the plan lives, and the settings each repository starts with. It follows Harbor, the project of a small port, with two code repositories, `harbor-api` and `harbor-web`, and a planning repository, `harbor-plan`. Put your own names in their place.

## Before you start

- Finish [get started with the web UI](getting-started.md): the service runs, and you are signed in to the web UI.
- Clone each repository with git. Ropeshift clones nothing: it registers the clones already on the machine the daemon runs on. [Project structure](project-structure.md#the-recommended-layout) recommends a layout for a project of one repository that grows to several.
- For a project that has no repository yet, write its first files with `ropeshift new` and run the git commands it prints, which make each folder's first commit ([start a project](project-structure.md#start-a-project)). This guide then registers the folders in the web UI.

In this guide, the code repositories are clones in one folder, and the planning repository is a clone beside it:

```text
harbor-plan/        the planning repository
src/                the folder of clones
├── harbor-api/
└── harbor-web/
```

## In the web UI

### 1. Open Projects

In the sidebar, under Installation, select Projects. Projects lists every project of the installation, with its state, its repositories, what waits in it, and its lanes. A fresh installation has one project, Default (`default`), with no repositories.

![Projects, listing Default with no repositories, and Create project at the top](assets/screenshots/projects-01-light.png#gh-light-mode-only)
![Projects, listing Default with no repositories, and Create project at the top](assets/screenshots/projects-01-dark.png#gh-dark-mode-only)

This guide leaves Default empty and creates a project for each product. Rename changes a project's name and keeps the display name it was created with, so a renamed Default still shows as Default.

### 2. Create a project

Select Create project. Enter the Display name, `Harbor`, which the web UI shows, and the Name, `harbor`, lowercase and hyphenated, which addresses and the command line's `--project` use.

![Create project, with the display name Harbor and the name harbor](assets/screenshots/projects-02-light.png#gh-light-mode-only)
![Create project, with the display name Harbor and the name harbor](assets/screenshots/projects-02-dark.png#gh-dark-mode-only)

Select Create project. Harbor opens on its Repositories, and the project switcher at the top shows Harbor.

### 3. Read the empty Repositories

Repositories says No repositories in Harbor yet, with Register repositories and Import an existing project. Import an existing project is for a project that already has code, documents, and branches to bring in as its plan, as [adopt an existing project](adopt.md) describes.

![Repositories of Harbor, with no repositories yet, Register repositories, and Import an existing project](assets/screenshots/projects-03-light.png#gh-light-mode-only)
![Repositories of Harbor, with no repositories yet, Register repositories, and Import an existing project](assets/screenshots/projects-03-dark.png#gh-dark-mode-only)

### 4. Choose the folder of clones

Select Register repositories. The dialog registers clones on the disk of the machine the daemon runs on, as its Worker line says. Under What to register, choose A folder of clones. In Folder, enter the absolute path of the folder that holds the clones. Depth says how many folder levels below it to look for clones, and 1 looks at the folder's children.

![Register repositories in Harbor, step 1, with A folder of clones, the folder, and a depth of 1](assets/screenshots/projects-04-light.png#gh-light-mode-only)
![Register repositories in Harbor, step 1, with A folder of clones, the folder, and a depth of 1](assets/screenshots/projects-04-dark.png#gh-dark-mode-only)

A folder that is itself a clone registers that one clone, and nothing inside a clone or a hidden folder is searched. So a workspace whose code repositories are clones in a folder inside it, as [project structure](project-structure.md#the-recommended-layout) recommends, takes two registrations: the workspace's clone with One repository, then its inner folder with A folder of clones.

### 5. Run the dry run

Select Run dry run. Step 2, Dry run, lists each clone it found, with its path as you entered it, its identity, and what registering would do with it. Nothing is recorded until you register.

![The dry run, listing harbor-api and harbor-web with their remotes, each marked Will register](assets/screenshots/projects-05-light.png#gh-light-mode-only)
![The dry run, listing harbor-api and harbor-web with their remotes, each marked Will register](assets/screenshots/projects-05-dark.png#gh-dark-mode-only)

| Result | What it means |
|--------|---------------|
| Will register | The repository is new to the installation, and registering adds it to this project |
| Already in this project | Registering records nothing new |
| Refused, with the reason | The clone cannot be registered here: a repository another project holds, which the reason names; a path that holds no clone; a shallow clone; or a clone that shares its first commit with another clone on this machine, as [how ropeshift tells repositories apart](#how-ropeshift-tells-repositories-apart) explains |

The foot of the dialog counts the refused clones and those already here. Back returns to step 1.

### 6. Register the repositories

Select Register 2 repositories. A message confirms it, and Repositories lists both, with their Name, Remote, Base, Review target, Merge method, Merge authority, and Forge.

![Repositories of Harbor, listing harbor-api and harbor-web at review target branch, merge method merge, and merge authority person_merges](assets/screenshots/projects-06-light.png#gh-light-mode-only)
![Repositories of Harbor, listing harbor-api and harbor-web at review target branch, merge method merge, and merge authority person_merges](assets/screenshots/projects-06-dark.png#gh-dark-mode-only)

Each repository is named after its clone's folder, and its base is its default branch. Each starts with the safe settings: the review reads its branch on this machine, and nothing is pushed before a merge; a change merges with a merge commit; and you merge each change yourself. Forge reads not needed, because a repository reviewed on its branch needs no `gh` sign-in. Step 8 describes each setting.

### 7. Register the planning repository

A project of several repositories plans in one of them, its planning repository: the plan of record lives there, and planning sessions run there. Harbor's is `harbor-plan`, a clone of its own beside the folder of clones.

Select Register repositories, choose One repository, and enter the clone's absolute path in Path to the clone. Select Run dry run, then Register 1 repository. Repositories now lists three.

### 8. Name the planning repository

In the table, select `harbor-plan`. Its record opens below the table. Under Planning repository, check The project's plan lives in this repository. The bar at the foot of the record says what you changed, `planning repository no → yes`, before anything is saved.

![The record of harbor-plan, with The project's plan lives in this repository checked, and the change named at its foot](assets/screenshots/projects-07-light.png#gh-light-mode-only)
![The record of harbor-plan, with The project's plan lives in this repository checked, and the change named at its foot](assets/screenshots/projects-07-dark.png#gh-dark-mode-only)

Select Save. The row of `harbor-plan` is marked Planning. Intake now reads goals, plans, documents, milestones, contracts, and the manifest only from `harbor-plan`, and reports a plan file it finds in any other repository, so a misplaced plan is seen and never read. A project of one repository plans in that repository, with no box to check.

The record holds every setting of the repository:

| Setting | What it decides | It starts at |
|---------|-----------------|--------------|
| Name | The repository's name in the project, which a work order's `repo` names | The clone's folder name |
| Base branch | The branch lanes start from, as the remote holds it ([where a lane starts](onboarding.md#where-a-lane-starts)) | Unset, which is the default branch |
| Start from | Whether work that depends on other work in this repository starts from accepted commits before they merge, `accepted`, or only from merged ones, `merged`. Squash and rebase rule out `accepted` | `merged` |
| Review target | Where a change is reviewed: on its branch on this machine, `branch`, or in a pull request, `pull_request` | `branch` |
| Forge binding | The repository on its forge, such as `github.com/owner/repo`, which pull requests open against | Empty, which takes it from a remote on a forge |
| Merge authority | How much of each change you decide after the review passed it ([merge authority](how-it-works.md#merge-authority)) | `person_merges` |
| Merge method | `merge`, `squash`, or `rebase` | `merge` |

Each repository keeps its own settings, so the planning repository can stay at `branch` and `person_merges` while the code repositories review through pull requests, as [connect a GitHub repository](github.md) sets up. The merge authority never rises by itself.

### 9. Open the Overview

In the sidebar, select Overview. Harbor's Overview has Register repositories and Start planning session at the top, then its figures, Plan, which says no plan has milestones yet, Lanes, Needs you, Review agreement, and Recent activity, which lists the registrations and the change you saved. Start planning session writes the plan, in the next guide.

![The Overview of Harbor, with no plan yet, Register repositories, and Start planning session](assets/screenshots/projects-08-light.png#gh-light-mode-only)
![The Overview of Harbor, with no plan yet, Register repositories, and Start planning session](assets/screenshots/projects-08-dark.png#gh-dark-mode-only)

### 10. Create a second project

Each product is a project of its own, with its own repositories, plan, and ledger. Press `P`, or select the project switcher at the top, then select Create project. Create Tidewater as in step 2, and it opens on Tidewater's Repositories. Register its clones as in steps 4 to 6. A repository belongs to one project, so a clone that Harbor holds is refused in Tidewater, and the reason names Harbor's project.

### 11. Switch between projects

Press `P` again. The switcher lists every project, Default, Harbor, and Tidewater, with how many items wait in each when any do, and a check on the project you are in. Type in Filter projects to narrow the list, and choose a project to open the same view there. Below the list are Create project, Import an existing project, and Manage projects, which opens Projects.

![The project switcher, listing Default, Harbor, and Tidewater, with Create project, Import an existing project, and Manage projects](assets/screenshots/projects-09-light.png#gh-light-mode-only)
![The project switcher, listing Default, Harbor, and Tidewater, with Create project, Import an existing project, and Manage projects](assets/screenshots/projects-09-dark.png#gh-dark-mode-only)

## How ropeshift tells repositories apart

A repository is identified by its remote, or by its first commit when it has none, so every clone of one repository is the same repository, and the dry run shows that identity for each clone. The first commit is the root reached by following first parents from the tip of the default branch, so a workspace that takes in another repository's history with an unrelated merge gains a second root and keeps the identity it had.

A shallow clone, such as one made with `git clone --depth 1`, holds only the latest commits, and the oldest of them stands in for a first commit that changes with the clone's depth. Registering one is refused, and a repository the daemon reads from `--source` that is a shallow clone is not read, as below:

```text
/work/api is a shallow clone; its first commit is not its repository's; run git fetch --unshallow
```

Run `git fetch --unshallow` in the clone to fetch its whole history, then register it.

Two clones cut from one template, or two forks of one repository, start at the same first commit, and a first commit alone cannot tell them apart. A clone is refused when this machine's worker keeps another clone with that first commit at another path that still exists, and the two have different remotes or neither has one:

```text
shares its first commit `5ca1ab1e` with repository `template-a` at `/work/template-a`; ropeshift identifies a repository by that commit, so give this clone a remote of its own or a history of its own
```

To register it, give the clone a remote of its own, or a history of its own. A second clone of one remote is the same repository and is not refused, and a clone at the same path with a new remote refreshes its record, because a remote can move. Within one registration, the second such clone gets the same refusal. A clone with no remote that shares any first commit it holds with a repository another project holds is refused too, and the reason names that commit and that project. A repository the daemon reads from `--source` that shares its first commit this way is not read, and the daemon says so once when the refusal begins, and again if it ends and begins again, not at each poll.

A repository registered by an earlier release was named by the lowest root it had, and keeps that ID: registering its clone again finds that record, in the order [the command line](cli.md#projects) gives. A repository registered since is matched only by its own first-parent root, so two repositories that each merged a third one's history are each their own record, whichever registers first. A workspace with a remote of its own that merged another repository's history is a repository of its own, and that repository's record does not change. A repository you removed is never matched this way, so a workspace that took in its history registers as a repository of its own, and the removed record stays removed.

## Keep a manifest in the planning repository

A manifest lists the project's repositories in the plan itself, so the set of repositories is reviewed like any other part of the plan, and a new machine can register them from it. It is a file of kind `repositories` in the planning repository, which you write and commit with git. The web UI has no editor for it. A planning session's first prompt shows it the format and the rule for `path`, so it can write one for you, and `ropeshift template show workspace/plan/repositories.md` prints the format with a comment on each field. This is one for the harbor project:

```markdown
+++
kind = "repositories"
id = "harbor"
title = "Harbor repositories"
plan = "harbor-v1"

[[repository]]
name = "harbor-api"
remote = "https://git.example.com/harbor/harbor-api.git"
path = "../src/harbor-api"
description = "The berths and the sailings, and the API the port's pages call"

[[repository]]
name = "harbor-web"
remote = "https://git.example.com/harbor/harbor-web.git"
path = "../src/harbor-web"
description = "The port's public pages"
+++

The repositories of Harbor.
```

- `path` is where the clone sits relative to the planning repository's clone. It may lead beside it, as here, or into a folder inside it, such as `repos/harbor-api`. It must stay inside the folder that holds the planning repository's clone, and must not be that clone itself.
- The plan lists the manifest in its `documents` as `repositories:harbor`, so approving the plan covers it.
- A manifest names repositories and nothing else. It never sets a review target, a merge method, a base branch, a forge binding, or a merge authority: those stay your settings, so a plan can never grant ropeshift authority. A key the format does not have is reported as a problem, with no effect.
- A repository of the project the manifest does not list is reported, and never removed.

When you approve a plan version whose manifest names a repository the project lacks, the approval registers it, as you, from the clone at its path, with the safe settings and the manifest's name for it. Before you approve, the plan approval page and its card in Needs you list each repository approving would register, with its path, where that path leads, and its remote.

- Ropeshift clones nothing. A repository with no clone at its path stays unregistered until a later approval finds it.
- A clone whose remote differs from the manifest's is refused.
- A repository you removed stays removed, and only registering it again brings it back.

On a new machine, `ropeshift repository add --manifest <file>` registers what a manifest lists with no plan approval, and has no control in the web UI. [Onboarding](onboarding.md#start-the-daemon-and-run-the-sample) gives every rule of the manifest.

### Keep the clones inside the planning repository

A workspace can keep its code repositories in a folder of its own, such as `repos/`, and its manifest then lists each clone by a path inside the planning repository's clone:

```markdown
+++
kind = "repositories"
id = "harbor"
title = "Harbor repositories"
plan = "harbor-v1"

[[repository]]
name = "harbor-api"
remote = "https://git.example.com/harbor/harbor-api.git"
path = "repos/harbor-api"
description = "The berths and the sailings, and the API the port's pages call"

[[repository]]
name = "harbor-web"
remote = "https://git.example.com/harbor/harbor-web.git"
path = "repos/harbor-web"
description = "The port's public pages"
+++

The repositories of Harbor.
```

A path may go deeper, as `repos/payments/api`. Ropeshift registers each clone from its path as it does a clone beside the planning repository. Ignore the folder in the planning repository's `.gitignore`, so git shows nothing of the clones:

```text
repos/
```

A path inside the planning repository's clone that the planning repository does not ignore is registered all the same, and is reported. The outcome in the plan approval page and its card, in the approval's registration, and in `ropeshift repository add --manifest` has this reason:

```text
the planning repository does not ignore `repos/harbor-api`; add it to `.gitignore`, or its files show as untracked content there and can be committed by mistake
```

When the planning repository's clone is on the machine of the daemon's worker, intake reports the same as a problem of the manifest file, which `source list` shows. The problem does not stop the manifest from being recorded. A path that leads out of the folder holding the planning repository's clone is refused, also through a symbolic link inside the clone.

## Where lanes work

Each lane works in a git worktree of its own, never in your checkout. By default, the worktree goes outside every clone, in the installation's worktree directory, at `<worktree directory>/<project>/<repository>/<lane>`:

| System | Worktree directory |
|--------|--------------------|
| macOS | `~/.ropeshift/worktrees` |
| Linux | `$XDG_DATA_HOME/ropeshift/worktrees`, or `~/.local/share/ropeshift/worktrees` |

To put lane worktrees somewhere else, set a root in your own profile layer, `profile.toml` in the installation's directory ([where ropeshift keeps its state](onboarding.md#where-ropeshift-keeps-its-state)). `ropeshift init --worktree-root <absolute path>` writes that file with the root set, when none exists. A path that names neither `{project}` nor `{repo}` gets `/{project}/{repo}` appended, and `init` says so; a relative path exits 2. `init` never changes a profile that exists, so with one there, or to change the root later, edit the file. No setting of the web UI changes it:

```toml
[worktree]
root = "/path/to/lanes/{project}/{repo}"
```

`{repo}` becomes the repository's folder name, and `{project}` the project's ID. Write an absolute path, because the root expands no `~` and no variable. A root that is a registered clone, or lies inside one, is refused: the lane fails with the reason `worktree_inside_clone` before any worktree exists, and Needs you names the root and the clone. [Where lane worktrees go](project-structure.md#where-lane-worktrees-go) explains the places and the refusal.

To check a root before a lane does, run `ropeshift worker doctor`, or `ropeshift init` again. Its `Worktree root:` section names the root in force and the layer that set it, the built-in root or your profile. For each checkout this worker keeps, in every project, whose root resolves inside a clone, it adds a line with the project, the checkout, the root, the clone, and the fix. A built-in root outside every clone adds no line.

## Spend settings

A project's `[spend_policy]` holds the values that stop a lane from spending credits on waiting ([waiting costs no credits](how-it-works.md#waiting-costs-no-credits)). It is a versioned policy of kind `spend`, which only a member publishes, with `ropeshift policy publish --file F`, and which `ropeshift policy list --kind spend` and `ropeshift policy show --kind spend N` read. No plan, repository file, or agent can change it, and a lane's profile layer that names one of its keys is refused as an unknown setting. A version states `version` and any key it changes, each version is newer than the one in force, and a key it leaves out keeps its default. A lane reads the version in force when it starts, so a new version reaches the next lane. The built-in version is this one:

```toml
[spend_policy]
version = 1
max_live_wait = "5m"
max_tool_sleep = "60s"
wait_rules_off = []
wait_rule_patterns = { W1 = [], W2 = [], W3 = [], W4 = [] }
```

| Key | What it sets | Range |
|-----|--------------|-------|
| `max_live_wait` | The longest a lane waits live with `lane_wait`. A longer wait parks the lane | 1 minute to 1 hour |
| `max_tool_sleep` | The sleep inside one command that rule W1 refuses, and any longer one | 1 second to 1 hour |
| `wait_rules_off` | The wait rules to turn off, by ID: `W1`, `W2`, `W3`, or `W4` | Those four IDs |
| `wait_rule_patterns` | Command prefixes a rule also matches, by rule ID | At most 64 per rule, each at most 200 bytes |

A value outside its range is refused, and the refusal names the key. This policy turns W4 off, so a lane may run `gh pr view`, and makes W3 refuse `kubectl rollout status` too:

```toml
[spend_policy]
version = 2
wait_rules_off = ["W4"]
wait_rule_patterns = { W3 = ["kubectl rollout status"] }
```

## Next

[Plan your first change](plan-your-first-change.md) is the next guide. For a project that already has code, documents, and branches, [adopt an existing project](adopt.md) brings it in as a plan instead.
