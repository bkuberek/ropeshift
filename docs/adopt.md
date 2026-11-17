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
- Lane worktrees go outside every clone by default, so a folder of clones needs no root of its own ([where lane worktrees go](project-structure.md#where-lane-worktrees-go)).

## In the web UI

### 1. Find the clones

In the sidebar, under Installation, select Projects, then Import an existing project. Repositories, the project switcher, and the command palette open the same page. Its four steps are Worker and folder, Dry run, Planning repository, and Adopt session.

Under Worker and folder:

- Worker is the machine whose disk holds the clones.
- Folder is an absolute path on that machine: one repository, or a folder of clones.
- Depth says how many folder levels below it to look for clones, and 1 looks at the folder's children.
- Project is A new project, named after the folder in New project's name until you change it, or a project you already have.

![Import an existing project, on Worker and folder, with the folder moorings, a depth of 1, and a new project named moorings](assets/screenshots/adopt-01-light.png#gh-light-mode-only)
![Import an existing project, on Worker and folder, with the folder moorings, a depth of 1, and a new project named moorings](assets/screenshots/adopt-01-dark.png#gh-dark-mode-only)

### 2. Read the dry run

Select Run dry run. A new project is created at this step, with nothing in it, because a dry run reads a project. The Dry run lists each clone under the folder with its identity, and records nothing else. Next, beside it, says how many repositories registering would add. Change returns to the first step.

![The dry run, listing moorings-api, moorings-docs, and moorings-web with their remotes](assets/screenshots/adopt-02-light.png#gh-light-mode-only)
![The dry run, listing moorings-api, moorings-docs, and moorings-web with their remotes](assets/screenshots/adopt-02-dark.png#gh-dark-mode-only)

Select Continue.

### 3. Choose where the plan lives

Under Planning repository, choose the repository that holds the plan, here `moorings-docs`. The adopt session writes the plan, and the documents it onboards, to this repository, on a branch of its own.

![Planning repository, with moorings-docs chosen among the three clones](assets/screenshots/adopt-03-light.png#gh-light-mode-only)
![Planning repository, with moorings-docs chosen among the three clones](assets/screenshots/adopt-03-dark.png#gh-dark-mode-only)

Select Continue.

### 4. Start the adopt session

Select Start adopt session. It registers the repositories in the project with the safe settings, names the planning repository, writes the inventory, and starts the session's lane. The session's Planning page opens, as for any planning session, and its first questions appear in the Outline.

![The Planning page of the adopt session, with its question about the branches in flight in the outline](assets/screenshots/adopt-04-light.png#gh-light-mode-only)
![The Planning page of the adopt session, with its question about the branches in flight in the outline](assets/screenshots/adopt-04-dark.png#gh-dark-mode-only)

### 5. Answer its questions

The session asks in Needs you, as [plan your first change](plan-your-first-change.md#4-answer-its-questions-in-needs-you) shows: here, which branches in flight the plan keeps. Choose an answer, and select Send this answer. What you type into the session becomes a draft decision in the plan, as for any planning session.

### 6. Read the grouped plan approval

When the session has finished, Needs you lists its plan approval. Select it, then Open the full plan approval. Beside the difference from the approved version, What the adoption does groups the plan:

| Group | What it holds |
|-------|---------------|
| What exists | Each `met` requirement and `existing` milestone or contract, with its evidence |
| What continues | Each work order that finishes a branch in flight |
| What is new | The remaining work |
| What is left out | Each branch you did not keep, with your reason |
| The adoption branch, as one change | Every file the adoption branch added, changed, moved, or deleted |

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

Open pull requests and issues are not read yet, and the inventory says so.

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
- A branch you leave out is recorded in the plan's `[[left_out]]` tables with your reason, so the approval shows it.

## A workspace with an inner folder of clones

Import takes one folder, and a folder that is itself a clone registers that clone and does not look inside it. For a workspace whose code repositories are clones in a folder inside it, as [project structure](project-structure.md) recommends, register the inner folder first: create the project, and in its Repositories, select Register repositories and choose A folder of clones. Then import the workspace into that project.

## What changes, and what does not

| Changes | Does not change |
|---------|-----------------|
| The planning repository gains one branch with the onboarded documents and the plan | Your default branch, until that branch merges |
| The project and its repositories are registered, with the safe settings | Any other repository: the session only reads them |
| Kept branches get work orders that continue them | The branches themselves, which ropeshift never moves |
| Your documents gain front matter | Their prose, their IDs, and their history |

Adopting does not move files between repositories. To use the layout in [project structure](project-structure.md), set up the workspace and its clones first, then adopt.

`ropeshift adopt` does the same from a terminal, as [the command line](cli.md) describes.

## Next

Watch work run, the next guide in [the README's documentation](../README.md#documentation), starts the plan's work. [Your first plan, end to end](first-plan.md#4-let-the-work-run) covers the same steps.
