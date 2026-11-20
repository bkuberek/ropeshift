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

**What the planning agent knows of your repositories' practices.** A planning session, and an adopt session, begins with what the [practices check](cli.md#commands) last found in each active repository of the project: the commit it read and how long ago, each open finding with its ID, severity, detail, and fix, each finding you acknowledged with your reason, and each practice the check could not settle. A repository no check has reported reads as not checked yet. The agent proposes foundation work for each open finding: a contract in the plan's first milestone whose statement names the finding IDs it fixes, with a criterion that a practices check at the merged commit no longer reports them. It proposes none for an acknowledged finding. Some findings need a person, because a lane holds no forge credential: `branch-protection`, `required-review`, rotating a leaked credential, and choosing a `license`. The agent lists those as steps for you in the plan's goal, each naming its finding ID, and proposes a work order only for the part a lane can do, such as removing a secret from a tracked file after you rotated it, or adding a `LICENSE` file after you chose the license in answer to its question. A finding never blocks the plan or its approval.

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

**A generation's digest** is what the worker records when a generation of a lane, an orchestrator, or an assistant ends: its turns, how many calls it made to each tool, the paths its file-writing tools were called with, when it started and ended, and how long its turns took, plus how long the model's API took on a headless session. The worker builds it from events it already receives, the stream of a headless session and the hooks of an interactive one, so it adds work to neither. It holds no command, no file's contents, no prompt, and no reply: a shell command counts as a call to the shell and nothing more, so a file a shell command wrote is not in its list of paths, which stops at the first 200, and the branch's diff shows that file. A figure a session cannot give is absent and never zero. A digest marked incomplete says the worker knows it missed events, such as a line it could not read. The ledger keeps one digest for each generation, in the project's records and, for an orchestrator, in the installation's, and a backup carries it. Nothing turns it off.

## Waiting costs no credits

A session that waits spends its credits on nothing: every turn it takes while it polls or sleeps reads its whole context again. Ropeshift never keeps a session alive to wait. Plain code waits instead, and a lane waits live for at most `max_live_wait`, five minutes by default.

| A lane waits on | What happens |
|-----------------|--------------|
| CI, a review, or a merge | Nothing: the lane reports its work and ends, and ropeshift starts the next step once the result is in |
| A person | The lane asks, ends its turn, and parks after `ask_wait` |
| A timer or an outside process, up to `max_live_wait` | The lane calls `lane_wait` and stays live |
| A timer or an outside process, longer | `lane_wait` answers `parking`, the lane ends its turn, and the worker parks it within one pass, and a restart of the worker in between still parks it. The lane wakes when the wait ends, or when you resume it |

A parked lane has no process and makes no model call until it wakes.

Each lane's hook also refuses a shell command that waits by construction, and tells the lane what to do instead. It reads what a command does and never how long it runs, so a long build or test run is never refused:

| Rule | What it refuses |
|------|-----------------|
| W1 | A `sleep` of `max_tool_sleep` or longer, 60 seconds by default, alone or inside a compound command |
| W2 | A `while`, `until`, or `for` loop whose body runs `sleep`, whatever its length |
| W3 | `watch`, `gh run watch`, and `gh pr checks --watch` |
| W4 | A read of CI status: `gh pr checks`, `gh pr view`, `gh run view`, `gh run list`, and `gh api` on a check, status, or run path |

The hook applies the rules from the generation's policy file, so it refuses with the worker stopped and inside the lane's subagents. Each refusal is a `wait_denied` event on the work order with the rule's ID, the lane, and the generation, and never the command. A rule inside a script the lane runs is not seen. The settings are in [spend settings](set-up-projects.md#spend-settings).

## A work order has a spend limit

