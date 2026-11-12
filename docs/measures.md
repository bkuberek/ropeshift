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
| Interruptions | One delivered batch of questions, however many it holds; one approval a person decided, at the web UI, the command line, or a pane; and one forced action: a member resetting a lane's breaker, resetting or replacing a stuck orchestrator, ending a lapsed holder's ownership, taking work out of `blocked`, resuming a lane only a person can resume, or saying what became of an effect a crash left unknown. An approval an orchestrator decided under delegation reached no person and does not count |
| Completed work orders | Work orders that reached `done` |
| Interruptions per completed work order | Interruptions over completed work orders |
| Overturn rate | Decisions and assumptions an orchestrator or a lane recorded, and assumptions anyone recorded, that were later overturned, over all of them |
| Rework | Rework work orders created, over completed work orders |
| Autonomous decision rate | Questions closed at rungs 1 to 4 by anyone but a member, over all questions closed. A member who answers an open question directly closes it, and that is no autonomous decision |
| Repeated-question rate | Questions that reached a person and turned out to have been asked or decided before: merged into an earlier question, answered by a decision recorded before they were asked, or answered by a decision that cites such a decision or question. Over questions that reached a person |
| Repeats prevented | Lanes attached to a question another lane asked |
| Park time | From a lane beginning to park to its wake firing, by what fired it, at the median and the 95th percentile |
| Flow time | From the first time a work order was `ready`, and from its creation, to `done`, at the median and the 95th percentile |
| Work in progress | Work orders in `claimed`, `running`, or `parked` |
| Cost per outcome | What lanes' agents reported, over work orders of that kind that reached `done`. A lane's cost is the difference between the session's running totals, summed over its generations, because a total accumulates across resumes. A turn a kill stopped reports nothing, so a total is a lower bound, and a surface says so when some lane reported a turn with no cost or lost its holder before it reported |

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

## Experience records

The ledger keeps one experience record per lane and one per decision. Each holds the policy version in force, the settings the action ran on, the options considered, and what was chosen, and its outcome is filled in once, which may be long after the action. Nothing else ever changes a record.

| Record | Opens | Settings | Outcome |
|--------|-------|----------|---------|
| A lane's | When its session first binds, or at its end when it never bound | Agent, the model asked for and the one that answered, effort, run mode, profile digest, the context thresholds of its role, and its work order's size | At its end: how it ended, its reason, and the cost its agent reported |
| A decision's | When it is recorded | The settings of the session that decided, an orchestrator's run or a lane, and the size of the work its question is about. A person's decision ran on no session | When a member overturns it, with the rework it created |

A work order's size is what its contract declares, `small`, `medium`, or `large`, and `unknown` when it declares none.
