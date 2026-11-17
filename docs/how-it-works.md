# How ropeshift works

Ropeshift turns a goal into reviewed, merged changes. Agents do the work, and you decide what only a person can decide. This page walks through the parts in the order work passes through them, in plain words, and links each one to the page with the detail.

```text
  You and a planning agent
          |  a planning session
          v
  The plan: requirements, milestones, contracts, work orders     <- you approve it
          |
          v
  Ready work --> the orchestrator claims it --> a lane does it
                                                  |  questions climb the ladder
                                                  v
                                   a review lane judges each criterion
                                                  |
                                                  v
                     accepted and merged, at the merge authority you chose
```

## The idea

Agents can work for a long time on their own, and they stop whenever they need a person. Ropeshift tries to make that rare. It settles what it can from what is already written down: the plan, the requirements, and earlier decisions. It brings you only what is truly yours to decide, in batches, with a recommendation. The number it watches is human interruptions per completed work order, and `ropeshift measures` prints it ([product measures](measures.md)).

## The plan

Work starts from a plan, and the plan lives in git, in files you can read and review like any other change.

**The planning session.** You start one with a goal, such as "Let partners see their own usage". A planning agent asks you what it needs to know, reads the code as it is, and writes the plan as files on a branch of its own. Your default branch does not change. You can talk to it in a terminal or answer its questions in the web UI ([plan your first change](plan-your-first-change.md)).

