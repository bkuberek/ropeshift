# Usage and measures

Ropeshift counts what happened to the work from its own ledger, so its measures need no telemetry pipeline and nothing configured. This guide reads the Overview's figures, the three tabs of Measures, a plan's forecast and a work order's estimate, the estimated cost on Usage, and the Audit's record of who did what. Its images show the sample installation of [run several projects at once](several-projects.md), in its project Default, where two work orders have finished. Put your own project in its place. [Product measures](measures.md) defines every figure, and each step links to the definitions it shows.

## Before you start

- Finish [watch work run](watch-work-run.md) and [review and merge](review-and-merge.md). Most measures count work that finished, so a project with none shows a view's empty state, which says what fills it.

## What the figures are

- A measure is read from the ledger, over a window that runs back from now. A count of now, such as the work in progress, is the exception.
- A rate with nothing under it has no value, and every view says so, as No data yet or no rate, instead of printing zero.
- A cost is what the agents reported, estimated at list price. It is not a bill, and it is a lower bound: a turn a kill stopped reports nothing, so a sum that misses one reads "at least".

## In the web UI

### 1. Read the Overview's figures

In the sidebar, select Overview. Six figures lead the page, and each opens the view that holds what it counts:

| Figure | What it counts | Opens |
|--------|----------------|-------|
| Waiting on you | What waits for you in this project | Needs you |
| Lanes running | The lanes that count against the caps | Floor |
| In review or merging | The work orders in acceptance or integrating | Reviews |
| Done in 7 days | The work orders that reached done in the last 7 days ([completed work orders](measures.md#definitions)) | Work orders, Completed |
| Interruptions per completed work order | The north star, over the same 7 days ([the north star](measures.md#the-north-star-and-its-counterweights)) | Measures |
| Estimated cost this week | What agents reported for the lanes that ended in the last 7 days ([cost per outcome](measures.md#definitions)) | Usage |

![The six key figures of Default's Overview](assets/screenshots/measures-01-light.png#gh-light-mode-only)
![The six key figures of Default's Overview](assets/screenshots/measures-01-dark.png#gh-dark-mode-only)

### 2. Read the north star and what keeps it honest

In the sidebar, select Measures. The Factory tab shows the project's fleet, its work orders and lanes by state, the sources it reads work from, and then Product measures, which read the last 7 days.

The north star is human interruptions per completed work order. Fewer interruptions mean more autonomy only while the measures beside it hold: a rising overturn rate means agents guess instead of asking, and rising rework means assumptions cost more than questions would have ([the north star and its counterweights](measures.md#the-north-star-and-its-counterweights)). Below them stand the interruptions by kind, the work orders completed, the repeated-question rate, the repeats prevented, the work in progress, flow time from ready and from intake to done, park time by what woke the lane, and cost per outcome ([definitions](measures.md#definitions)).

![Product measures of Default, with interruptions per completed work order beside the overturn rate, rework, and the autonomous decision rate](assets/screenshots/measures-02-light.png#gh-light-mode-only)
![Product measures of Default, with interruptions per completed work order beside the overturn rate, rework, and the autonomous decision rate](assets/screenshots/measures-02-dark.png#gh-dark-mode-only)

Review agreement, below, shows how often you agreed with the review after it judged work, over the last 30 days and over all time, for the project and for each repository beside its merge authority ([review agreement](measures.md#review-agreement)). It is evidence for choosing a merge authority, and ropeshift never changes one itself.

### 3. See where delivery time went

Select the Delivery tab. It reads the delivery record ropeshift keeps of each change and rework that ended, and the choices above it are in the address, so a link keeps them:

- Window reads the last 24 hours, 7 days, or 30 days, the default.
- Scope reads this project, or every active project, each apart.
- Group by reads this project's work by kind, size, roster agent, or repository.

A row for all work, then one for each group, gives the work done, failed, and canceled, the work done per week with a line of the last 12 weeks, and the elapsed time, the delivery time, and the estimated cost at p50 and p85, of the work that reached done. Elapsed time runs from the work's first ready to its end, and delivery time leaves out every wait for a lane ([delivery records](measures.md#delivery-records)). Where the time went draws each group's elapsed time by what it was spent on, such as queued, agent working, in review, checks and merge, and waiting for a person, which stands apart from the agents' time. Weekly trend lists the work done in each of the 12 weeks and its delivery time.

![The Delivery tab of Default over 30 days, by kind, with two changes done, where their time went, and the weekly trend](assets/screenshots/measures-03-light.png#gh-light-mode-only)
![The Delivery tab of Default over 30 days, by kind, with two changes done, where their time went, and the weekly trend](assets/screenshots/measures-03-dark.png#gh-dark-mode-only)

The sample's work ran in seconds, where yours runs in minutes and hours. With no work finished in the window, the tab says "Delivery measures appear when work orders finish."

### 4. Check the estimates against what happened

Select the Estimates tab. It compares each finished work order with the estimate a person saw before the work ran, its baseline, over the window and over all time ([calibration](measures.md#calibration)). Window chooses the period, and Repository, once the project has outcomes, reads one repository's work.

- How the work that ended was estimated gives how much work ended, and how much of it had a confident estimate, a low-confidence one, or none.
- Confident estimates and Low confidence give, for time and cost, how often the work finished at or below its p50 and its p85, and the median ratio of actual to p50. A calibrated estimator comes near 50%, 85%, and 1.
- Weekly calibration and Milestones follow, the second with each milestone's forecast dates beside the day it ended.

![The Estimates tab of Default, with two work orders ended, both with no estimate, and no rate yet](assets/screenshots/measures-04-light.png#gh-light-mode-only)
![The Estimates tab of Default, with two work orders ended, both with no estimate, and no rate yet](assets/screenshots/measures-04-dark.png#gh-dark-mode-only)

A rate shows only when at least 10 outcomes stand behind it, and the share of ended work from 10 ended, so a new project shows counts and "Not enough finished estimated work for a rate yet", as here. Both work orders that ended here count as Absent: nothing comparable had finished when their plan was approved, so the estimates in their baselines had no history. The rates fill as work finishes against an estimate a person saw: each change or rework that ends with an estimate in its baseline is one more outcome, and an estimate is confident once as many comparable work orders have finished as the estimate policy asks, 10 in its built-in version ([estimates](measures.md#estimates)).

### 5. Read a plan's forecast and a work order's estimate

In the sidebar, select Plans, then a plan. Its Forecast section gives the plan's end and each milestone's end at p50 and p85, with the estimated cost of the work that remains, from a simulation the daemon runs again when what it rests on changes ([forecasts](measures.md#forecasts)):

- Low confidence stands before Forecast when the estimates it rests on are few, and the explanation under it says how many trials and work orders, and which work orders have too few comparable outcomes or none.
- A date that is a lower bound reads "or later", and a cost "at least".
- The what-ifs give the p85 with one more lane, and with each wait for a person cut to the project's median wait.
- The last line says what makes the forecast again: its seed, trials, lane cap, ledger revision, and the time it ran from.

![The Forecast section of the Reliable order intake plan, with a low-confidence forecast of the plan and of Resilient calls, and two milestones with no history](assets/screenshots/measures-05-light.png#gh-light-mode-only)
![The Forecast section of the Reliable order intake plan, with a low-confidence forecast of the plan and of Resilient calls, and two milestones with no history](assets/screenshots/measures-05-dark.png#gh-dark-mode-only)

Here two work orders have finished, so the forecast is low confidence, its dates and costs are lower bounds, and a milestone none of whose work has comparable history says no history and gives no dates. A version the daemon has not forecast yet says "Forecast pending".

A work order's page has an Estimate panel at its side: the delivery time and cost at p50 and p85 that comparable finished work gives, with its badge, the explanation under How it was made, and its baseline once one is recorded ([estimates](measures.md#estimates)). An estimate with no comparable history says what is missing, and is never drawn as a band or as zero. The estimate policy, in Settings, sets how many outcomes a confident estimate needs ([estimate policy](measures.md#estimate-policy)).

### 6. Read usage and cost

In the sidebar, select Usage, under Installation.

- Sign-ins is where a sign-in's session and weekly windows go. This build has no source that reports them, so it says none has reported a window, and draws none as zero.
- Estimated cost by lane lists each lane of every project, the orchestrator's session among them, with its context at its last call, its tokens by type, the tokens each roster agent spent inside it, the cost its agent reported, and when it last called. A lane that reported no cost reads none reported ([telemetry](telemetry.md#the-local-store)).
- Estimated cost by outcome gives, for the project you were in, each kind of work's estimated cost over the last 7 days, the work done, and the cost per work order done ([cost per outcome](measures.md#definitions)).

![Usage, with no sign-in window reported, the estimated cost of each lane, and the cost by outcome in Default](assets/screenshots/measures-06-light.png#gh-light-mode-only)
![Usage, with no sign-in window reported, the estimated cost of each lane, and the cost by outcome in Default](assets/screenshots/measures-06-dark.png#gh-dark-mode-only)

### 7. Read who did what

In the sidebar, select Audit. The project's log lists its events newest first, each with its revision, its time, who acted, the act, and its subject. Select an event to read its whole record. Actor, Act, and Subject narrow the list, and Log, at the top, switches to the installation's own log: its projects, members, workers, and the installation's roster.

![The Audit of Default, with its events, who acted, the act, and its subject](assets/screenshots/measures-07-light.png#gh-light-mode-only)
![The Audit of Default, with its events, who acted, the act, and its subject](assets/screenshots/measures-07-dark.png#gh-dark-mode-only)

The Notices tab shows what each channel was told: each notice with its kind and reason, its channel, whether it was delivered or refused with the channel's reason, its attempts, when it began to wait and was last tried, and its delivery digest. Here only `web`, the channel Needs you reads, has notices; desktop and webhook rows appear once a member turns them on ([choose how you hear that something waits](settings-and-agents.md#5-choose-how-you-hear-that-something-waits)). Delivery is at least once, so a notice delivered after more than one attempt says the channel may have heard it twice.

## Next

[Learn ropeshift](../README.md#learn-ropeshift) lists the guides.
