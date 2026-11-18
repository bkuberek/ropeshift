# Product measures

Ropeshift computes its product measures from the ledger and nothing else, so they work with no telemetry pipeline, on one machine, with nothing configured. `ropeshift measures` prints them, the web UI's operations view shows the last 7 days, and the daemon copies them out as `ropeshift.measure.*` metrics every 5 minutes, to the local telemetry store and to an exporter when one is configured. Each read covers every page of the event log and the records of review calibration.

## The north star and its counterweights

The north star is human interruptions per completed work order. Interruptions alone reward an agent for never asking, so every surface shows the north star beside the overturn rate, rework, and the autonomous decision rate.

| If interruptions fall and | Then |
|---------------------------|------|
| Overturn rate and rework are stable | Autonomy is improving |
| Overturn rate rises | Agents are guessing instead of asking |
| Rework rises | Assumptions are costing more than questions would have |
| Autonomous decision rate is flat | Work has become simpler, or fewer questions are being raised |

A rate with nothing under it has no value, and every surface says so instead of printing zero.

## Definitions

A window runs back from now, `7d` by default. Work in progress is a count of now, and every other measure counts what happened inside the window.

| Measure | How the ledger gives it |
|---------|-------------------------|
| Interruptions | One delivered batch of questions, however many it holds; one approval a person decided, at the web UI, the command line, or a pane; one spend stop, each time a hard spend limit parks a work order's lane, so a work order that parks at the same limit again is another stop, and a lane that reaches the limit and completes is none, and whichever way a member ends it; and one forced action: a member resetting a lane's breaker, resetting or replacing a stuck orchestrator, ending a lapsed holder's ownership, taking work out of `blocked`, resuming a lane only a person can resume, or saying what became of an effect a crash left unknown. An approval an orchestrator decided under delegation reached no person and does not count |
| Completed work orders | Work orders that reached `done`. A review judges other work, so it is no completed work order, no flow time, and no work in progress |
| Interruptions per completed work order | Interruptions over completed work orders |
| Overturn rate | Decisions and assumptions an orchestrator or a lane recorded, and assumptions anyone recorded, that were later overturned, over all of them |
| Rework | Rework work orders created, over completed work orders |
| Autonomous decision rate | Questions closed at rungs 1 to 4 by anyone but a member, over all questions closed. A member who answers an open question directly closes it, and that is no autonomous decision |
| Repeated-question rate | Questions that reached a person and turned out to have been asked or decided before: merged into an earlier question, answered by a decision recorded before they were asked, or answered by a decision that cites such a decision or question. Over questions that reached a person |
| Repeats prevented | Lanes attached to a question another lane asked |
| Park time | From a lane beginning to park to its wake firing, by what fired it, at the median and the 95th percentile |
| Flow time | From the first time a work order was `ready`, and from its creation, to `done`, at the median and the 95th percentile |
| Work in progress | Work orders in `claimed`, `running`, or `parked` |
| Cost per outcome | What lanes' agents reported, over work orders of that kind that reached `done`: a review lane's cost is over the reviews that reached `done`. A lane's cost is the difference between the session's running totals, summed over its generations, because a total accumulates across resumes. A turn that stopped reports no cost, and neither does the turn a session crashed in or was killed in before its result, so a total is a lower bound only when some lane had such a turn or lost its holder before it reported, and a surface then reads it as "at least". A lane's end counts the same turns its telemetry counts, so the measures, the delivery record, and the lane's own cost read a lower bound in the same places |

## Review agreement

Whenever a member accepts or sends back work whose current attempt has verdicts from a review, ropeshift keeps a calibration record in the same transaction. A pull request the person merges on the forge, at a level where the person decides, counts as accepting the work it lands, and its record names the pull request in its reason and no member, because the forge's account is not one. At `person_approves`, approving the merge of a reviewed commit counts as accepting it, and denying it as sending it back. The record names the work order, the review, and the commit digest the review read, and it holds the outcome and the verdict on each criterion the member saw, the member's decision, whether the member agreed, the member's reason when one was given, the repository, and the merge authority level in force. It is evidence and nothing else: no calibration, and no rate computed from it, ever changes a repository's merge authority. Only a member raises or lowers the level.

