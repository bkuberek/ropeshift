# Run several projects at once

One installation runs every project you create, on the same machines. Each project keeps a ledger of its own, and Needs you, the Floor, and each machine's lane caps reach across them. This guide moves between projects, reads what waits in all of them, watches every project's lanes, sets the caps that hold them, and backs up and archives a project you are done with. Its images show a sample installation of three projects: Default, an orders API with a plan running; Catalog, with a lane at work and new work waiting; and Promotions, whose sale has ended. Put your own projects in their place.

## Before you start

- Finish [set up your projects](set-up-projects.md) for each project, and [watch work run](watch-work-run.md) in at least one.

## How projects share a machine

A project holds its own repositories, plans, work, decisions, and history. A worker serves every project of its installation, and the caps of its machine count the lanes of every project on it, so the projects together never run more lanes than the machine allows. Each project also has a fleet mode and caps of its own, which hold its work inside the machine's caps.

## In the web UI

### 1. Switch project

Press `P`, or select the project's name at the top of the page. The switcher lists every active project with how much waits in it, then each archived project, marked read-only. Type in the filter at the top to narrow the list. Choose a project, and the same view opens in that project. The last entries create a project, import an existing project, and open the list of projects.

![The project switcher, with Default and Catalog and what waits in each, and Promotions with nothing waiting](assets/screenshots/several-01-light.png#gh-light-mode-only)
![The project switcher, with Default and Catalog and what waits in each, and Promotions with nothing waiting](assets/screenshots/several-01-dark.png#gh-dark-mode-only)

### 2. Read what waits in every project

In the sidebar, select Needs you. It lists what waits on you in every active project, the most important first and then the oldest, and says how many items wait in how many projects. Each item carries its project's name, and its card acts in that project. The Project menu, below the kinds, narrows the list to one project, and All active projects shows every project again. An archived project waits on nobody, so it is not listed.

![Needs you, with items from Default and Catalog, each labeled with its project](assets/screenshots/several-02-light.png#gh-light-mode-only)
![Needs you, with items from Default and Catalog, each labeled with its project](assets/screenshots/several-02-dark.png#gh-dark-mode-only)

### 3. Watch every project's lanes

In the sidebar, select Floor. It shows the current project's lanes on each machine that serves it. At the top right, choose All projects to add the lanes of every other active project on those machines, after the current project's and each marked with its project. The choice appears once another project is active. A lane of another project has no actions here: switch to its project to act on it. With All projects, each machine's Lanes counts the running lanes of every project against its cap, here four of nine, Catalog's among them; the status bar counts the current project's lanes only. The cap itself always counts every project's lanes.

![The Floor with All projects, with Catalog's lane after the lanes of Default, and the machine's lanes counted against its cap](assets/screenshots/several-03-light.png#gh-light-mode-only)
![The Floor with All projects, with Catalog's lane after the lanes of Default, and the machine's lanes counted against its cap](assets/screenshots/several-03-dark.png#gh-dark-mode-only)

### 4. Set the machine's lane caps

In the sidebar, select Settings, then Admission and lane caps, under Installation. A machine's caps count the lanes of every project on it, and the orchestrator and the assistant never count.

- Max lanes is the most lanes the machine runs at once. Recommended follows the worker's recommendation, No cap lifts it, and A number sets your own.
- Lanes per sign-in caps the lanes one sign-in runs at once, because every session on a subscription draws from the same usage limits. 0 caps nothing.
- Recommendation gives the cap the worker computed from its machine, with its reason, and the cap in force.

Select Save. The caps are saved in your user profile on that machine, and the worker reads a change at its next measure, with no restart. A repository's profile can lower them for its own lanes, and can raise none. A lane a cap holds back waits with the reason on its work order, and starts once the machine has room.

![Admission and lane caps, with Max lanes, Lanes per sign-in, and the Recommendation](assets/screenshots/several-04-light.png#gh-light-mode-only)
![Admission and lane caps, with Max lanes, Lanes per sign-in, and the Recommendation](assets/screenshots/several-04-dark.png#gh-dark-mode-only)

### 5. Pause, drain, or cap one project

In the sidebar, select Settings, then Fleet, under Project. Fleet mode is Running, Paused, or Draining:

| Mode | What it does |
|------|--------------|
| Running | The project's work is claimed and its lanes start, within the caps |
| Paused | No new claim, and no new lane. The lanes that run go on |
| Draining | Takes no claim and starts no new generation for a lane. Each running lane finishes its turn, bounded by `drain_deadline` (10 minutes by default), then completes or parks. Draining does not end a parked lane |

Pause fleet, Drain fleet, and Run fleet change it at once. Below, the project's caps hold its claims inside the machine's: Work in progress limit, Lanes per repository, and Lanes per worker, where 0 caps nothing. Lanes per worker keeps one project from taking every lane of a machine that serves several. Select Save to keep them.

![The Fleet section of Default, Running, with its three caps](assets/screenshots/several-05-light.png#gh-light-mode-only)
![The Fleet section of Default, Running, with its three caps](assets/screenshots/several-05-dark.png#gh-dark-mode-only)

### 6. Back up and archive a project

In the sidebar, select Projects, under Installation. Each project shows its state, its repositories, what waits in it, and its lanes.

- Back up downloads a copy of the project's ledger as a `.db` file. The copy is made for the download, and nothing of it stays on the daemon's machine. Import backup, on the same page, makes a project from such a file.
- Archive… makes the project read-only: its views stay, and no lane starts in it until it is restored. Every lane of it must have ended first: the refusal reads `lane <id> of project <name> has not ended: drain the project with fleet drain, let running lanes finish, and cancel the work of any parked lane with work cancel`. Draining does not end a parked lane, so before an archive, resume its work with the fleet running and let it finish, or end it with `work cancel`. Select Archive in the dialog.

An archived project waits on nobody, the switcher marks it read-only, and its row offers Restore, which makes it active again, and Remove…, which deletes its ledger after offering a backup.

![Projects, with Promotions archived and its Restore](assets/screenshots/several-06-light.png#gh-light-mode-only)
![Projects, with Promotions archived and its Restore](assets/screenshots/several-06-dark.png#gh-dark-mode-only)

## On the command line

[The CLI for power users](cli-for-power-users.md#run-several-projects-at-once) gives the command for each step of this guide.

## Next

[Settings and agents](settings-and-agents.md) changes a policy and restores an earlier version, edits the agent roster, and names the settings only a file holds.
