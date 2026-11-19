# Project structure

Ropeshift reads your plan from git, so where your files live decides what it sees. This page recommends one layout that works for a project of one repository and grows to many, explains why each part is where it is, and describes the file format the plan is written in.

## The recommended layout

Keep one workspace repository that is also the project's planning repository. It holds the instructions every agent reads, the plan of record, and a Makefile that sets up the rest. Clone the code repositories into a folder inside it that git ignores, and ignore `.ropeshift/worktrees/` too: the lane worktrees of those code repositories go there, and the lane worktrees of the workspace repository itself go outside it.

```text
acme-workspace/                 the workspace: a git repository, and the planning repository
├── AGENTS.md                   the project and its rules, read by every agent
├── CLAUDE.md                   the same instructions for Claude Code, or an import of AGENTS.md
├── Makefile                    setup, fetch, and status for every repository
├── .gitignore                  ignores repos/ and /.ropeshift/worktrees/
├── plan/                       the plan of record, in ropeshift's file format
│   ├── goal.md                 kind = "goal"
│   ├── plan.md                 kind = "plan"
│   ├── repositories.md         kind = "repositories": the manifest
│   ├── requirements.md         kind = "requirements"
│   ├── architecture.md         kind = "architecture"
│   ├── milestones/
│   │   └── m1-accounts.md      kind = "milestone"
│   └── contracts/
│       ├── accounts-api.md     kind = "contract", its work orders in acme-api
│       └── accounts-web.md     kind = "contract", its work orders in acme-web
├── docs/                       notes in your own format, read as documents
├── repos/                      git-ignored: each code repository, a clone of its own
│   ├── acme-api/
│   └── acme-web/
└── .ropeshift/worktrees/       git-ignored: the lane worktrees of each code repository
    ├── acme-api/
    └── acme-web/

/path/to/worktree-directory/    the installation's worktree directory, outside the workspace
└── <project>/acme-workspace/   the lane worktrees of the workspace repository itself
```

The folder of clones can have any name, such as `repos/` or `development/`. The names `acme-workspace`, `acme-api`, and `acme-web` stand for your own.

A project of one repository needs none of the extra parts: that repository is its planning repository, and the plan can live in a `plan/` folder beside the code. Start there, and move to a workspace when a second repository joins.

## Start a project

`ropeshift new` writes a new project's starting files into a folder, and prints the commands that register it ([command line](cli.md#a-new-project)). It needs no daemon and no session, and never replaces a file. Choose the shape by where the plan lives:

| Command | Writes | For |
|---------|--------|-----|
| `ropeshift new workspace <folder> --repository acme-api=<remote>` | The workspace above, with each code repository in the Makefile and the manifest at `repos/<name>` | A project of several repositories. The printed `make -C <folder> setup` clones each one into `repos/`, and `make register` registers only the clones the Makefile lists |
| `ropeshift new monorepo <folder>` | `AGENTS.md`, `CLAUDE.md`, the plan in `plan/`, and `catalog-info.yaml` beside the code | A project of one repository, which plans in itself |
| `ropeshift new repository <folder> --project acme` | `AGENTS.md`, `CLAUDE.md`, and `catalog-info.yaml`, with no `plan/` | A code repository of a project that plans in another repository, such as a new one in a workspace's `repos/` |

Every shape also writes `.gitignore`, `SECURITY.md`, and `CODEOWNERS`, and a monorepo and a repository a `.pre-commit-config.yaml`. The project is named after the folder. When the folder has no repository of its own, the printed commands make its first commit first, because a repository is registered by its first commit.

