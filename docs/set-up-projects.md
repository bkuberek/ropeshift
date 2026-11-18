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

Each lane works in a git worktree of its own, never in your checkout. By default, the worktree of a clone inside the project's workspace goes in the workspace's `.ropeshift/worktrees/<repository>/<lane>`, and every other worktree goes outside every clone, in the installation's worktree directory, at `<worktree directory>/<project>/<repository>/<lane>`. The workspace root needs the workspace to ignore `/.ropeshift/worktrees/`, which the `.gitignore` of `ropeshift new` does; without that line, every lane keeps the root outside every clone and its start notice names the line to add. The workspace's own lanes always stay outside it. The workspace is the folder your profile names in `[projects.<project ID>]`, else the planning repository's clone when it holds the project's other clones in an ignored folder ([the workspace on a machine](project-structure.md#the-workspace-on-a-machine)). The worktree directory is:

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

To check a root before a lane does, run `ropeshift worker doctor`, or `ropeshift init` again. Its `Worktree root:` section names the root in force and the layer that set it, the built-in root or your profile. For each checkout this worker keeps, in every project, whose root resolves inside a clone, it adds a line with the project, the checkout, the root, the clone, and the fix. A built-in root outside every clone adds no line. For each project that has a workspace, the doctor also prints a `Workspace` line with the folder, how it was found, and whether the folder ignores `.ropeshift/worktrees/`, with the line to add when it does not, and the root each of the project's checkouts gets. A workspace is one your profile names, or the project's planning repository's clone when it holds the project's other clones in an ignored folder. The doctor needs no daemon, so it reads the project's planning repository from the state the worker last saved: a machine whose worker has never connected lists only the workspaces your profile names.

## Spend settings

A project's `[spend_policy]` holds the values that stop a lane from spending credits on waiting ([waiting costs no credits](how-it-works.md#waiting-costs-no-credits)) and set the limits on what a work order may spend. Every value is a named key with a default, and the policy is versioned: only a member publishes a version, with `ropeshift spend set`, or with `ropeshift policy publish --file F`, and `ropeshift spend show` and `ropeshift policy list --kind spend` read it. No plan, repository file, or agent can change it, and a lane's profile layer that names one of its keys is refused as an unknown setting. A delegate's change is a confirmation card that only a member confirms, and no delegate policy exempts it. Every version states every key, so a file that leaves one out is refused and the version always says what it runs on, each version is newer than the one in force, and a value outside its key's range is refused with the key and the range named. A version stored with a value outside its key's range is refused when it is read. A lane reads the version in force when it starts, and records it, so a new version reaches the next lane and a lane's history shows what it ran on. The limits of a work order follow the version in force at once. The built-in version is this one:

```toml
[spend_policy]
version = 1
# Waits
max_live_wait = "5m"
max_tool_sleep = "60s"
wait_rules_off = []
wait_rule_patterns = { W1 = [], W2 = [], W3 = [], W4 = [] }
# Limits
soft_quantile = "p85"
hard_multiplier = 2.0
low_confidence_outcomes = 6
low_confidence_soft_share = 0.5
no_history_limit = { small = 10.0, medium = 25.0, large = 60.0 }
review_share = 0.5
review_soft_share = 0.5
plan_hard_multiplier = 2.0
project_daily_limit = 0.0
project_weekly_limit = 0.0
project_warn_share = 0.8
assistant_daily_warn = 0.0
budget_flag_headroom_share = 0.1
# No progress
repeat_limit = 3
repeat_denials_to_park = 2
no_progress_share = 0.5
no_progress_park_multiplier = 2.0
no_history_no_progress = 5.0
# Waiting alert
waiting_alert_share = 0.5
waiting_alert_window = "30m"
waiting_alert_min_calls = 5
# Session classes
assistant_class = "standard"
```

Amounts are US dollars at list price, and 0 means no limit or no warning. A share runs from 0 to 1, and a share of 0 turns off the limit or the check it sets, as an amount of 0 does: `review_share` of 0 leaves a review with no hard limit, `review_soft_share` and `low_confidence_soft_share` of 0 leave no soft limit, and `no_progress_share` of 0 turns off the spend-since-progress check. A share of 0 never gives a limit of zero dollars. The defaults are starting values to tune, not measurements. The wait keys, the keys that set a work order's limits, and `budget_flag_headroom_share` act now. The keys for plan and project ceilings, no progress, the waiting alert, and session classes are held and shown now, and act when the mechanism each one names ships.

| Key | What it sets | Default | Range |
|-----|--------------|---------|-------|
| `max_live_wait` | The longest a lane waits live with `lane_wait`. A longer wait parks the lane | `"5m"` | 1 minute to 1 hour |
| `max_tool_sleep` | The sleep inside one command that rule W1 refuses, and any longer one | `"60s"` | 1 second to 1 hour |
| `wait_rules_off` | The wait rules to turn off, by ID: `W1`, `W2`, `W3`, or `W4` | `[]` | Those four IDs |
| `wait_rule_patterns` | Command prefixes a rule also matches, by rule ID | `[]` for each rule | At most 64 per rule, each at most 200 bytes |
| `soft_quantile` | Where a work order's soft limit sits in its cost band | `"p85"` | `p50` or `p85` |
| `hard_multiplier` | The hard limit of a work order, as a multiple of its band's p85 | 2.0 | 1 to 10 |
| `low_confidence_outcomes` | The finished work orders a band needs to be trusted | 6 | 1 to 100 |
| `low_confidence_soft_share` | The soft limit of a work order with no trusted band, as a share of its hard limit | 0.5 | 0 to 1 |
| `no_history_limit` | The hard limit of a work order with no trusted band, by size: `small`, `medium`, `large` | 10.0, 25.0, 60.0 | 0 to 1,000,000 each |
| `review_share` | A review's hard limit as a share of the reviewed work order's | 0.5 | 0 to 1 |
| `review_soft_share` | A review's soft limit as a share of its own hard limit | 0.5 | 0 to 1 |
| `plan_hard_multiplier` | The hard limit of a plan, as a multiple of its forecast's p85 | 2.0 | 1 to 10 |
| `project_daily_limit` | The project's daily ceiling | 0.0 | 0 to 1,000,000 |
| `project_weekly_limit` | The project's weekly ceiling | 0.0 | 0 to 1,000,000 |
| `project_warn_share` | The share of each ceiling where a notice sits | 0.8 | 0 to 1 |
| `assistant_daily_warn` | The assistant's daily warning, per project | 0.0 | 0 to 1,000,000 |
| `budget_flag_headroom_share` | The room a headless session's budget flag has over a hard limit | 0.1 | 0 to 1 |
| `repeat_limit` | How many identical calls a lane may repeat before the next is denied | 3 | 2 to 20 |
| `repeat_denials_to_park` | How many denials park the lane | 2 | 1 to 10 |
| `no_progress_share` | What a lane may spend with no progress before a message, as a share of its band's p50 | 0.5 | 0 to 1 |
| `no_progress_park_multiplier` | That share times this is where the lane parks | 2.0 | 1 to 10 |
| `no_history_no_progress` | What a lane with no trusted band may spend with no progress before a message | 5.0 | 0 to 1,000,000 |
| `waiting_alert_share` | The share of recent spend that waiting may reach before a notice | 0.5 | 0 to 1 |
| `waiting_alert_window` | The span that share is read over | `"30m"` | 1 minute to 24 hours |
| `waiting_alert_min_calls` | The calls that span needs before it notifies | 5 | 1 to 1,000 |
| `assistant_class` | The capability class the project's assistant runs on | `"standard"` | `small`, `standard`, `large`, `frontier` |

The installation keeps two more keys, because the orchestrator serves every project on its machine and no project holds them. They are `[installation_spend_policy]`, a versioned policy of kind `installation_spend` that `ropeshift spend set --installation` changes:

```toml
[installation_spend_policy]
version = 1
orchestrator_class = "standard"
orchestrator_daily_warn = 0.0
```

`orchestrator_class` is one of the four classes above, and `orchestrator_daily_warn` runs from 0 to 1,000,000.

### Change a setting

`ropeshift spend set <key>=<value>...` publishes a new version from the one in force with only the keys named changed. A key of a table or a list is named by its part, and a list is comma separated:

```sh
ropeshift spend set hard_multiplier=3
ropeshift spend set no_history_limit.medium=40 soft_quantile=p50
ropeshift spend set wait_rules_off=W4 wait_rule_patterns.W3="kubectl rollout status"
ropeshift spend set --installation orchestrator_daily_warn=40
```

`ropeshift spend show` prints each key with its value, the version in force and who published it, and each open work order's charged spend against its limits. `ropeshift policy list --kind spend` is the history, and `ropeshift spend revert <version>` records an earlier version as the newest, so its values are in force again. This policy turns W4 off, so a lane may run `gh pr view`, and makes W3 refuse `kubectl rollout status` too, with every other key stated at its default:

```toml
[spend_policy]
version = 2
# Waits
max_live_wait = "5m"
max_tool_sleep = "60s"
wait_rules_off = ["W4"]
wait_rule_patterns = { W1 = [], W2 = [], W3 = ["kubectl rollout status"], W4 = [] }
# Limits
soft_quantile = "p85"
hard_multiplier = 2.0
low_confidence_outcomes = 6
low_confidence_soft_share = 0.5
no_history_limit = { small = 10.0, medium = 25.0, large = 60.0 }
review_share = 0.5
review_soft_share = 0.5
plan_hard_multiplier = 2.0
project_daily_limit = 0.0
project_weekly_limit = 0.0
project_warn_share = 0.8
assistant_daily_warn = 0.0
budget_flag_headroom_share = 0.1
# No progress
repeat_limit = 3
repeat_denials_to_park = 2
no_progress_share = 0.5
no_progress_park_multiplier = 2.0
no_history_no_progress = 5.0
# Waiting alert
waiting_alert_share = 0.5
waiting_alert_window = "30m"
waiting_alert_min_calls = 5
# Session classes
assistant_class = "standard"
```

### Charged spend and limits

Every lane reports what each generation spent. A work order's charged spend is the cost its turns reported, across every lane that served it, a retried work order's earlier lanes included, plus each turn that reported nothing, because a stopped turn reports nothing, charged at the mean cost of the known turns of its generation, or of its lane when the generation has none. A surface that shows it says "estimated, at list price, with N turns estimated by average". On a subscription the figure is notional, and it is still a consistent measure of how much model work a session used.

A `change` or `rework` work order has its soft limit at `soft_quantile` of the cost band recorded when its plan was approved, or of its current band when it has no baseline, and its hard limit at `hard_multiplier` times that band's p85. A band drawn from fewer than `low_confidence_outcomes` finished work orders, or no estimate at all, gives the work order the `no_history_limit` of its size as its hard limit, and `low_confidence_soft_share` of that as its soft limit. A `review` has `review_share` of the reviewed work order's hard limit. Reaching a soft limit raises the `over_estimate` condition and one notice event per limit, and stops nothing. `ropeshift spend --work-order <id>` prints a work order's charged spend against its limits, and the same figures reach the API ([spend](api.md#spend)). A lane never sees its band, its limits, or what remains: its pack, its brief, and what it can read hold none of them. Work that a hard limit stopped is marked `spend_limited` in its delivery record, and an estimate that draws on it reads its cost as at least what it recorded. A lane that brings its work order to the hard limit is stopped, and [what a stop does](how-it-works.md#a-work-order-has-a-spend-limit) is described with how ropeshift works. A new version of the spend policy, or a revert, moves every limit at once, and wakes a lane the old limit parked if the new one is above what its work order was charged.

## How ropeshift reads CI

For a repository that reviews through pull requests, ropeshift reads each pull request's required checks itself, in code, and no session waits on CI ([waiting costs no credits](how-it-works.md#waiting-costs-no-credits)). A repository's CI keys sit on its record beside its merge method and review target, each with a default, and a maintainer changes them with `ropeshift repository set <repository> ci.<key>=<value>`, in the repository's settings in the web UI, or through the repository's change in the API. A delegate's change is a card that only a member confirms. A value outside its key's range is refused with the key and the range named. `repository show` prints every key with its value in force.

| Key | What it sets | Default | Range |
|-----|--------------|---------|-------|
| `ci_no_history_p50` | The expected duration of a run, until the repository's own [history](#ci-duration-history) gives one | 10m | 10s to 6h |
| `ci_no_history_p85` | The duration most runs finish within, until a history gives one | 20m | the p50 to 12h |
| `ci_min_interval` | The closest two reads of one pull request may be | 30s | 5s to 1h |
| `ci_steady_interval` | The gap between reads while every run is queued and once a run is past its p85 | 5m | the minimum gap to 1h |
| `ci_history_window` | How many recent records the history keeps | 50 | 5 to 500 |
| `ci_history_min_records` | The records a repository needs to have a history | 5 | 1 to 50, and no more than the window |
| `ci_timeout_multiplier`, `ci_timeout_no_history` | When CI that has not finished reaches a person | 3, 3h | 1.5 to 20, 10m to 72h |
| `ci_queue_timeout_multiplier`, `ci_queue_timeout_no_history` | When a run no runner picked up reaches a person | 3, 30m | 1.5 to 20, 1m to 24h |
| `ci_auto_rerun` | How many times ropeshift reruns an infrastructure failure | 1 | 0 to 3 |
| `ci_log_excerpt_lines`, `ci_log_excerpt_bytes` | The size of the failing job's log in a rework brief | 200, 8192 | 10 to 2000, 1 KiB to 64 KiB |
| `ci_webhook_secret_env` | The name of the variable in the server's environment that holds the key GitHub signs this repository's webhook deliveries with ([webhooks](github.md#webhooks-make-a-read-come-sooner)). It is the variable's name and never the key, and the name starts with `ROPESHIFT_HOOK_`, so no other variable of the server's environment can be named | none | an uppercase name that starts with `ROPESHIFT_HOOK_`, such as `ROPESHIFT_HOOK_WIDGETS` |

The read schedule runs on a clock that advances only while a required run is in progress, from the earliest start among the required runs. While every run is queued, ropeshift reads every `ci_steady_interval`. Once a run is in progress it reads once at the expected p50, then at half the remaining time to the p85 each time, never closer than `ci_min_interval`, and every `ci_steady_interval` after the p85 for as long as the run lasts, however many hours that is. A new push to the branch reads again at once.

### Red results are classified first

When every required check of a head has finished and one did not pass, ropeshift classifies it by the check's conclusion before any session starts. The classes are fixed, and no setting moves one:

| Conclusion | Class | What happens |
|------------|-------|--------------|
| `failure` | A test failure | The work goes back to rework. Its brief carries the check's name and link and the end of the failing job's log, at most `ci_log_excerpt_lines` lines and `ci_log_excerpt_bytes` bytes. A commit status that failed is a test failure too, and has no log |
| `failure`, with the same check failed on the base branch's tip | A red base | Nothing goes back and no lane starts. Needs you shows `ci_base_red` |
| `cancelled`, `timed_out`, `startup_failure`, `stale`, and a conclusion ropeshift does not know | Infrastructure | No lane starts. The worker reruns the failed jobs of the run, up to `ci_auto_rerun` times for each head, and then Needs you shows `ci_infrastructure_failure`. A check that is not a run of the repository's own GitHub Actions has no rerun to make, so it is raised at once |
| `action_required` | A person on the forge | Nothing starts. Needs you shows `ci_action_required` |

A head with a test failure is sent back whatever else failed on it, because its next commit runs every check again. The base branch's tip is read once for each repository in a forge interval, and only when a required check concluded `failure`; a read the forge refuses waits like any other, and holds the pull request's report until it answers, so a red base is never sent back for want of a read. Only check runs are compared, so a commit status that failed on the base is not seen.

Every rerun is a call the worker makes through the forge and an event with its class and its number, and a refusal by the forge, for a run that is still going, is recorded with its reason. `ci_auto_rerun` bounds only the reruns ropeshift makes by itself. A member's own rerun comes on top of them, is counted apart, and never uses one up.

The log in a rework brief is text anyone who can change a workflow can write, so the brief holds it as data. The escape sequences and the control characters other than newline and tab are removed. What has the shape of a credential, such as a GitHub token, an AWS access key, a `Bearer` token, or a private key, is replaced with `[redacted: <rule>]`, and a value GitHub already masked stays `***`. The end of the log is cut to the bounds above. It sits in a fenced block that no line of it can close, under words that say it is output to read and not instructions to follow. The work waits until the worker has read the log, and a log the forge no longer holds is an answer: the brief says none was read.

### When CI asks a person

CI that does not finish, or that no agent can mend, is one item in Needs you and one notice, and it starts no agent. The clock for both runs from the push, or from the last rerun, and a limit read from the repository's history stays within the range of its no-history key: never under the least it allows, and never over the most, 72 hours for the CI timeout and 24 hours for the queue.

- **CI has not finished** (`ci_not_finished`) is raised once the head's required checks have run for `ci_timeout_multiplier` times the repository's p85 of push-to-completed time, or for `ci_timeout_no_history` with no history. It offers Wait longer, which adds one more p85, or the time you name; Record a rerun, which says the checks were rerun on the forge, where a person runs them because ropeshift does not yet, and starts the head's CI clock again; and Open the run. `ropeshift work ci-wait <id> [--by 45m]` and `ropeshift work ci-rerun <id>` do the same from a terminal.
- **No runner picked it up** (`ci_queue_stalled`) is raised once a required run has stayed queued for `ci_queue_timeout_multiplier` times the repository's p85 queue time, or for `ci_queue_timeout_no_history`. A run still queued raises only this item, whichever limit is shorter, because the fix is a runner and not a rerun. It offers Open the run.
- `ci_infrastructure_failure`, `ci_base_red`, and `ci_action_required` are raised by the classification of a finished head: an infrastructure conclusion that stayed after `ci_auto_rerun` reruns, a failure the base branch's tip shares, and a check that waits for a person on the forge. Each offers Open the run, and an infrastructure failure also offers Record a rerun.

The work order's page shows the expected CI time, or "no history", with the time the head has run and the time it waited for a runner. Measures shows each repository's CI duration.

### CI duration history

When every required check of a head finishes, ropeshift keeps a CI record: the repository, the head, when ropeshift first held the head, when the first required run started, when the last one completed, how long the push waited for a run to start, the attempt, and the class of the result: pass, failure, infrastructure, or a check that waits for a person. A failure beside runs the forge cancelled because of it is a failure. A rerun of the same head is another record, and a head with no required check has none. A repository's expected duration is the p50 and p85, by nearest rank, of the push-to-completed times of its `ci_history_window` most recent records that ended in a pass or a failure, so an infrastructure fault says nothing about how long CI takes. With fewer than `ci_history_min_records` of them the repository has no history, and the no-history keys give the expected duration until it has. The read schedule above runs by the repository's own p50 and p85 from the record that makes the minimum.
## Next

[Plan your first change](plan-your-first-change.md) is the next guide. For a project that already has code, documents, and branches, [adopt an existing project](adopt.md) brings it in as a plan instead.
