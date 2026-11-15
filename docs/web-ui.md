# Web UI

The web UI is where a member decides what waits for a person, reads the plan and its documents, watches the lanes, talks to the sessions, and changes the settings. The daemon serves it at `http://127.0.0.1:7317`, on loopback only; `--web` or `ROPESHIFT_WEB` chooses another loopback address, or `off` ([the daemon's flags](cli.md#the-daemon)). Open that address in a browser on the same machine and sign in with the passphrase `ropeshift init` asked for, or `ropeshift passphrase set` set ([onboarding](onboarding.md#run-ropeshift-init)). Every view has the same frame: the project switcher (`P`), the command palette (`Ctrl K`, or `⌘ K` on macOS), the count of what needs you, the conversation panel (`.`), and a sidebar that groups the current project's views under Plan, Work, Talk, and Project, then the installation's. `?` lists every keyboard shortcut. The page follows the system's light or dark setting until you choose a theme.

The screenshots below are `v0.1.0-alpha.15` running locally with sample data.

## Needs you

![Needs you, with the waiting items oldest first and the selected work's review verdicts beside them](assets/screenshots/needs-you-light.png#gh-light-mode-only)
![Needs you, with the waiting items oldest first and the selected work's review verdicts beside them](assets/screenshots/needs-you-dark.png#gh-dark-mode-only)

Needs you lists everything that waits for a member in every active project, oldest first, and is the page the web UI opens on. The filters narrow the list to Decisions, Approvals, Cards, or Stuck, and the project menu to one project. Selecting an item opens its card beside the list, with what it asks, why it waits, and the evidence for it. `J` and `K` move through the list, and `A` moves focus to the card's primary button.

What a member does depends on the kind of item:

- **Work waiting for acceptance**: read the review's verdict on each criterion, then Accept, or Send back for rework.
- **Stuck work**: work that is blocked, parked, stalled, lapsed, tripped, or failed offers the actions that fit its state, such as Resume, Reset the breaker, Start the next generation, End this claim, Back to ready, Back to planning, Retry, or Cancel this work.
- **Failed work**: a failed lane leaves its work failed, and nothing retries it on its own, so it waits here from the time it failed. Retry asks why, as `ropeshift work retry` does. Work that used every attempt it was given shows Retry with an extra attempt, because only a member can add one. The ledger does not cancel failed work, so its card offers no Cancel.
- **Questions**: a batch an orchestrator could not settle, with its recommendation marked. Choose an answer or write Something else, then send the batch.
- **Approval requests**: a lane asks to run one call. Allow this one call, or Deny, with an optional reason the lane reads.
- **Merge requests**: at `person_approves`, the merge of a work order's reviewed commit waits for you. The card names the repository, the pull request or the branch and the base it lands on, the reviewed commit the approval is bound to, the merge method, and the merge authority, and it says what each choice does. Approve merge records your approval of that commit and asks the worker to merge it, and Deny sends the work back to rework, with your reason in its brief. A merge the forge or git refused is a held merge, which says why it stopped: Retry merge tries it again, and Deny sends the work back. A new commit on the branch, or work that leaves integration, voids the request. The review's verdicts and checks are on the work order, which the card links to, and the same card shows on the work order's page.
- **Plan approval and contract review**: approve a plan version, or pass a contract review or send the work back to planning.
- **Uncertain effects**: say whether an effect a crash left unknown happened, did not happen, or should run again.
- **Confirmation cards**: a delegate or the assistant prepared a consent operation. Read the exact operation and Confirm or Decline.

[Authorization](authorization.md#who-can-do-what) says who may decide each kind, [delegates](authorization.md#delegates) explains confirmation cards, and [the command line](cli.md) has a command for each decision, such as `ropeshift work accept` and `ropeshift approval approve`.

## Roadmap

![Roadmap, with a plan's milestones in priority order, their progress, and a banner for the version that waits](assets/screenshots/roadmap-light.png#gh-light-mode-only)
![Roadmap, with a plan's milestones in priority order, their progress, and a banner for the version that waits](assets/screenshots/roadmap-dark.png#gh-dark-mode-only)

Roadmap shows the project's goals in priority order, each with its plans and the version of each plan in force. Under each plan come its success measures, its milestones in priority order with how much of each milestone's work is done and how the rest stands by state, and each milestone's work by dependency layer, with the critical path outlined. When a newer version of a plan waits for approval, a banner says so, and Review version opens its plan approval page.

Start planning session opens a planning session from here, as `ropeshift plan start` does: give the goal as text or read it from a file, then choose the planning repository and the worker. [Start a planning session](onboarding.md#start-a-planning-session) describes what the session does and how its plan reaches you.

## Plan approval

![Plan approval, with the difference from version 1, the draft decisions, and the card that approves or declines the version](assets/screenshots/plan-approval-light.png#gh-light-mode-only)
![Plan approval, with the difference from version 1, the draft decisions, and the card that approves or declines the version](assets/screenshots/plan-approval-dark.png#gh-dark-mode-only)

Plan approval is the page for one version of a plan, at `/projects/<project>/plan?id=<plan>&version=<n>`. It opens from the plan's card in Needs you, from the Roadmap's banner, and from Plans. It says where the version came from and its digest, then shows the difference from the version in force, the draft decisions the version holds with the question each answers, the contracts it adds or changes with their criteria and work orders, and the documents it covers. For an adopted project, it also shows what the adoption does, grouped.

The card beside it says what approving does, and binds the approval to the version's digest. Approve version records each draft decision as yours and passes the contract review of the work the plan covers. Decline asks for a reason; the session that wrote the plan reads it, and writes a new version when it can meet it. A version a newer one replaced is void, and a version already decided is read-only. [Start a planning session](onboarding.md#start-a-planning-session) and [adopt an existing project](onboarding.md#adopt-an-existing-project) describe the same approval from the command line.

## Documents and Traceability

![Documents, with a requirements document in the reader and the trace of one requirement to its criteria, work orders, and verdicts](assets/screenshots/documents-light.png#gh-light-mode-only)
![Documents, with a requirements document in the reader and the trace of one requirement to its criteria, work orders, and verdicts](assets/screenshots/documents-dark.png#gh-dark-mode-only)

Documents holds the project's requirements, specs, designs, and other documents, and has two tabs: Reader and Traceability.

- **Reader** lists the documents by kind and shows one at a time, at the version you choose: its file, digest, review state, and the plan that approved it. Compare with shows how two versions differ, and the link button copies the document's address. A document's text comes from a repository, so the reader shows it as text only. In a requirements document, each requirement shows where it stands, and selecting one opens its trace beside the reader: the criteria that serve it under their contracts, the work orders that carry them, and the verdicts on that work with the commit each judged.
- **Traceability** is a matrix of every requirement, with its status, its criteria, its work orders, and its latest verdict, or the evidence when existing code already meets it. Filter it by status or by plan.

Both tabs are for reading. `ropeshift document` and `ropeshift requirement` read the same records from [the command line](cli.md).

## Decisions and Open questions

Decisions lists the project's decisions with their status, rationale, lineage, and what each replaced or was replaced by. Open questions lists the questions that still wait, with where each stands on the ladder, how it got there, and its options and recommendation. Both narrow to one category from the row of links above the list, and the link keeps the choice in the address.

Add category, at the end of that row, adds a category to the project, as `ropeshift category add` does. A name is lowercase words joined by hyphens, such as `release-planning`, and the dialog says so before it sends anything else. A category stays once added, and the built-in ones are always there. The command palette lists Add category too, and opens the same dialog.

Record decision, in the heading of Decisions, records a decision as `ropeshift decision record` does: its statement and rationale, whether it is reversible or one way, its scope, its categories, what it cites, and the decision in force it replaces, with no rework. More adds the question it answers, the overlapping decisions it is judged compatible with, and an ID of your own. Record as an assumption records a reversible choice that lets work go on, as `--assumed` does. Recording is one-way, so Review comes first and says what is about to be recorded, and Record decision, or Record assumption, sends it once, as yours; to change it afterwards, overturn it. An ID that starts with `pd-` is refused, because only plan approval names decisions under that prefix. When the decision's scope overlaps one in force, the answer says which. The command palette lists Record decision and opens the same dialog.

Find a record to cite, under Citations in both dialogs, searches the project's decisions, requirements, contracts, and questions for some words, as `ropeshift search` does, and lists the best matches. Cite adds one to the citations as `kind:ID`, such as `requirement:FR-RETRY-1`, and a record already cited says so.

Overturn, on a decision in force, reverses it as `ropeshift decision overturn` does: give what stands instead, and why, with what it cites, and More lets you name the new decision's ID, under the same rule as Record decision. The overturned decision stays on record as overturned, the new one takes its place, each completed work order that rested on the old one goes to rework, and each lane that read it is told, in one step that stands or does not. The dialog then lists the decision that took its place and each work order it sent to rework, each a link, and how many lanes it told. A decision that was replaced or overturned has no Overturn.

Record question, in the heading of Open questions, records a question as it is, as `ropeshift question record` does: its text, any of the project's categories, and the work order, contract, or plan it is about. A question can hold work on that record until it is answered, and the box for that is available once the question names one. Dismiss, on a question that still waits, asks first and then closes the question with no answer; it stays on record. The command palette lists Record question and opens the same dialog.

Answer, on a question that waits, opens the answer where the question stands, through the control Needs you answers with: the options with the recommendation chosen, or Something else for your own words, and a one-way choice starts with nothing chosen. Send this answer records it once, as yours, as the decision that closes the question, which is what `ropeshift question answer` does.

Each control on these views is a member's own act, through the route the command line uses, so the rules are the same. A member's session does each one directly, and a delegate or the assistant that prepares the same operation makes a confirmation card for a member to confirm in Needs you; the web listener takes no delegate's credential. [Authorization](authorization.md#who-can-do-what) says who may do what, and [the command line](cli.md) has a command for each: `ropeshift question record`, `question dismiss`, and `question answer`, `decision record` and `decision overturn`, `category add`, and `search`.

## Work orders

Work orders lists every work order of the project under a tab for each group: Open, In progress, On hold, Stuck, Review and merge, Completed, and All. A work order's page shows its lifecycle, its review, its checks, and its lanes, beside the decision it waits on and its facts.

Start moves ready work forward. On a ready work order, Start opens a dialog that claims the work for a worker and then starts its lane, which is what `ropeshift work claim` and `ropeshift lane start` do in turn. The dialog lists the workers the installation holds, the ones the daemon sees now first, each with its lanes against its cap and its level. With one worker, that worker is chosen already. A lane ID is optional, and an empty one lets ropeshift name the lane. Start stays disabled, with its reason beside it, while no worker has joined or none is chosen. A refusal of the claim or of the lane start shows in the daemon's words in the dialog. A claim that went through before a refused lane start leaves the work claimed, which the dialog says, and its Start lane sends the lane start alone. A claimed work order that has no lane shows Start lane for the same reason. Work that is still in intake, planning, or contract review shows Start disabled, with the state it is in, and work that runs or has ended shows neither.

The server says what each work order allows now, from the state machine, and the page offers those actions and no other. Retry, in the page's header, returns failed work to ready. More actions holds the rest: Release the claim for claimed work, Send back to planning for work in contract review, End this claim for lapsed work, Back to ready and Back to planning for blocked work, Cancel this work for any work that has not ended, and Archive the worktree of a lane that ended. A decision card beside the page offers some of these already, and the menu leaves those to it. Each opens a dialog that says what it does before anything is sent. Retry, Send back to planning, End this claim, Back to ready, Back to planning, and Cancel this work ask why. Release the claim names the worker that holds the claim and gives it back, as `ropeshift work release` does, which returns the work to ready. Archive the worktree asks the worker that ran the lane to remove it, as `ropeshift lane archive` does. A worktree that holds work no remote has is refused with the worker's reason, which the dialog shows first, and Archive anyway forces it, as `--force` does. The Lanes tab lists the worktrees that can be removed.

The command palette lists the same actions for the work order in view, and Start work and Start lane for a work order a search finds beside its other actions. A link to a work order's page opens the dialog of one action: `?dialog=start`, `retry`, `release`, `send-back`, `reassign`, `resolve`, `replan`, `cancel`, or `archive`.

## Floor

![Floor, with one worker's measurements and its lanes in the working, ended, parked, and needs-a-decision states](assets/screenshots/floor-light.png#gh-light-mode-only)
![Floor, with one worker's measurements and its lanes in the working, ended, parked, and needs-a-decision states](assets/screenshots/floor-dark.png#gh-dark-mode-only)

Floor shows every machine that serves the project, and its lanes. Each machine shows its orchestrator, its lanes against its cap, and its latest CPU, memory, memory pressure, free disk, and load per core, with how fresh those readings are. Each lane shows its work order, its state, whether the model and the ledger are reachable from it, its generation, and its freshness. The switch at the top shows the lanes of this project or of all projects.

Above the machines, Ready to start lists the work that waits for a lane: ready work, and claimed work with no lane yet. Each row has the same Start or Start lane as the work order's page, through the same dialog. Work the server lists as stuck stays in Needs you, and the panel is not there when nothing waits.

A member opens a lane's page with Open. Message talks to a lane that runs headless, and Open pane to one that runs in a terminal pane, both in the conversation panel. A lane's actions menu holds what the server allows for the lane and for its work order, such as Resume, Start the next generation, Reset the breaker, Retry, End this claim, Back to ready, Back to planning, Cancel this work, or Archive the worktree of a lane that ended, each explained before it runs. Cancel this work is there for every lane whose work has not ended, not only for stuck work. The Workers page lists each machine with its orchestrator, which Start orchestrator, Stop orchestrator, and Details act on, and Details reads what `ropeshift orchestrator show` prints. [How many lanes run at once](onboarding.md#how-many-lanes-run-at-once) and [what the worker measures](onboarding.md#what-the-worker-measures) cover the caps and the readings.

## Conversations and the dock

![Conversations, with a conversation with the assistant and the orchestrator, planning session, and lanes a member can talk to](assets/screenshots/conversations-light.png#gh-light-mode-only)
![Conversations, with a conversation with the assistant and the orchestrator, planning session, and lanes a member can talk to](assets/screenshots/conversations-dark.png#gh-dark-mode-only)

Conversations lists your conversations with the project's assistant, then the sessions you can talk to: the orchestrator on each machine, planning sessions, and lanes. Choosing one opens it beside the list, with its state, its worker, its generation, and when an idle assistant stops. New starts a conversation with the assistant, Switch target (`T`) moves to another session, and the conversation's menu renames it, clears or compacts its context, stops its session, or deletes it. Type a message and send it with `Ctrl Enter`, or `⌘ Enter` on macOS. A stopped conversation resumes when you send to it.

The assistant acts for you through the API: operational actions run at once, and decisions become confirmation cards you confirm. Its cards, and a session's approval requests and questions, appear inline in the conversation as the same cards Needs you shows, so deciding one in either place resolves both.

The dock is the same surface in a panel on the right of every view. Open or close it with `.` or the conversation button in the top bar, and drag its edge to resize it. It holds the same target as the Conversations view. [The assistant](authorization.md#the-assistant) describes what the assistant may do, and `ropeshift chat` holds the same conversations from [the command line](cli.md).

## Agents

![Agents, with the project's roster, the source of each agent's settings, and one agent's definition](assets/screenshots/agents-light.png#gh-light-mode-only)
![Agents, with the project's roster, the source of each agent's settings, and one agent's definition](assets/screenshots/agents-dark.png#gh-dark-mode-only)

Agents is the roster every session receives, and the routing guide that tells a session which agent to pick. It has an installation scope and a project scope; the switch at the top chooses which one you read and edit. The Roster tab lists each agent with its access, class, effort, approval, and the scope its settings come from: built in, the installation, or the project. Selecting one shows its definition: its purpose, kind, access, class, effort, approval, model, allowed and denied tools, and instructions. Driver preview names the model it runs on for each driver.

A member edits an agent's fields and confirms the change with Review change, disables or enables an agent, resets the changes this scope made to it, or adds one with Add agent. Add agent starts from a copy of another agent, or from Paste TOML: an `[[agent]]` table, or its keys on their own, which the page reads as `ropeshift agent add --file` reads a file. It shows the agent the text holds, or what stops it, before you save, and a definition the roster refuses is refused in the roster's words. The name you give is the name added, as it is for the command. The Routing guide tab edits the guide, and History lists the roster's versions, so an earlier one can be restored. [Agents](authorization.md#agents) explains how the roster reaches each session, and `ropeshift agent` makes the same changes from [the command line](cli.md).

## Settings

![Settings, with the installation, workers, project, and personal sections, and the project's fleet mode and caps](assets/screenshots/settings-light.png#gh-light-mode-only)
![Settings, with the installation, workers, project, and personal sections, and the project's fleet mode and caps](assets/screenshots/settings-dark.png#gh-dark-mode-only)

Settings gathers every setting a member changes in the web UI, in four groups:

- **Installation**: the admission and lane caps of each machine, which the worker's recommendation sits beside, the autonomy defaults, when an idle assistant stops, and a backup of the installation as a download.
- **Workers**: each worker's autonomy level and its run windows, and acknowledging the stated risk of a subscription worker.
- **Project**: the fleet mode, Running, Paused, or Draining, with the project's caps, then the review, integration, decision, lane, recovery, delegation, and delegate policies, notifications, and the estimate policy.
- **You**: this browser's theme and whether single-key shortcuts are on.

Admission and lane caps shows one machine at a time, and a switch at the top chooses it when several workers have registered. Max lanes follows the worker's recommendation, caps nothing, or is a number, and lanes per sign-in is a number where 0 caps nothing, which is the same choice `ropeshift worker caps` offers. The Recommendation panel shows the number the worker computed from its cores and memory, the reasoning behind it, and when it reported. A number above the recommendation saves with a warning that names the recommendation and why, because the cap is your decision. The caps are saved in the user profile on the worker's machine, so the page, the command, and the file are one setting, and a worker on another machine is refused: its caps are in the profile on that machine. [How many lanes run at once](onboarding.md#how-many-lanes-run-at-once) covers what a cap holds back.

Estimate policy sets how a project's estimates read its delivery records: the comparable outcomes a confident estimate needs, never fewer than 5, how many of the most recent ones an estimate reads, how many trials a forecast runs, from 1 to 20,000, and whether the project shares its delivery history with the installation's pool. The field names what the pool holds and what it never does, and a value the policy would refuse holds Review change back. A version that stops sharing takes the project's records out of the pool, and when that cannot run at once, the page says so in the API's words and the next delivery tick does it. [Estimate policy](measures.md#estimate-policy) lists each setting.

A policy change shows what changes before you save it, and each policy keeps its versions, so an earlier one can be restored. [Onboarding](onboarding.md#before-the-first-lane) explains what each policy governs, from [merging accepted work](onboarding.md#merging-accepted-work) to [Claude subscriptions and automated use](onboarding.md#claude-subscriptions-and-automated-use). [Delegates](authorization.md#delegates) covers the delegate policy, and [notifications](notifications.md) where a notice goes.

## Members

![Members, with the member's live sessions and a delegate credential with its project, kind, expiry, and state](assets/screenshots/members-light.png#gh-light-mode-only)
![Members, with the member's live sessions and a delegate credential with its project, kind, expiry, and state](assets/screenshots/members-dark.png#gh-dark-mode-only)

Members lists everyone who can sign in to the installation, with their live sessions, the last sign-in, and when the next session expires. This release has no roles, and adding or removing members is not on this page yet. Sign out this session ends the one in this browser; Sign out all sessions ends every session of yours, in every browser and shell, and revokes every delegate credential of yours.

Delegates lists the delegate credentials you made, each with its project, kind, expiry, last use, and state. Create delegate makes one for an agent outside ropeshift, bound to one project, and shows the credential once; Revoke ends it. [Delegates](authorization.md#delegates) explains what a delegate may do, and [the command line](cli.md#delegates) how it presents its credential.

## Measures

![Measures, with the fleet mode, limits, and counts of work orders and lanes by state](assets/screenshots/measures-light.png#gh-light-mode-only)
![Measures, with the fleet mode, limits, and counts of work orders and lanes by state](assets/screenshots/measures-dark.png#gh-dark-mode-only)

Measures shows the project's fleet, its mode, how many items wait for a person, its workers, and its limits, then how many work orders and lanes are in each state, the sources work is read from, and the product measures: the north star, human interruptions per completed work order, beside the measures that keep it honest, with review agreement, cost per outcome, and park time. Every number comes from the ledger, and the page is for reading; its count of what waits links to Needs you. [Product measures](measures.md) defines each measure, and `ropeshift measures` prints them.
