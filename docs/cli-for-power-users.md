# The CLI for power users

Everything the guides do in the web UI, a command does too, with a few commands that have no web control. This guide lists, for each guide before it, the web action and its command, then the commands with no web control and why, sessions in a shell, the sample contract `ropeshift init` writes, and how to adopt a project from a terminal. [The command line](cli.md) describes every command and every flag, and `ropeshift help <command>` prints one command's usage.

## Before you start

- Finish [get started with the web UI](getting-started.md): the service runs, and the terminal has `ropeshift` on its `PATH`. A command reaches the daemon of the machine it runs on.
- A command that changes something needs a member's session. Without one, it asks for the passphrase on the terminal, for that one command. [Sessions in a shell](#sessions-in-a-shell) holds one for the whole shell.
- A command on a project's records acts on one project. It takes the project from `--project <name>`, else from `ROPESHIFT_PROJECT`, else from the repository the command runs in, else from the only active project ([projects](cli.md#projects)). The examples name the project with `--project`.
- Every command takes `--json`: its result is one JSON document on stdout, and a failure is one on stderr, with the same exit code ([exit codes](cli.md#exit-codes)).
- A command takes an ID where the web UI shows a title. `ropeshift work list`, `ropeshift plan list`, `ropeshift lane list`, and `ropeshift worker list` print them.

## Sessions in a shell

The first passphrase is set on a terminal, by `ropeshift init`. After that, a shell holds a session with one command:

```sh
eval "$(ropeshift login)"
```

It asks for the passphrase on the terminal, then puts a short-lived session in the shell's environment. No file holds it, and no lane inherits it. Every command of the shell then runs as you, and the ledger records each one as yours. `ropeshift sign-out` ends the shell's session, and `ropeshift sign-out --all` ends every session of yours and revokes every delegate credential of yours. `ropeshift member list` lists the members with a live session. `ropeshift passphrase set` changes the passphrase, which ends your other sessions.

A command with no session asks for the passphrase on the terminal. It never reads it from stdin, a flag, or a variable, so a process with no terminal cannot sign in. An agent outside ropeshift acts with a delegate credential instead, which `ropeshift delegate create` makes ([delegates](cli.md#delegates)).

## Get started with the web UI

The terminal steps of the guide are commands already. The web steps:

| Web action | Command |
|------------|---------|
| Sign in with the passphrase | `eval "$(ropeshift login)"`, once for each shell |
| Check the worker: Workers and resources, Machines and Drivers | `ropeshift worker list`, and `ropeshift worker doctor` for each agent CLI of this machine and its sign-in |
| Find the worker's level: Settings, Autonomy and run windows | `ropeshift worker autonomy <worker>` |
| Read the stated risk | `ropeshift worker risk`, which prints the stated risk and its digest |
| I stand behind it | `ropeshift worker acknowledge-risk <worker> --digest <digest>` |
| Choose another level | `ropeshift worker level <worker> manual\|attended\|unattended` |
| Needs you, and its count | `ropeshift needs` lists what waits in every project |
| The conversation panel | `ropeshift chat` talks to the assistant of the project, and `ropeshift chat list` lists your conversations |
| The version in the status bar | `ropeshift version`, which also prints the daemon's |
| The palette and the shortcuts | None. They are the page's own |

## Set up your projects

| Web action | Command |
|------------|---------|
| Projects | `ropeshift project list`, and `ropeshift project show <name>` for one |
| Create project, with Display name and Name | `ropeshift project create harbor --display-name Harbor` |
| Rename | `ropeshift project rename <name> <new-name> [--display-name <text>]` |
| Register repositories, One repository | `ropeshift repository add <clone> --project harbor` |
| Register repositories, A folder of clones, with Depth | `ropeshift repository add <folder> --depth 1 --project harbor` |
| Run dry run | The same command with `--dry-run`, which records nothing |
| Repositories | `ropeshift repository list`, and `ropeshift repository show <name>` for one |
| The record: Planning repository | `ropeshift repository set harbor-plan --planning`, and `--planning=false` stops it |
| The record: Name, Base branch, Start from, Review target, Forge binding, Merge authority, Merge method | `ropeshift repository set <name> --name <name> --base <branch> --start-from merged\|accepted --review-target branch\|pull_request --forge-binding <host/owner/repo> --merge-authority person_merges\|person_approves\|review_approves --merge-method merge\|squash\|rebase` |
| Remove repository | `ropeshift repository remove <name>` |
| Create a second project, and switch with `P` | `ropeshift project create tidewater --display-name Tidewater`, and `--project tidewater` on each command that follows |
| Start a project from a template | `ropeshift new workspace <path>`, `ropeshift new monorepo <path>`, or `ropeshift new repository <path>`, which write the files and print the git commands to run ([a new project](cli.md#a-new-project)), and `ropeshift template show` lists the templates |
| Where lanes work | `ropeshift init --worktree-root <absolute path>` when `profile.toml` does not exist yet, and `ropeshift worker doctor` prints the root in force ([where lanes work](set-up-projects.md#where-lanes-work)) |

## Plan your first change

| Web action | Command |
|------------|---------|
| Start planning session, with Goal, Planning repository, and Worker | `ropeshift plan start --goal "<goal>" --repository <name> --worker <worker> --project harbor`, which attaches your terminal to the session. `--goal-file <path>` reads the goal from a file, and `--no-attach` leaves your terminal alone |
| The Planning page, and Type into the session | `ropeshift lane attach <lane>` puts your terminal on the session's pane, and detaching leaves it running |
| Needs you: the questions of a batch | `ropeshift question batches` lists each question with its options and its recommendation |
| Choose an answer for each, and Send answers | `ropeshift question answer-batch <batch> --answers <file>`, which records every answer or none, and `--accept-reversible` takes the recommendation of every reversible question the file does not answer. `ropeshift question answer <id> --answer <text>` answers one question |
| The Outline tab | `ropeshift lane outline <lane>` |
| The plan approval card and page | `ropeshift plan show <plan>` prints the version, the difference, the draft decisions, the forecasts, and the repositories a manifest lists |
| Approve version 1 | `ropeshift plan approve <plan> --version 1` |
| Decline… with a reason | `ropeshift approval deny <approval> --digest <digest> --reason <text>`, with the approval and its digest from `ropeshift approval list` and `ropeshift approval show <approval>` |
| The Roadmap | `ropeshift plan list`, `ropeshift plan show <plan>`, and `ropeshift view roadmap`, which prints the view as JSON |

## Adopt an existing project

| Web action | Command |
|------------|---------|
| Import an existing project: Folder, Depth, Project, Run dry run | `ropeshift adopt <path>... --depth <n> --project <name> --dry-run`, run in the first clone |
| Choose the Planning repository | `--planning <name>`, by the clone's folder name |
| Start adopt session | `ropeshift adopt <path>... --project <name>`, which attaches your terminal to the session |
| Restructure, and the design repository | `--restructure`, `--design <name>`, and `--leave-out <path>` |
| Answer in Needs you, and approve the plan | As [plan your first change](#plan-your-first-change) |

[Adopt from a terminal](#adopt-from-a-terminal) says what the command reads and prints.

## Watch work run

| Web action | Command |
|------------|---------|
| Start orchestrator, in Workers and resources | `ropeshift orchestrator start <worker>`, and `ropeshift orchestrator show <worker>` says its state. `ropeshift orchestrator stop <worker>` stops it and no lane |
| Open a run window, with Hours | `ropeshift worker window-open <worker> --for 4h`, which defaults to 4 hours and allows 12. `ropeshift worker window-close <worker>` ends it and stops no lane |
| Start on a work order, with Worker | `ropeshift work claim <work-order> --worker <worker>`, then `ropeshift lane start <work-order>` |
| Floor | `ropeshift lane list`, `ropeshift lane show <lane>`, and `ropeshift top`, which watches the fleet in a terminal |
| Open on a lane | `ropeshift lane attach <lane>` for a lane in a terminal pane |
| The lines above a pane's screen, and Copy all | `ropeshift lane scrollback <lane> --lines <count>` prints the newest lines of the scrollback as text, 200 without `--lines`, and says on stderr when the pane keeps more |
| Message, to a lane or the orchestrator | `ropeshift message send <lane> --body <text>`, or `ropeshift message send --orchestrator <worker> --body <text>`. `ropeshift message list <lane>` and `ropeshift message show <id>` say what became of each. `ropeshift orchestrator attach` follows the orchestrator's stream |
| Needs you: a batch an orchestrator could not settle | `ropeshift question batches`, and `ropeshift question answer-batch <batch> --answers <file>` |
| Allow this one call, and Deny | `ropeshift approval list`, `ropeshift approval show <approval>`, then `ropeshift approval approve <approval> --digest <digest>` or `ropeshift approval deny <approval> --digest <digest>`, each with an optional `--reason <text>` the lane reads |
| Stuck: Resume | `ropeshift lane resume <work-order>` |
| Stuck: Reset the breaker | `ropeshift lane reset <work-order>` |
| Stuck: Start the next generation | `ropeshift lane respawn <work-order>` |
| Stuck: Retry | `ropeshift work retry <work-order> --reason <text>` |
| Stuck: Back to ready, and Back to planning | `ropeshift work resolve <work-order> --reason <text>`, and `ropeshift work replan <work-order> --reason <text>` |
| Stuck: End this claim, and Cancel this work | `ropeshift work reassign <work-order> --reason <text>`, and `ropeshift work cancel <work-order> --reason <text>` |
| The Overview's progress | `ropeshift project show <name>`, `ropeshift plan show <plan>`, and `ropeshift work list` |

## Review and merge

| Web action | Command |
|------------|---------|
| The review lane, and Start | `ropeshift work list` lists the review work order the control plane made, and `ropeshift work claim <review> --worker <worker>` then `ropeshift lane start <review>` start it |
| Reviews, the queue | `ropeshift work list`, and `ropeshift work show <work-order>` prints each review, each verdict, what the work order waits for, and its pull request |
| The Review tab | `ropeshift work show <work-order>` |
| The Changes tab | `ropeshift work acceptance <work-order>`, which prints each branch with its files and lines, the criteria, the checks, and what is left |
| The Activity tab | `ropeshift work timeline <work-order>` |
| Accept | `ropeshift work accept <work-order>` |
| Send back for rework | `ropeshift work rework <work-order> --reason <text>` |
| Merge authority `person_approves` | `ropeshift repository set <name> --merge-authority person_approves` |
| Approve merge | `ropeshift approval approve <approval> --digest <digest>`, or `ropeshift work accept <work-order>` for work in `integrating`, which approves the merge of its reviewed commit |
| Deny | `ropeshift approval deny <approval> --digest <digest> --reason <text>`, which sends the work back to rework |
| Archive the worktree | `ropeshift lane archive <lane>`, and `--force` for a worktree with unpushed work |
| Agreement | `ropeshift repository show <name>` prints the repository's merge authority and its review agreement, over the last 30 days and over all time |

At `person_merges`, the default, ropeshift merges nothing, and you merge the branch with git ([merge at person_merges](review-and-merge.md#merge-at-person_merges)).

## Connect a GitHub repository

Signing in to the GitHub CLI is a terminal step in the guide, with `gh auth login` and `gh auth status`.

| Web action | Command |
|------------|---------|
| Review target `pull_request`, and Forge binding | `ropeshift repository set <name> --review-target pull_request --forge-binding github.com/<owner>/<repo>`, and the binding may be left out when the remote names the repository |
| Check the worker's sign-in | `ropeshift repository show <name>` prints what each worker found of its `gh` sign-in for the repository |
| The Pull request and checks tab | `ropeshift work acceptance <work-order>` prints the pull request, its head commit, and its checks, each marked `required` where the base branch requires it |
| Reviews, Pull requests | `ropeshift work list`, and `ropeshift work show <work-order>`, which prints the `pull request` line |
| Merge method | `ropeshift repository set <name> --merge-method merge\|squash\|rebase` |
| Required checks, and a check that asks a person | `ropeshift work ci-wait <work-order>`, `ropeshift work ci-rerun <work-order>`, and `ropeshift measures ci` prints how long each repository's CI takes. `ropeshift repository set <name> ci.<key>=<value>` sets a repository's CI keys ([how ropeshift reads CI](set-up-projects.md#how-ropeshift-reads-ci)) |

## Run several projects at once

| Web action | Command |
|------------|---------|
| The project switcher | `ropeshift project list`, and `--project <name>` or `ROPESHIFT_PROJECT` on every command that follows |
| Needs you across projects | `ropeshift needs`, and `ropeshift top` |
| Floor, All projects | `ropeshift lane list --project <name>` for each project, and `ropeshift top` |
| Settings, Admission and lane caps | `ropeshift worker caps <worker> --max-lanes <n>\|none\|recommended --per-sign-in <n>\|none`, and without flags it prints the caps beside the recommendation |
| Settings, Fleet: Running, Paused, Draining | `ropeshift fleet run`, `ropeshift fleet pause`, `ropeshift fleet drain`, and `ropeshift fleet show` |
| The project's caps | `ropeshift fleet caps --wip <n> --per-repository <n> --per-worker <n>`, where 0 caps nothing |
| Back up | `ropeshift backup <file> --project <name>` |
| Import backup | `ropeshift project import <file> --name <name>` |
| Archive…, and Restore | `ropeshift project archive <name>`, and `ropeshift project restore <name>` |
| Remove… | `ropeshift project remove <name> --confirm <name> --backup <file>`, or `--no-backup` |

## Settings and agents

| Web action | Command |
|------------|---------|
| A policy: Review change | `ropeshift policy list --kind <kind>`, and `ropeshift policy show <version> --kind <kind>` prints one version as its author wrote it. `ropeshift policy publish --file <file>` publishes a new version from a file that names its table and a version newer than the one in force |
| Restore an earlier version | `ropeshift policy show <version> --kind <kind>`, then publish its body under a new version number with `ropeshift policy publish --file <file>` |
| Decision policy | `--kind decision` |
| Notifications, and the notices | `--kind notify`, and `ropeshift notice list` says what each channel was told |
| Settings, Spend, under Project, and Orchestrator spend, under Installation | `ropeshift spend show`, `ropeshift spend set <key>=<value>...`, and `ropeshift spend revert <version>`, the last two with `--installation` for Orchestrator spend. [Spend settings](set-up-projects.md#spend-settings) says what they change, and `ropeshift policy list --kind spend` lists the versions |
| Agents, Roster | `ropeshift agent list`, and `ropeshift agent show <name>` |
| Change an agent | `ropeshift agent set <name> <field>=<value> --project <name>`, or `--installation` for the installation's layer |
| Add agent, from a copy or Paste TOML | `ropeshift agent add <name> --from <agent> --project <name>`, or `--file <file>` |
| Disable, Enable, and Reset | `ropeshift agent disable <name>`, `ropeshift agent enable <name>`, and `ropeshift agent reset <name>`, each with `--project <name>` or `--installation` |
| The Routing guide | `ropeshift agent guide show`, `ropeshift agent guide set @<file>`, and `ropeshift agent guide reset` |
| History, and restoring a roster version | `ropeshift agent history [<name>]`, and `ropeshift agent revert <version> --project <name>` |
| Members | `ropeshift member list` |
| Sign out all sessions | `ropeshift sign-out --all` |
| Delegates, Create delegate | `ropeshift delegate create --project <name> --label <text> --expires 8h`, which shows the credential once, and `ropeshift delegate list`, `ropeshift delegate revoke <id>`, and `ropeshift delegate remove <id>` |
| Settings, Backup | `ropeshift backup <file> --installation` |
| Change passphrase | `ropeshift passphrase set` |

## Usage and measures

| Web action | Command |
|------------|---------|
| The Overview's figures | `ropeshift measures`, and `ropeshift needs` |
| Measures, Factory | `ropeshift measures --window <duration>` prints the product measures, the north star first |
| Delivery, with Window and Group by | `ropeshift measures delivery --window <duration> --by kind\|size\|agent\|repository`, and `--by project` reads every active project, each apart |
| Estimates | `ropeshift measures estimates --window <duration>`, and `ropeshift measures history` prints the delivery history a planning session sees |
| A plan's Forecast | `ropeshift plan forecast <plan> --version <n>` |
| A work order's Estimate panel | `ropeshift work show <work-order>` |
| Usage | `ropeshift telemetry lanes` prints each lane's context, tokens, and cost |
| Audit | `ropeshift audit`, and `--installation` reads the installation's own log. `--actor`, `--kind`, and `--subject` narrow it |
| Audit, Notices | `ropeshift notice list` |

## Troubleshooting

| Where the web UI shows it | Command |
|---------------------------|---------|
| The web UI does not load | `ropeshift health`, which exits 9 when nothing answers, and `ropeshift service install` starts the service |
| Workers and resources, Drivers | `ropeshift worker doctor` |
| The Orchestrator column | `ropeshift orchestrator show <worker>`, and `ropeshift orchestrator start <worker>` |
| Waiting for a run window | `ropeshift worker autonomy <worker>`, and `ropeshift worker window-open <worker>` |
| A Paused or Draining fleet | `ropeshift fleet show`, and `ropeshift fleet run` |
| A work order's facts, and what holds it back | `ropeshift work show <work-order>` prints a `waiting` line, such as `waiting_for_merge` |
| A start that is refused | `ropeshift work claim` and `ropeshift lane start` print the refusal in the daemon's words |
| Needs you, Stuck | `ropeshift needs`, and the commands of [watch work run](#watch-work-run) |
| A planning session that does not start | `ropeshift work show <work-order>` says `no_interactive_mode` when tmux is missing |
| A forgotten passphrase | A shell that holds a session changes it with `ropeshift passphrase set` |

## Commands with no web control

These commands have no control in the web UI. Every other command has one, or has a web action that does the same.

| Command | Why it stays on the command line |
|---------|----------------------------------|
| `ropeshift init` | It sets up the machine: the first passphrase, which comes only from a terminal, the worker, your profile, and the doctor |
| `ropeshift service install`, `ropeshift service uninstall` | They act on the machine's service manager, launchd or systemd, which the daemon cannot manage for itself |
| `ropeshift top` | It is the terminal's own view of the fleet. Needs you, Floor, and Workers and resources show the same in the web UI |
| `ropeshift telemetry query` | It gives an orchestrator agent the worker's raw telemetry. Usage shows each lane's context, tokens, and cost |
| `ropeshift work write` | It stores one event under a claim's fence, which the worker's protocol relies on. A person never runs it |
| `ropeshift repository add --manifest <file>` | It registers the repositories a manifest lists on a new machine, with no plan approval. The web UI registers them when you approve a plan whose manifest lists them ([keep a manifest](set-up-projects.md#keep-a-manifest-in-the-planning-repository)) |

`ropeshift login` and `ropeshift lane attach` have a web counterpart, Sign in and a lane's screen, and each puts the session or the pane in your terminal. The agent CLI and the worker start `ropeshift hook`, `mcp`, `statusline`, `lane-pane`, and `lane-shim`, and a person never does.

## The sample contract

`ropeshift init` writes `sample-contract.md` to the installation's directory ([where ropeshift keeps its state](onboarding.md#where-ropeshift-keeps-its-state)), and keeps one already there. It is a contract with one criterion, that `README.md` names the command which runs the tests, and one small work order that writes it. Its work is safe to run, so a first lane on it risks nothing.

To run it without a planning session, copy it into a repository the project holds, change the identifiers, the title, and the criteria to your own work, and commit it. Intake reads it, and its work order waits for a review:

```sh
ropeshift work list
ropeshift work review <work-order>
ropeshift work claim <work-order> --worker <worker>
ropeshift lane start <work-order>
```

The first command names the work order, and the second passes its review. The third and the fourth are Start in [watch work run](watch-work-run.md#3-start-a-work-order-yourself), or open a run window, and the worker's orchestrator, which starts with the worker once a passphrase is set, claims the work for you.

## Adopt from a terminal

`ropeshift adopt` reads the layout around the repository it runs in before it registers anything. It reads that repository, every folder the repository ignores, searched for clones down to `--depth` levels (one by default), and every `<path>` you name, each a clone or a folder searched to the same depth. It never scans the folder around the repository, so a clone beside it is seen only when a path names it. [The shapes](adopt.md#shapes) say what it offers for each layout.

| Flag | What it does |
|------|--------------|
| `--dry-run` | Prints the plan and records nothing: no project, no record, no checkout, and no file. With `--json` it prints the plan as JSON |
| `--restructure` | Sends the plan's digest, its choices, the request it was read with, and the digest of each of its parts with the adopt work, and prints the moves and the steps for code repositories as commands for you to run. Without it adopt never restructures, and with it a layout of the `single` shape that already holds an instruction file is a usage error. [The workspace files](adopt.md#the-workspace-files) and [consolidating a design repository](adopt.md#consolidating-a-design-repository) say what the worker does with it |
| `--depth N` | How many levels below a folder to look for clones |
| `--planning NAME` | The repository that holds the plan, by its clone's folder name. By default the shape says, and when it says none, adopt asks on the terminal, and with no terminal stops with exit 2 and the names to choose from |
| `--design NAME` | The design repository, by its clone's folder name |
| `--leave-out PATH` | A code tree of the design repository that a consolidation removes, relative to its top. Repeatable |

The design repository is the clone that holds plan content, a committed file in ropeshift's format, else the clone with the most committed Markdown documents. A tie names none, and the dry run says to pass `--design NAME`.

A code tree is the topmost directory below a repository's top that holds a build file: `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `setup.py`, `pom.xml`, `build.gradle`, `build.gradle.kts`, `Gemfile`, `mix.exs`, `composer.json`, `CMakeLists.txt`, or a `*.csproj`. A build file at the top makes the whole repository one code tree, which cannot be left out.

### The dry run of adopt

The dry run prints one fact to a line:

| Line | What it says |
|------|--------------|
| `Layout`, `Clone` | The clone adopt runs in, and each clone found with where it was found and what it holds |
| `Shape`, `Planning repository`, `Design repository` | The shape, where the plan lives, and the design repository with why it is the one |
| `would register`, `already registered`, `refused`, `not registered` | Each repository, with the outcome a registration gives it. A `consolidate` layout lists the design repository as `not registered` |
| `Practices` | Under each repository the dry run read, the practices check as `ropeshift practices` prints it, with no acknowledgements, no worker, and no age: the open findings by severity, each with why it matters and its fix, the practices the check could not settle, and the practices met. The check reads the base branch a registration would record, as the clone last fetched it from `origin` (the dry run fetches nothing), on this machine, with no forge binding, for at most 30 seconds a repository, so `branch-protection`, `required-review`, and `license` are not applicable, and it records nothing; adopting records the check. A repository the check cannot read, or does not finish in time, prints `Practices` and the reason on one line. `--json` carries each result as `practices` and the reason as `practices_note` |
| `add`, `append` | A file the restructuring would add to the planning repository, or the lines it would append, each only when the repository lacks it |
| `Files the layout lacks` | A heading, followed by one `add` line for each of `AGENTS.md`, `CLAUDE.md`, `Makefile`, and `.gitignore` that the planning repository lacks, which are written from the templates `ropeshift new` uses. [The files a layout lacks](adopt.md#the-files-a-layout-lacks) says when each is listed |
| `Steps for you`, `step`, `run` | A heading, then each code repository whose clone holds no instruction file, with the `[ -e <clone>/AGENTS.md ] || ropeshift template show repository/agents.md > <clone>/AGENTS.md` command for you to run, because adoption writes to no other repository. A clone the plan moves is named where the move leads |
| `Left out of the Makefile`, `left out` | A heading, then each repository the Makefile the restructuring writes leaves out, with its path and the reason. `--restructure` names them again when the session starts. The manifest still lists them |
| `take`, `collision` | A path a consolidation takes from the design repository, and a path both repositories hold with the version kept: `workspace`, or `union` for `.gitignore` |
| `code tree` | A code tree of the design repository with its build file, and whether it is left out |
| `move`, `run` | A clone to move into the planning repository's folder of clones, and the commands that do it, which ropeshift never runs |
| `Digest` | The SHA-256 of the plan, which `--restructure` sends back, with the request the plan was read with and the digest of each of its parts, so the worker finds a layout that changed in between and names the parts that differ. The practices are no part of it |

Ropeshift never moves, deletes, or writes to a clone other than through a lane worktree of the planning repository, and never changes a remote.

## Next

[Learn ropeshift](../README.md#learn-ropeshift) lists the guides, and [the command line](cli.md) describes every command and flag.