A review's outcome reads from its verdicts: `pass` when every criterion passed, `fail` when any failed, and `cannot_judge` otherwise.

| Member's decision | After `pass` | After `fail` | After `cannot_judge` |
|-------------------|--------------|--------------|----------------------|
| Accept | Agreement | Disagreement | Unknown |
| Send back | Disagreement | Agreement | Unknown |

Three things write no record: a decision of the control plane itself, such as accepting reviewed work at `review_approves` or sending a failed review back by rule; work no review judged; and a member deciding while the review is still open. A defect found later in work accepted after a `pass`, recorded as the rework of a `done` work order, writes a disagreement against that earlier verdict, bound to the same commit digest.

Review agreement is read for the workspace and for each repository, over the last 30 days and over all time, ending at the report's window. It does not follow the report's own span, because a week of agreement says little. Each repository is listed with the merge authority level in force now, so the record reads beside the level it is evidence for.

| Measure | How the ledger gives it |
|---------|-------------------------|
| Decisions after verdicts | Calibration records made in the period, including a defect found later in approved work |
| Agreed, disagreed, unknown | Records by agreement. A decision after a review that could not judge is unknown, and agrees with nothing |
| Agreement rate | Agreed over agreed plus disagreed. Unknown decisions are in neither |
| Approvals overturned | Work the review passed that the person sent back, or later found a defect in |
| Later defects | The approvals overturned by a defect found after the work was accepted |
| Failures overridden | Work the review failed that the person accepted |

A review fails work back to a lane by rule until its rounds are used up, so a person meets a failed verdict only at the bound on review rounds. Failures overridden therefore count accepts at that bound.

`ropeshift repository show` prints the same figures for one repository, and the web UI's operations view shows them in its measures section.

## Capacity intervals

A worker's reports are not kept, so the control plane records from each one when the worker's running lanes stand at one of its lane caps: `max_lanes` or `max_lanes_per_sign_in`. The lanes stand at a cap when they reach it or pass it, counted as admission counts them, and a cap of 0 caps nothing. A capacity interval names the worker, the cap, and its value, and holds the first report that saw the cap, the last, and its end, each by the control plane's clock less the report's age. The age is the worker's word, so the control plane holds it between 0 and the window a report stays observed: a worker's clock or a faulty report moves an interval's edges by at most that window and never past the control plane's own time, and a report that really was held longer is dated late by the excess. Nor is a report dated before its session opened or before the worker's report before it, so a session's first report begins no interval before the session existed, and a later one never moves an interval back.

| A report | Does |
|----------|------|
| Sees a cap reached and no interval of it standing | Begins an interval at the report |
| Sees the same cap at the same value | Keeps the interval standing until the report |
| Sees the lanes below the cap, or the cap at another value | Ends the interval at the report |
| Comes from a new session, or more than the window a report stays observed after the last report that saw the cap | Ends the interval at that last report, because the worker stopped reporting in between |

An interval that stands ends, as far as anyone can read, at the last report that saw it, so a worker that stops reporting ends its intervals at its last heartbeat. Recording takes no revision and appends no event, as keeping the report does, and a report of a session that is no longer current records nothing. The intervals live in the installation database beside the workers they describe.

## Change size

When a lane ends with a commit, its worker records with the lane's result what the lane's branch changed between the base commit its claim named and its final commit: the files changed and the lines added and removed. It reads them from the repository's history with `git diff --numstat`, detecting renames, so a moved file is one changed file, and a binary file counts as a changed file with no lines. Recording them with the result keeps them independent of a checkout and of a branch deleted after a merge.

The control plane keeps the change size on the lane's `branch` artifact. A lane with no base commit, a lane that made no commit, and a worker that could not measure the change record none, which is not a change of nothing. `GET /v1/work-orders/{id}/acceptance` returns it as each artifact's `change`, with `files`, `added`, and `removed`, and `ropeshift work acceptance` prints it after the branch's commit.