Every `change`, `rework`, and `review` work order has a soft limit and a hard limit on what it is charged, across every lane that served it, so a retry or a relaunch never starts a fresh count. Charged spend is the cost the generations reported at list price, plus each turn that reported none at the mean cost of the generation's known turns. A turn that stopped is one turn: it is charged at its own estimate when that is above the mean, and at the mean when it is not, and never both. Charged spend only grows. A known turn that costs little lowers the mean the stopped turns are charged at, so the ledger keeps the highest charge each generation reached and charges no less than that, and a work order a hard limit stopped is not taken back under it by a cheap turn that lands after the stop. The limits come from the work order's cost band, or from its size when it has none, and they are settings ([spend settings](set-up-projects.md#spend-settings)). A soft limit warns and stops nothing. The hard limit stops the lane:

1. The worker reads the lane's spend at every report that shows what the session has spent so far, during a turn, and at the end of every turn. A headless session reports no total until a turn ends, so during a turn the figure is estimated from the usage of each model call the session has made, at list price, and never below it: a cache write at the one-hour rate, the dearest rate of any model of the call's family a session can still run (an ID that names Opus 4, Opus 4.1, or no Opus version is priced at Opus 4's), the rate of a regional cloud endpoint when the ID is a Bedrock or Google Cloud ID that does not name the global one, the long-context rate for a call over 200,000 input tokens on a model that has one, twice the rate for a call whose usage says it ran in fast mode or whose model ID ends in `-fast`, $10 for each 1,000 web searches the call's usage counts, and for a model it does not know the highest rate of any family. Each call counts once, whether the session reports it in its stream events, in its assistant messages, or both, and a subagent's calls count with the lane's. The qualification of a version records whether its stream's usage events were observed ([qualification](qualification.md)), and where they were not the estimate rests on the assistant messages. The estimate is a bound, and the total that ends the turn replaces it. What the lane's earlier generations were charged counts too, as the ledger holds it, so a lane that a second machine resumes is held to the same limit. Once a report shows the work order at its hard limit, no tool call of the lane starts: the next one is refused with a reason that tells the lane to hand off, and that hold is in place before the handoff's first upload, so a slow control plane leaves no window.
2. The lane is asked once to hand off. It spends nothing else: that one call is bounded by `handoff_timeout`, and a lane that does not answer in time hands off with only what the worker can record.
3. The lane parks with the trigger `spend_limit`. It has no process and makes no model call. It wakes when the work order's hard limit is raised above what it was charged, or when a person resumes it. Only a member wakes it: a delegate's `lane resume` of a lane that waits on a limit is refused. A raise, a new version of the spend policy, and a revert each settle that in the transaction that changes the limit, so the lane wakes once, and a raise that arrives while the lane is still parking is read at its parked commit. A worker that restarts in between still parks the lane, and keeps what stopped it, so the park never reads as a raise. A raise to a limit that stays at or below what the work order is already charged is refused, because it would wake the lane only to stop it again.

Each generation also checks the limit as it starts. One that begins with nothing left to spend, because it crossed while a handoff was in flight, or because a member raised the limit too little, parks before its first turn, with no process and no model call. A worker that cannot read what the work order allows runs the lane for 30 seconds, asking again every 5, and then holds it and parks it with the trigger `spend_unreadable`, which wakes on the same terms: a limit nobody can read is not a limit.

A headless generation also starts with `--max-budget-usd`, set to what remains of the hard limit plus `budget_flag_headroom_share` of the limit, on a version whose qualification observed the flag ([qualification](qualification.md)). It is a backstop for a worker that is slow to stop the lane, and never the limit: the client counts only its own estimate and only its own process. A turn the client ends at that cap parks the lane the same way. A pane has no such flag, and a version that never passed the check runs on the worker's hold alone.

You raise the limit with `ropeshift spend raise <work-order> --by 50` or `--to 80`, which lifts it by a share of the limit in force or to an amount, or let the lane go on for one more generation with `ropeshift spend resume <work-order>`. Each prints the limit before and after, and each is recorded that way on the work order's spend. A resume sizes one more generation by the mean charge of the generations that recorded spend, or by the work order's estimate when none did, says which, and refuses work with neither, so that you raise it instead. Raising a limit and resuming a lane are a member's alone: an orchestrator has no tool for either, and a delegate's call is a confirmation card that no delegate policy exempts ([authorization](authorization.md)).

A stop does not have to end in more spending. `ropeshift spend block <work-order>` blocks the work order with the reason `spend_limit`, and `ropeshift spend replan <work-order>` sends it to planning, where its contract is reviewed again. Each ends the lane the limit parked and retires its wake, so a raise made later wakes nothing, and each is a member's alone as a raise is. A work order a limit parked is one item in Needs you, however many lanes served it, and its limits, band, and what each lane left are on it ([the web UI](web-ui.md)). It counts as one interruption in the [product measures](measures.md).

A lane never sees its band, its limits, or what remains. It learns of a limit from the refusal at the hard stop and from nothing else. An orchestrator cannot raise a limit, and `lane_restart`, `lane_reset`, and `work_retry` are refused for work at its hard limit, with a reason that names the limit and no request for you to grant past it. Your own `ropeshift work retry`, `ropeshift lane reset`, and `ropeshift lane respawn`, and your grant of a restart request raised before the limit was reached, are refused the same way, as is an orchestrator's stop of a lane, with a message that names the limit and says to raise it with `ropeshift spend raise`: the limit is what stops the spending, so raising it is the decision, and it is yours. Work a hard limit stopped is marked `spend_limited` in its delivery record, and every stop is an event on the work order's spend.

## A project has a daily and a weekly limit

`project_daily_limit` and `project_weekly_limit` are ceilings on what a project's lanes are charged in a day and in a week. Both are unset by default, because ropeshift cannot know the scale of your project: set them with `ropeshift spend set project_daily_limit=80`. By default a day is the UTC calendar day, and a week is the ISO week, which starts on Monday at midnight UTC. Ropeshift reads one clock, the ledger's, so every worker and every screen agrees on when a day ends, whatever time zone you or a machine is in. The figure counts the same charged spend a work order's limit does, summed over every lane of the project's work orders that reported it in the window. Spend counts toward the window in which its report reaches the control plane, and not the moment it was spent: a lane reports while a generation runs only when its project has a limit, and otherwise when the generation ends, so spend that crossed midnight is in the window of the report that carried it. An assistant's chat has no part in it: it has a soft daily warning of its own.

A project past a limit stops starting work, and a limit of 0 is none:

1. No work order of the project is claimed or has a lane started. A claim is refused with the limit, what the project was charged, when the window ends, and the command that raises it. A worker is offered no work from the project.
2. Every running lane of the project is held at its next tool call, asked once to hand off, and parked with the trigger `spend_limit`, exactly as a lane at its work order's hard limit is, because the park is the same one. A generation that begins in a held project parks before its first turn, with no process and no model call.
3. The lane wakes once, when the window ends or when you raise the limit, whichever comes first. It then goes on by the ordinary claim, so work resumes the next day with no one there.

A lane that runs in a project with a limit tells the ledger what it has spent about once a minute, and asks again whether the project is held, because a project's spend is every lane's and the ledger knows a generation's only when its worker says. A lane overshoots a project's limit by no more than it spends in that minute. A project with no limit asks nothing of its lanes before a generation ends, except that each lane reads again, about once a minute, whether you have set one, so a limit you set or lower while lanes work reaches them at their next tool call after that minute, and not when their generation ends. A lane that cannot get an answer from the control plane for 30 seconds no longer holds the word that its project had room: it is held and parked like a lane whose work order's limit it cannot read, with the trigger `spend_unreadable`, and it wakes as soon as the ledger can say it is under its limits.

You raise the limit of the window you are in with `ropeshift spend raise --window day --to 120` or `--by 50`. It prints the limit before and after, and it is recorded that way. A raise lifts the day or the week it was made in, and the next one starts from the limit in your policy again, because a limit that stays raised is a setting, and `ropeshift spend set` changes a setting. A raise has the same terms as a work order's: it is a member's alone, an orchestrator has no tool for it, and a delegate's call is a confirmation card that no delegate policy exempts ([authorization](authorization.md)). A project held by both limits goes on when both are lifted or have ended. A project's limit never reaches another project: each project's tally lives in its own ledger.

Reaching a soft limit, `project_warn_share` of each limit and 0.8 by default, and reaching a hard limit each raise an alert, once for each limit and window, as a work order's limits do. So do the assistant's `assistant_daily_warn`, the orchestrator's `orchestrator_daily_warn`, a published change of a spend setting, and a raise. The web UI shows every alert, and you choose which others carry each kind, such as the desktop or a webhook, in `[notify_policy]` ([spend alerts](notifications.md#spend-alerts)).

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

How far the orchestrator goes depends on the [worker's autonomy level](authorization.md#who-can-do-what). On a worker you set to `unattended`, you left it the run of the work, so once a member accepts the worker's one-way acknowledgement, it decides a one-way choice too, as long as the decision policy keeps none of the question's categories: it records the choice as a one-way decision with its rationale and citations, and the gate refuses to bring it to you. The acknowledgement reads "The orchestrator will make one-way decisions outside security, compliance and data for you.", every worker needs it once, one on an API key too, and it is apart from the stated risk of a subscription; until a member accepts it in Settings or with `ropeshift worker acknowledge-one-way`, every one-way choice reaches you at `unattended` too. Such a decision blocks no work: Needs you shows it for review, where Keep records it as yours and Overturn reverses it. Only a one-way choice in a category the policy keeps, a conflict between requirements, a change of scope, and the plan itself reach you. On an `attended` or `manual` worker, every one-way choice reaches you. Every decision an orchestrator records names it as the decider and states why, and you can overturn it from Decisions. To keep a kind of choice with you whatever the level, add its category to the policy's one-way categories ([choose what an orchestrator decides](settings-and-agents.md#4-choose-what-an-orchestrator-decides)).

A decision record kept as a file, such as an architecture decision record, takes part in the first rung while its newest version has the status `accepted`. The orchestrator reads its title and body and cites it as `decision:<id>`, the ID its front matter gives. A record that is proposed, rejected, or superseded answers nothing, and one whose newest version leaves `accepted` leaves the lookup with it.

Questions reach you in batches, each with its options and a recommendation. A lane may give its question options, the one it recommends, and why, and its question keeps them when the orchestrator brings it to you without options or a recommendation of its own; one the orchestrator states replaces the lane's. A recommendation the lane gives must be one of its options. Every answer is recorded as a decision with its rationale, so the next lane that asks finds it on the first rung. One-way decisions about security, compliance, and data always come to you. A planning session's questions come straight to you too, because the plan is yours ([decisions in the web UI](web-ui.md#needs-you)).

### What a decision does to work

A decision names what it binds in its scope: `plan:<id>` for a plan, its contracts, and the work under them, `contract:<id>` for a contract and the work that pins it, `wo:<id>` for a work order and its rework, and `repo:<id>` for the work that runs in a repository. An answer takes the scope of its question, so the answers of a plan review bind the plan. Ropeshift finds the work through the ledger, never from the text of an ID, and a decision with no scope binds nothing. A scope a person or an orchestrator names must name a record of its kind that the ledger holds, a repository by its identity, or the decision is refused, so a mistyped ID never binds nothing in silence.

A decision that is in force and binds work acts on it in the transaction that records it, whether it is reversible or not and whoever recorded it:

| Where the work stands | What happens |
|-----------------------|--------------|
| Not started, and `ready` | It returns to contract review. When a person recorded a one-way decision, the work waits there until a member passes its review again, which reads a choice nobody can take back. Any other decision holds nothing, and the next tick passes the review again with the decision in what it reads. When a member's decision binds work of an approved plan, the plan [needs revision](#a-plan-that-needs-revision), and the work that decision binds waits in contract review until a member settles it |
| Claimed, running, parked, lapsed, or blocked | Its lane is told, and its next context pack holds the decision. A lane that asked the question the decision answers reads the answer in place of a notice, and goes on. Any other lane goes on or parks as the [lane policy](cli.md) says |
| Finished: waiting for acceptance, integrating, or done | A one-way decision, and an overturn, start one rework of it. A reversible decision lists it on record, and a member reworks what it touches |
| Failed or canceled | Nothing. A retry starts from a pack that holds the decision |

A plan, a contract review, and a review are never moved or reworked by a decision. Their lanes are told. A batch of decisions starts at most one rework of a finished work order. A rework that has not finished is the end of its chain, so it takes the later decisions instead of a second rework beside it: one that has not started reads them from its pack, and one with a lane is told and has its pack renewed. Work that was already reworked and finished is reworked once more at the end of its chain of rework, never at each link.

An answer can name the decision in force it replaces. Answering and replacing are one transaction: the old decision ends `replaced`, naming the answer, and what the answer binds is applied once. A draft decision that a member's decision replaced or overturned outside the plan is not decided again by a later version of the plan while its text is unchanged, and that version's approval lists it as replaced and names the decision in force.
The lane policy's `on_binding_decision` is `notify`, the default, or `park`, and it answers for every decision that binds a lane's work. A policy that sets `on_one_way_decision` answers for one-way decisions instead, so a team can park on what cannot be undone and tell on the rest. It does not answer for a one-way decision an orchestrator made at `unattended`, which blocks no work and answers to `on_binding_decision` as any other decision does. A decision that replaces one with the same content, as Keep does, binds nothing anew.

### A plan that needs revision

A decision a member records, answers, or overturns, whose scope contains work of an approved plan version, marks that plan as needing revision. The plan keeps one open revision at a time. It lists the decisions made since the approved version, in the order they were made, and the finished work the reversible ones reached. A decision an orchestrator records within policy marks nothing, because covered work already proceeds under policy, and neither does a decision that binds no work of an approved plan.

While a revision is open, the work its decisions bind, by their scopes, waits in contract review, and the plan's other work goes on. The plan shows once in Needs you with its decisions, the finished work they reached, and two choices:

- **Confirm** records that the approved version stands with those decisions. It closes the revision and runs the definition of ready again for the work that waited.
- **Revise** starts a planning session whose brief lists the decisions with their statements and scopes. The version it writes needs its own approval, which never inherits the earlier one and shows those decisions beside the difference from the approved version. Approving it closes the revision for the decisions the approval showed, which the approval records as the decisions the revision listed when the member approved: the order the ledger recorded them in decides, never a clock, so a decision recorded in the same millisecond after the approval, or at an earlier time because the host's clock was set back, is not one it showed. A decision recorded after the approval opens a new revision, since that version, and the work it binds keeps waiting. An approval with no such record, as one made by an earlier release has, splits the revision by time. The approval's page goes on showing the decisions it settled. A finished work order that decisions on both sides of the approval reached is listed in both revisions, and the rework a member made of it stays with the revision of the decisions it was made for, so the new revision offers rework for the later decisions.

A reversible decision does not rework finished work by itself, because that would rework every finished work order of a plan for each decision. The revision lists the work it reached, and the card offers Create rework for each finished work order, which starts one rework naming the decisions that reached it. The same is `ropeshift work decision-rework`. The card names only the decisions still in force, and offers no rework of work that only overturned decisions reached. A one-way decision, and an overturn, rework the finished work they reach at once, so the revision lists none for them. Only a member with a session confirms or revises a plan or makes that rework: no delegate, orchestrator, or planning lane can ([the command line](cli.md) names each command).

## The ledger and Needs you

**The ledger** is ropeshift's record of execution: a local database for each project that holds claims, lanes, outcomes, reviews, questions, decisions, approvals, and merges, each with who did it. The plan stays in your repository, and the ledger records what happened to it.

**Needs you** is the one list of everything that waits for a person, across every project, oldest first: a plan to approve, questions, work to accept, a merge to approve, a gated command a lane asked to run, and stuck work. The web UI opens on it, `ropeshift needs` prints it, and `ropeshift top` watches it in a terminal.

## Where you come in

| When | What you do | In the web UI | On the command line |
|------|-------------|---------------|---------------------|
| A goal needs a plan | Talk with the planning agent | Start planning session, then the Planning page | `ropeshift plan start` |
| A plan version is ready | Approve it, or decline it with a reason | Needs you: Approve this plan, or Decline… | `ropeshift plan approve` |
| A question reaches the last rung | Answer it | Needs you: choose an answer and send it | `ropeshift question answer` |
| A lane asks to run a gated command | Allow that one call, or deny it | Needs you: Allow this one call, or Deny | `ropeshift approval approve` |
| A review passes | Accept, approve the merge, or nothing, by merge authority | Needs you: Accept, or Approve merge | `ropeshift work accept` |
| Work is stuck | Resume, retry, replan, or cancel it | Needs you, Stuck: the card's actions | `ropeshift lane resume` and `ropeshift work retry` |

Everything else runs without you. [The CLI for power users](cli-for-power-users.md) gives the command for each action of the guides.

## Next steps

- Install it and sign in to the web UI: [get started with the web UI](getting-started.md).
- Create your projects and register their repositories: [set up your projects](set-up-projects.md).
- See how to lay out your repositories: [project structure](project-structure.md).
