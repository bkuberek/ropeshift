# Adopt an existing project

Most projects already have code, some documents, and a few branches in flight. Adopting brings such a project into ropeshift without rewriting it: an adopt session reads what is there, onboards your documents into ropeshift's format, marks what the code already does, and plans what remains. You review the result as one plan and one branch, and your default branch does not change until you decide.

For a project that already has code, this guide takes the place of [set up your projects](set-up-projects.md) and [plan your first change](plan-your-first-change.md), the two guides before it. It follows Moorings, which books a harbor's moorings for visiting boats, with two code repositories, `moorings-api` and `moorings-web`, and its documents in `moorings-docs`. Put your own names in their place.

## What adopting does

1. It registers the project's repositories, creating the project when none holds them.
2. It writes an inventory of every repository, with no model involved.
3. It starts an adopt session: a planning session whose brief is built for existing code. The session asks you what only you can answer.
4. It ends in one plan approval. The session's branch holds every file it added, changed, or moved, and merges only at the merge authority you chose.

Nothing reaches a default branch before you approve the plan and the adoption branch merges. If you decline, the branch is yours to delete, and nothing else changed.

## Before you adopt

- Finish [get started with the web UI](getting-started.md): the service runs, and you are signed in to the web UI.
- Clone each repository with git. Ropeshift clones nothing: it registers the clones already on the machine the daemon runs on. Here they sit in one folder:

```text
moorings/
├── moorings-api/    code, with the branch feature/cancel-booking in flight
├── moorings-docs/   the documents, where the plan will live
└── moorings-web/    code, with the branch spike/map-view in flight
```

