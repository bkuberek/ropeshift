# Start a multi-repository project

This guide starts a project of several repositories from an empty folder. You write a workspace, which is the project's planning repository, let a planning session decide the code repositories and list them in the manifest, create each one, and approve the plan, which registers them. It follows Harbor, the project of a small port, with its workspace `harbor-workspace` and its code repositories `harbor-api` and `harbor-web`. Put your own names in their place, and your GitHub account or organization in place of `<owner>`.

When you already know the code repositories, `ropeshift new workspace` writes them with the workspace, each with `--new-repository <name>`, and lists them in the manifest ([start a project](project-structure.md#start-a-project)). This guide is for a project whose repositories come out of the plan.

## Before you start

- Finish [get started with the web UI](getting-started.md): the service runs, and you have a member's session. The registration commands ask for the passphrase on the terminal.
- Work on the machine the daemon's worker runs on. Approving a plan reads each repository's clone on that machine.
- To create the repositories on GitHub, sign in to the GitHub CLI on that machine ([create a new project's repository on GitHub](github.md#create-a-new-projects-repository-on-github)).

## The order that matters

Approving a plan version registers each repository its manifest names from the clone at its path, as the clones stand at that moment, and an approval registers only once. So create every code repository after the planning session writes the manifest and before you approve the version. A repository whose clone is missing when you approve stays unregistered, and [register it after the approval](#if-you-approved-first).

## Steps

### 1. Write the workspace

Write the workspace's starting files into a new folder, and name the project with `--name`:

```sh
ropeshift new workspace /srv/code/harbor-workspace --name harbor
```

```text
wrote   .gitignore
wrote   AGENTS.md
wrote   CLAUDE.md
wrote   CODEOWNERS
wrote   Makefile
wrote   SECURITY.md
wrote   plan/goal.md
wrote   plan/plan.md
wrote   plan/requirements.md

To register it, run these with a member's session:
  git -C /srv/code/harbor-workspace init
  git -C /srv/code/harbor-workspace --literal-pathspecs ls-files -z --cached --others --exclude-standard -- .gitignore AGENTS.md CLAUDE.md CODEOWNERS Makefile SECURITY.md plan/goal.md plan/plan.md plan/requirements.md | xargs -0 git -C /srv/code/harbor-workspace --literal-pathspecs add --
  git -C /srv/code/harbor-workspace commit -m "chore: start harbor"
  ropeshift project create harbor
  make -C /srv/code/harbor-workspace register
```

With no code repository named, the workspace has no manifest yet, and its `.gitignore` already ignores `repos/`, where the code repositories go. Run the printed commands in order. The git commands make the workspace's first commit, because a repository is registered by its first commit. `ropeshift project create harbor` creates the project, and `make register` registers the workspace and names it the planning repository, with `ropeshift repository set harbor-workspace --planning`. `--first-commit` makes the first commit for you, and `--create-remote github:<owner>` also creates the workspace's own repository on GitHub and pushes it.

In the web UI, on Projects, select Start a project from a template. Several repositories asks for at least one code repository, so a workspace with none starts on the command line. When you know one of them, enter it there, and add the rest with the steps below. Create project harbor and register opens the Register dialog on the project's Repositories with the folder filled, and [name the planning repository](set-up-projects.md#8-name-the-planning-repository) marks it as the one the plan lives in.

### 2. Plan the repositories

Start a planning session in the workspace:

```sh
ropeshift plan start --goal "Show every berth and its sailings on the public pages" --project harbor
```

Tell the session which code repositories the project needs, and have it write the manifest, `plan/repositories.md`, with each one at `repos/<name>`. The session's first prompt shows it the manifest's format. Have the plan list the manifest in its `documents` as `repositories:harbor`, so approving the plan covers it:

```markdown
+++
kind = "repositories"
id = "harbor"
title = "Harbor repositories"
plan = "harbor-v1"

[[repository]]
name = "harbor-api"
remote = "https://github.com/<owner>/harbor-api.git"
path = "repos/harbor-api"
description = "The berths and the sailings, and the API the port's pages call"

[[repository]]
name = "harbor-web"
remote = "https://github.com/<owner>/harbor-web.git"
path = "repos/harbor-web"
description = "The port's public pages"
+++

The code repositories of the harbor project. The planning repository, which holds this file, is not listed.
```

`remote` is optional. When the manifest names one, approval refuses a clone whose remote is another, compared by host and path, so `git@github.com:<owner>/harbor-api.git` matches `https://github.com/<owner>/harbor-api.git`. When the session has what it needs, it submits the plan as a version for your approval.

In the web UI, on the Overview, select Start planning session, and follow the session on the Planning page, as [plan your first change](plan-your-first-change.md) describes.

### 3. Read what approving would register

Find the plan's ID, then read the version that waits:

```sh
ropeshift plan list --project harbor
ropeshift plan show harbor-v1 --project harbor
```

Under the approval, `plan show` lists each repository the manifest names, read now on the daemon's worker's machine. Each line holds the outcome, the name, the path as the manifest writes it, where that path leads, the remote, and the reason when there is one, separated by tabs. No code repository exists yet, so each is refused, with this reason for `harbor-api`:

```text
harbor-api is not registered: no git clone is at /srv/code/harbor-workspace/repos/harbor-api, which does not exist
```

A folder that exists but holds no clone is refused with `no git clone is at <path>`. Reading the plan registers nothing. Do not approve the version yet.

In the web UI, in Needs you, select the plan approval. Its card lists Repositories approving registers, each marked Refused with the reason. Open the full plan approval shows the same list.

### 4. Create each code repository

Write each code repository at its manifest path, in the project, and create it on GitHub:

```sh
ropeshift new repository /srv/code/harbor-workspace/repos/harbor-api --project harbor --create-remote github:<owner>
ropeshift new repository /srv/code/harbor-workspace/repos/harbor-web --project harbor --create-remote github:<owner>
```

Each command writes the repository's starting files, makes its first commit, creates `<owner>/<name>` on GitHub, private unless you give `--public`, adds it as `origin`, and pushes. The repository on GitHub takes the folder's name. The workspace ignores `repos/`, so each folder becomes a repository of its own. Each command then prints `ropeshift repository add <path> --project harbor`, which registers the repository now. You can run it or leave it to the approval, which then lists the repository as already registered.

Add each repository to `REPOSITORIES` in the workspace's `Makefile`, as `name=remote`, so `make setup` clones it on another machine.

Read the plan again. Each line now says `would register`:

```sh
ropeshift plan show harbor-v1 --project harbor
```

In the web UI, on Projects, select Start a project from a template, then Add a code repository to a project planned elsewhere. Enter the Folder, `/srv/code/harbor-workspace/repos/harbor-api`, and the Project, `harbor`. Keep Make the first commit checked, choose Create on GitHub under Remote, and choose the Owner and the Visibility. Select Write files. Repeat it for `harbor-web`. [Start a project from a template](web-ui.md#start-a-project-from-a-template) describes every field.

### 5. Approve the version

```sh
ropeshift plan approve harbor-v1 --version 1 --project harbor
```

The command prints what approving would register, then approves the version and prints a `registered` line for each repository. Each takes the manifest's name and the safe settings: the review reads its branch on this machine, a change merges with a merge commit, and you merge each change yourself. `ropeshift repository list --project harbor` lists the workspace and both code repositories.

In the web UI, select Approve this plan on the card in Needs you, or Approve version 1 on the full plan approval.

## If you approved first

An approval registers once, and keeps what it found. Running `ropeshift plan approve` again on that version prints `Berths and sailings on the public pages version 1 is already approved` and registers nothing, and the approval still lists the repositories it refused. Create each missing repository as in step 4, then register it by hand:

```sh
ropeshift repository add /srv/code/harbor-workspace/repos/harbor-api /srv/code/harbor-workspace/repos/harbor-web --project harbor
```

`ropeshift repository add --manifest /srv/code/harbor-workspace/plan/repositories.md --project harbor` registers what a manifest file lists in the same way, from the clones at its paths. Or leave it to the plan: the next version of it you approve registers each repository whose clone is there by then.

In the web UI, on the project's Repositories, select Register repositories, choose A folder of clones, enter `/srv/code/harbor-workspace/repos` in Folder with a Depth of 1, and select Run dry run, then Register ([set up your projects](set-up-projects.md#4-choose-the-folder-of-clones)).

## Next

The approved plan's work orders are ready to run. [Watch work run](watch-work-run.md) starts them, and [project structure](project-structure.md#growing-a-project) shows how the project grows from here.
