# Troubleshooting

This guide finds the cause of a problem and fixes it. Each section starts with what you see, says where the web UI shows the cause, and gives the fix. A problem that stops the web UI from loading starts in a terminal, because nothing else can show it. Its images show the sample installation of the guides before it, where the Default project has an orders API with work running. Put your own work in their place.

## Before you start

- Finish [get started with the web UI](getting-started.md), so you know where Workers and resources, Settings, and Needs you are.
- [Watch work run](watch-work-run.md) says how work starts, which several of these sections rely on.

## The web UI does not load

You see no page, or a browser error, at `http://127.0.0.1:7317`. Ask the daemon from a terminal:

```sh
ropeshift health
```

It says the control plane answered and read its ledger. It exits 9 when nothing answers, so the daemon is not running or is not running as this installation.

- Start the service, with `ropeshift service install`, or `brew services start ropeshift` on a Homebrew install. Use one, never both, because two daemons for one installation conflict ([run the daemon as a service](onboarding.md#run-the-daemon-as-a-service)).
- Read the daemon's output for why it stopped. On Linux it is in `journalctl --user -u ropeshiftd`. On macOS it is in the log named after the launchd agent in the installation's directory, or in `$(brew --prefix)/var/log/ropeshift.log` for a Homebrew service. A daemon that refuses to start on a database is [its own section](#database-refuses-to-start).
- A browser tab on another port or another machine shows nothing: the web UI is served on this machine's loopback address, port 7317 unless `--web` or `ROPESHIFT_WEB` names another. Another program holding the port stops the daemon from serving it, and the daemon's output says so.

A stop of the service stops no lane, and the daemon that starts next adopts them.

## A driver is not usable

A work order does not start, or a start is refused because the worker has no usable driver. In the sidebar, select Workers and resources. Drivers lists each agent CLI of each machine with its result: Usable, Not usable, or Not reported. A driver is usable when its CLI is present and its sign-in is not signed out. Version, Qualification, and Sign-in say what the worker found, and Reason says why a driver is not usable.

![Drivers, with claude-code Not usable, its sign-in signed out, and the reason](assets/screenshots/trouble-01-light.png#gh-light-mode-only)
![Drivers, with claude-code Not usable, its sign-in signed out, and the reason](assets/screenshots/trouble-01-dark.png#gh-dark-mode-only)

- Signed out: sign in to the agent CLI in a terminal, through its vendor's own flow, with `claude`, and check it with `claude auth status`. Ropeshift never handles the credentials.
- Not present: install the agent CLI on that machine, where the service finds it. The service keeps the `PATH` of the shell that installed it, so run `ropeshift service install` again from a shell that finds the CLI.

The doctor runs when a worker starts, so a result is as old as the worker's last connect, and Freshness says when that was. A sign-in that returns while the worker runs shows as signed out until the worker connects again, so restart the service after you sign in: `ropeshift service install` restarts it, and so does `brew services restart ropeshift`. The restart stops no lane.

## Ready work is not claimed

Work is ready and nothing starts it. Ready work is claimed by your machine's orchestrator, or by you with Start. In the sidebar, select Workers and resources. In your worker's Orchestrator column:

- An orchestrator starts with its worker, and one a member stopped claims nothing. Select Start orchestrator ([check the orchestrator](watch-work-run.md#1-check-the-orchestrator)).
- Waiting for a run window means the worker is attended, the level a subscription worker starts at, and it claims work only inside a run window you opened. Select Open run window, which opens Settings, Autonomy and run windows, then Open window ([open a run window](watch-work-run.md#2-open-a-run-window)).
- Waiting for work means nothing is ready for it, so check that the work is ready and not held, as the next section says.

A project whose fleet is Paused or Draining claims nothing either: in Settings, Fleet, select Run fleet ([pause, drain, or cap one project](several-projects.md#5-pause-drain-or-cap-one-project)).

## Work waits

Work is ready, or stands in a state that is not moving, and does not start. Open the work order from Work orders, and read its state and its lane. Ready work that cannot start is held for one of these reasons, and the web UI names it where you try to start it: select Start on the work order, choose the worker, and select Start. The refusal appears in the dialog, in the daemon's words.

![The Start dialog, refused because the work's dependency is accepted and not yet in the base branch](assets/screenshots/trouble-02-light.png#gh-light-mode-only)
![The Start dialog, refused because the work's dependency is accepted and not yet in the base branch](assets/screenshots/trouble-02-dark.png#gh-dark-mode-only)

| Reason | What holds the work | Fix |
|--------|---------------------|-----|
| `waiting_for_merge` | A work order in the same repository that this one depends on is accepted, and its commit is not yet in the base branch | Merge the dependency's branch, and the reason clears by itself ([review and merge](review-and-merge.md#merge-at-person_merges)) |
| `dependencies_diverge` | The accepted work of the dependencies is on separate lines of history, and none holds the others | Merge the dependencies' branches into one line |
| `base_unavailable` | The base branch could not be fetched, or does not exist | Fix the repository's remote or its base branch, and the next claim tries again |
| `no_checkout` | This machine keeps no clone of the work's repository, or its directory was deleted or moved | Register the clone on this machine with Register repositories ([set up your projects](set-up-projects.md)) |
| `continued_branch_missing` | The branch the work continues is on neither the remote nor the checkout | Restore the branch |

[Where a lane starts](onboarding.md#where-a-lane-starts) explains the base each lane starts from.

Work that waits on a question is not held at all: its lane parked on a blocking question. In the sidebar, select Needs you, find the question, and answer it, and the lane goes on ([answer a lane's question](watch-work-run.md#9-answer-a-lanes-question)).

## A lane parks, stalls, or trips

A lane stops going on. In the sidebar, select Needs you, then Stuck, and select the item. Its card says why in a line and offers the actions its state allows:

| What you read | What it means | Action |
|---------------|---------------|--------|
| Its lane is parked until a person says go, or waits on an answer | The lane saved its work and stopped its process | Answer what it waits on, or Resume ([resume a parked lane](watch-work-run.md#8-resume-a-parked-lane)) |
| Its lane shows no progress | The lane stalled | Open the lane from its work order and read what it last did, then choose from the actions the card offers |
| Its lane crashed too often, and its breaker stopped it | The lane tripped | Read the lane, then Reset the breaker |
| Its lane failed | The lane ended without an outcome and nothing retries failed work on its own | Retry, or Cancel this work |
| Its lane stopped, and no answer to a question would change that | The lane reported itself blocked | Read its reason, then Back to ready or Back to planning |

![The card of a tripped lane, with its breaker stopped and Reset the breaker](assets/screenshots/trouble-03-light.png#gh-light-mode-only)
![The card of a tripped lane, with its breaker stopped and Reset the breaker](assets/screenshots/trouble-03-dark.png#gh-dark-mode-only)

A lane trips when it crashes more times, within the restart window, than its profile allows. Reset the breaker starts a new generation of the lane from its pinned brief. If what crashed it will crash it again, it trips again, so read the lane's page for what it last did before you reset it.

## A start is refused

Select Start on a work order, and the dialog answers `claim refused by workspace policy` with a reason. The reasons name a cap or a state of the project:

- `work in progress at 4 of 1`, or `lanes in this repository at 2 of 2`, or `lanes on this worker at 3 of 3`: a cap holds the claim. The project's caps are in Settings, then Fleet, as Work in progress limit, Lanes per repository, and Lanes per worker, where 0 caps nothing. The machine's own cap is Max lanes in Settings, then Admission and lane caps. Raise the cap, or wait for a lane to end ([set the machine's lane caps](several-projects.md#4-set-the-machines-lane-caps)).
- `the fleet is in` and its mode: the project's fleet is paused or draining. Select Run fleet in Settings, then Fleet.
- A reason from the table above holds the work.

![The Start dialog, refused because the project's work in progress is at its limit](assets/screenshots/trouble-04-light.png#gh-light-mode-only)
![The Start dialog, refused because the project's work in progress is at its limit](assets/screenshots/trouble-04-dark.png#gh-dark-mode-only)

## A planning session does not start

A planning session runs in a tmux pane, because you are present by construction. Without tmux on the machine, the work order stops in `blocked` with the reason `no_interactive_mode`, and no session runs. Install tmux, run `ropeshift service install` again from a shell that finds it, because the service keeps that shell's `PATH`, and start the planning session again.

## A forgotten passphrase

Ropeshift stores a hash of the passphrase, not the passphrase, and has no command or page that recovers or resets one. The web UI's Change passphrase asks for the passphrase in force, so a page that is still signed in cannot change it.

- A shell that still holds a session, from `eval "$(ropeshift login)"` within the last hour, changes it with `ropeshift passphrase set`, which asks for nothing more.
- With no session, the installation cannot be signed in to. To start again, stop the service, move the state directory aside ([where ropeshift keeps its state](onboarding.md#where-ropeshift-keeps-its-state)), and run `ropeshift init`, which asks for a new passphrase and makes a new installation. The records of the old one stay in the directory you moved.

## Database refuses to start

Every database in the state directory records a checksum of each schema change it applied: the installation database, each project's ledger, the telemetry store, and the worker's journal. Before the daemon applies anything to a database, it compares those checksums with the schema changes its build ships. When one differs, the daemon changes nothing in that database, prints an error like this one, and exits:

```text
ropeshiftd solo: upgrade <state directory>: migrated: installation migration 1 (0001_installation.sql) changed after it was applied: recorded sha256 <recorded>, shipped sha256 <shipped>
to start, run the build that migrated this database, or restore the state directory from a backup; docs/troubleshooting.md#database-refuses-to-start says how: <link to this page>
```

A release never changes a schema change it shipped, so another build migrated this database, usually a development build. Do one of these:

- Run the build that migrated the database.
- Move the current state directory aside, so nothing the other build wrote is lost, and restore the state directory from a copy made before that build ran.

Backups made with `ropeshift backup` cover the installation database and each project's ledger, as [backing up](onboarding.md#back-up-a-project-and-the-installation) describes. They do not cover the telemetry store or the worker's journal. When the refusal names one of those two, run the build that migrated it, or restore a full copy of the state directory.

A database that a later release migrated does not stop an earlier one. After a downgrade, the daemon prints a warning naming each database whose applied schema changes its build does not ship, and the schema changes themselves, and starts on the schema the later release left. A project's warning starts with the database file it is about, such as `projects/<project ID>.db`, and a start gives each warning once.

## On the command line

[The CLI for power users](cli-for-power-users.md#troubleshooting) gives the command for each step of this guide.

## Next

[Modes](modes.md) says what solo, group, and cluster mode are, their status, and which one to use today.