- Decide which repository holds the plan. Adopting writes only there. If none of your repositories should, create one first, such as the workspace repository in [project structure](project-structure.md), and commit at least one file to it.
- A project that already has an approved plan is refused: plan its next work with a planning session instead, as [plan your first change](plan-your-first-change.md) shows.
- Lane worktrees never go in a clone. A clone in the workspace's ignored folder of clones gets its worktrees in the workspace's ignored `.ropeshift/worktrees/` folder, and every other clone gets them outside every clone, so a folder of clones needs no root of its own ([where lane worktrees go](project-structure.md#where-lane-worktrees-go)). The detection never searches a `.ropeshift` folder, so a lane worktree is never found as a clone to adopt, in a dry run or otherwise.

## In the web UI

### 1. Find the clones

In the sidebar, under Installation, select Projects, then Import an existing project. Repositories, the project switcher, and the command palette, where it is Adopt a project, open the same page. Its four steps are Worker and folders, Dry run, Planning repository, and Adopt session.

Under Worker and folders:

- Worker is the machine whose disk holds the clones.
- Folders are absolute paths on that machine, one to a line. The first is the clone you adopt from, the top of a git clone with a commit, as the repository `ropeshift adopt` runs in is. Each other line is a clone, or a folder of clones. Here the first is `moorings-docs`, and the second is the folder `moorings`, which holds all three.
- Depth says how many folder levels below each folder after the first to look for clones, and 1 looks at the folder's children. It also covers the folders the first clone ignores.
- Project is A new project, named after the first folder in New project's name until you change it, or a project you already have.

![Import an existing project, on Worker and folders, with the folders moorings-docs and moorings, a depth of 1, and a new project named moorings-docs](assets/screenshots/adopt-01-light.png#gh-light-mode-only)
![Import an existing project, on Worker and folders, with the folders moorings-docs and moorings, a depth of 1, and a new project named moorings-docs](assets/screenshots/adopt-01-dark.png#gh-dark-mode-only)

### 2. Read the dry run

Select Run dry run. It reads the plan, which [the dry run of the command line](cli-for-power-users.md#the-dry-run-of-adopt) prints, and records nothing: no project is created yet. The Dry run lists the layout, the shape, where the plan lives, and each clone with what it holds and what registering would do with it. Next, beside it, says how many repositories registering would add. Change returns to the first step.

![The dry run, listing moorings-api, moorings-docs, and moorings-web with their remotes](assets/screenshots/adopt-02-light.png#gh-light-mode-only)
![The dry run, listing moorings-api, moorings-docs, and moorings-web with their remotes](assets/screenshots/adopt-02-dark.png#gh-dark-mode-only)

When the plan offers a restructuring, which every shape but `single` always does and `single` does when the repository holds no instruction file, the Restructure choice below the list names everything it would do on the adoption branch: each file added, the files the layout lacks, each path taken from the design repository, each collision with the version kept, each move and each step for a code repository as the commands you run yourself, and a check box for each code tree of the design repository. Checking a tree leaves it out of a consolidation and reads the plan again, so its digest changes. Nothing restructures unless you check Restructure the planning repository as listed.

The page leaves two choices to the command line: `--design` names the design repository when the layout leaves it open, and `--planning` names the repository that holds the plan, which a restructuring needs settled. When the layout settles no repository for the plan, Restructure is off, and the next step chooses the repository for an adoption without one.

Select Continue.

### 3. Choose where the plan lives

Under Planning repository, choose the repository that holds the plan, here `moorings-docs`. The adopt session writes the plan, and the documents it onboards, to this repository, on a branch of its own.

![Planning repository, with moorings-docs chosen among the three clones](assets/screenshots/adopt-03-light.png#gh-light-mode-only)
![Planning repository, with moorings-docs chosen among the three clones](assets/screenshots/adopt-03-dark.png#gh-dark-mode-only)

Select Continue.

### 4. Start the adopt session

Select Start adopt session. It reads the plan again: a plan that differs from the one you read, because a clone appeared or went away in between, starts nothing, and the page shows the refusal and the new plan, with Restructure unchecked, to read again. Otherwise it creates the project when it is new, registers the repositories in the project with the safe settings, names the planning repository, writes the inventory, and starts the session's lane. With Restructure checked, the adopt work carries the plan's digest and your choices, as `--restructure` sends them. The session's Planning page opens, as for any planning session, and its first questions appear in the Outline.

![The Planning page of the adopt session, with its question about the branches in flight in the outline](assets/screenshots/adopt-04-light.png#gh-light-mode-only)
![The Planning page of the adopt session, with its question about the branches in flight in the outline](assets/screenshots/adopt-04-dark.png#gh-dark-mode-only)

### 5. Answer its questions

The session asks in Needs you, as [plan your first change](plan-your-first-change.md#4-answer-its-questions-in-needs-you) shows: here, which branches in flight the plan keeps. Choose an answer, and select Send this answer. What you type into the session becomes a draft decision in the plan, as for any planning session.

### 6. Read the grouped plan approval

When the session has finished, Needs you lists its plan approval. Select it, then Open the full plan approval. Below the difference from the approved version, What the adoption does groups the plan:

| Group | What it holds |
|-------|---------------|
| What exists | Each `met` requirement and `existing` milestone or contract, with its evidence |
| What continues | Each work order that finishes a branch in flight |
| What is new | The remaining work |
| What is left out | Each branch you did not keep, with your reason |
| The adoption branch, as one change | Every file the adoption branch added, changed, moved, or deleted |
| What the consolidation imports | In a [consolidation](#consolidating-a-design-repository), what approving does beyond the plan |

![The plan approval of the adoption, with What exists, What continues, What is new, and What is left out](assets/screenshots/adopt-05-light.png#gh-light-mode-only)
![The plan approval of the adoption, with What exists, What continues, What is new, and What is left out](assets/screenshots/adopt-05-dark.png#gh-dark-mode-only)

The plan's card in Needs you shows the same groups, with the files under What the branch changes.

### 7. Approve the plan

Select Approve version 1. Approving records your answers as decisions, accepts the session's work, and records the plan at the approved commit, so its work orders exist before any merge. To decline instead, select Decline…, and give the reason. The session reads it, and writes a new version when it can meet it.

The adoption branch then merges like any planning branch: at `person_merges`, the default, you merge it yourself, and at `person_approves` or `review_approves` ropeshift merges it at the approved commit ([a planning branch](onboarding.md#a-planning-branch)).

## The inventory

Before the session's lane starts, the worker writes an inventory of every repository of the project into the lane's context folder. No model writes it, so the same commits give the same inventory:

- Each repository's remote, default branch, and its tip.
- Every Markdown file, with its front matter format and headings.
- The files already in ropeshift's format, and whether intake reads them cleanly.
- Every branch the base does not hold: where it left the base, and how far ahead and behind it is.
- Every open pull request, by number, at most 200 of the newest: its number, title, head and base branch, address, whether it is a draft, whether its branch lives in a fork, and when it last changed. A title is clipped to 256 characters, without control, direction-changing, or zero-width characters, because people outside the project write it. When the forge holds more, the inventory says so.

Open pull requests are read for a repository whose remote names a repository on a forge, when this worker's `gh` sign-in for that forge is healthy. For any other repository the inventory records them as omitted with the reason: its remote names no forge repository, this worker has no forge adapter, the sign-in is signed out or missing, or the forge refused the listing. The pull requests are what the forge showed when the inventory was written, so the same commits give the same file only while the open pull requests are the same. Open issues are not read yet, and the inventory says so.

## The workspace files

With `--restructure`, in every shape but `consolidate`, the worker adds files to the planning repository on the adoption branch before the adopt session starts, so the session's branch holds them from its first prompt. It acts after the lane's worktree exists and before the inventory is written, in two steps.

1. **It reads the layout again, as the plan did.** The adopt work kept the request the plan was read with (the clone adopt ran in, the paths, the depth, and the names `--planning` and `--design` gave) and the digest of the plan. The worker plans from that request again and compares digests. When they differ, the work fails with the reason `layout_changed`, and its outcome names the parts of the plan that differ, from `layout`, `shape`, `planning`, `design`, `repositories`, `files`, `takes`, `collisions`, `code_trees`, and `moves`, with both digests. Nothing is written. Run `ropeshift adopt --restructure` again to plan from the layout as it is. A restructuring that was kept without its request and its parts fails with the reason `restructure_incomplete` instead, naming what it lacks, because the layout cannot be read again without them and did not change. Its detail tells you to run `ropeshift adopt --dry-run` again and restart with `--restructure`.
2. **It commits the files the plan lists.** One commit on the adoption branch, `chore: add the workspace files`, holds the files below. Each is added only when the repository lacks it: an existing file is never changed, and a line is never added twice.

| File | What the commit adds |
|------|----------------------|
| `plan/repositories.md` | The manifest: a file of kind `repositories` whose `id` is the project's name and which names no plan, with a table for each repository holding its name, remote, and path relative to the planning clone, and an empty description. Each path is where the clone sits once the moves below are made |
| `.gitignore` | When the repository has one: the line `repos/`, when clones sit beside the planning repository, because `repos/` is the folder the moves lead into, and the line `/.ropeshift/worktrees/`, when git does not ignore that folder in the planning clone. Ropeshift asks git, so a rule in a global ignore file or in the clone's own exclude file counts, and a line that only names the folder does not. When it has none, the file from [the template](#the-files-a-layout-lacks) |
| `Makefile`, `.gitignore` when absent, `AGENTS.md`, `CLAUDE.md` | The files a layout lacks, below |

A crash between the commit and its record is safe: a restart looks for the commit by its subject on the branch, and finds it or finds nothing, and the files are never committed twice. Once the commit is there the layout is not compared again, because you may have made the moves since.

The manifest reads back with the rest of the format. The adopt session is told the commit and the files it holds, and that the plan lists the manifest in its `documents` as `repositories:<project>`, so the plan holds it and approving the plan registers each repository it names that the project lacks.

### The files a layout lacks

The dry run lists, under the heading `Files the layout lacks`, each file of the proposed shape that the planning repository does not hold, against the tree it holds once any consolidation is imported. Each is written from the template that `ropeshift new` writes it from, over the project's name and the repositories found, and an existing file is never changed:

| File | Template | Shapes | Written when |
|------|----------|--------|--------------|
| `AGENTS.md` | `monorepo/agents.md` for `single`, `workspace/agents.md` for every other shape | All | The repository holds neither `AGENTS.md` nor `CLAUDE.md` |
| `CLAUDE.md` | `claude.md`, which imports `AGENTS.md` | All | The repository holds neither `AGENTS.md` nor `CLAUDE.md` |
| `Makefile` | `workspace/makefile`, with the targets `setup`, `fetch`, and `status` | All but `single` | The repository holds no `GNUmakefile`, `makefile`, or `Makefile` |
| `.gitignore` | `workspace/gitignore` | All but `single` | The repository holds no `.gitignore` |

A file the repository holds under another case, such as `agents.md` or `makefile`, counts as the file, so no file is written beside it.

The Makefile lists each repository as `name=remote`, or `name=remote=path` when its clone is not at `repos/<name>`, and `make setup` clones it there, making the folders above it. A repository goes into the Makefile when its name is made only of letters, digits, `.`, `_`, and `-`, and its path relative to the planning repository is made only of such segments joined by `/`, with no empty, `.`, or `..` segment, at any depth. Any other repository is left out of the Makefile, and the dry run and `--restructure` name each one with the reason. The manifest still lists it. `make register` registers the workspace and each carried repository's clone by its own path, so a nested clone is registered and a clone that is not listed is not. A remote that a Makefile cannot carry safely is left empty, so `make setup` names the clone to make by hand. A local path remote is written as a `file://` URL.

A repository of the `single` shape that holds no instruction file is offered `--restructure` for these two files, and the commit holds nothing else. The adopt session's brief shows the starting `AGENTS.md` for the project whenever the planning repository holds no instruction file and the worker wrote none, and asks the session to turn it into the project's own from what it reads. With `--restructure` the worker writes the file, and the brief tells the session to edit the committed file and shows no template.

### Steps for code repositories

Ropeshift writes to no clone but the planning repository's. The dry run lists, under the heading `Steps for you`, each code repository whose clone holds neither `AGENTS.md` nor `CLAUDE.md`, with the command that writes the repository template to it:

```text
[ -e <clone>/AGENTS.md ] || ropeshift template show repository/agents.md > <clone>/AGENTS.md
```

The command writes nothing when the file is there, so it never truncates an uncommitted one. A clone the plan moves is named where the move leads, because you run the moves first. The planning repository, the design repository a consolidation imports, and a clone the registration refuses are not listed. `--restructure` prints the steps again when the session starts. Review the file and commit it in that repository yourself.

### The moves

For each repository registered beside the planning repository's clone, the plan lists two commands, which `--dry-run` prints and `--restructure` prints again when the session starts:

```text
mv <clone> <planning clone>/repos/<name>
ropeshift repository add <planning clone>/repos/<name>
```

They are yours to run. Ropeshift never moves, deletes, or writes to a clone other than through a lane worktree of the planning repository, so the worker lists the moves and runs none of them, and the session tells you about them and runs none either.

## Consolidating a design repository

With `--restructure`, in the `consolidate` shape, the worker imports the design repository's history into the workspace on the adoption branch. The shape is a workspace that holds no plan content, with its design repository in a folder the workspace ignores, such as `repos/`. The worker acts at the same point as for the workspace files, after the lane's worktree exists and before the inventory is written, and starts with the same check: it reads the layout again as the plan did and compares digests, and the work fails with `layout_changed` or `restructure_incomplete` as [the workspace files](#the-workspace-files) describes. The design repository's choices travel with the work: its clone, its first commit, and the code trees you left out.

In the lane's worktree of the workspace, the worker then:

1. Fetches the design repository's tip from its clone with `git fetch`, which reads the clone and writes nothing to it. The tip is the one the plan read: the remote's default branch when the clone has a remote, else its default branch.
2. Checks that the fetched history begins at the first commit the plan stored for the design repository, following first parents. When it begins elsewhere, the work fails with `layout_changed` and nothing is merged.
3. Merges it with `git merge --allow-unrelated-histories --no-ff --no-commit`, so the workspace is the first parent and the design repository the second, and each design file arrives at its own path at the root. No commit is rewritten.
4. Settles the paths both repositories hold, and removes each tree you left out.
5. Adds what the other restructurings add, each only when the merged tree lacks it: the manifest of the code folder with the design repository left out, the files [a layout lacks](#the-files-a-layout-lacks), and the `/.ropeshift/worktrees/` line when git does not ignore that folder in the workspace.
6. Commits all of it as one merge commit, `chore: import the <design> history into the workspace`, in the product's name, with no hook run.

Every design commit keeps its ID, so `git log` and `git blame` on a design file read through the merge with no `--follow`. The workspace's identity does not change: ropeshift identifies a repository by its first-parent root, which stays the workspace's own, and the merge gives its history a second root.

### Paths both repositories hold

| Path | What the merge commit keeps |
|------|-----------------------------|
| `.gitignore` | The workspace's lines, followed by each of the design repository's lines the workspace lacks, in order |
| Any other path both hold | The workspace's version. The design repository's version stays in the merge's second parent, which `git show <second parent>:<path>` reads |

The dry run lists each of these paths as a `collision` with the version kept, and each path the merge takes from the design repository as a `take`. A path the design repository holds beside a path the workspace holds as a folder, or the reverse, cannot be kept apart by keeping a version, and the work fails with the path named and the worktree as it was.

### Code trees you leave out

A code tree of the design repository comes with the history and stays in the workspace, unless you pass `--leave-out PATH` or clear its box in Import after the dry run. The merge commit then removes the tree, and it stays readable in the second parent and in the old repository. A tree left out never takes a file with it that the workspace held at a path inside it. Moving a code tree into a repository of its own is yours to do.

### What the session is told

The inventory of the planning repository is read at the merge commit, so the session onboards the documents the design repository brought. Its brief names the merge commit, its second parent, every colliding path with the version kept, every tree left out, and the design repository's instruction files, `AGENTS.md` and `CLAUDE.md`, at the second parent. It tells the session to merge those instructions into the workspace's own as one commit on its branch, keeping both sets of rules, and to ask you about each contradiction between them.

### The plan approval

The plan approval of a consolidation, in Needs you, on its full page, and in `ropeshift plan show`, lists under What the consolidation imports:

- The merge commit that imported the history, and its second parent, the design repository's tip, read from the adoption branch.
- Every path both repositories held, with the version the merge kept.
- Every code tree left out.
- When the project holds the old design repository as a registered repository, its removal, which is one of the approval's effects: approving removes it in the same transaction and keeps its record as history.
- At `person_merges`, that you merge the branch yourself with a merge commit, because only a merge commit keeps the design history.

A merge the control plane cannot read from the branch is said, with why, and the trees left out and the removal are listed all the same.

### A restart, and the clones

A crash at any point is safe. A restart looks for the merge commit on the branch by its subject and its two parents, and finds it or finds nothing. A merge begun and not committed is thrown away, with the files the effect wrote, and made again from the start. Once the merge commit is there the layout is not compared again, because you may have moved clones or committed to the design repository since, and the worker makes no second merge.

### After the branch merges

Once the ledger records the adoption branch's merge, whether ropeshift merged it or read your merge of its pull request from the forge, the daemon checks that the base branch contains the design repository's tip, the merge commit's second parent, with `git merge-base --is-ancestor` in the planning repository's checkout on its machine, after a fetch. When the base does not contain it, as after a squash or a rebase merge, Needs you lists the adoption's work as Design history missing, naming the branch, the base, the commit the base stood at, and the missing commit, and a notice with the reason `history_missing` reaches the channels you chose. Merge that commit into the base with a merge commit to bring the history back. The check runs again 10 minutes later, then after twice as long each time, up to once a day, and the item clears at the first check that finds the commit; a base found to contain it is never checked again. A branch at `person_merges` with review target `branch` merges with nothing on record, so it is not checked, and a group server's control plane does not run the check.

Ropeshift merges the adoption branch with a merge commit whatever the repository's merge method, at `person_approves` and `review_approves`, because a squash or rebase merge keeps the files and drops the design repository's commits: only a merge commit keeps the second parent. At `person_merges` you merge it yourself, so merge it with a merge commit: the plan approval and the pull request's description say so.

The design repository is not registered by adopt, and its clone is never written, archived, or given another remote: ropeshift never deletes, archives, or rewrites it. If the project already holds it as a registered repository, approving the plan removes it from the project in the approval's transaction, as `ropeshift repository remove` does, and keeps its record as history. While a work order that is not finished targets it, the approval is refused with that work order named: finish or cancel the work, then approve. The worker keeps its checkout of the removed repository until the next intake tick, which drops the checkout of every repository the project holds as removed before it reads any source, so that clone is offered to no lane, read by no intake, and listed by no report of the worker's checkouts. Archive the old repository on its forge yourself when you no longer need it, and delete the clone when you are done with it.

## The adopt session

The session is a planning session, so it runs in a pane, its questions are yours, and it survives a detach, a park, and a restart of the daemon. Its brief asks the agent to:

- **Onboard, not rewrite.** Add front matter to your existing requirements, specs, architecture notes, and decision records, keeping their prose and their IDs. Move a file only when the format needs it elsewhere, with `git mv`, so its history follows it. A decision record gets front matter of kind `decision` and a `status` of `proposed`, `accepted`, `superseded`, or `rejected`, which the session takes from the record's own status line, or `proposed` when the record states none. It needs no plan. Once its status is `accepted`, the record answers a lane's question like any decision you have recorded: the orchestrator finds it on the first rung of the question ladder, so check the status the session chose for each record before you merge the branch.
- **Ask about what belongs to someone else.** A file whose front matter belongs to another tool, a document whose meaning is unclear, and two documents that contradict each other are questions for you, not guesses.
- **Mark what is done**, with evidence.
- **Plan what remains**, with shared contracts first, as any plan does.
- **Ask which branches in flight to keep.**
- **Write nothing else.** The session commits only on its own branch of the planning repository, and reads every other repository through read-only worktrees.

A project's own rules help the session too: say in `AGENTS.md` or `CLAUDE.md` which document wins when two disagree, and where plan files go.

## Work that already exists

The session records what the code already does, so the plan does not ask for it again:

- A requirement the code already meets gets `status = "met"`, with `evidence` that cites files and tests as `<repository>:<path>`. It needs no criterion, and Traceability shows it as met by existing code.
- A milestone or contract the code already delivers gets `existing = true` and no work orders. Nothing that depends on it waits for it.

These are claims, and approving the plan is you saying they hold. The approval lists each one with its evidence, so you can check them.

## Branches in flight

For each branch the base does not hold, the session asks you whether to keep it:

- A kept branch becomes a work order with `continues = "<branch>"`. Its lane starts from the branch's tip, on a branch of its own, and its review reads the whole change from where the branch left the base. Ropeshift never moves, rewrites, or deletes the branch it continues.
- A kept open pull request is its head branch: that branch becomes a work order with `continues`, as a kept branch does. A pull request whose branch lives in a fork cannot be continued by branch name, so it goes in `[[left_out]]`.
- A branch you leave out is recorded in the plan's `[[left_out]]` tables with `ref = "branch:<name>"` and your reason, and a pull request you leave out with `ref = "pull:<number>"`. A ref for an issue is `issue:<id>`, such as `issue:PROJ-123`. The approval shows each one.

## A workspace with an inner folder of clones

Import reads the layout as `ropeshift adopt` does. For a workspace whose code repositories are clones in a folder it ignores, as [project structure](project-structure.md) recommends, write the workspace's own path as the first folder: the plan finds the clones in every folder the workspace ignores, down to the depth. A clone beside the workspace is found only when a later line names it, or names the folder that holds it.

## What changes, and what does not

| Changes | Does not change |
|---------|-----------------|
| The planning repository gains one branch with the onboarded documents and the plan | Your default branch, until that branch merges |
| With `--restructure`, that branch also holds one commit of the workspace files, or, in a consolidation, one merge commit that imports the design repository's history | Any clone: the moves are commands you run, and the design repository's clone is only read |
| The project and its repositories are registered, with the safe settings | Any other repository: the session only reads them |
| Kept branches get work orders that continue them | The branches themselves, which ropeshift never moves |
| Your documents gain front matter | Their prose, their IDs, and their history |

Adopting does not move clones between folders, and it brings files from one repository into another only in a consolidation, as a merge that keeps the design repository's history. To use the layout in [project structure](project-structure.md), run the moves a restructuring lists, or set up the workspace and its clones first, then adopt.

## Shapes

The layout the dry run reads has one of four shapes, which decides where the plan lives and what a restructuring offers:

| Shape | When | Planning repository | `--restructure` offers |
|-------|------|---------------------|------------------------|
| `single` | No clone found other than the one adopt runs in | It | The instruction files, when it holds none |
| `planned` | It holds plan content, and clones were found | It | The manifest, when no `repositories` file exists, the ignore line for the folder lane worktrees go in, and the files the layout lacks |
| `consolidate` | It holds no plan content, clones sit in a folder it ignores, and one of them is the design repository | It | Importing the design repository's history in one merge commit, with the manifest, the ignore line for the folder lane worktrees go in, and the files the merged tree lacks |
| `workspace_files` | Any other layout with clones | It when a folder it ignores holds clones; else the design repository when one was chosen and is another clone; else none is settled, and `--planning NAME` names it | The manifest, the ignore lines, and the files the layout lacks |

## On the command line

[The CLI for power users](cli-for-power-users.md#adopt-an-existing-project) gives the command for each step of this guide, with its flags and what its dry run prints.

## Next

Approving the plan creates its work orders. [Watch work run](watch-work-run.md), the next guide, starts them, and [Learn ropeshift](../README.md#learn-ropeshift) lists the guides.