**What the plan holds.** Each file is Markdown with a small header that says what kind of file it is ([project structure](project-structure.md#the-repository-file-format)):

| Part | What it is |
|------|------------|
| Requirements | What the product must do, each with a stable ID such as `PAY-12` |
| Specs, architecture, experience designs | How it is built, citing the requirements they address |
| Decision records | A choice already made and why, with a status of proposed, accepted, superseded, or rejected |
| Milestones | Steps in priority order, each waiting for the milestones it depends on |
| Contracts | What one piece of work must deliver, as criteria a reviewer can check, each criterion serving a requirement |
| Work orders | The units an agent carries out under a contract, each in one repository |

**Plan approval.** When the session finishes, the plan waits for you as one version. You read what it holds and how it differs from the last version you approved, then approve it or decline it with a reason. Approving decides the answers you gave in conversation, and makes the plan's work ready to run. Nobody else can approve a plan: not the agent that wrote it, and not an orchestrator ([plan approval](web-ui.md#plan-approval)).

## Contract review and ready work

Before any work starts, its contract is reviewed, so questions come up before an agent spends time, not halfway through. Approving a plan passes the contract review of every work order it covers. A contract you write by hand, outside a plan, waits for a review of its own.

Work becomes `ready` only when it passes a definition of ready: its contract is reviewed, no blocking question is open, its repository and agent exist, and every criterion serves a requirement of its plan. Anything that holds it back is named on the work order.

## The orchestrator and lanes

**A lane** is one agent session doing one work order: one writer, in one git worktree, on a branch of its own. It never touches your checkout. It starts from the repository's base branch, your default branch unless you name another, and commits on its own branch ([where a lane starts](onboarding.md#where-a-lane-starts)). A lane can read the other repositories of its project, read-only, so it works against the code it builds on.

**A lane that cannot go on parks.** It saves its work, stops its process so it uses no quota, and resumes when its answer arrives.

**The orchestrator** is one agent per machine that keeps the work moving. It claims ready work, starts lanes, starts a review when a lane finishes, and settles questions within the decision policy you set. A worker that runs on a Claude subscription starts at `attended`: its orchestrator claims work only inside a run window you open ([Claude subscriptions and automated use](onboarding.md#claude-subscriptions-and-automated-use)). You can also claim and start any work order by hand.

**A deterministic daemon** keeps each session healthy, starts a lane again after a crash, and fences out a lane that lost its claim, so two writers never write the same work.

## Review

An agent never approves its own work. When a lane finishes with a commit, ropeshift opens a review of that commit, and a second, read-only lane judges every criterion of the contract: `pass`, `fail`, or `cannot_judge`, each with reasons and citations to files and lines. A failed criterion sends the work back for another attempt, with the findings in its first prompt. After three failed reviews, the work waits for you ([who reviews a lane's work](onboarding.md#who-reviews-a-lanes-work)).

## Merge authority

What happens after a passing review is your choice, set per repository:

| Level | What you do |
|-------|-------------|
| `person_merges`, the default | You accept the work and merge it yourself |
| `person_approves` | You approve the merge, and ropeshift merges the reviewed commit |
| `review_approves` | The review's pass is the approval, and ropeshift merges once every required check is green |

Ropeshift merges only the exact commit the review read, never through an agent, and records every approval and merge. Work merges after the work it depends on, in its own repository or another ([merging accepted work](onboarding.md#merging-accepted-work)).

## Projects of several repositories

Many products are more than one repository: an API, a web client, a shared library, and a repository that holds the plan. Ropeshift treats them as one project with one plan ([set up your projects](set-up-projects.md)).

**One planning repository.** The plan of record lives in one repository of the project, its planning repository, and intake reads plan files only from there. Planning sessions run there.

**One repository per work order.** A contract lives in the planning repository, and each of its work orders names the repository it changes with `repo`. Work that spans repositories is several work orders joined by dependencies ([a contract across repositories](project-structure.md#a-contract-across-repositories)).

**Dependencies across repositories.** Work that depends on work in another repository becomes ready only once that work is `done`: once its merge landed, or, in a repository at `person_merges` with review target `branch`, once you accepted it, because ropeshift cannot see a merge you make yourself. Within one repository, a repository set to start from `accepted` lets dependent work start from accepted work before it merges. That never applies across repositories, because a build in one repository cannot use an unmerged branch of another.

**Read-only reads.** A lane writes only in its own repository. It also reads, read-only, each repository its work order's dependencies target, at the commit ropeshift's merge of that dependency made, or at the base branch tip when ropeshift recorded no merge, and each repository its contract names in `reads`, such as `reads = ["acme-api"]`, at the base branch tip. The worker checks each one out beside the lane's worktree, in a folder named after the lane with `.reads` after it, and the lane's brief names them. The lane may read them and may not edit, commit, push, or change branch there, and the worker removes them when the lane ends. The review of that lane reads the same repositories at the same commits, so it judges the work against the code the writer saw. A planning session reads every repository of the project at its base branch tip.

**Merge order.** Changes merge one by one, in dependency order, within and across repositories. Approved work whose dependency is not yet `done` waits in `integrating`, and `ropeshift work show` prints a `waiting` line naming each dependency with its repository and state. At `person_merges` with pull requests, the same line tells you which pull request to merge first. Merges across repositories are not atomic: each lands on its own. A milestone whose work spans repositories is done once its work has merged in every one. In a repository at `person_merges` with review target `branch`, the roadmap shows such a milestone as accepted, not merged ([merging accepted work](onboarding.md#merging-accepted-work)).

## Questions and decisions

When a lane has a question, the question climbs a ladder, and a person is the last rung:

1. Look it up in existing decisions, requirements, contracts, and accepted decision records. A match answers it.
2. Match it against open questions, so the same question is never asked twice.
3. The orchestrator decides it within the decision policy, and records why.
4. If the choice can be undone, the orchestrator records an assumption and goes on. You can overturn it later.
5. Only then does it reach you: a one-way decision, a conflict between requirements, or a change of scope.

A decision record kept as a file, such as an architecture decision record, takes part in the first rung while its newest version has the status `accepted`. The orchestrator reads its title and body and cites it as `decision:<id>`, the ID its front matter gives. A record that is proposed, rejected, or superseded answers nothing, and one whose newest version leaves `accepted` leaves the lookup with it.

Questions reach you in batches, each with its options and a recommendation. Every answer is recorded as a decision with its rationale, so the next lane that asks finds it on the first rung. One-way decisions about security, compliance, and data always come to you. A planning session's questions come straight to you too, because the plan is yours ([decisions in the web UI](web-ui.md#needs-you)).

### What a decision does to work

A decision names what it binds in its scope: `plan:<id>` for a plan, its contracts, and the work under them, `contract:<id>` for a contract and the work that pins it, `wo:<id>` for a work order and its rework, and `repo:<id>` for the work that runs in a repository. An answer takes the scope of its question, so the answers of a plan review bind the plan. Ropeshift finds the work through the ledger, never from the text of an ID, and a decision with no scope binds nothing. A scope a person or an orchestrator names must name a record of its kind that the ledger holds, a repository by its identity, or the decision is refused, so a mistyped ID never binds nothing in silence.

A decision that is in force and binds work acts on it in the transaction that records it, whether it is reversible or not and whoever recorded it:

| Where the work stands | What happens |
|-----------------------|--------------|
| Not started, and `ready` | It returns to contract review. When a person recorded a one-way decision, the work waits there until a member passes its review again, which reads a choice nobody can take back. Any other decision holds nothing, and the next tick passes the review again with the decision in what it reads |
| Claimed, running, parked, lapsed, or blocked | Its lane is told, and its next context pack holds the decision. A lane that asked the question the decision answers reads the answer in place of a notice, and goes on. Any other lane goes on or parks as the [lane policy](cli.md) says |
| Finished: waiting for acceptance, integrating, or done | A one-way decision, and an overturn, start one rework of it. A reversible decision lists it on record, and a member reworks what it touches |
| Failed or canceled | Nothing. A retry starts from a pack that holds the decision |

A plan, a contract review, and a review are never moved or reworked by a decision. Their lanes are told. A batch of decisions starts at most one rework of a finished work order. A rework that has not finished is the end of its chain, so it takes the later decisions instead of a second rework beside it: one that has not started reads them from its pack, and one with a lane is told and has its pack renewed. Work that was already reworked and finished is reworked once more at the end of its chain of rework, never at each link.

The lane policy's `on_binding_decision` is `notify`, the default, or `park`, and it answers for every decision that binds a lane's work. A policy that sets `on_one_way_decision` answers for one-way decisions instead, so a team can park on what cannot be undone and tell on the rest.

## The ledger and Needs you

**The ledger** is ropeshift's record of execution: a local database for each project that holds claims, lanes, outcomes, reviews, questions, decisions, approvals, and merges, each with who did it. The plan stays in your repository, and the ledger records what happened to it.

**Needs you** is the one list of everything that waits for a person, across every project, oldest first: a plan to approve, questions, work to accept, a merge to approve, a gated command a lane asked to run, and stuck work. The web UI opens on it, `ropeshift needs` prints it, and `ropeshift top` watches it in a terminal.

## Where you come in

| When | What you do | Where |
|------|-------------|-------|
| A goal needs a plan | Talk with the planning agent | A terminal, or Needs you |
| A plan version is ready | Approve it, or decline it with a reason | Needs you, or `ropeshift plan approve` |
| A question reaches the last rung | Answer it | Needs you, or `ropeshift question answer` |
| A lane asks to run a gated command | Allow that one call, or deny it | Needs you, or `ropeshift approval approve` |
| A review passes | Accept, approve the merge, or nothing, by merge authority | Needs you, or `ropeshift work accept` |
| Work is stuck | Resume, retry, replan, or cancel it | Needs you, or Floor |

Everything else runs without you.

## Next steps

- Install it and sign in to the web UI: [get started with the web UI](getting-started.md).
- Create your projects and register their repositories: [set up your projects](set-up-projects.md).
- See how to lay out your repositories: [project structure](project-structure.md).
