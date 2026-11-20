# Watch work run

This guide runs the work of an approved plan in the web UI. You start your machine's orchestrator, open a run window, start a work order yourself, and watch its lane on the Floor and on its own page. Then you answer what lanes ask of you: a command a policy gates, a lane that parked, and a question. It follows Harbor, the project of a small port, whose plan has its first work order ready, Serve the list of berths in `harbor-api`. Put your own project and work in their place.

## Before you start

- Finish [plan your first change](plan-your-first-change.md), or [adopt an existing project](adopt.md): the project has an approved plan, and at least one of its work orders is ready.
- On a worker signed in with a subscription, each lane runs in a terminal pane on the worker's machine, so that machine needs tmux, which [get started with the web UI](getting-started.md) installs.

## How work starts

A work order whose definition of ready passes is ready, and something must claim it. One orchestrator per machine claims ready work, starts its lanes, and claims each review when a lane finishes. You can also start any ready work order yourself.

Work runs in milestone order: work of a milestone waits for the work of each milestone it depends on. Work that depends on other work in the same repository starts from that work once its accepted commit is in the base branch, and until the commit is merged, it reads `waiting_for_merge` ([where a lane starts](onboarding.md#where-a-lane-starts)). In Harbor, Serve the sailings of each berth waits for Serve the list of berths, and the berths page waits for the API's milestone.

## In the web UI

### 1. Check the orchestrator

In the sidebar, select Workers and resources. Machines lists each worker, with its state, its lanes against its cap, its measures, and its orchestrator, and below it are the worker's drivers and the sessions on it. Your worker's orchestrator starts with the worker, so its Orchestrator column already reads it; select Start orchestrator only if a member stopped it. The column shows the orchestrator's state, such as Provisioning while its session starts and Idle while it waits for work, and Stop orchestrator replaces the button. Sessions lists the orchestrator beside the lanes, as a session that does not count toward the machine's caps. Stopping it stops no lane. Here the two lanes that read Ended are the sessions of [plan your first change](plan-your-first-change.md) in Harbor and [adopt an existing project](adopt.md) in `moorings`.

![Workers and resources, with the worker's orchestrator idle and the sessions on the worker](assets/screenshots/run-01-light.png#gh-light-mode-only)
![Workers and resources, with the worker's orchestrator idle and the sessions on the worker](assets/screenshots/run-01-dark.png#gh-dark-mode-only)

### 2. Open a run window

A worker signed in with a subscription starts at `attended`, and its orchestrator claims work only inside a run window. A worker on an API key starts at `unattended`, and needs no window.

In the sidebar, select Settings, then Autonomy and run windows. Under your worker, in the Run window panel, enter the Hours the window stays open, and select Open window. The panel then says when the window closes. A window closes by itself, and Close window closes it now. Closing a window stops no lane.

![The Run window panel, with the window open and when it closes](assets/screenshots/run-02-light.png#gh-light-mode-only)
![The Run window panel, with the window open and when it closes](assets/screenshots/run-02-dark.png#gh-dark-mode-only)

Inside the window, the orchestrator claims ready work and starts its lanes on its own. The panel above it sets the worker's level, and Read the stated risk shows what each level lets the worker do.

### 3. Start a work order yourself

To run one work order now, in the sidebar, select Work orders, and open a ready work order, here Serve the list of berths. Select Start. The dialog claims the work for a worker, then asks the worker to start its lane.

- Worker is the machine that runs the lane, with its state, its lanes against its cap, and its level. Workers the daemon sees now come first.
- Lane ID is optional. Leave it empty and ropeshift names the lane, or give it a name you will know on the Floor, here `serve-berths`.

![Start Serve the list of berths, with the worker and the lane ID](assets/screenshots/run-03-light.png#gh-light-mode-only)
![Start Serve the list of berths, with the worker and the lane ID](assets/screenshots/run-03-dark.png#gh-dark-mode-only)

Select Start. The work order is claimed for that worker, and the worker starts its lane. A start the worker refuses, such as one at its lane cap, leaves the work claimed and says why.

### 4. Watch the Floor

In the sidebar, select Floor. It shows:

- Ready to start, while ready work waits that nothing has claimed, each with Start.
- Each machine that serves the project, with its orchestrator's state, its lanes against its cap, and its measures. The cap counts the lanes of every project on that machine.
- Each lane on it, with its work order, its state and what it waits on, whether the model and the ledger are reachable from it, its generation, and how fresh that reading is. Open pane, Open, and the lane's menu of actions sit at the end of its row.

The switch at the top shows this project's lanes, or those of All projects.

![The Floor, with the worker, its measures, and the berths lane waiting on a decision](assets/screenshots/run-04-light.png#gh-light-mode-only)
![The Floor, with the worker, its measures, and the berths lane waiting on a decision](assets/screenshots/run-04-dark.png#gh-dark-mode-only)

Here the berths lane has committed its first change and asked to run a command its policy gates, so it reads Needs a decision. The lane above it, which reads Ended, is the planning session of [plan your first change](plan-your-first-change.md).

### 5. Open a lane

Select Open on a lane. The lane's page shows what the lane is doing now: its terminal pane, live from the worker, with Type into the session under it, or the event stream of a lane without a pane. Beside it, Facts gives the lane's state, its work order, its liveness, whether the model and the ledger are reachable, its generation, and its worker. Below are its generations, with why each started and ended, and the messages sent to it. Talk to the lane opens the conversation panel on it.

![The lane page of the berths lane](assets/screenshots/run-05-light.png#gh-light-mode-only)
![The lane page of the berths lane](assets/screenshots/run-05-dark.png#gh-dark-mode-only)

### 6. Allow or deny a gated call

A lane that asks to run a command its policy gates waits for a person, and Needs you lists the request. In the sidebar, select Needs you, and select the request, here `Allow Bash?`. Its card shows the tool and the exact operation the lane would run, here `rm -rf internal/berths/legacy`, the lane and its work order, and the digest the decision binds to: if the call changes, the approval is void. The work order's page shows the same card while the request is open.

![An approval request, with the call the lane asks to run, Allow this one call, and Deny](assets/screenshots/run-06-light.png#gh-light-mode-only)
![An approval request, with the call the lane asks to run, Allow this one call, and Deny](assets/screenshots/run-06-dark.png#gh-dark-mode-only)

Select Allow this one call, or select Deny. Reason, which the lane reads, is optional for either. Allowing lets this one call run, and the lane goes on. A request that waits longer than the lane's wait parks the lane, as [resume a parked lane](#8-resume-a-parked-lane) says.

### 7. Talk to a lane

On the Floor, Open pane opens the conversation panel with the lane as its target. The panel names the lane, its work order, its worker, and its state, and above the session it lists what waits on you from the lane, such as an open request. Here the berths lane works on after the call you allowed, with nothing waiting, so the session fills the panel. A lane in a terminal pane takes what you type in the pane, which the panel shows live with Type into the session under it. On a lane without a pane, the button is Message, and what you write reaches the lane once, as a message that grants it nothing. Switch target moves the panel to another lane or to the assistant, and Close the conversation panel closes it.

![The conversation panel on the working berths lane, with its pane and Type into the session](assets/screenshots/run-07-light.png#gh-light-mode-only)
![The conversation panel on the working berths lane, with its pane and Type into the session](assets/screenshots/run-07-dark.png#gh-dark-mode-only)

### 8. Resume a parked lane

A lane whose blocking question or request waits past its wait parks: it saves its work and stops its process, so it uses no quota. It goes on once what it waits on is decided, or once a person resumes it.

In Needs you, Stuck lists the work that cannot go on by itself, such as work a lane reported blocked or that failed, and a lane that parked, stalled, or tripped. Select an item to read why it stopped and the actions its state allows. Here a lane on Finish cancelling a booking, a work order of the project `moorings` that you started as in step 3, asked a question it cannot go on without, and parked when its wait ran out. Its card says what the lane waits for: the answer to its question, or a person.

![Needs you, with the parked lane of moorings, what it waits for, and Resume](assets/screenshots/run-08-light.png#gh-light-mode-only)
![Needs you, with the parked lane of moorings, what it waits for, and Resume](assets/screenshots/run-08-dark.png#gh-dark-mode-only)

Select Resume, and the lane goes on now, without waiting: it starts its next generation from where it parked. Answering its question instead wakes it too, and the answer reaches its next generation's first prompt.

### 9. Answer a lane's question

A lane asks a question when its contract does not answer it. The orchestrator takes each question up its ladder: it records the answer the record already gives, assumes one where the choice can be undone, and brings you the rest in Needs you, under Decisions, with its options and its recommendation. A question from a planning session comes to you directly.

In the sidebar, select Open questions. It lists every question that is still open, whoever holds it, and Answer answers one yourself. Here the berths lane asked `In what order should GET /berths list the berths?`, and went on without the answer. Select Answer on the question. It shows its options, and Something else for an answer in your own words.

![Open questions, with the lane's question and its answer form](assets/screenshots/run-09-light.png#gh-light-mode-only)
![Open questions, with the lane's question and its answer form](assets/screenshots/run-09-dark.png#gh-dark-mode-only)

Choose an option, or choose Something else and write your answer, and select Send this answer. The answer is recorded once, as your decision, and the question closes. The decision binds the work the question is about. A lane that asked the question reads the answer and goes on, and any other lane still on that work is told of the decision, and goes on or parks as its lane policy says. Work that already finished keeps the decision on its record, unless the decision is one nobody can take back: that sends the work back for one rework, as overturning a decision later does ([what a decision does to work](how-it-works.md#what-a-decision-does-to-work)).

### 10. Follow progress on the Overview

In the sidebar, select Overview. Its figures count what waits on you, the lanes running, the work waiting for acceptance or a merge, and the work done in the last 7 days, and give the interruptions per completed work order and the estimated cost this week. Below them, Plan shows each milestone with its progress and its state, Lanes the lanes running, and beside them what waits on you, how often you agreed with the review, and the recent activity. Roadmap shows how much of each milestone's work is done, and the critical path.

![The Overview, with the work running and the plan's milestones](assets/screenshots/run-10-light.png#gh-light-mode-only)
![The Overview, with the work running and the plan's milestones](assets/screenshots/run-10-dark.png#gh-dark-mode-only)

## On the command line

[The CLI for power users](cli-for-power-users.md#watch-work-run) gives the command for each step of this guide.

## Next

[Review and merge](review-and-merge.md) reads the review of the finished work, accepts it, and merges it.