## Delivery records

Ropeshift derives where each work order's time went from its events, with no model, and keeps a delivery record for every `change` and `rework` work order that reaches `done`, `failed`, or `canceled`. The daemon's delivery fold reads what each project's log added every `--delivery-tick`, from a cursor it keeps, and re-derives each work order an event ends. Its first run reads the log from the start, so it backfills every work order that ended before it.

A timeline covers a work order's time from its first `ready` to its end with no gap and no overlap, in segments of one kind each:

| Kind | Time the work order spent |
|------|---------------------------|
| `queued` | Waiting for a lane, ready or claimed before its lane started. Its cause is `lane_cap` while the project's work-in-progress cap was full or every worker stood at a lane cap, and `dispatch` otherwise |
| `held` | Held by a start hold or an admission hold, with the hold's reason, or back in planning after its first `ready` |
| `working` | Running with its agent at work |
| `ladder` | Waiting on a question or an approval that an orchestrator or the ladder closed |
| `person` | Waiting on anything a member's act ended |
| `usage_limit` | Parked on a usage limit, or held back by one at admission |
| `review` | In acceptance while its review is open |
| `integration` | Waiting on checks or the merge, when no member's act ended the wait |
| `external` | Parked on a check, a time, or another work order |
| `stuck` | Lapsed, blocked, or failed, until an orchestrator or a policy moved it |

A wait belongs to the party whose act ended it, so a wait a member ended is always `person` and never agent time. Each segment names its rework round, its lane, and the act that ended it.

A delivery record holds what an estimate matches on: kind, size, the roster agent and its capability class, the repository and its merge authority level, and the number of criteria. It also holds the model, milestone, plan, outcome, rounds, the time in each segment kind, each wait for a person, the tokens and estimated cost of every lane that served the work order, its review lanes included, and the change size of its last branch. Its four durations are elapsed time, from the first `ready` to the end; delivery time, which leaves out every wait for a lane; lane time, while a lane of it or of one of its reviews was claimed or running; and person time.

`ropeshift work timeline` and `GET /v1/work-orders/{id}/timeline` show one work order's timeline, derived when read, and its delivery record once it ended. `ropeshift measures delivery` and `GET /v1/measures/delivery` read the records over a window, `30d` by default, grouped by `kind`, `size`, `agent`, or `repository`: the work done, failed, and canceled, throughput per week, elapsed and delivery time and estimated cost at p50 and p85 by nearest rank, and the share of elapsed time in each segment kind, from the work that reached `done` alone, with the work done each week over the last 12 weeks. `ropeshift measures delivery --by project` reads every active project's look back apart through the API, one group each.

The measures tick copies the delivery measures out with the product measures, for each of their windows, from the records the fold keeps. No label names a work order, a title, or a person:

| Metric | Unit | Labels |
|--------|------|--------|
| `ropeshift.measure.delivery.elapsed` | `ms` | window, quantile (`p50`, `p85`), kind, size, capability class, repository |
| `ropeshift.measure.delivery.time` | `ms` | The same |
| `ropeshift.measure.delivery.throughput` | `{work_order}` per week | window, kind, size, capability class, repository |
| `ropeshift.measure.delivery.segment_share` | `1` | window, kind, size, segment kind |

A group with no work in the window has no point, and elapsed and delivery time have none until some work in the group reached `done`.

Capacity intervals and change sizes are recorded only from the release that added them. A record whose work began before the fold's first run says so with `lane_cap_known` false, and a record without a change size has none, which is not a change of nothing. The capability class and merge authority level are read when the record is made. A new version of the derivation drops every timeline and record and folds the log again.

## Estimates

An estimate is a band of delivery time and estimated cost for one `change` or `rework` work order that has not ended, drawn from the delivery records of comparable work that reached `done`. It matches on what is known before the work starts: its kind; its size, `unknown` when it declares none; the capability class of the agent it names or the roster's default mapping gives it; its repository and that repository's merge authority level; and the number of criteria in its contract. It takes the narrowest match with at least `min_samples` records, from the `recent_samples` most recent, and gives their 50th and 85th percentiles; with fewer at every match it is labeled `low` confidence and says why, and with none it gives no number and says what is missing.

