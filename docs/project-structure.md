# Project structure

Ropeshift reads your plan from git, so where your files live decides what it sees. This page recommends one layout that works for a project of one repository and grows to many, explains why each part is where it is, and describes the file format the plan is written in.

## The recommended layout

Keep one workspace repository that is also the project's planning repository. It holds the instructions every agent reads, the plan of record, and a Makefile that sets up the rest. Clone the code repositories into a folder inside it that git ignores. Keep lane worktrees somewhere else entirely.

```text
acme-workspace/                 the workspace: a git repository, and the planning repository
├── AGENTS.md                   the project and its rules, read by every agent
├── CLAUDE.md                   the same instructions for Claude Code, or an import of AGENTS.md
├── Makefile                    setup, fetch, and status for every repository
├── .gitignore                  ignores repos/
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
└── repos/                      git-ignored: each code repository, a clone of its own
    ├── acme-api/
    └── acme-web/

/path/to/lanes/                 lane worktrees, outside the workspace
├── acme-api/
└── acme-web/
```

The folder of clones can have any name, such as `repos/` or `development/`. The names `acme-workspace`, `acme-api`, and `acme-web` stand for your own.

A project of one repository needs none of the extra parts: that repository is its planning repository, and the plan can live in a `plan/` folder beside the code. Start there, and move to a workspace when a second repository joins.

## Why each part is where it is

| Part | Why |
|------|-----|
| The workspace is the planning repository | A project plans in one repository, its plan of record. Once you name it with `ropeshift repository set <name> --planning`, intake reads goals, plans, documents, milestones, contracts, and the manifest only from it, and reports a plan file found anywhere else, so a misplaced plan is seen and never read |
| `AGENTS.md` or `CLAUDE.md` | A planning session runs in a worktree of the planning repository, so its agent reads these instructions. Say what the project is, where plan files go (`plan/`), which document wins when two disagree, and which checks a change must pass |
| `Makefile` | Ropeshift clones nothing. A registered repository whose clone is missing holds its work as `no_checkout` until the clone is there. A `make setup` that clones each missing repository, a `make fetch`, and a `make status` make a fresh machine ready in one step |
| `repos/` is git-ignored | Intake reads only what git has committed, file by file, so the clones inside the workspace are never read as part of it. Each clone is a repository of its own, registered on its own |
| The manifest lists `repos/acme-api` | A manifest path is relative to the planning repository's clone, and may lead into a folder inside it, or beside it, such as `../acme-api`. It must stay inside the folder that holds the planning repository's clone ([projects of several repositories](multiple-repositories.md#5-keep-a-manifest-in-the-planning-repository)) |
| Lane worktrees are outside | See [where lane worktrees go](#where-lane-worktrees-go) |

## Where lane worktrees go

Each lane works in a git worktree of its own. By default, ropeshift puts it outside every clone, in the installation's worktree directory, at `<worktree directory>/<project>/<repository>/<lane>`:

| System | Worktree directory |
|--------|--------------------|
| Linux | `$XDG_DATA_HOME/ropeshift/worktrees`, or `~/.local/share/ropeshift/worktrees` when `XDG_DATA_HOME` is unset |
| macOS | `~/.ropeshift/worktrees`, a path with no space, which some build tools mishandle |
| With `ROPESHIFT_STATE_DIR` set | `<state directory>/worktrees`, so a second installation and every test keep their lanes apart from yours |

`ROPESHIFT_WORKTREE_DIR` replaces the worktree directory in every case. `<project>` is the project's ID, which a rename keeps, and `<repository>` is the name of the repository's folder, so two projects whose clones share a folder name never share a lane folder. A lane in `repos/acme-api` of a workspace therefore never lands in `repos/`, where scripts that loop over `repos/*` would find it, or inside the workspace, where an agent also reads the workspace's instruction files from a parent folder ([Claude Code memory](https://code.claude.com/docs/en/memory)).

To put lane worktrees somewhere else, set the root in your own profile layer, `profile.toml` in the installation's directory ([where ropeshift keeps its files](cli.md#the-daemon)):

```toml
[worktree]
root = "/path/to/lanes/{project}/{repo}"
```

`{repo}` becomes the name of the repository's folder, and `{project}` the project's ID, so each repository's lanes stay apart. A relative root is read from the repository's folder. Write an absolute path: the root expands no `~` and no variable. A lane's own `--profile` layer cannot change the root. Each lane's branch is still `lane/<lane>` in its repository.

A root that is a registered clone, or lies inside one, is refused, whoever set it, with symbolic links followed and the clones of every project on the machine counted. The lane fails before any worktree exists, with the reason `worktree_inside_clone`. Needs you and `ropeshift work show` name the root, the clone, and the layer that set the root, and say to set `[worktree] root` in your profile to a folder outside every clone, or to remove it to use the built-in root. `ropeshift repository add` reports a clone whose root would lie inside it and registers it all the same, so you can fix the setting before the first lane.

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
| `contract` | A contract, its criteria, and its work orders | `plan`, `milestone`, `shared`, `uses`, `documents`, `reads`, `existing`, `[[criteria]]`, `[[work_orders]]` |
| `repositories` | The manifest of the project's repositories | `plan`, and a `[[repository]]` table for each, with `name`, `remote`, `path`, and `description` |

### IDs and versions

- Every file has a stable `id`. Keep it when you rename or move the file, and the record follows.
- One document cites another as `<kind>:<id>`, such as `spec:accounts-api`. A plan lists every document it holds in `documents`.
- Never write a version number. Each committed change to a file is a new version, numbered by the ledger, and an approval binds to the exact content it read.
- A requirement's ID is yours, such as `ACC-3`, and each criterion of a contract names the requirements it serves.

### Decisions

There is no file kind for a decision. A plan carries the answers you gave its planning session as draft decisions, in its `[[decision]]` tables, and approving the plan records each one in the ledger as your decision. Decisions made later, by you or an orchestrator, are recorded in the ledger directly, and `ropeshift decision list` finds them. Decision records you keep in your own format stay in your repository as prose, and lanes can read them.

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

## What to commit, and what not

| Commit | Do not commit |
|--------|---------------|
| The plan files under `plan/` | The folder of clones, which `.gitignore` excludes |
| `AGENTS.md`, `CLAUDE.md`, the `Makefile`, and `.gitignore` | Ropeshift's state: it lives in the installation's directory, never in a repository |
| Your own documents | Lane worktrees, which live under the worktree root |
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

- `--ref <branch>` reads that branch instead of each default branch. Use it only when every repository has that branch. A team on a long-lived branch usually keeps the default ref and sets each repository's base branch instead, so lanes start from that branch ([set up a project](set-up-a-project.md#6-choose-the-branch-lanes-start-from)).
- `--dir <folder>` reads only that folder of each repository. With a named planning repository you seldom need it, because plan files are read only from there. If you set it, every repository's plan files and documents must sit under that folder, and the planning agent must be told so in the instructions file, because nothing else tells it.

## Next steps

- Set up a project of one repository: [set up a project](set-up-a-project.md).
- Grow to several repositories: [projects of several repositories](multiple-repositories.md).
- Bring in a project that already exists: [adopt an existing project](adopt.md).
