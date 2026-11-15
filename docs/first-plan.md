# Your first plan, end to end

This guide turns a goal into accepted work: a planning session writes the plan, you approve it, lanes carry out its work orders, a review agent judges each change, and you accept the result. Each step shows the web UI first, then the command line that does the same.

Before you start, finish [Get started](getting-started.md): the daemon runs with your repository as a source, you can sign in to the web UI at `http://127.0.0.1:7317`, and `ropeshift worker list` names your worker. The planning session runs in a tmux pane, so the machine needs tmux.

## 1. Start a planning session

In the web UI:

1. Open Roadmap, and select Start planning session. Plans and Planning have the same button.
2. In Goal, write what the plan is for, such as "Let partners see their own usage and rate limits". You can read the goal from a file instead, or leave it empty and the session asks.
3. Choose the Planning repository and the Worker, and select Start planning session.

You see the Planning page for the new session. It shows the session's pane, and beside it Plan in progress: the documents, milestones, contracts, and draft decisions the session has committed to its branch so far.

![The Planning page, with the session's pane, the field that types into it, and the plan in progress beside it](assets/screenshots/planning-light.png#gh-light-mode-only)
![The Planning page, with the session's pane, the field that types into it, and the plan in progress beside it](assets/screenshots/planning-dark.png#gh-dark-mode-only)

From the command line:

```sh
ropeshift plan start --goal "Let partners see their own usage and rate limits"
```

Your terminal joins the session's pane. Detach with `C-b d`, which leaves the session running, and rejoin it with `ropeshift lane attach <lane-id>`; `ropeshift lane list` names the lane. `--no-attach` starts the session and prints the command that joins it, and the address of its Planning page.

The session plans in the project's planning repository, and reads every other repository of the project, read-only. It writes only on its own branch, so your default branch does not change.

## 2. Answer its questions

The planning agent asks what it needs to know to write the plan. Where its questions reach you depends on whether a terminal is attached to the session.

With a terminal attached, as `ropeshift plan start` attaches yours, the agent asks in the conversation, and you answer there.

With no terminal attached, its questions go to Needs you as product questions, which no orchestrator can answer for you. The Planning page does not count as an attached terminal, so a session started from the web UI asks here:

1. Open Needs you, and select the questions item. Each question shows its options, with the recommended one marked, and Blocks work on a question the plan cannot go on without.
2. Choose an option for each, or Something else and write your answer.
3. Select the send button, such as Send 2 answers. Each answer is recorded once, as yours.

![Needs you, with two questions from a session, each with its options and the recommended one marked](assets/screenshots/needs-you-question-light.png#gh-light-mode-only)
![Needs you, with two questions from a session, each with its options and the recommended one marked](assets/screenshots/needs-you-question-dark.png#gh-dark-mode-only)

To say more to the agent, type in Type into the session on the Planning page and select Send. With Press Enter after the text checked, the line ends with Enter, and with the field empty, Enter, Escape, and the up and down arrows go straight to the session. Detach stops what you type from reaching the session, which keeps running, and Attach joins it again.

From the command line:

```sh
ropeshift question batches
ropeshift question answer <question-id> --answer "A higher tier for partners"
```

`question batches` prints each waiting question with its options and recommendation. An answer through Needs you or `question answer` is recorded as your decision at once, and every lane attached to the question hears it. An answer you give in the conversation becomes a draft decision in the plan, which your approval in the next step decides. The session does not submit the plan while a blocking question is open.

## 3. Review and approve the plan version

When the session has finished, the plan version waits for you. Needs you lists it as a plan approval.

1. Open Needs you, and select the plan approval. Its card shows what the version holds, how it differs from the last version you approved, and each draft decision.
2. Select Open the full plan approval to read the version in full: the difference from the version in force, the draft decisions with the question each answers, the contracts it adds or changes with their criteria and work orders, and the documents it covers. Roadmap's banner, Review version, opens the same page.
3. Select Approve version, or Decline with a reason. The session reads the reason, and writes a new version when it can meet it.

![Plan approval, with the difference from the last version, the draft decisions, and the card that approves or declines the version](assets/screenshots/plan-approval-light.png#gh-light-mode-only)
![Plan approval, with the difference from the last version, the draft decisions, and the card that approves or declines the version](assets/screenshots/plan-approval-dark.png#gh-dark-mode-only)

From the command line:

```sh
ropeshift plan list
ropeshift plan show <plan-id>
ropeshift plan approve <plan-id>
```

`plan show` prints the plan and the request to approve it: the version, the difference, and every draft decision. `plan approve` approves the version that waits, bound to its digest, so a version that changed since you read it is refused.

Approving does three things at once:

- It records each draft decision as your decision.
- It passes the contract review of each work order the version covers, so you pass no review per work order.
- It accepts the planning session's work order. Intake records the plan at the approved commit at its next read, and the plan's work orders appear.

The planning branch stays where the session left it. At the merge authority `person_merges`, the default, you merge it yourself when you choose; [a planning branch](onboarding.md#a-planning-branch) covers the other levels.

## 4. Let the work run

Each work order whose definition of ready passes is `ready`, and something must claim it. One orchestrator per machine claims ready work, starts its lanes, and claims each review when a lane finishes.

1. Open Workers and resources, and select Start orchestrator on your worker.
2. A worker on a subscription starts at `attended`, and its orchestrator claims only inside a run window. Open Settings, and under Workers, in your worker's Run window panel, enter the Hours and select Open window. A window closes by itself, and closing one stops no lane.

From the command line:

```sh
ropeshift orchestrator start <worker-id>
ropeshift worker window-open <worker-id>
```

`worker window-open` opens a window of 4 hours unless `--for` says otherwise, and 12 hours at most. `ropeshift worker autonomy <worker-id>` shows the worker's level, its run window, and whether its orchestrator can claim now. A worker on an API key starts at `unattended`, and needs no window. To run one work order by hand instead, claim it and start its lane, as in [Get started](getting-started.md#7-run-the-lane).

Work runs in milestone order: work of a milestone waits for the work of each milestone it depends on. Work that depends on other work in the same repository starts from that work once its accepted commit is in the base branch, and until you merge it, it reads `waiting_for_merge` ([where a lane starts](onboarding.md#where-a-lane-starts)).

## 5. Watch the lanes

Open Floor. It shows each worker with its orchestrator and its lanes, and for each lane its work order, its state, whether the model and the ledger are reachable from it, its generation, and how fresh that reading is.

![Floor, with one worker's measurements and its lanes in the working, ended, parked, and needs-a-decision states](assets/screenshots/floor-light.png#gh-light-mode-only)
![Floor, with one worker's measurements and its lanes in the working, ended, parked, and needs-a-decision states](assets/screenshots/floor-dark.png#gh-dark-mode-only)

- Open shows a lane's page, and Open pane joins a lane that runs in a terminal pane.
- Roadmap shows how much of each milestone's work is done, and the critical path.
- A lane that needs a person, such as one that asks to run a command its policy gates or parks on a question, appears in Needs you.

From the command line:

```sh
ropeshift top
ropeshift lane list
ropeshift lane attach <lane-id>
```

`top` watches what needs you, the lanes, and the worker in one terminal view, and changes nothing.

## 6. Read the reviews and accept the work

When a lane ends with a commit, a review lane reads the change and gives each criterion of the contract a verdict, `pass`, `fail`, or `cannot_judge`, with its reasons and citations to files, lines, and requirements. A failed criterion sends the work back for another attempt on its own, until three reviews have failed it, after which it waits for you. When every criterion passes, the work waits for you.

1. Open Needs you, and select the work order. Its card shows the verdict on each criterion.
2. To read more, open the work order's page. Its Review tab shows What the review found, and Changes shows the change.
3. Select Accept, or Send back for rework with a reason, which reaches the next attempt's first prompt.

![A work order waiting for acceptance, with the review's verdict on each criterion and the Accept button](assets/screenshots/work-order-light.png#gh-light-mode-only)
![A work order waiting for acceptance, with the review's verdict on each criterion and the Accept button](assets/screenshots/work-order-dark.png#gh-dark-mode-only)

From the command line:

```sh
ropeshift work show <work-order-id>
ropeshift work acceptance <work-order-id>
ropeshift work accept <work-order-id>
ropeshift work rework <work-order-id> --reason "The retry budget does not hold under load"
```

`work show` prints each verdict as a `verdict` line, and `work acceptance` lists the evidence: each branch with the files and lines it changed, the check results, and the verdicts. Accepted work reads `done`.

At `person_merges` with review target `branch`, the defaults, accepting the work does not merge it: you merge each lane's branch yourself. [Connect a GitHub repository](github.md) shows how to review through pull requests, and how to let ropeshift merge.

## Where to go next

- What a planning session reads and writes, and adopting a project that already exists: [onboarding](onboarding.md#start-a-planning-session).
- Each view of the web UI: [the web UI guide](web-ui.md).
- Who may approve what: [authorization](authorization.md).
