# Settings and agents

Settings holds what a member changes in the web UI: the machines' caps, each worker's autonomy, the project's policies, and your own preferences. Agents holds the roster every session receives, and Members the people who sign in and the delegates who act for them. This guide finds a setting, changes a policy and restores an earlier version, reads the decision policy and the notifications, changes an agent and adds one, creates a delegate, backs up the installation, and names the settings only a file holds. Its images show the sample installation of [run several projects at once](several-projects.md), in its project Default. Put your own project in its place.

## Before you start

- Finish [set up your projects](set-up-projects.md). The policies govern what [watch work run](watch-work-run.md) and [review and merge](review-and-merge.md) show, so those guides help you choose them.

## How settings are kept

A policy is kept as versions. Each change records a new version, with who saved it and when, and what reads the policy reads the newest version from then on. An earlier version is never changed or deleted: restoring one records its settings as the newest. Only a member saves a version, so no orchestrator, lane, or delegate changes a policy. The agent roster is kept the same way, one line of versions for the installation and one for each project.

A machine's lane caps are kept in your own profile on that machine, as [run several projects at once](several-projects.md#4-set-the-machines-lane-caps) shows, and some settings are kept only in a file, which [the last section](#settings-only-a-file-holds) names.

## In the web UI

### 1. Find a setting

In the sidebar, select Settings. The sections stand in four groups:

| Group | Sections |
|-------|----------|
| Installation | Admission and lane caps, Autonomy defaults, Assistant, Orchestrator spend, Backup, and About |
| Workers | Autonomy and run windows |
| Project | Fleet, then the review, integration, decision, lane, recovery, delegation, and delegate policies, Notifications, Estimate policy, and Spend |
| You | Appearance and keyboard, and Change passphrase |

