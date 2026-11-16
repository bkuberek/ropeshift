# Adopt an existing project

Most projects already have code, some documents, and a few branches in flight. Adopting brings such a project into ropeshift without rewriting it: an adopt session reads what is there, onboards your documents into ropeshift's format, marks what the code already does, and plans what remains. You review the result as one plan and one branch, and your default branch does not change until you decide.

## What adopting does

1. It registers the project's repositories, creating the project when none holds them.
2. It writes an inventory of every repository, with no model involved.
3. It starts an adopt session: a planning session whose brief is built for existing code. The session asks you what only you can answer.
4. It ends in one plan approval. The session's branch holds every file it added, changed, or moved, and merges only at the merge authority you chose.

Nothing reaches a default branch before you approve the plan and the adoption branch merges. If you decline, the branch is yours to delete, and nothing else changed.

## Before you adopt

- Finish steps 1 to 3 of [set up a project](set-up-a-project.md): ropeshift installed, `ropeshift init` run, and the daemon running.
- Lane worktrees go outside every clone by default, so a folder of clones needs no root of its own ([where lane worktrees go](project-structure.md#where-lane-worktrees-go)).
- A project that already has an approved plan is refused: plan its next work with `ropeshift plan start` instead.
- For a project of several repositories, decide which repository holds the plan. Adopting writes only there. If none of your repositories should, create one first, such as the workspace repository in [project structure](project-structure.md), and commit at least one file to it.

## 1. Start the adopt session

In the repository, with the daemon running:

```sh
cd /path/to/acme-app
ropeshift adopt
```

1. It uses the project that holds the repository, or creates one named after the repository's folder, or the one `--project` names.
2. It registers the repository, as `ropeshift repository add` does, and prints what became of it.
3. It writes the inventory, starts the session, and puts your terminal in it, as `ropeshift plan start` does.

Detach with `C-b d`, which leaves the session running, and rejoin with `ropeshift lane attach <lane>`. With `--no-attach`, it starts the session, prints the command that joins it, and the session's questions reach Needs you.

In the web UI, Projects and Repositories both have Import an existing project:

1. Under Worker and folder, choose the Worker whose disk holds the clones, enter the Folder as an absolute path, the Depth to look for clones, and the Project: A new project, named after the folder until you change it, or an existing one. Select Run dry run.
2. The Dry run lists each clone under the folder and what registering would do with it. It records nothing. Select Continue.
3. Under Planning repository, choose the repository that holds the plan, and select Continue.
4. Select Start adopt session. The session's planning pane opens.

![Import an existing project, on its first step, with the worker, folder, depth, and project fields](assets/screenshots/import-light.png#gh-light-mode-only)
![Import an existing project, on its first step, with the worker, folder, depth, and project fields](assets/screenshots/import-dark.png#gh-dark-mode-only)

## 2. Adopt several repositories at once

Name the folders of clones after the command. Each path registers the clone at it, or every clone one level below it:

```sh
cd /path/to/acme-workspace
ropeshift adopt repos --planning acme-workspace
```

This registers `acme-workspace`, where the command runs, and every clone in `repos/`. A project of several repositories plans in the repository `--planning` names, or the one it already plans in. With neither, the command asks on the terminal which repository holds the plan. A clone another project holds is refused, and the rest go on.

The web UI's Import takes one folder. A folder that is itself a clone registers that clone and does not look inside it. For a workspace with its code repositories in an inner folder, register the inner folder first with Register repositories in Repositories, then import the workspace into that existing project.

## 3. The inventory

Before the session's lane starts, the worker writes an inventory of every repository of the project into the lane's context folder. No model writes it, so the same commits give the same inventory:

- Each repository's remote, default branch, and its tip.
- Every Markdown file, with its front matter format and headings.
- The files already in ropeshift's format, and whether intake reads them cleanly.
- Every branch the base does not hold: where it left the base, and how far ahead and behind it is.

Open pull requests and issues are not read yet, and the inventory says so.

## 4. The adopt session

The session is a planning session, so it runs in a pane, its questions are yours, your answers become draft decisions in the plan, and it survives a detach, a park, and a restart of the daemon. Its brief asks the agent to:

- **Onboard, not rewrite.** Add front matter to your existing requirements, specs, architecture notes, and decision records, keeping their prose and their IDs. Move a file only when the format needs it elsewhere, with `git mv`, so its history follows it.
- **Ask about what belongs to someone else.** A file whose front matter belongs to another tool, a document whose meaning is unclear, and two documents that contradict each other are questions for you, not guesses.
- **Mark what is done**, with evidence.
- **Plan what remains**, with shared contracts first, as any plan does.
- **Ask which in-flight branches to keep.**
- **Write nothing else.** The session commits only on its own branch of the planning repository, and reads every other repository through read-only worktrees.

A project's own rules help the session too: say in `AGENTS.md` or `CLAUDE.md` which document wins when two disagree, and where plan files go.

## 5. Work that already exists

The session records what the code already does, so the plan does not ask for it again:

- A requirement the code already meets gets `status = "met"`, with `evidence` that cites files and tests as `<repository>:<path>`. It needs no criterion, and Traceability shows it as met by existing code.
- A milestone or contract the code already delivers gets `existing = true` and no work orders. Nothing that depends on it waits for it.

These are claims, and approving the plan is you saying they hold. The approval lists each one with its evidence, so you can check them.

## 6. Branches in flight

For each branch the base does not hold, the session asks you whether to keep it:

- A kept branch becomes a work order with `continues = "<branch>"`. Its lane starts from the branch's tip, on a branch of its own, and its review reads the whole change from where the branch left the base. Ropeshift never moves, rewrites, or deletes the branch it continues.
- A branch you leave out is recorded in the plan's `[[left_out]]` tables with your reason, so the approval shows it.

## 7. Review the plan and the branch

When the session ends, the plan waits for you:

```sh
ropeshift approval list
ropeshift approval show <approval-id>
```

`approval show` prints the plan grouped, which is what you read before you approve:

| Group | What it holds |
|-------|---------------|
| What exists | Each `met` requirement and `existing` milestone or contract, with its evidence |
| What continues | Each work order that finishes an in-flight branch |
| What is new | The remaining work |
| What is left out | Each branch you did not keep, with your reason |
| Files | Every file the adoption branch added, changed, moved, or deleted |

The plan's card in Needs you shows the same groups, and the plan approval page shows them in full. To approve, use the digest `approval show` printed:

```sh
ropeshift approval approve <approval-id> --digest <digest>
```

Approving records your answers as decisions, accepts the session's work, and records the plan at the approved commit, so its work orders exist before any merge. To decline, run `ropeshift approval deny <approval-id> --digest <digest> --reason <text>`, or select Decline on the plan approval page. The session reads your reason, and writes a new version when it can meet it.

The adoption branch then merges like any planning branch: at `person_merges`, the default, you merge it yourself, and at `person_approves` or `review_approves` ropeshift merges it at the approved commit ([a planning branch](onboarding.md#a-planning-branch)). `ropeshift work acceptance <work-order>` names the branch and its commit.

## What changes, and what does not

| Changes | Does not change |
|---------|-----------------|
| The planning repository gains one branch with the onboarded documents and the plan | Your default branch, until that branch merges |
| The project and its repositories are registered, with the safe settings | Any other repository: the session only reads them |
| Kept branches get work orders that continue them | The branches themselves, which ropeshift never moves |
| Your documents gain front matter | Their prose, their IDs, and their history |

Adopting does not move files between repositories. To use the layout in [project structure](project-structure.md), set up the workspace and its clones first, then adopt.

## Next steps

- Run the plan's work: [set up a project](set-up-a-project.md#10-run-the-work).
- Plan the next piece of work with a planning session: [your first plan](first-plan.md).
- Work across the project's repositories: [projects of several repositories](multiple-repositories.md).
