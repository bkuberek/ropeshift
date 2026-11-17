# Plan your first change

This guide turns a goal into an approved plan in the web UI. You start a planning session, tell it what you want while it asks its questions, answer them in Needs you, follow the plan it writes on its branch, and approve the version it submits. Approving the plan creates its work orders. It follows Harbor, the project of a small port, with its code repositories `harbor-api` and `harbor-web` and its planning repository `harbor-plan`. Put your own project and goal in their place.

## Before you start

- Finish [set up your projects](set-up-projects.md), the guide before this one: the project has its repositories, and one of them is its planning repository.
- The planning session runs in a terminal pane on the worker's machine, so that machine needs tmux, which [get started with the web UI](getting-started.md) installs.

## In the web UI

### 1. Start a planning session

In the sidebar, select Overview, then Start planning session. Roadmap, Plans, and Planning have the same button.

- In Goal, write what the plan is for, such as "Show every berth and its sailings on the public pages." Or read the goal from a file fills it from a file instead, and a goal left empty is one the session asks you for.
- Planning repository is where the session writes the plan. It starts at the project's planning repository, marked `(planning)`.
- Worker is the machine that runs the session. It starts at your worker when the installation has one.

![Start planning session, with the goal, the planning repository harbor-plan, and the worker](assets/screenshots/plan-01-light.png#gh-light-mode-only)
![Start planning session, with the goal, the planning repository harbor-plan, and the worker](assets/screenshots/plan-01-dark.png#gh-dark-mode-only)

Select Start planning session. The session writes the plan and its draft decisions on a branch of its own in the planning repository, and reads every other repository of the project, read-only. Nothing reaches a default branch until you approve the plan.

### 2. Read the Planning page

The Planning page opens on the new session. Its head names the goal, the project, the session's work order and lane, and the lane's state, with Talk to the planning session, which opens the conversation panel on it, and Detach. Below it, the session's pane shows its terminal, live from the worker, with Type into the session under it.

Beside the pane, Plan in progress has two tabs. Outline shows what the session has committed to its branch: here the goal it wrote first, and the question it asked you, marked Blocking, which is why it has not submitted a version yet. History shows the delivery history the session plans from, which is empty on a first plan.

![The Planning page, with the session's pane and Type into the session, and the outline with the goal and a blocking question](assets/screenshots/plan-02-light.png#gh-light-mode-only)
![The Planning page, with the session's pane and Type into the session, and the outline with the goal and a blocking question](assets/screenshots/plan-02-dark.png#gh-dark-mode-only)

### 3. Tell the session what you want

In Type into the session, type what the session should know, such as "Put the sailings under each berth, not on a page of their own." With Press Enter after the text checked, the line ends with Enter, as if you typed it in the pane. Select Send.

![The session's pane, with a line in Type into the session and Press Enter after the text checked](assets/screenshots/plan-03-light.png#gh-light-mode-only)
![The session's pane, with a line in Type into the session and Press Enter after the text checked](assets/screenshots/plan-03-dark.png#gh-dark-mode-only)

An answer you give in the session becomes a draft decision in the plan, which your approval decides. Here the Outline gains the plan's file and its draft decision, and the session asks a second question that your line leaves open.

Say what you want while the session's questions wait. When the session has what it needs, it writes the plan and submits it. A turn that ends without a submission waits for your next line, and the Planning page shows Waiting for you beside the session's state. When `ask_wait`, five minutes unless the profile says otherwise, runs out with no terminal attached, the session parks, and Needs you offers Resume.

With the field empty, Enter, Escape, and the up and down arrows go straight to the session, as do the keys under the field. Detach stops what you type from reaching the session, which keeps running, and Attach joins it again.

### 4. Answer its questions in Needs you

A session you start from the web UI has no terminal attached, so it asks its questions in Needs you, as product questions that no orchestrator can answer for you. The Planning page does not count as an attached terminal. Each question waits there as an item, and questions the session asks together wait as one item.

In the sidebar, select Needs you. It opens on the oldest item, here the session's first question, with its options, Something else for an answer in your own words, and Blocks work on a question the plan cannot go on without.

![Needs you, with the first question from the planning session, its options, and Something else](assets/screenshots/plan-04-light.png#gh-light-mode-only)
![Needs you, with the first question from the planning session, its options, and Something else](assets/screenshots/plan-04-dark.png#gh-dark-mode-only)

Choose an option, or choose Something else and write your answer. The button counts the answers that are ready, `Send 0 of 1 answer` until you choose. Select Send this answer. The answer is recorded once, as your decision, and the session hears it. The item stays on the page, saying you answered it, until you move on. Then select the next question, and answer it the same way.

### 5. Follow the plan in the Outline

In the sidebar, select Planning, which opens the session that runs. As the session commits, the Outline fills in:

- Documents: each file the branch adds, marked New, or changes, marked Changed.
- Milestones: each milestone, with its priority.
- Contracts: each contract, with how many criteria and work orders it lists.
- Draft decisions: each answer the plan drafts, with its question.

Only committed files show, so a file the session is still writing appears at its next commit.

![The outline, with the documents, two milestones, three contracts with their criteria and work orders, and one draft decision](assets/screenshots/plan-05-light.png#gh-light-mode-only)
![The outline, with the documents, two milestones, three contracts with their criteria and work orders, and one draft decision](assets/screenshots/plan-05-dark.png#gh-dark-mode-only)

### 6. Let the session submit the version

When the plan is finished, the session submits it as a version for your approval, and its lane ends. The Planning page then says No planning session is running. Once you approve the version, it shows that session's plan below, at the approved commit. The session never submits while a blocking question is open.

Needs you then lists the version as a plan approval, and only that: approving the plan accepts the session's work order, so the work order does not wait there as well.

### 7. Read the plan approval in Needs you

In Needs you, select the plan approval, `Approve Berths and sailings on the public pages, version 1?`. Its card shows who requested it, the version, the digest it binds to, and what it holds; how it differs from the version you approved last, here A first version, with every record it adds; and the draft decisions. Approve this plan approves it from the card, and Deny declines it with the reason you give.

![Needs you, with the plan approval card of version 1 and Open the full plan approval](assets/screenshots/plan-06-light.png#gh-light-mode-only)
![Needs you, with the plan approval card of version 1 and Open the full plan approval](assets/screenshots/plan-06-dark.png#gh-dark-mode-only)

### 8. Open the full plan approval

Select Open the full plan approval to read the version in full: who wrote it and when, the difference from the version in force, the draft decisions with the question each answers, the contracts it adds or changes with their criteria and work orders, and the documents it covers. For a first version from a planning session, the ledger holds the contracts and the documents once you approve it, so the page lists every record the version adds. Until then, the documents section can say The version covers no document, though the card counts the plan's files under Holds: it lists only documents the ledger already holds. When the plan's manifest lists a repository the project lacks, the page lists what approving would register.

![Plan approval of version 1, with the records it adds and the card Your decision](assets/screenshots/plan-07-light.png#gh-light-mode-only)
![Plan approval of version 1, with the records it adds and the card Your decision](assets/screenshots/plan-07-dark.png#gh-dark-mode-only)

### 9. Approve or decline the version

The card Your decision says what approving decides, and the digest it binds to: if the plan changes, the approval is void. Open the planning session's work order, under it, opens the session's work order.

![Your decision, with Approve version 1 and Decline](assets/screenshots/plan-08-light.png#gh-light-mode-only)
![Your decision, with Approve version 1 and Decline](assets/screenshots/plan-08-dark.png#gh-dark-mode-only)

Select Approve version 1. The card then reads Approved, with who approved it and when. Approving does three things at once:

- It records each draft decision as your decision.
- It passes the contract review of each work order the version covers, so you pass no review per work order.
- It accepts the planning session's work. Intake records the plan at the approved commit, and the plan's work orders appear.

To decline instead, select Decline…, and give the reason. The session reads it, and writes a new version when it can meet it.

### 10. See the plan on the Roadmap

In the sidebar, select Roadmap. It shows the goal with its plan and approved version, then each milestone in priority order with its exit criteria, its progress, and its state, and below them the work of each milestone in layers by what it depends on, with the critical path. Work with nothing to wait for is Ready, and work that waits for other work shows Contract review until that work is done. Each milestone also carries a forecast of when it ends. On a first plan it says no history, because none of its work has finished yet, and it gives dates once work has finished.

![The Roadmap's two milestones in priority order, each with its progress, its forecast, and its state](assets/screenshots/plan-09-light.png#gh-light-mode-only)
![The Roadmap's two milestones in priority order, each with its progress, its forecast, and its state](assets/screenshots/plan-09-dark.png#gh-dark-mode-only)

## The planning branch

The session's branch holds the plan, and merges like any change, at the planning repository's merge authority. At `person_merges`, the default, you merge it yourself when you choose. At `person_approves` and `review_approves`, ropeshift merges it at the approved commit. [A planning branch](onboarding.md#a-planning-branch) gives every case.

## Next

Approving the plan creates its work orders. [Watch work run](watch-work-run.md) starts them, and [Learn ropeshift](../README.md#learn-ropeshift) lists the guides.