The Project group holds the sections of the project you are in, under its name, and shows only inside a project, where Settings opens on its Fleet ([pause, drain, or cap one project](several-projects.md#5-pause-drain-or-cap-one-project)). The line under each section's title says what it governs.

![Settings of Default, with its four groups and the project's Fleet](assets/screenshots/settings-01-light.png#gh-light-mode-only)
![Settings of Default, with its four groups and the project's Fleet](assets/screenshots/settings-01-dark.png#gh-dark-mode-only)

### 2. Change a policy

Select Recovery policy, which says what an orchestrator may do on its own when a lane stalls, trips, fails, or blocks. A section that holds a policy has two tabs, Settings and History, and the line above its fields names the version in force and who saved it. Until a member saves a version, a project runs on each policy's built-in version: the built-in recovery policy restarts a stalled lane, resets a tripped lane's breaker at most twice a day, retries failed work, replans blocked work, and gives each work order 3 attempts. [Recovery](authorization.md#recovery) explains each setting.

Change a field, here Breaker resets per day, from 2 to 1. The field is marked Changed, and the bar under the fields names each change, here `max_breaker_resets_per_day 2 → 1`. Discard drops the changes. A value the policy would refuse names its fix at the field, and Review change waits until it is fixed.

Select Review change. The dialog shows the policy's text as it is and as the next version would record it, and says that what reads the policy reads the new version from now on.

![Review the recovery policy change, with the breaker resets going from 2 to 1 in version 2](assets/screenshots/settings-02-light.png#gh-light-mode-only)
![Review the recovery policy change, with the breaker resets going from 2 to 1 in version 2](assets/screenshots/settings-02-dark.png#gh-dark-mode-only)

Select Save version 2. Keep editing closes the dialog and keeps your changes.

### 3. Restore an earlier version

Select the History tab. It lists every version, newest first, each with who saved it, when, and what it changed from the version before it. The newest reads In force, and each earlier one offers Revert.

![The History tab of the recovery policy, with version 2 in force and version 1, the built-in version, with Revert](assets/screenshots/settings-03-light.png#gh-light-mode-only)
![The History tab of the recovery policy, with version 2 in force and version 1, the built-in version, with Revert](assets/screenshots/settings-03-dark.png#gh-dark-mode-only)

Select Revert on version 1. The same review opens, titled Revert to version 1, and Save version 3 records version 1's settings as version 3. Version 2 stays in the history.

### 4. Choose what an orchestrator decides

Select Decision policy. An orchestrator takes a question up the ladder before it reaches you, and this policy bounds what it may settle on its own:

- Reversible decisions: `assume`, the one value the policy has. An orchestrator settles a reversible choice by recording an assumption, which a member can overturn later.
- One-way categories: a question in one of these categories always waits for you. The built-in version names `security`, `compliance`, and `data`, and the boxes list every category the project has.
- Assumptions per work order: once one work order holds this many assumptions, the next question about it waits for you. The built-in version allows 5.

![The decision policy of Default, with reversible decisions assumed, its one-way categories, and five assumptions per work order](assets/screenshots/settings-04-light.png#gh-light-mode-only)
![The decision policy of Default, with reversible decisions assumed, its one-way categories, and five assumptions per work order](assets/screenshots/settings-04-dark.png#gh-dark-mode-only)

The other policies of the Project group save, review, and restore the same way:

| Section | What it governs |
|---------|-----------------|
| Review policy | Says where the bound on review rounds is set: in a profile, a file this page does not change ([settings only a file holds](#settings-only-a-file-holds)) |
| Integration policy | Lists each repository's merge authority, where dependent work starts from, and its review target, with Edit record, which opens the repository's record |
| Lane policy | Whether a contract that changes, or a decision that lands, under a running lane tells the lane and lets it go on, `notify`, or also stops it until a member resumes it, `park` |
| Delegation policy | The approval classes an orchestrator decides for a lane, so they never wait on you ([delegation](authorization.md#delegation)) |
| Delegate policy | The consent operations a delegate runs without a card, and whether a delegate's lane start may raise the model and effort ([delegates](authorization.md#delegates)) |
| Estimate policy | How many finished outcomes an estimate needs and reads, the trials of a forecast, and whether the project shares its delivery history ([estimate policy](measures.md#estimate-policy)) |

### 5. Choose how you hear that something waits

Select Notifications. The web UI always shows what waits on you, in Needs you, and sends nothing anywhere. The other channels are off until a member turns them on:

- Desktop notifications shows a notice on the daemon's machine.
- Webhook address posts each notice as JSON to an address that is `https`, or `http` to this machine alone.
- Webhook secret variable names the daemon's environment variable that holds the key that signs each delivery. It is the variable's name, never the key.

![Notifications of Default, with desktop notifications off and no webhook](assets/screenshots/settings-05-light.png#gh-light-mode-only)
![Notifications of Default, with desktop notifications off and no webhook](assets/screenshots/settings-05-dark.png#gh-dark-mode-only)

A notice leaves the machine, so it carries the project's name, one fixed sentence for what waits, such as "A lane stopped, and it needs you.", and the notice's kind and delivery digest, and never a title, a question, or a path. [Notifications](notifications.md) lists every field, and the Audit's Notices tab shows what each channel was told ([usage and measures](usage-and-measures.md#7-read-who-did-what)).

### 6. Read an agent

In the sidebar, select Agents. A session receives the roster as the agents it may hand work to, and a lane can run as one of them. The switch at the top chooses the scope you read and change: the installation's, or the project's. The project's roster is the built-in roster, then the installation's changes over it, then the project's over those.

The Roster tab lists each agent with its access, class, effort, and approval, and its source: the nearest scope that changed any of its fields. Select an agent, here `heavy-implementation`. Its definition gives each field with its source, Built-in, Installation, Project, or Unset, so you see which scope set it. Here the project raised its effort to `max`.

![Agents of Default, with the roster and the definition of heavy-implementation, its effort set by the project](assets/screenshots/settings-06-light.png#gh-light-mode-only)
![Agents of Default, with the roster and the definition of heavy-implementation, its effort set by the project](assets/screenshots/settings-06-dark.png#gh-dark-mode-only)

Driver preview names the model the agent runs on for each driver: the agent's own model for that driver when its definition names one, under Model for `<driver>`, such as Model for `claude-code`, and otherwise the model its class maps to in your own profile, under `[agent.classes]`. Here the field is unset, and the `large` class runs on `opus`.

### 7. Change an agent

Change a field, here Instructions. The bar under the definition names the fields you changed. Select Review change: the dialog shows each changed line of the agent, as it is and as it will be.

![Review the roster change, with the new instructions of heavy-implementation](assets/screenshots/settings-07-light.png#gh-light-mode-only)
![Review the roster change, with the new instructions of heavy-implementation](assets/screenshots/settings-07-dark.png#gh-dark-mode-only)

Select Save version, which names the scope's next roster version. Sessions started after it receive the new roster, and a running lane keeps the roster it started with. Beside the agent's name, Disable keeps the agent from every session, and no lane runs as it, until Enable; Reset agent drops every change this scope made to it.

### 8. Add an agent

Select Add agent. Copy an agent starts the new agent as a copy of one you choose, which you then change. Paste TOML reads a definition: one `[[agent]]` table, or its keys on their own, as `ropeshift agent add --file` reads a file. The dialog shows the agent the text holds, as the lines the roster adds, or says at the field what stops it, such as a key no agent has or a line the TOML cannot read. Name starts as the table's own name, and the name in the field is the one added.

![Add agent with Paste TOML, a pasted sql-migration table, and the agent it holds](assets/screenshots/settings-08-light.png#gh-light-mode-only)
![Add agent with Paste TOML, a pasted sql-migration table, and the agent it holds](assets/screenshots/settings-08-dark.png#gh-dark-mode-only)

Select Save version. An agent needs a purpose, instructions, a kind, an access, a class, an effort, and at least one allowed tool, and an approval left out is `none`. A definition the roster refuses, such as one with no instructions or with a class the roster does not have, is refused in the roster's words, and nothing is saved.

### 9. Edit the routing guide, and restore a roster version

The Routing guide tab holds the text every session reads to pick an agent for a piece of work, with the scope it comes from. Change it and select Review change, as for an agent. When this scope set the guide, Reset to the layer below returns it to the guide of the scope beneath.

The History tab lists this scope's roster versions, newest first, each with who saved it, when, and what it changed. The newest reads In force, and Revert on an earlier one records that version as the newest.

![The History tab of the project's roster, with the added agent, the changed instructions, and the earlier versions with Revert](assets/screenshots/settings-09-light.png#gh-light-mode-only)
![The History tab of the project's roster, with the added agent, the changed instructions, and the earlier versions with Revert](assets/screenshots/settings-09-dark.png#gh-dark-mode-only)

### 10. Manage your sessions and delegates

In the sidebar, select Members, under Installation. Members with a live session lists each member with their sessions, their last sign-in, and when the next session expires. This release has no roles: every member can answer, decide, and change anything. Sign out this session ends the one in this browser. Sign out all sessions ends every session of yours, in every browser and shell, and revokes every delegate credential of yours.

Delegates lists the delegate credentials you made, each with its project, kind, expiry, last use, and state. A delegate lets an agent outside ropeshift act for you in one project: it runs what is operational, and what needs your consent waits as a card you confirm. Select Create delegate, choose the project, give it a label, which the audit and its cards name, and choose when it expires. Select Create. The next dialog shows the credential once, as a line to run in the agent's shell, with Copy; no page shows it again. Revoke ends a delegate.

![Members, with the live sessions, and the delegates Release assistant and Release notes writer](assets/screenshots/settings-10-light.png#gh-light-mode-only)
![Members, with the live sessions, and the delegates Release assistant and Release notes writer](assets/screenshots/settings-10-dark.png#gh-dark-mode-only)

[Delegates](authorization.md#delegates) explains what a delegate may do.

### 11. Back up the installation

In Settings, select Backup, under Installation. Back up installation downloads a copy of the installation's own records: the projects' registry, the members and the passphrase's hash, and the workers. A project's own records back up from Projects, one project at a time ([back up and archive a project](several-projects.md#6-back-up-and-archive-a-project)).

### 12. Change your passphrase

In Settings, select Change passphrase, under You. Enter the current passphrase and the new one twice; a passphrase has at least 8 bytes and at most 1024. A change ends every other session of yours, in every browser and terminal, and this browser stays signed in. A delegate's credential keeps working; only its expiry, Revoke, or Sign out all sessions ends it. The first passphrase is set in a terminal, by `ropeshift init` or `ropeshift passphrase set`. [Settings](web-ui.md#settings) describes the form.

## Settings only a file holds

Some settings have no control in the web UI. They live in the lane profile, a TOML file in two places:

| File | Where | Who it serves |
|------|-------|---------------|
| Your own profile | `profile.toml` in the installation's directory ([where ropeshift keeps its state](onboarding.md#where-ropeshift-keeps-its-state)), which `ropeshift init` writes | Every lane on this machine |
| A repository's profile | `.ropeshift/profile.toml` at the root of the repository, committed with its code | The lanes of that repository, on every machine |

A lane reads the built-in layer, then your profile over it, then the repository's over yours. The repository's profile applies once it is committed to the base branch: a lane reads it from the commit its worktree begins from, and never from the clone's working tree ([a repository's profile layer](onboarding.md#a-repositorys-profile-layer)). A repository's profile can narrow what a lane does and never widen it: it can add a gate and lower a cap, and cannot raise the permission mode or a cap of yours. `ropeshift template show user/profile.toml` and `ropeshift template show repository/profile.toml` print each file's template with its keys explained, and the Templates view shows the same.

| Setting | File | What it sets |
|---------|------|--------------|
| `[worktree] root` | Yours | Where lane worktrees go. `ropeshift init --worktree-root` writes it when it writes the file ([where lanes work](set-up-projects.md#where-lanes-work)) |
| `[worktree] push_on_stop` | Yours | Whether a lane's branch is pushed to its remote when the lane stops, off by default |
| `[agent.classes]` | Yours | The model each capability class of the roster runs on, which Driver preview reads |
| `[agent] model`, `permission_mode`, and `gates` | Either | The model of a lane that runs as no roster agent, the agent's permission mode, and the actions that always ask a person ([what a lane does without asking](onboarding.md#what-a-lane-does-without-asking)) |
| `[review] max_review_rounds` | Either, usually the repository's | How many times the review judges one work order before work it still fails waits for you, 3 by default |
| `[admission]` estimates and reserves | Yours | What one lane takes, from which the worker recommends a lane cap ([how many lanes run at once](onboarding.md#how-many-lanes-run-at-once)). The caps themselves are also in Settings |
| `[resources]` thresholds | Yours | How often the worker measures, and where a measure warns ([what the worker measures](onboarding.md#what-the-worker-measures)) |
| `[lifecycle]` timers | Either | When a lane is stalled, how long it waits for an answer before it parks, and how often it restarts |
| `[telemetry]` | Yours | Whether telemetry keeps content, and how long it keeps it ([telemetry](telemetry.md#profile-settings)) |
| `[hooks] on_lane_created` | Either, usually the repository's | A command that runs once in each new worktree, such as installing dependencies |

A key the profile does not know refuses the whole profile, and no lane starts until it is fixed, so a misspelled setting never keeps its default in silence. A lane reads the files when it starts. The policies have a command too: `ropeshift policy publish --file F` publishes a version from a file, as Review change does ([the command line](cli.md)).

## Your own agent CLI setup

Lanes load none of your own Claude Code setup: no settings, hooks, status line, plugins, skills, commands, or subagents from `~/.claude`, no `~/.claude/CLAUDE.md` or `~/.claude/rules/`, no MCP server or connector of yours, and no auto memory. They keep your sign-in. A lane reads the repository's own `.claude/` settings, skills, and subagents, its `CLAUDE.md` or `AGENTS.md`, the agent roster above, and ropeshift's own MCP server, so put what a project's lanes need in the repository or in its lane profile. A repository's `.mcp.json` does not reach lanes either. [Your own agent CLI setup](authorization.md#your-own-agent-cli-setup) names the switch that keeps out each piece.

## On the command line

[The CLI for power users](cli-for-power-users.md#settings-and-agents) gives the command for each step of this guide.

## Next

[Usage and measures](usage-and-measures.md) reads the figures ropeshift keeps of the work: its measures, its forecasts, the estimated cost of each lane, and the audit log.
