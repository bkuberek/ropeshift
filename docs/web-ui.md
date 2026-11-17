# Web UI

The web UI is where a member decides what waits for a person, reads the plan and its documents, watches the lanes, talks to the sessions, and changes the settings. The daemon serves it at `http://127.0.0.1:7317`, on loopback only; `--web` or `ROPESHIFT_WEB` chooses another loopback address, or `off` ([the daemon's flags](cli.md#the-daemon)). Open that address in a browser on the same machine and [sign in](#sign-in) with the passphrase `ropeshift init` asked for, or `ropeshift passphrase set` set ([onboarding](onboarding.md#run-ropeshift-init)). Settings changes the passphrase later. Every view has the same frame: the project switcher (`P`), the command palette (`Ctrl K`, or `⌘ K` on macOS), the count of what needs you, the conversation panel (`.`), and a sidebar that groups the current project's views under Plan, Work, Talk, and Project, then the installation's. `?` lists every keyboard shortcut. The page follows the system's light or dark setting until you choose a theme.

The screenshots below are `v0.1.0-alpha.15` running locally with sample data.

## Sign in

With no session, the page shows Sign in in place of any view: the ropeshift mark and name, the passphrase field, and a link to Passphrase help, which opens the guide that says where the passphrase comes from ([onboarding](onboarding.md#run-ropeshift-init)). The field accepts a paste and a password manager, and it holds the keyboard when the page opens. The passphrase goes to the daemon and nowhere else: it empties after every try, and the session comes back in a cookie the page cannot read. On a phone the form fills the width.

The page says what happened in the place it belongs to:

- **An empty try** says to enter the passphrase, at the field, and sends nothing.
- **A passphrase that does not match** shows at the field, empties it, and puts the keyboard back on it. The daemon records each refusal in the installation's log without the guess ([authorization](authorization.md#changing-the-passphrase-from-the-web-ui)).
- **A session that ended** while you were using the page shows "Your session ended" above the form: you signed out in another tab, the session ran out, or a change of the passphrase ended it. What you were doing stays on the page behind the form and is there when you sign in again. A first visit and a sign-out of your own show no such notice.
- **A daemon that did not answer** says nothing was checked, and to check that the daemon runs with `ropeshift health`.
- **No passphrase set** says to run `ropeshift passphrase set` on a terminal, because the first passphrase is never set from a browser.

## Keyboard

Every action has a keyboard path, and `?` lists the shortcuts. A single-key shortcut is off while the cursor is in a text field or the terminal pane, and Settings turns them all off under Appearance and keyboard. `Ctrl K` (`⌘ K` on macOS) opens the command palette from anywhere, including a text field.

| Keys | Does |
| --- | --- |
| `Ctrl K`, or `/` | Opens the command palette |
| `?` | Shows the shortcut sheet |
| `G` then `N`, `O`, `R`, `D`, `W`, `F`, `C`, `A`, `S`, `U`, `P` | Goes to Needs you, Overview, Roadmap, Documents, Work orders, Floor, Conversations, Agents, Settings, Usage, or Projects |
| `P` | Opens the project switcher |
| `.` | Opens or closes the conversation panel, with the cursor in its message box |
| `T` | Switches the conversation target |
| `J`, `K` | Moves to the next or the previous item of a list or a table |
| `A` | Moves focus to the open decision's primary button, which you then press |
| `Esc` | Closes the palette, a dialog, a menu, or the drawer, and returns focus |

No single key decides anything: `A` moves focus, and `Enter` presses. The `G` waits a moment for its second key. Settings and Agents open the current project's page, and Overview the project's own page.

The command palette finds a view, a record, or an action. With nothing typed it lists the current page's actions, then recent records, then views. `>` narrows it to actions and `#` to records, and the words of a query match in any order. The arrow keys move, `Enter` runs the entry, and `Esc` closes. An action opens its dialog or its page, and the palette never decides for you: a card that waits for a decision opens with its primary button focused.

Every operation that changes something has an action in the palette, as the web UI has a control for it. Decide lists each card that waits in Needs you, and a typed query finds the ones past the first five. Each Settings section is `Settings:` and its name, and each project has its rename, archive, restore, remove, and back up. An open question has Answer question and Dismiss question, and a decision in force has Overturn decision. Add agent, Register repositories, Change repository, Remove repository, and the conversation's own actions, such as Rename the conversation and Compact the conversation's context, stand beside them. A work order's actions stand in the palette for the work order in view, and for any work order a search finds.

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
- **Plan approval and contract review**: approve a plan version, or pass a contract review or send the work back to planning. Approving a planning session's version accepts the session's work order, so that work order does not wait here beside it; once you decline the version, it waits for acceptance again. A version that lists a repository manifest shows each repository approving would register, and the card approves only once it has read them; once you approve, it shows what became of each entry, or why registration could not run.
- **Uncertain effects**: say whether an effect a crash left unknown happened, did not happen, or should run again.
- **Confirmation cards**: a delegate or the assistant prepared a consent operation. Read the exact operation and Confirm or Decline.

[Authorization](authorization.md#who-can-do-what) says who may decide each kind, [delegates](authorization.md#delegates) explains confirmation cards, and [the command line](cli.md) has a command for each decision, such as `ropeshift work accept` and `ropeshift approval approve`.

## Overview

Overview is a project's own page, at `/projects/{project}`, and `G` then `O` goes to it. Its heading holds Register repositories, which opens the dialog Repositories opens, and Start planning session, which opens the dialog the Roadmap and Plans views open. Both are off for an archived project, whose page says so and where to restore it.

A row of six figures comes first, and each links to the view that holds what it counts:

- **Waiting on you**: what waits for you in this project, which Needs you lists.
- **Lanes running**: the lanes that count against the caps, which Floor lists.
- **In review or merging**: the work orders in acceptance or integrating, which Reviews lists.
- **Done in 7 days**: the work orders completed in the last seven days, which Work orders lists under Completed.
- **Interruptions per completed**: the north star of [product measures](measures.md), over the same days. A rate that counted nothing reads No data yet, never zero.
- **Estimated cost this week**: the cost agents reported over the last seven days, which Usage breaks down. It reads at least a figure when part of it is a lower bound, and None reported when no lane reported a cost.

The main column holds the current plan's milestones, each with its progress and state, and the lanes that have not ended, each with its state and the machine it runs on. The side column holds the top five items of Needs you for this project, the longest wait first, each opening its card; the review agreement of the last 30 days; and the project's recent events with who acted. Each part reads on its own, so a read that fails shows its failure in that part and leaves the rest of the page standing.

A project with no repository shows an empty state with Register repositories and Import an existing project, in place of the figures.

## Roadmap

![Roadmap, with a plan's milestones in priority order, their progress, and a banner for the version that waits](assets/screenshots/roadmap-light.png#gh-light-mode-only)
![Roadmap, with a plan's milestones in priority order, their progress, and a banner for the version that waits](assets/screenshots/roadmap-dark.png#gh-dark-mode-only)

Roadmap shows the project's goals in priority order, each with its plans and the version of each plan in force. Under each plan come its success measures, its milestones in priority order with how much of each milestone's work is done and how the rest stands by state, and each milestone's work by dependency layer, with the critical path outlined. When a newer version of a plan waits for approval, a banner says so, and Review version opens its plan approval page.

Under each plan the Roadmap shows its forecast, and in each milestone's row that milestone's, as `ropeshift plan forecast` prints them: [a forecast](#forecasts) of the newest version the daemon's forecast tick has forecast. The what-ifs and what makes a forecast again wait under What-ifs and how it was made, and a low-confidence forecast's explanation never does.

Start planning session opens a planning session from here, as `ropeshift plan start` does: give the goal as text or read it from a file, then choose the planning repository and the worker. [Start a planning session](onboarding.md#start-a-planning-session) describes what the session does and how its plan reaches you.

## Plans

Plans lists the project's plans, each with its status, the plan version in force, the version that waits, and how much of its work is done. A plan's page shows its versions, the difference from the approved version, its forecast, its contracts, its reviews, and its work. Review version opens the approval page of a version that waits.

Start planning session opens a planning session from here as it does from [Roadmap](#roadmap). Start contract review starts a contract review of a plan, as `ropeshift plan review` does: a work order that reads the plan and its contracts at their newest versions and collects every question a lane would have to stop and ask. On a plan's page the dialog names that plan, and on the list it asks which one. The review is recorded as a work order that is ready, and its page opens next, where Start claims it for a worker and starts its lane. The work the review reads stays where it is until the plan is approved. A plan with no work that names a repository has nothing for a review to read, and the daemon refuses it in words that show in the dialog. A second press after a refusal repeats the request under one ID, so it never starts two reviews. The command palette lists Start contract review and opens the dialog on Plans. Like the other controls, it is your own act through the route the command line uses, so the rules are the same, and the web listener takes no delegate's credential.

### Forecasts

A plan's page has a Forecast section, and the Roadmap shows the same forecasts, with the words `ropeshift plan forecast` prints for them. A forecast gives the plan's end and the end of each of its milestones at p50 and p85, with the estimated cost at list price, from a simulation of the plan's unfinished work that the daemon's forecast tick makes again when what it rests on changes, and at least hourly. A page reads what the tick last kept and forecasts nothing itself.

- Forecast carries the Low confidence badge before it when the estimates it rests on are few, and the explanation it was made with stays on the page in words: how many trials and work orders, and which work orders have too few comparable outcomes or none.
- A date that is a lower bound reads "or later" and a cost "at least". Work with no history is left out of the trials, so a forecast that leaves some out has lower bounds throughout.
- The what-ifs are the p85 with one more lane, shown when the plan has a lane cap, and with each wait for a person cut to the median wait of the project's recent work.
- The last line says what makes the forecast again: its seed, trials, lane cap, ledger revision, and the time it ran from.
- A milestone with no comparable history, no worker to run its work, or no unfinished work says so in a word with its explanation, and shows no dates.
- A version the tick has not forecast yet says "Forecast pending" and shows no range. A version a newer one replaced before the tick forecast it says it has none.

### Estimate bands

An estimate is one work order's band of delivery time and estimated cost, in the words `ropeshift work show` prints, with the explanation it was made with. Every page that shows one draws it the same way:

- The line says the estimate's label, its delivery time at p50 and p85, and its cost at p50 and p85. A confident estimate carries no badge. A low-confidence estimate carries the Low confidence badge before the word Estimate, and its explanation stays on the page in words, never only in a tooltip.
- A band that is open, because fewer than 6 records stand behind it, has no p85 of its own: it reads "p85 more than" the longest record's time and cost, and the picture has no p85 tick, with the fill running to the end of the track under an arrow. A cost that holds a lower bound reads "cost at least".
- An estimate with no history says what is missing, such as "No estimate: no medium change has finished in this project yet", and is never drawn as a band or as zero.
- The track draws the band to scale: a solid fill for a confident estimate and a hatch for a low-confidence one, with a tick at p50 and one at p85. The sentence in its label carries the same numbers, so the picture is never the only way to read them.
- Where space is short, as in the side of a work order's page and in the planning pane, the band keeps its badge and its explanation waits under How it was made. Where there is room, the explanation is in full.

## Plan approval

![Plan approval, with the difference from version 1, the draft decisions, and the card that approves or declines the version](assets/screenshots/plan-approval-light.png#gh-light-mode-only)
![Plan approval, with the difference from version 1, the draft decisions, and the card that approves or declines the version](assets/screenshots/plan-approval-dark.png#gh-dark-mode-only)

Plan approval is the page for one version of a plan, at `/projects/<project>/plan?id=<plan>&version=<n>`. It opens from the plan's card in Needs you, from the Roadmap's banner, and from Plans. It says where the version came from and its digest, then shows the difference from the version in force, the draft decisions the version holds with the question each answers, the contracts it adds or changes with their criteria and work orders, the estimates it rests on, and the documents it covers. For an adopted project, it also shows what the adoption does, grouped.

The Estimates group says how many of the version's estimates are confident, low confidence, and absent, and the ledger revision they read. Under it come the version's forecasts, the plan's with its remaining cost and each milestone's, named by the milestone's title and by its ID where the version lists none, each with its what-ifs and explanation as [forecasts](#forecasts) show them, then each work order's [estimate](#estimate-bands). A version the forecast tick has not forecast says "Forecast pending" and still shows the estimates intake made. The group shows what the API returns and computes nothing of its own.

When the version lists a repository manifest, the page shows each repository approving would register, with its path, where that path leads, and its remote, and after the approval, what was registered and why an entry was not. The card beside it says what approving does, that approving records the estimates shown as the baseline finished work is measured against, that history finished later never voids the approval, and binds the approval to the version's digest. Approve version records each draft decision as yours and passes the contract review of the work the plan covers. Decline asks for a reason; the session that wrote the plan reads it, and writes a new version when it can meet it. A version a newer one replaced is void, and a version already decided is read-only. [Start a planning session](onboarding.md#start-a-planning-session) and [adopt an existing project](onboarding.md#adopt-an-existing-project) describe the same approval from the command line.

## Planning

The planning page, at `/projects/<project>/planning?lane=<lane>`, shows a planning session's pane beside the plan the session has committed to its branch, as `ropeshift lane outline` prints it. When a request to approve a version stands for the branch's tip and the ledger holds its plan, the outline has an Estimates part: how many of the version's estimates are confident, low confidence, and absent, the ledger revision they read, the plan's and each milestone's [forecast](#forecasts), and each work order's [estimate](#estimate-bands), each band keeping its badge and holding its explanation under a control. A milestone's forecast reads "Forecast pending" until the forecast tick has forecast the version. A branch with no such request, or whose first version the ledger holds no plan for, shows no Estimates part.

The panel beside the terminal has two tabs, Outline and History. History is labeled "What the planning session sees" and shows the delivery history the session's pack holds, as `ropeshift measures history` prints it: the project's finished work by kind and size, then by kind, size, capability class, and repository, from the project's own records alone. Each group shows how many work orders it holds, its delivery time, lane time, and cost at p50 and p85, its mean rounds, and its median change size, and a group under `min_samples` carries the Low confidence badge and the explanation the session receives. An open p85 reads "more than" and a cost that holds a lower bound reads "at least". A project with no finished work says "No finished work yet. Work orders will have no estimate until comparable work finishes." [What a planning session sees](measures.md#what-a-planning-session-sees) defines the history.

## Documents and Traceability

![Documents, with a requirements document in the reader and the trace of one requirement to its criteria, work orders, and verdicts](assets/screenshots/documents-light.png#gh-light-mode-only)
![Documents, with a requirements document in the reader and the trace of one requirement to its criteria, work orders, and verdicts](assets/screenshots/documents-dark.png#gh-dark-mode-only)

Documents holds the project's requirements, specs, designs, decision records, and other documents, and has two tabs: Reader and Traceability.

- **Reader** lists the documents by kind and shows one at a time, at the version you choose: its file, digest, review state, and the plan that approved it. A decision record also shows its status, beside each version in the selector. Compare with shows how two versions differ, and the link button copies the document's address. A document's text comes from a repository, so the reader shows it as text only. In a requirements document, each requirement shows where it stands, and selecting one opens its trace beside the reader: the criteria that serve it under their contracts, the work orders that carry them, and the verdicts on that work with the commit each judged.
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

Work orders lists every work order of the project under a tab for each group: Open, In progress, On hold, Stuck, Review and merge, Completed, and All. A work order's page shows its lifecycle and, under the tabs Review, Changes, Pull request and checks, Lanes, and Activity, the work's review, changes, checks and merge, lanes, and activity, beside the decision it waits on and its facts.

New work order, in the heading of Work orders, records a work order as `ropeshift work create` does. Give it a title and its acceptance criteria, one to a line. The work order is recorded with the contract its criteria make and passes contract review like any other, and work with no criterion does not become ready. More takes the rest of what the command takes: the kind, which is a change or plan work that writes a plan to its repository, a statement of what the work must deliver, the repository its lane runs in, the work orders it depends on, the requirements its criteria serve, the roster agent its lane runs as, its size, its priority, and an ID of your own. The dialog reads IDs, requirement keys, and the priority as the ledger does before it sends, and opens More for a problem it finds there. The daemon's refusal shows at the foot of the dialog, with what you wrote kept, and a second press after a refusal repeats the request under the same ID, so it never makes two. Create work order opens the new work order's page. The command palette lists New work order and opens the same dialog. An outside delegate that creates work makes a confirmation card for you to confirm in Needs you, unless the delegate policy exempts it, as [Authorization](authorization.md#delegates) describes. The web listener takes no delegate's credential, so the dialog is always your own act.

Start moves ready work forward. On a ready work order, Start opens a dialog that claims the work for a worker and then starts its lane, which is what `ropeshift work claim` and `ropeshift lane start` do in turn. The dialog lists the workers the installation holds, the ones the daemon sees now first, each with its lanes against its cap and its level. With one worker, that worker is chosen already. A lane ID is optional, and an empty one lets ropeshift name the lane. Start stays disabled, with its reason beside it, while no worker has joined or none is chosen. A refusal of the claim or of the lane start shows in the daemon's words in the dialog. A claim that went through before a refused lane start leaves the work claimed, which the dialog says, and its Start lane sends the lane start alone. A claimed work order that has no lane shows Start lane for the same reason. Work that is still in intake, planning, or contract review shows Start disabled, with the state it is in, and work that runs or has ended shows neither.

The server says what each work order allows now, from the state machine, and the page offers those actions and no other. Retry, in the page's header, returns failed work to ready. More actions holds the rest: Release the claim for claimed work, Send back to planning for work in contract review, End this claim for lapsed work, Back to ready and Back to planning for blocked work, Cancel this work for any work that has not ended, and Archive the worktree of a lane that ended. A decision card beside the page offers some of these already, and the menu leaves those to it. Each opens a dialog that says what it does before anything is sent. Retry, Send back to planning, End this claim, Back to ready, Back to planning, and Cancel this work ask why. Release the claim names the worker that holds the claim and gives it back, as `ropeshift work release` does, which returns the work to ready. Archive the worktree asks the worker that ran the lane to remove it, as `ropeshift lane archive` does. A worktree that holds work no remote has is refused with the worker's reason, which the dialog shows first, and Archive anyway forces it, as `--force` does. The Lanes tab lists the worktrees that can be removed.

The command palette lists the same actions for the work order in view, and Start work and Start lane for a work order a search finds beside its other actions. A link to a work order's page opens the dialog of one action: `?dialog=start`, `retry`, `release`, `send-back`, `reassign`, `resolve`, `replan`, `cancel`, or `archive`.

Changes shows what `ropeshift work acceptance` prints, from the same route. Diff summary lists each branch the work's lanes left, with its commit and, when the worker measured it, the files and lines it changed. A branch nobody measured says so, which is not a change of nothing. Criteria and verdicts lists each acceptance criterion with where its evidence stands and the verdict a review gave it, bound to the commit the review read, with the review's reasons and what it cites. Checks lists the check results on record. What is left says what the evidence leaves to do: what the work waits on, a review that has not ended, each check that failed, and what the acceptance table asks of a second check or a member before the work can be accepted. A work order that is done has nothing left. The tab reads when it opens and again when the ledger changes.

Pull request and checks lists the check results on record, each merge approval with the commit it approved, the commit it merged as once merged, and its pull request, and the artifacts the work left. Until the work has a reviewed commit, it says "No checks yet".

Activity shows what `ropeshift work timeline` prints. Where the time went totals the work order's time from its first `ready`: Elapsed, Agent time (the agent working, the orchestrator answering it, and its review), Waiting for a person, and Other waits (the queue, holds, checks and merge, usage limits, external waits, and being stuck). The three parts add to the elapsed time. Waiting for a person is a total of its own and never counts as agent time: a wait a member's act ended is always one. Timeline draws one row for each rework round, each segment to scale on one axis in the tone and fill of its kind, and under it a table lists every segment with its start, end, length, kind, round, lane, and who ended it. A queued segment names its cause and a held one its reason, and [delivery records](measures.md#delivery-records) define each kind. Work that has not ended runs its last segment to the moment of the read, and the tab says so. Once a change or a rework has ended, Delivery record adds its outcome, rounds, delivery time, lane time, time waiting for a person, each wait, its estimated cost at list price (at least that when part of it was never reported), its tokens, and what it changed. Other kinds of work leave no delivery record, and the tab says so. A work order that never became ready has no timeline. Estimate shows the work order's [estimate](#estimate-bands) beside its baseline, each in full, as `ropeshift work show` prints them, with a tick for the actual delivery time on the baseline's track once the work has one, and "Actual: none yet" until then.

The side of a work order's page has an Estimate panel with the same two bands, the current estimate and the baseline its finished work is measured against, each keeping its badge and holding its explanation under a control. A work order with no estimate says "No estimate was made for this work order." and one with no baseline says "No baseline is recorded for this work order yet."

## Floor

![Floor, with one worker's measurements and its lanes in the working, ended, parked, and needs-a-decision states](assets/screenshots/floor-light.png#gh-light-mode-only)
![Floor, with one worker's measurements and its lanes in the working, ended, parked, and needs-a-decision states](assets/screenshots/floor-dark.png#gh-dark-mode-only)

Floor shows every machine that serves the project, and its lanes. Each machine shows its orchestrator, its lanes against its cap, and its latest CPU, memory, memory pressure, free disk, and load per core, with how fresh those readings are. Each lane shows its work order, its state, whether the model and the ledger are reachable from it, its generation, and its freshness. The switch at the top shows the lanes of this project or of all projects.

Above the machines, Ready to start lists the work that waits for a lane: ready work, and claimed work with no lane yet. Each row has the same Start or Start lane as the work order's page, through the same dialog. Work the server lists as stuck stays in Needs you, and the panel is not there when nothing waits.

A member opens a lane's page with Open. Message talks to a lane that runs headless, and Open pane to one that runs in a terminal pane, both in the conversation panel. A lane's actions menu holds what the server allows for the lane and for its work order, such as Resume, Start the next generation, Reset the breaker, Retry, End this claim, Back to ready, Back to planning, Cancel this work, or Archive the worktree of a lane that ended, each explained before it runs. Cancel this work is there for every lane whose work has not ended, not only for stuck work. The Workers page lists each machine with its orchestrator, which Start orchestrator, Stop orchestrator, and Details act on, and Details reads what `ropeshift orchestrator show` prints. [How many lanes run at once](onboarding.md#how-many-lanes-run-at-once) and [what the worker measures](onboarding.md#what-the-worker-measures) cover the caps and the readings.

## Lane

A lane's page shows its screen or its event stream, its facts, and under them Messages, which lists what was sent to the lane, oldest first, as `ropeshift message list` does. A row gives the message, its kind, the generation it was written for, its state, when it was sent, its body, and the lane's reply. A state claims only what its signal proves: a write to a session shows that bytes were handed over, and only a reply says the lane read the message. A message that ended without being injected says why, such as that it timed out or that its generation ended, and a body and a reply keep their line breaks and are never cut. A lane nobody wrote to says so. The [command reference](cli.md) lists each state `ropeshift message show` names.

## Workers

Workers lists each machine that serves the installation, with the CPU, memory, free disk, and lanes against the cap its worker last measured, its orchestrator, and its freshness, then every session on it. A measure the machine cannot provide says unavailable and never reads as zero.

Drivers shows each worker's doctor result, one row for each agent CLI a worker named when it connected, as `ropeshift worker doctor` prints it for a machine with no daemon. A row gives the driver, a badge, the version, whether that version is qualified, the kind and status of its sign-in, and when the worker last reported. The badge reads Usable when the CLI is present and its sign-in is not signed out, the rule an offer applies, so a sign-in the doctor could not read stays usable. A driver that is not usable says why in the doctor's words, starting with its word:

- `absent` and the doctor's detail, when the CLI is not on the machine.
- `unusable`, the doctor's detail, and the fix, such as "Run `claude --version` to see why, and repair or reinstall Claude Code.", when the CLI is on `PATH` and failed its version check.
- `signed out` and the kind of its sign-in, when the CLI is there and its sign-in is signed out.

A worker that named no driver, because its doctor failed when it started or the worker predates the report, shows one row badged Not reported. The worker runs the doctor when it starts, and changing its driver restarts it, so a result is as old as the worker's last connect and is never marked stale. On a narrow screen each driver stacks into a block with its reason in full. [Onboarding](onboarding.md#run-ropeshift-init) covers the check.

## Usage

Usage breaks down the estimated cost of a project's outcomes, then lists the cost of each lane as `ropeshift telemetry lanes` does. A lane's row gives its context at its last call, its tokens by type, the tokens each roster agent spent inside it, which are part of the lane's tokens and never added to them, the cost its agent reported, and when it last called. A cost reads at least a figure, because a turn a kill stopped reports nothing, and it is estimated at list price from what agents reported, so it is not a bill. A lane that reported no cost says none reported, and no cost is ever drawn as zero. A control plane with no telemetry store says it reads none, and a store that holds nothing says no lane has reported. [Telemetry](telemetry.md#the-local-store) covers where the figures come from.

## Reviews

Reviews is the work that waits for you after its lane finished, at `/projects/{project}/reviews`. The command palette lists Reviews and one entry for each of its tabs.

Queue lists the work orders that wait for acceptance or for a merge, the longest wait first. Each row shows the work order, its state, the verdicts of its review counted by kind, its checks counted by where each stands, the merge authority in force, and what it waits on, in the ledger's words when it has them. Each row offers the acts the server lists for it and no others: Accept, which accepts the work in one press as `ropeshift work accept` does; Rework, which asks why and sends the work back for another attempt as `ropeshift work rework` does; and Approve merge, or Retry merge for a merge that was held, which opens the merge card of [Needs you](#needs-you), bound to the reviewed commit, where Approve merge or Deny records your decision as `ropeshift approval approve` does. Work with nothing for you to do, such as a merge you approved that the worker has not reported, says so. An act the daemon refuses shows its reason in the row. An empty queue says Nothing is in review.

Pull requests lists the open pull requests ropeshift opened for work in review: the pull request and the branch it merges into its base, its work order, its head commit, whether a verdict read that commit, its checks, and whether the forge says it merges cleanly. A pull request whose head moved since the review read it says it was not read by a review. Its address shows as text and is never a link.

Agreement shows how often you agreed with the review agent after it judged work, for the project and for each repository, over the last 30 days and over all time: the decisions, the agreement, the work you approved and then rejected, the work you rejected and then approved, and the defects found later in work you accepted. With fewer than ten decisions in a period, it shows the counts and says Not enough decisions for a rate yet in place of a percentage. Each repository shows its merge authority beside its record. ropeshift never changes a merge authority level itself: the record is the evidence for changing one in Repositories. [Review agreement](measures.md#review-agreement) defines the figures.

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

- **Installation**: the admission and lane caps of each machine, which the worker's recommendation sits beside, the autonomy defaults, when an idle assistant stops, a backup of the installation as a download, and About, which gives the daemon's [version](#version).
- **Workers**: each worker's autonomy level and its run windows, and acknowledging the stated risk of a subscription worker.
- **Project**: the fleet mode, Running, Paused, or Draining, with the project's caps, then the review, integration, decision, lane, recovery, delegation, and delegate policies, notifications, and the estimate policy.
- **You**: this browser's theme and whether single-key shortcuts are on, and changing your passphrase.

Admission and lane caps shows one machine at a time, and a switch at the top chooses it when several workers have registered. Max lanes follows the worker's recommendation, caps nothing, or is a number, and lanes per sign-in is a number where 0 caps nothing, which is the same choice `ropeshift worker caps` offers. The Recommendation panel shows the number the worker computed from its cores and memory, the reasoning behind it, and when it reported. A number above the recommendation saves with a warning that names the recommendation and why, because the cap is your decision. The caps are saved in the user profile on the worker's machine, so the page, the command, and the file are one setting, and a worker on another machine is refused: its caps are in the profile on that machine. [How many lanes run at once](onboarding.md#how-many-lanes-run-at-once) covers what a cap holds back.

Change passphrase replaces the passphrase you sign in with, as `ropeshift passphrase set` does after the first. It asks for the current passphrase and for the new one twice, and says before you send anything that a passphrase has at least 8 bytes and at most 1024, counted in bytes, so a character of an accented or non-Latin script may take two or more. The upper bound is there because a terminal reads a passphrase a line at a time, and a longer one set here could not be typed again at the terminal, which would lock you out of it. Each field names its own fix: a missing current passphrase, a new one outside the bounds with the bytes it has, and two new ones that differ, and the first field with a problem takes the keyboard. A current passphrase the daemon refuses shows at its own field, empties, and keeps what you typed for the new one. The first passphrase is set on a terminal, by `ropeshift init` or `ropeshift passphrase set`, so with none set the form says to run it there.

A change that goes through ends every other session of yours, in every browser and terminal, and the notice says how many it ended. This browser stays signed in, on a new session token: a copy of the cookie it had before opens nothing, and the page keeps working because the answer sets the new cookies. A delegate's credential keeps working, and the notice says how many do, with a link to Sign out all sessions on [Members](#members), which ends them. A change that lost to another one, made at the same moment, says so and asks you to sign in again; it is not a wrong passphrase and is not recorded as one. Another page of yours that was open then shows [Sign in](#sign-in), with a notice that its session ended. The form empties once it is sent, and no passphrase is written to the address, the page, or the browser's storage. The command palette lists Settings: Change passphrase, and the form is reachable by keyboard alone. [Authorization](authorization.md#changing-the-passphrase-from-the-web-ui) lists what the daemon requires of a change.

Estimate policy sets how a project's estimates read its delivery records: the comparable outcomes a confident estimate needs, never fewer than 5, how many of the most recent ones an estimate reads, how many trials a forecast runs, from 1 to 20,000, and whether the project shares its delivery history with the installation's pool. The field names what the pool holds and what it never does, and a value the policy would refuse holds Review change back. A version that stops sharing takes the project's records out of the pool, and when that cannot run at once, the page says so in the API's words and the next delivery tick does it. [Estimate policy](measures.md#estimate-policy) lists each setting.

A policy change shows what changes before you save it, and each policy keeps its versions, so an earlier one can be restored. [Onboarding](onboarding.md#before-the-first-lane) explains what each policy governs, from [merging accepted work](onboarding.md#merging-accepted-work) to [Claude subscriptions and automated use](onboarding.md#claude-subscriptions-and-automated-use). [Delegates](authorization.md#delegates) covers the delegate policy, and [notifications](notifications.md) where a notice goes.

## Audit

Audit lists a log's events newest first, with who acted, the act, and its subject, and opens one event's whole record. The Installation log holds the installation's own events, and a project's log holds that project's. Filters narrow it by actor, act, and subject.

A project's log has a second tab, Notices, which shows what each channel was told, as `ropeshift notice list` does: one row for each notice on each channel with its kind and reason, its state, its attempts, when it was first due and last tried, and its delivery digest. Delivery is at least once, so a channel that took a notice after more than one attempt may have heard it more than once, and its row says so. A channel that refused a notice shows the reason it gave. With no channel turned on the tab says no channel has been told anything. [Notifications](notifications.md) covers the channels and what a notice may say.

## Members

![Members, with the member's live sessions and a delegate credential with its project, kind, expiry, and state](assets/screenshots/members-light.png#gh-light-mode-only)
![Members, with the member's live sessions and a delegate credential with its project, kind, expiry, and state](assets/screenshots/members-dark.png#gh-dark-mode-only)

Members lists everyone who can sign in to the installation, with their live sessions, the last sign-in, and when the next session expires. This release has no roles, and adding or removing members is not on this page yet. Sign out this session ends the one in this browser; Sign out all sessions ends every session of yours, in every browser and shell, and revokes every delegate credential of yours.

Delegates lists the delegate credentials you made, each with its project, kind, expiry, last use, and state. Create delegate makes one for an agent outside ropeshift, bound to one project, and shows the credential once; Revoke ends it. [Delegates](authorization.md#delegates) explains what a delegate may do, and [the command line](cli.md#delegates) how it presents its credential.

## Version

The right end of the status bar names the version the daemon runs, such as `v0.1.0-alpha.16`, and leads to Settings, About. A build that is not a release shows its nearest release and short commit, such as `v0.1.0-alpha.16+abc1234`, and its full version is the link's name and tooltip. The Daemon out of date warning sits beside it, and on a phone shows only its icon. Settings, About, gives the same version with the commit and the build date when the build stamped them, and the release installed at the daemon's own executable path when the daemon could read it. `ropeshift version` prints the daemon's version beside its own.

A package manager replaces the binary on disk and leaves the daemon running the build it started from. The daemon reads the installed binary's version once a minute, and when that is a later release than the one running, the status bar shows Daemon out of date, and About shows a warning that says the same. The warning names the fix: `brew services restart ropeshift` when the daemon runs through `brew services`, and otherwise restarting the service. A development build, or a binary the daemon cannot read, shows no warning. [Versioning](versioning.md#what-a-daemon-reports) describes what the daemon compares.

## Measures

![Measures, with the fleet mode, limits, and counts of work orders and lanes by state](assets/screenshots/measures-light.png#gh-light-mode-only)
![Measures, with the fleet mode, limits, and counts of work orders and lanes by state](assets/screenshots/measures-dark.png#gh-dark-mode-only)

Measures shows the project's fleet, its mode, how many items wait for a person, its workers, and its limits, then how many work orders and lanes are in each state, the sources work is read from, and the product measures: the north star, human interruptions per completed work order, beside the measures that keep it honest, with review agreement, cost per outcome, and park time. Every number comes from the ledger, and the page is for reading; its count of what waits links to Needs you. [Product measures](measures.md) defines each measure, and `ropeshift measures` prints them.

### Delivery

The Delivery tab beside Factory shows where delivery time went and how much work finished, as `ropeshift measures delivery` prints it, from the delivery records the daemon keeps. Window reads the last 24 hours, 7 days, or 30 days. Group by reads this project's records by kind, size, roster agent, or repository, and Scope, All projects, reads every active project's records apart, one group for each project, because the percentiles of separate projects do not combine. The choices are in the address, so a link keeps them.

- A row for all work, and one for each group, gives the work done, failed, and canceled, throughput per week with a line of the work done in each of the last 12 weeks, and the elapsed time, delivery time, and estimated cost at p50 and p85. Time and cost count the work that reached done alone, a cost with a lower bound reads "at least", and a measure with no work done reads "no data".
- Where the time went draws each group's elapsed time as a bar of the kinds of segment, each with a word, a tone, and a fill of its own, and the same shares in words beside it. Time waiting for a person is set apart from the agents' time.
- Weekly trend lists the 12 weeks up to the end of the window for all work: the work done in each and its delivery time.
- With no finished work in the window the tab says "Delivery measures appear when work orders finish."

### Estimates

The Estimates tab beside Delivery shows how often finished work landed inside the estimates it was baselined with, as `ropeshift measures estimates` prints it, from this project's own outcomes. [Calibration](measures.md#calibration) defines what counts. Window reads the last 24 hours, 7 days, or 30 days, and all time is always shown beside it. Repository reads all of the project's work or one repository's. The choices are in the address, so a link keeps them.

- How the work that ended was estimated gives, for the window and for all time, how much work ended and how much of it had a confident estimate, a low-confidence one, or none. The share shows from 10 ended.
- Confident estimates are the headline, and Low confidence stands apart after them, so a thin band never hides whether the confident estimates are calibrated. Each has a table for the window and one for all time: a row for all work, then one for each tier and each estimator version, with the outcomes and how they ended, the count of open p85 bands, and for time and for cost the p50 hit rate, the p85 hit rate, and the median ratio of actual to p50. A hit rate reads as its counts and its percent, with a meter beneath it and a tick at the value a calibrated estimator comes near, 50% for p50 and 85% for p85. A ratio comes near 1.
- A rate shows from 10 outcomes behind it. Under that, the row shows its counts and "no rate", a ratio shows "no median", and the tab says "Not enough finished estimated work for a rate yet". An outcome whose baseline p85 was open counts in the p50 rate and the ratio, and is left out of the p85 rate.
- Weekly calibration lists the weeks up to the end of the window that had outcomes, a table for each confidence, with the same columns.
- Milestones give how often milestones ended at or before their forecast p50 and p85 dates, confident and low confidence apart, and each ended milestone's forecast dates beside the day it ended, with "or later" on a date that is a lower bound.
- With no work ended, the tab says "Estimate calibration appears when work orders finish."