With no shape, `ropeshift new` asks first, in plain words, whether the project will live in one repository or in several, and writes the monorepo for one and a workspace for several. It can also make each repository's first commit and give each a remote, a URL or a new repository on GitHub ([create a new project's repository on GitHub](github.md#create-a-new-projects-repository-on-github)). In a workspace each new code repository is written at `repos/<name>` from the repository shape's files, and the manifest lists it, so approving the first plan registers it.

## Growing a project

The model does not change as a project grows, so each step adds files and settings and moves nothing that exists:

1. **One repository.** Start it with `ropeshift new monorepo`, whose `plan/` sits beside the code. That repository is the planning repository.
2. **A second repository.** Keep the first as the planning repository and add a manifest, `plan/repositories.md`, that lists the second at its path, or restructure into a workspace with `ropeshift adopt --restructure` ([adopt an existing project](adopt.md)). A new code repository for a project planned elsewhere starts with `ropeshift new repository`.
3. **Several repositories.** A workspace is the planning repository, with each code repository cloned into its ignored `repos/` folder and listed in the manifest. `ropeshift new workspace` starts one, and `ropeshift adopt --restructure` writes the files a layout lacks.

## Why each part is where it is

| Part | Why |
|------|-----|
| The workspace is the planning repository | A project plans in one repository, its plan of record. Once you name it with `ropeshift repository set <name> --planning`, intake reads goals, plans, documents, milestones, contracts, and the manifest only from it, and reports a plan file found anywhere else, so a misplaced plan is seen and never read |
| `AGENTS.md` or `CLAUDE.md` | A planning session runs in a worktree of the planning repository, so its agent reads these instructions. Say what the project is, where plan files go (`plan/`), which document wins when two disagree, and which checks a change must pass |
| `Makefile` | Ropeshift clones nothing. A registered repository whose clone is missing holds its work as `no_checkout` until the clone is there. A `make setup` that clones each missing repository, a `make fetch`, and a `make status` make a fresh machine ready in one step. A `make register` registers the workspace and the clone of each repository the Makefile lists, each by its own path at any depth, skips a listed repository with no clone, and registers no clone the Makefile does not list |
| `repos/` is git-ignored | Intake reads only what git has committed, file by file, so the clones inside the workspace are never read as part of it. Each clone is a repository of its own, registered on its own |
| The manifest lists `repos/acme-api` | A manifest path is relative to the planning repository's clone, and may lead into a folder inside it, or beside it, such as `../acme-api`. It must stay inside the folder that holds the planning repository's clone ([keep a manifest in the planning repository](set-up-projects.md#keep-a-manifest-in-the-planning-repository)) |
| `/.ropeshift/worktrees/` is git-ignored | Lane worktrees of the clones in `repos/` go there, inside the workspace, so the agent in each of those lanes loads the workspace's `CLAUDE.md` as well as its repository's own. The workspace's own lanes stay outside. See [where lane worktrees go](#where-lane-worktrees-go) and [the instructions a lane loads](#the-instructions-a-lane-loads) |

## Where lane worktrees go

Each lane works in a git worktree of its own. By default, ropeshift puts it in one of two places. A clone inside the project's [workspace](#the-workspace-on-a-machine) goes in the workspace's ignored `.ropeshift/worktrees/` folder, as the next section says. Every other clone goes outside every clone, in the installation's worktree directory, at `<worktree directory>/<project>/<repository>/<lane>`:

| System | Worktree directory |
|--------|--------------------|
| Linux | `$XDG_DATA_HOME/ropeshift/worktrees`, or `~/.local/share/ropeshift/worktrees` when `XDG_DATA_HOME` is unset |
| macOS | `~/.ropeshift/worktrees`, a path with no space, which some build tools mishandle |
| With `ROPESHIFT_STATE_DIR` set | `<state directory>/worktrees`, so a second installation and every test keep their lanes apart from yours |

`ROPESHIFT_WORKTREE_DIR` replaces the worktree directory in every case, and must be an absolute path: the daemon refuses to start on a relative one. A relative `XDG_DATA_HOME` is ignored, as the XDG specification says. `<project>` is the project's ID, which a rename keeps, and `<repository>` is the name of the repository's folder, so two projects whose clones share a folder name never share a lane folder. A lane in `repos/acme-api` of a workspace therefore never lands in `repos/`, where scripts that loop over `repos/*` would find it, and never lands in a clone, where its files would show as untracked content.

### The workspace on a machine

A project's workspace on a machine is the folder that holds its clones there. Ropeshift finds it when a lane's worktree is placed, and stores nothing:

1. The folder your own profile layer names, in `[projects.<project ID>]`, as an absolute path. It may be a plain folder and not a clone. An empty `workspace = ""` says the project has no workspace on this machine, and stops the search.
2. Otherwise the checkout of the project's planning repository, when this machine keeps another checkout of the project inside it at a path it ignores, as `repos/acme-api` is in the recommended layout.
3. Otherwise the project has none. A design repository with its code repositories beside it has no workspace until the clones move into its ignored folder.

```toml
[projects.my-project-id]
workspace = "/path/to/workspace"
```

A clone that lies inside the workspace, and is not the workspace, gets the built-in root `<workspace>/.ropeshift/worktrees/<repository>/<lane>`, so the agent in a lane loads the workspace's instruction files from the folders above its own ([the instructions a lane loads](#the-instructions-a-lane-loads)). Three kinds of lane keep the root outside every clone:

- The workspace repository's own lanes. A worktree of the workspace nested in it would load the workspace's instructions twice.
- A clone outside the workspace, and every clone of a project with no workspace.
- A lane of a workspace that does not ignore `.ropeshift/worktrees/`. Add this line to the workspace's `.gitignore`. The workspace `.gitignore` that `ropeshift new` writes has it, and `ropeshift adopt --restructure` adds it on the adoption branch:

  ```text
  /.ropeshift/worktrees/
  ```

  The line names the worktree folder and not `.ropeshift/`, because the workspace commits its own profile layer at `.ropeshift/profile.toml`. Until the line is committed, the lane's start notice names the workspace and the line, and `ropeshift worker doctor` does too. When the workspace is the checkout of a repository that reviews through pull requests, the worker also reports the unignored folder with that repository's forge sign-in, and the Forge column of Repositories shows the workspace and the line to add on that repository. Ropeshift asks git whether the folder is ignored, with `git check-ignore`, so a rule in a global ignore file counts, and it never writes the line to a default branch. A workspace that is a plain folder needs no line.

  The folder a lane would use must also stay in the workspace once links are followed. A symbolic link planted for `.ropeshift`, for `.ropeshift/worktrees/`, for the repository's folder in it, or for a lane's own folder would lead lanes out of the workspace, past the ignore rule that guarded the path as written, and so would a link at any of them to a folder nothing is at, where git would make the worktree. A lane whose folder leaves the workspace keeps the root outside every clone, for a plain folder as for a clone. Its start notice and the doctor's line for that checkout say a link leads it out, and ropeshift follows no such link.

A workspace ropeshift cannot check, as when git refuses to answer for a clone whose `.ropeshift` is a link, stops no lane that does not use it. A lane with a root you set starts as it would with no workspace, a lane on the built-in root starts outside every clone with the reason in its start notice, and `ropeshift worker doctor` prints the workspace's error as a line of its own and reports the rest.

A root you set in your own profile layer wins over both places. A root inside the workspace's clone is allowed only under `.ropeshift/worktrees/`, and only while the workspace ignores it; a root under that folder while it is not ignored is refused with `worktree_inside_clone`, and the detail names the line to add. A root inside any other clone, every code clone in the workspace included, is refused as before. A lane that already has a worktree goes on in it, so adding the workspace, or its ignore line, moves no lane; only a worktree made afterwards uses the workspace folder. `ropeshift lane archive` removes a worktree wherever it is and leaves the empty `.ropeshift/worktrees/<repository>` folder.

A `.ropeshift` folder is ropeshift's own. No scan of `repository add`, `adopt`, or `adopt --dry-run` searches one, a path inside one is refused by name, and a lane worktree in one is never found or registered as a clone.

### The instructions a lane loads

Ropeshift starts Claude Code in the lane's worktree, as its working directory, and passes no flag or setting that limits which instruction files it loads. Claude Code loads `CLAUDE.md` and `CLAUDE.local.md` from its working directory and from every directory above it, ordered from the filesystem root down, so the instructions nearest the worktree come last. It reads an `AGENTS.md` directly only when none of those directories holds a `CLAUDE.md` or `CLAUDE.local.md` ([Claude Code memory](https://code.claude.com/docs/en/memory)). Every `CLAUDE.md` that `ropeshift new` writes holds the line `@AGENTS.md`, so the `AGENTS.md` beside it loads through that import. Where the worktree is decides what a lane loads:

| Lane | Its worktree | What it loads besides its repository's own instructions |
|------|--------------|---------------------------------------------------------|
| A clone inside the workspace, which ignores `.ropeshift/worktrees/` | `<workspace>/.ropeshift/worktrees/<repository>/<lane>` | The workspace's `CLAUDE.md`, with the `AGENTS.md` it imports, before the repository's own |
| A clone inside a workspace that does not ignore `.ropeshift/worktrees/` | `<worktree directory>/<project>/<repository>/<lane>` | Nothing of the workspace's |
| The workspace repository's own lane | `<worktree directory>/<project>/<workspace>/<lane>` | Nothing more: its repository's instructions are the workspace's, loaded once |
| A clone outside every workspace, or of a project with no workspace | `<worktree directory>/<project>/<repository>/<lane>` | Nothing of the workspace's |
| Any lane under a root you set | Under that root | Any `CLAUDE.md` in the root's folders or above them |

Every lane also loads a `CLAUDE.md` in a folder above the worktree, such as one in your home folder, and your own `~/.claude/CLAUDE.md`. By default, a workspace that holds an `AGENTS.md` and no `CLAUDE.md` gives a lane in its `.ropeshift/worktrees/` folder nothing, because the repository's `CLAUDE.md` in the working directory stops Claude Code from reading `AGENTS.md` files directly. A rule that every change to a repository must follow belongs in that repository's own `AGENTS.md`, or in the contract, because a lane whose worktree is outside the workspace reads only those.

### Your own root

To put lane worktrees somewhere else, set the root in your own profile layer, `profile.toml` in the installation's directory ([where ropeshift keeps its files](cli.md#the-daemon)):

```toml
[worktree]
root = "/path/to/lanes/{project}/{repo}"
```

`{repo}` becomes the name of the repository's folder, and `{project}` the project's ID, so each repository's lanes stay apart. A relative root is read from the repository's folder. Write an absolute path: the root expands no `~` and no variable. A lane's own `--profile` layer cannot change the root. Each lane's branch is still `lane/<lane>` in its repository.

A root that is a registered clone, or lies inside one, is refused, whoever set it, with symbolic links followed and the clones of every project on the machine counted. The lane fails before any worktree exists, with the reason `worktree_inside_clone`. Needs you and `ropeshift work show` name the root, the clone, and the layer that set the root, and say to set `[worktree] root` in your profile to a folder outside every clone, or to remove it to use the built-in root. `ropeshift repository add` reports a clone whose root would lie inside it and registers it all the same, so you can fix the setting before the first lane. It judges the root as a lane's start does, with the project's workspace counted: a root under the workspace's ignored `.ropeshift/worktrees/` is not reported, and one under a folder the workspace does not ignore is, with the line to add.

The root applies to worktrees not yet made. A lane that already has a worktree of its own goes on in it at its next generation, wherever it is, so an upgrade from the old default, `../<repository>-lanes`, never moves a lane in flight, and the refusal does not stop one. A worktree is the lane's own when it stands in a folder named for the lane and is the one the lane's journal recorded; a worktree of a person's, or one another lane left, that holds the lane's branch is never taken, and the lane's start is refused as git refuses a branch two worktrees would hold. The worker notes in the daemon's log, once for each lane run, that the lane kept its worktree and where the root in force would put a new one, and when the worktree lies inside a clone, where a new worktree is refused and the kept one stays until the lane ends. New lanes, and lanes whose worktree was archived, take the root in force. The refusal checks every clone the worker keeps, the daemon's `--source` repositories, and the repository the lane works in.

## The repository file format

Plan files are Markdown with TOML front matter between `+++` lines. The front matter says what kind of file it is and gives it a stable ID, and the body is your prose.

### What intake reads

Intake reads each repository at the commit its ref points to, the default branch unless the daemon's `--ref` names another. A file is a plan file when all of these hold:

- It is committed. An edit nobody committed is never read.
- Its name ends in `.md`, in lowercase.
- Its first line is exactly `+++`, and its front matter ends at the next `+++` line.
- Its `kind` is one of the kinds below.

A Markdown file with no front matter is an ordinary document: `ropeshift document list` lists it, and intake records no plan record from it. A file whose front matter does not parse, has an unknown kind, or names something the plan does not hold is a problem that `ropeshift source list` reports with the reason, and intake records nothing from it.

### Kinds

| `kind` | Holds | Key fields beyond `id` and `title` |
|--------|-------|------------------------------------|
| `goal` | A goal and how success is measured | `success_measures` |
| `plan` | A plan, the documents it holds, and its draft decisions | `goal`, `documents`, `[[decision]]`, `[[left_out]]` |
| `requirements` | Product requirements | `plan`, and a `[[requirement]]` table for each, with `id`, `text`, `status`, and `evidence` |
| `spec` | A technical spec | `plan`, `requirements` |
| `architecture` | An architecture document | `plan`, `requirements` |
| `experience-design` | An experience design | `plan`, `requirements`, `assets` |
| `milestone` | A milestone | `plan`, `priority`, `depends_on`, `existing`, `[[exit_criteria]]` |
| `decision` | A decision record, such as an architecture decision record | `status` (`proposed`, `accepted`, `superseded`, or `rejected`; `proposed` when absent), `plan`, `requirements` |
| `contract` | A contract, its criteria, and its work orders | `plan`, `milestone`, `shared`, `uses`, `documents`, `reads`, `existing`, `[[criteria]]`, `[[work_orders]]` |
| `repositories` | The manifest of the project's repositories | `plan`, and a `[[repository]]` table for each, with `name`, `remote`, `path`, and `description` |

### IDs and versions

- Every file has a stable `id`. Keep it when you rename or move the file, and the record follows.
- The `id` of a `plan` or `contract` file is a plain ID: 1 to 64 ASCII letters, digits, `-`, and `_`, such as `payments-v2`. A file with an `id` such as `2026.q4` or `payments v2` is a problem that `ropeshift source list` reports with the file, the ID, and this rule, and intake records nothing from it.
- One document cites another as `<kind>:<id>`, such as `spec:accounts-api`. A plan lists every document it holds in `documents`.
- Never write a version number. Each committed change to a file is a new version, numbered by the ledger, and an approval binds to the exact content it read.
- A requirement's ID is yours, such as `ACC-3`, and each criterion of a contract names the requirements it serves.

### Decisions

A decision record you keep in your repository, such as an architecture decision record, is a file of kind `decision`. Its `status` is `proposed`, `accepted`, `superseded`, or `rejected`, and `proposed` when the file states none; any other value makes the file a problem. It may name no plan, because a record often predates any plan. Each committed change to the file is a new version, so a change of status is a version, and a version keeps the status it had.

```markdown
+++
kind = "decision"
id = "adr-0007"
title = "Bound every call with a retry budget"
status = "accepted"
+++

The context, the decision, and its consequences, in prose.
```

A contract cites a decision record as `decision:adr-0007`, and a plan may list it in `documents`. The contract is pinned to the version at its commit, so a record that is superseded later changes nothing a lane already read, and the lane's context pack holds that version with its status. `ropeshift document recorded decision:adr-0007 --json` lists every version with its status, and for a record that names a plan, the Documents view shows the status beside each version.

A plan also carries the answers you gave its planning session as draft decisions, in its `[[decision]]` tables, and approving the plan records each one in the ledger as your decision. Decisions made later, by you or an orchestrator, are recorded in the ledger directly, and `ropeshift decision list` finds them. A decision record in another format stays in your repository as prose, and lanes can read it.

### A minimal contract

One file is enough for a first work order. A contract that names no plan gets a plan of its own:

```markdown
+++
kind = "contract"
id = "health-endpoint"
title = "Add a health endpoint"

[[criteria]]
id = "c1"
text = "GET /healthz returns 200 and the build version"

[[work_orders]]
id = "health-endpoint-order"
title = "Add the health endpoint"
repo = "."
size = "small"
+++

The load balancer needs an endpoint that says the service is up, and which build it runs.
```

`repo = "."` means the repository that holds the file. In a project of several repositories, `repo` names the repository a work order changes, by its name in the project. Every planning session is shown an example of each kind, and writes these files for you.

### A contract across repositories

In a project of several repositories, the contracts live in the planning repository, and each work order names the repository it changes by its name in the project. A design decision written down in the workspace, which the web client then implements, looks like this:

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
- `uses = ["accounts-api"]` builds on the shared contract `accounts-api`, which is marked `shared = true` and whose work order has `repo = "acme-api"`. Every work order here waits for that contract's work, so the API lands first ([projects of several repositories](how-it-works.md#projects-of-several-repositories)).

## What to commit, and what not

| Commit | Do not commit |
|--------|---------------|
| The plan files under `plan/` | The folder of clones, which `.gitignore` excludes |
| `AGENTS.md`, `CLAUDE.md`, the `Makefile`, and `.gitignore` | Ropeshift's state: it lives in the installation's directory, never in a repository |
| Your own documents | Lane worktrees, which live under the worktree root or in the workspace's ignored `.ropeshift/worktrees/` |
| A repository's `.ropeshift/profile.toml`, when you use one | A lane's context pack: it sits in a folder of the lane's worktree that git does not see |

## What ropeshift owns, and what stays yours

| Ropeshift's ledger holds | Your repositories hold |
|--------------------------|------------------------|
| Each work order's state, claims, and lanes | The plan of record, as files |
| Reviews, verdicts, approvals, and merges, with who did each | The code, and every change once it merges |
| Questions, and the decisions recorded through ropeshift | Your own design notes and decision records |
| Policies, the agent roster, and the audit | Your build, your tests, your commit hooks, and your CI |
| Measures and delivery records | Status notes and anything else in your own format |

The plan is reviewable as a change like any other, and the ledger records what happened to it. A change to an approved plan's files is a new plan version, which you approve.

## Intake settings for one installation

The daemon's `--ref` and `--dir` flags apply to every repository it reads, in every project of the installation:

- `--ref <branch>` reads that branch instead of each default branch. Use it only when every repository has that branch. A team on a long-lived branch usually keeps the default ref and sets each repository's base branch instead, so lanes start from that branch ([where a lane starts](onboarding.md#where-a-lane-starts)).
- `--dir <folder>` reads only that folder of each repository. With a named planning repository you seldom need it, because plan files are read only from there. If you set it, every repository's plan files and documents must sit under that folder, and the planning agent must be told so in the instructions file, because nothing else tells it.

## Next steps

- Create a project and register its repositories: [set up your projects](set-up-projects.md).
- How work spans several repositories: [projects of several repositories](how-it-works.md#projects-of-several-repositories).
- Bring in a project that already exists: [adopt an existing project](adopt.md).