Ropeshift makes estimates in sets. Intake makes one set for each plan version it records, in the same transaction, for every open work order of the contracts the version covers. The set is not part of the plan's digest, so new history never changes what an approval approved. Work made outside a plan, such as ledger work, a contract that names no plan, or the rework an overturned decision makes, is estimated when it is made. Each set records the ledger revision it read, the estimator version, the estimate policy version, and a digest of what its estimates say. A set never changes, and a work order's current estimate is the one its newest set holds. After each fold, every `--delivery-tick`, the daemon estimates the open work again and keeps a new set wherever an estimate changed, because work finished, the estimate policy changed, or the work's own keys did: a set of the plan version that covers the work, which holds all of that version's open work, and one set for changed work outside every plan version. A tick that changes no estimate keeps nothing.

Each estimate keeps its confidence, the keys it matched and those it dropped, how many records it drew on against `min_samples`, their work order IDs, the shortest and longest of them, its percentiles, and the explanation it was made with, which every surface shows as it was written. `ropeshift work show` prints the current estimate as an `estimate` line under its label, and its [baseline](#baselines) as a `baseline` line once one is recorded. `GET /v1/work-orders/{id}/estimate` returns them as `estimate`, or `null` for work no estimate was made for, and `baseline`, or `null` before one is recorded, with the baseline's `id`, the `approval_id` that recorded it, absent for work made outside a plan, `recorded_at`, and the `estimate` it holds.

## Estimate policy

A project's `[estimate_policy]` sets how its estimates read its delivery records. It is a versioned policy of kind `estimate`, which only a member publishes, with `ropeshift policy publish` or `POST /v1/projects/{project}/policies`, and which `ropeshift policy list --kind estimate` lists. No calibration changes it on its own. A version states every setting, and each version is newer than the one in force. The built-in version is this one:

```toml
[estimate_policy]
version = 1
min_samples = 10
recent_samples = 50
forecast_trials = 2000
share_history = false
```

| Setting | Meaning |
| --- | --- |
| `min_samples` | The comparable delivery records a confident estimate rests on, never fewer than 5. An estimate on fewer is low confidence and says why |
| `recent_samples` | How many of the most recent comparable records an estimate reads, never fewer than `min_samples` |
| `forecast_trials` | The trials a forecast runs, from 1 to 20,000 |
| `share_history` | Whether the project adds its delivery records to the installation's pool and draws on the pool for its broadest comparison. Off unless a member turns it on. See [the installation pool](#the-installation-pool) |

### The installation pool

The installation's pool holds the delivery records of every project whose estimate policy shares its history, and only such projects draw on it. It holds only work that reached `done`, which is all an estimate draws on, and for each its kind, size, and capability class, its elapsed, delivery, lane, and person time, its estimated cost and whether that cost is a lower bound, and the day it ended. It names no work order, repository, agent, model, plan, milestone, or person, and holds no title, tokens, or content. A project's records stay in its own database beside the pool, which records only which project shared each one so that project can replace its share or take it out; no read returns that.

Every `--delivery-tick`, a project that shares history replaces its share whenever its records changed, even when its fold failed. A project takes its share out as soon as a member publishes an estimate policy with `share_history = false`, and when it is archived or removed; a restored project shares again on its next tick while its policy still shares history. When the take-out cannot run at once, the policy is still in force, the answer to the publish carries a `notice` that says so, and the next tick takes the records out. A share read under an earlier policy version than the one in force changes nothing, so a tick that ran as a member stopped sharing cannot put the records back.

A project that shares history estimates at its broadest match, kind and size alone, from its own records and the pool's records of other projects: such an estimate counts the pool's records in `pool_samples`, names only the project's own work orders in `sample_ids`, and says it drew on the installation pool. It does so only when the pool records among the most recent records it would draw on come from at least 3 other projects, so no estimate rests on one other project's work. A read of the pool marks which of its records came from the same project by a number that holds for that read alone and is no project ID; the estimator only counts the marks, and no estimate keeps them. Below 3, the estimate draws on the project's own records alone, and its explanation says that the pool is used only from 3 other projects, in the same words whether the pool holds comparable work of fewer projects or none, so an estimate never tells that another project finished such work.

A pooled estimate's shortest and longest, `delivery_min_ms`, `delivery_max_ms`, `cost_min_usd`, and `cost_max_usd`, and the range its explanation gives come from the project's own records alone; when it drew on none of them, they are 0 and the explanation gives no range. Nor does a pooled band show a pool record as its p85 when that record is the longest: whenever its p85 is the longest of the records it drew on, as it is in a band open above, on 5 or 6 records, and at low confidence on 6 to 12, and that longest is another project's, the band reads as open above its p50, with no upper value, and its explanation says that the longest comparable record is another project's. Its cost band does the same on its own when its p85 cost is the highest and that is another project's. An estimate with no history says whether the pool was searched. Estimates already kept stay as they were made after a project stops sharing; the next tick estimates the work again without its records. A store with no installation database holds one project, so it has no pool, and no estimate speaks of one.

## Forecasts

A forecast gives a plan's and each milestone's completion date and remaining estimated cost at p50 and p85, from a seeded simulation over the plan's unfinished work. Each trial draws one record for each work order from the records its estimate rests on, runs the work in dependency order, by priority within what is ready, at most the lane cap at a time, and holds a lane for each record's lane time and then waits out the rest of its delivery time. The lane cap follows the product's rule: zero caps nothing. Started work draws only from records that ran longer than it has so far and keeps the remainder. Work with no history is left out and listed, and the dates and costs are then lower bounds; so they are when the plan's work waits on unfinished work outside the plan, or when started work has run longer than every comparable record. Started work whose estimate drew on the installation pool compares its time so far with the project's own comparable records alone: until it has run longer than one of them, its forecast reads as its estimate does; its explanation never counts the records that ran longer; and once it has run longer than every one of the project's own, the dates are lower bounds. A p85 whose trial ended at another project's longest record alone, or cost that project's highest alone, reads as open above the p50, as a pooled estimate's band does, and the explanation says so.

The seed is the digest of the plan version's digest, the ledger revision, and the estimator version, and a forecast records it with its trials, the cap, the time it ran from, the median wait for a person, and the estimate policy version, so the same revision and plan version give the same forecast. A forecast is low confidence when the work its dates rest on includes an estimate that is low or has no history, and its explanation names that work. Under a lane cap that is all of the plan's unfinished work, because any of it can take a lane first; with no cap, a milestone's dates rest only on its own work and the work it waits on. Two reruns of the same draws give the p85 date with one more lane and with every wait for a person cut to the project's median wait; the second leaves started work as it is. A milestone none of whose unfinished work has history gets no dates, nor does a plan whose project no worker serves.

Every `--forecast-tick`, the daemon forecasts the newest version of each plan that is due, on a tick of its own and never on a request, because the simulation of a large plan takes seconds. A shutdown stops the tick before each plan and between a forecast's trials, and keeps nothing of the forecast it stopped, which the next tick makes. A version is due when it has no forecast; when what its forecast rests on changed, as when its work ended or started, an estimate or the records an estimate rests on moved, the version was approved, the lane cap changed, or a worker came or went; and at least hourly while its forecast has dates, once the ledger's time is an hour past the time that forecast ran from. Otherwise the tick keeps nothing. A forecast runs from the recorded time of the revision it read, and started work's time so far is read at that time, so the same revision gives the same forecast; in a ledger with no new event, that time stays where its last event left it. Its work is the open `change` and `rework` work of the contracts the version covers, each estimated as an estimate is made then. Each dependency names the work order admission finds for it, by the ID its file or tracker gave the target: work of the forecast waits for it in the trials, work that ended counts for nothing, and other unfinished work, or a name nothing holds yet, is outside the plan, so the dates are lower bounds. The lane cap is the tighter of the project's work-in-progress cap and the sum of the lane caps of the workers the installation registers, each held to the project's lanes per worker, and a worker with no cap leaves the sum uncapped; with no worker registered, nothing can run the plan's work. That cap is all the forecast holds work to: it does not model the per-repository cap, a worker's autonomy and run windows, whether a worker's agents can take a work order's capability class, or the fleet's mode, so its dates can be early when one of them holds work back. The median wait for a person is the median of every wait for a person in the project's `recent_samples` most recent delivery records.

Each forecast names the estimate set it was made from: the version's newest set when that set says what the forecast's estimates say, or else a new set of the version that the tick keeps beside the forecast. A version with no open work is forecast from no estimate, so its forecasts name no set. A plan version keeps the latest forecast of the plan and of each milestone, and the last one of each UTC day, so the newest shows the forecast now and the others show how it moved. Forecasts and the sets the tick keeps take no revision and append no event, because they are derived from the log, and the transaction that keeps them can write nothing else. A run keeps nothing when, by the time it keeps, intake has recorded a newer version of the plan: the newer version's estimates stay current, and the next tick forecasts it.

`ropeshift plan forecast [<plan>] [--version N]` prints a plan version's latest forecasts, each under its label with its dates and costs, its lower bounds as "or later" and "at least", its what-ifs, and the explanation it was made with, and then the estimates of the set it was made from. `GET /v1/projects/{project}/plans/{id}/forecast` returns them with every field behind them, as [the API reference](api.md#reading-a-plan-by-version-and-a-planning-sessions-outline) describes. Neither runs a forecast: until the tick has forecast a version, both say "forecast pending", as `ropeshift plan show` and `ropeshift plan approve` do where they print the version's forecasts with its approval. A version a newer one replaced before the tick forecast it has no forecast and never will.

## Baselines

A baseline is what finished work is measured against: the estimates and forecasts a person saw before the work ran. Approving a plan version records what its approval shows, in the approval's transaction, with an `estimate.baselined` event: the estimate set and the forecasts of the version as `GET /v1/projects/{project}/plans/{id}/forecast` answers them then, which are the latest forecasts the forecast tick kept and the set they were made from, or, before the tick has forecast the version, its newest set and "forecast pending". An approval that showed "forecast pending" records a baseline with no forecast, so its milestones are never measured against one. The baseline keeps the forecasts it showed whole, because the tick replaces a day's forecast later that day. Work made outside a plan, such as ledger work, a contract that names no plan, or the rework an overturned decision makes, records the estimate it was made with as its baseline, in the transaction that made it, with no approval and no event of its own.

A planning lane's branch asks for approval before intake records it, so the ledger holds none of its work and its approval shows no estimate and no forecast. Approving it records a baseline with neither. The estimates intake makes when it records the version afterwards are no baseline, because nobody saw them before approving, so that work is measured against a baseline only if a later approved version of the plan shows its estimate.

A baseline never changes. A work order's baseline is the first one recorded whose set estimated it, so a later approval of a new version of the plan, or a new estimate as history grows, never moves it.

### Estimate outcomes

When a baselined `change` or `rework` ends, the delivery fold writes its estimate outcome beside its delivery record: the baseline estimate's confidence, tier, and estimator version, whether its band was open, the actual delivery time and estimated cost, whether each was at or below the baseline's p50 and p85, and the ratio of each to the p50. Work that ended `failed` or `canceled`, and work whose baseline estimate had no history, is an outcome with no comparison, so it still counts toward the share of work whose estimate was confident, low confidence, or absent. An open band has no p85 comparison, for time or cost. A cost compares only when neither the actual cost nor the band's is a lower bound, because a lower bound can read as at or below a band it exceeded, and a ratio needs a p50 above zero. Work whose baseline was recorded only after it ended, and work with no baseline, has no outcome. A retry of failed work drops its outcome until it ends again.

A milestone's outcome compares when its last work order ended with its baseline forecast, and keeps that forecast's confidence. Its baseline is the first baseline of its plan whose version lists it, so a milestone first approved while its forecast was pending has no outcome, whatever a later approval showed. It ends once every `change` and `rework` of that version's contracts that names it has ended, and compares only when all of them reached `done` and the forecast has dates. A date the forecast gave as a lower bound, read "or later", has no comparison: with `lower_bound` `p85` only the p50 date compares, and with `all` neither does. A milestone's outcome also holds what its work cost, which it does not compare, because a forecast's cost is of the work that remained when it ran.

Outcomes are derived from the log and the baselines, so they take no revision and append no event, and a new version of the derivation drops them and folds them again with every timeline and record.

## Calibration

Calibration shows how often finished work landed inside the estimates it was baselined with, from its [estimate outcomes](#estimate-outcomes). It reads the project's own outcomes and delivery records alone, never the installation pool, so no other project's work reaches a rate. It is evidence about the estimator: it never changes an estimate, a forecast, a schedule, or the estimate policy.

Over the last window, `30d` by default, and over all time, calibration gives for all of the project's work and then for each repository:

- The p50 hit rate, the share of finished work whose delivery time, or estimated cost, was at or below its baseline p50, calibrated near 0.5.
- The p85 hit rate, the same at p85, calibrated near 0.85.
- The median ratio of actual to p50, by nearest rank, calibrated near 1.
- Each of these by tier and by estimator version, and over every tier.
- The share of the work that ended whose baseline estimate was confident, low confidence, or absent. Absent covers work with no baseline and an estimate with no history.
- A weekly line: the same counts and rates for each of the 12 weeks up to the window's end.

Confident and low-confidence outcomes are counted apart, so early low-confidence outcomes never hide whether the confident estimator is calibrated. The confident rates are the headline. The rank rule claims the same 50 and 85 percent below `min_samples`, so the low-confidence rates beside them show how far a thin band holds. Milestones are reported the same way: each milestone's baseline forecast, with its p50 and p85 dates and lower bound, beside the day its last work order ended, and how often milestones ended at or before the forecast dates, confident and low-confidence forecasts apart.

These rules decide what counts:

- An outcome whose baseline p85 was open counts in the p50 hit rate and the ratio, and not in the p85 hit rate. Its count shows as `p85_open`.
- A rate shows only when at least 10 outcomes stand behind that rate; below 10, calibration shows the counts and no rate. Each rate is held to this on its own count: with 12 outcomes of which 4 were open, the p50 rate shows over 12 and the p85 rate shows no rate over 8. A cost compares only when neither the actual cost nor the band's is a lower bound, so the cost rates often rest on fewer outcomes than the time rates. The share of ended work shows from 10 ended.
- Failed and canceled work counts apart, in the outcome counts and the share, and compares nothing.
- Work counts in the window it ended in.
- An estimate that drew on the installation pool is calibrated on this project's own work that ended against it, so a tier 6 group holds only this project's outcomes and never a pooled record.

`ropeshift measures estimates [--window D]` and `GET /v1/measures/estimates?window=D` read calibration, with the recent window `30d` by default and all time always shown. Both return one scope for all of the project's work, then one for each repository, each holding the recent window, all time, and a weekly line of the 12 weeks up to the window's end, and the milestones, which belong to no repository. With `--json`, a rate is absent where fewer than 10 outcomes stand behind it, and its `n` and `hits` stay. A project with no outcomes reads all counts as zero.

The measures tick copies calibration of all of the project's work out with the product measures, for each of their windows. A point exists only where its rate shows. `tier` is a tier from 1 to 6, or `all` for every tier. No label names a work order, a repository, an estimator version, or a person, and forecasts are not exported, because a forecast belongs to one milestone:

| Metric | Unit | Labels |
|--------|------|--------|
| `ropeshift.measure.estimate.hit_rate` | `1` | window, quantile (`p50`, `p85`), quantity (`time`, `cost`), tier, confidence (`confident`, `low`) |
| `ropeshift.measure.estimate.ratio` | `1` | window, quantity, tier, confidence |
| `ropeshift.measure.estimate.share` | `1` | window, confidence (`confident`, `low`, `none`) |

## What a planning session sees

A planning lane, the agent session that writes a plan, reads two things about delivery, so that it sizes work and chooses agents with evidence: the project's delivery history, and the estimates and forecasts of each plan version it submitted. It never writes an estimate. No other session reads either. A change, rework, review, or contract review lane and an orchestrator receive no estimate, forecast, or history, so a band cannot anchor how a lane works or how a reviewer judges, and no claim, admission, lease, deadline, priority, review, or merge decision reads one.

### The delivery history in its pack

The planning lane's context pack holds a `delivery_history` item named `project`, after every other item, so a pack cut for its budget keeps the records a lane works from first and lists the history as one the lane can read. It is the project's `change` and `rework` work that reached `done`, aggregated twice: by kind and size, and by kind, size, capability class, and repository. Each group reads its `recent_samples` most recent records, as an estimate does, and gives:

- how many finished work orders it holds;
- delivery time, lane time, and estimated cost at p50 and p85, by nearest rank, with a cost that is a lower bound read as "at least";
- the mean number of rounds;
- the median change size, in files and lines, over the work whose lane recorded one.

A group of fewer than `min_samples` finished work orders is labeled low confidence and carries the explanation an estimate from just those records would carry: how many it holds against the minimum, the keys it set aside, and how far apart they were. A group at or above `min_samples` reads confident. A project with no finished work says so.

`ropeshift measures history` and `GET /v1/projects/{project}/measures/history` show a person what the lane reads: the command prints the item's text, and the route returns the groups with each repository's name by ID. The pack's item and the read are built by the same function from the same records, so they never differ. The web UI shows it on the planning pane's History tab ([web UI](web-ui.md#planning)).

The history is built from the project's own delivery records alone, whatever `share_history` says. The installation pool answers only the last tier of an estimate ([the installation pool](#the-installation-pool)), so sharing history changes no figure in it, and it never counts or names another project's work. Like the rest of the pack it is built again for each new version of the pack, which a new claim makes and which a change to a decision the pack holds makes for the lane's next generation, so a running generation keeps the history it read. The lane reads it as it stands now with its `context_get` tool, kind `delivery_history` and reference `project`; the one tool serves every session, so it lists the kind for all, but for any lane but a planning lane, and for an orchestrator, the read is refused as a kind nobody named.

### The estimates and forecasts of a version it submitted

A plan version a planning lane submitted is one intake recorded from that lane's accepted work. After the forecast tick has forecast it, the daemon sends the lane a notice as a message of kind `notice` through the path every message to a lane takes, with what the version's approval shows: each forecast of the plan and of each milestone, and each estimate of its work, each with its confidence label, its dates or band, its lower bounds as "or later", "more than", and "at least", and the explanation stored with it. It holds only what those estimates and forecasts say, so an estimate that drew on the pool shows the band it kept, which never shows another project's record, and no explanation says more than where it was made.

The notice goes once for each lane and version, to a lane whose session is open. A lane whose session ended is sent nothing, because nobody would read it, and the same figures stay on `ropeshift plan forecast` and in the API. A later forecast of the same version sends nothing more, and a version read from files, or from work no planning lane did, is told to no lane.

## Experience records

The ledger keeps one experience record per lane and one per decision. Each holds the policy version in force, the settings the action ran on, the options considered, and what was chosen, and its outcome is filled in once, which may be long after the action. Nothing else ever changes a record.

| Record | Opens | Settings | Outcome |
|--------|-------|----------|---------|
| A lane's | When its session first binds, or at its end when it never bound | Agent, the model asked for and the one that answered, effort, run mode, profile digest, the context thresholds of its role, and its work order's size | At its end: how it ended, its reason, and the cost its agent reported |
| A decision's | When it is recorded | The settings of the session that decided, an orchestrator's run or a lane, and the size of the work its question is about. A person's decision ran on no session | When a member overturns it, with the rework it created |

A work order's size is what its contract declares, `small`, `medium`, or `large`, and `unknown` when it declares none.
