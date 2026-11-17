# Review and merge

This guide takes finished work from its review to your default branch in the web UI. A review lane judges the change against its contract, you read the verdicts and the change, accept the work or send it back, and the change merges at the repository's merge authority. It follows Harbor, whose berths lane has finished Serve the list of berths in `harbor-api`. Put your own project and work in their place.

## Before you start

- Finish [watch work run](watch-work-run.md): a lane has ended with a commit on its branch.

## How a review works

When a lane ends with a commit, a review lane reads the change and gives each criterion of the contract a verdict, `pass`, `fail`, or `cannot_judge`, with its reasons and citations to files, lines, and requirements. A failed criterion sends the work back for another attempt on its own, until three reviews have failed it, after which it waits for you. When every criterion passes, the work waits for you.

What happens once the review passes depends on the repository's merge authority:

| Level | What you do | What ropeshift merges |
|-------|-------------|-----------------------|
| `person_merges`, the default | Accept the work, then merge its branch with git when you choose | Nothing |
| `person_approves` | Approve its merge in Needs you, which accepts it | The reviewed commit, once you approve |
| `review_approves` | Nothing, unless the merge cannot land | The reviewed commit, once the review passes |

Ropeshift merges only the commit the review judged, in one step of its own, never through an agent. `review_approves` lets a passing review merge with no person in between, so keep a first project at `person_merges` or `person_approves` until you have seen how often you agree with the review. [Connect a GitHub repository](github.md#merging-through-pull-requests) gives what each level does through pull requests.

## In the web UI

### 1. Start the review

A lane that ends with a commit makes a review work order, here `Review: Serve the list of berths`, and its work waits for acceptance while the review runs. Inside a run window, the orchestrator claims the review and starts its review lane. To start it yourself, in the sidebar, select Floor. Ready to start lists the review, with the work order it judges after Review of. Select its Start, give a Lane ID if you want one, here `review-serve-berths`, and select Start, the same dialog as a work order's in [watch work run](watch-work-run.md#3-start-a-work-order-yourself). The review lane shows on the Floor like any lane, reads the change, and ends with its verdicts.

### 2. Read what the review found

In the sidebar, select Work orders, then the Review and merge tab, and open the work order. Its Review tab shows What the review found: the commit the verdict is on and the review's round, then each criterion of the contract with its verdict and the review's reasons, and citations to the files, lines, and requirements it read. Beside it, the work's card says what it waits for, with Accept and Send back for rework.

![The work order's Review tab, with the criterion's verdict, its reasons, and its citations](assets/screenshots/review-01-light.png#gh-light-mode-only)
![The work order's Review tab, with the criterion's verdict, its reasons, and its citations](assets/screenshots/review-01-dark.png#gh-dark-mode-only)

### 3. Read the change

Select the Changes tab. Diff summary gives each branch with its commit and how many files and lines it changed. Below it are the criteria with their verdicts, the checks the lane ran with the criterion each covers, and what is left before the work is done. A check the lane wrote about its own work never confirms a criterion by itself: a check the lane did not write, or a member, must.

![The Changes tab, with the branch, its commit, and the files it changed](assets/screenshots/review-02-light.png#gh-light-mode-only)
![The Changes tab, with the branch, its commit, and the files it changed](assets/screenshots/review-02-dark.png#gh-dark-mode-only)

### 4. Read the Reviews queue

In the sidebar, under Work, select Reviews. Queue lists the work that waits for your acceptance or a merge, each with its state and its repository's merge authority, how its review went, what it waits on, and the actions that move it on, here Accept and Rework. Pull requests lists the work that lands through one.

![Reviews, with the berths work waiting for acceptance and its verdict](assets/screenshots/review-03-light.png#gh-light-mode-only)
![Reviews, with the berths work waiting for acceptance and its verdict](assets/screenshots/review-03-dark.png#gh-dark-mode-only)

### 5. Accept the work, or send it back

In Needs you, select the work, here Serve the list of berths, waiting for acceptance. Its card says why it waits for you, shows what the review found, and names its lane and the lane's state.

![Needs you, with the berths work waiting for acceptance, Accept, and Send back for rework](assets/screenshots/review-04-light.png#gh-light-mode-only)
![Needs you, with the berths work waiting for acceptance, Accept, and Send back for rework](assets/screenshots/review-04-dark.png#gh-dark-mode-only)

At `person_merges`, the default, select Accept, and the work reads `done`. Accepting does not merge it: you merge the branch yourself, as [merge at person_merges](#merge-at-person_merges) shows. Or select Send back for rework with a reason, which reaches the first prompt of the work's next attempt. Your word does not wait for the review: accepting or sending back work whose review has not ended cancels that review.

To have ropeshift merge the work from the web UI instead, do not accept it yet. Go on to the next step while it still waits for acceptance.

### 6. Let ropeshift merge

In the sidebar, select Repositories, then the repository, here `harbor-api`. In its record, under Merge authority, choose `person_approves`, and select Save. Below the choice, the record gives how often you agreed with the review on this repository over 30 days. The level never rises by itself.

![The record of harbor-api, with person_approves chosen, the agreement on it, and Save for the unsaved change](assets/screenshots/review-05-light.png#gh-light-mode-only)
![The record of harbor-api, with person_approves chosen, the agreement on it, and Save for the unsaved change](assets/screenshots/review-05-dark.png#gh-dark-mode-only)

At `person_approves`, work that passed its review waits for you as a merge request instead of for acceptance, and approving the merge accepts it. Work that already waited for acceptance, as the berths work does here, becomes a merge request at once. Ropeshift merges each change you approve at its reviewed commit, and pushes the merge it makes to the base branch when the repository has a remote, without forcing.

### 7. Approve the merge

In Needs you, select the merge request, `Approve the merge of <work order's title>?`. Its card names the repository, the branch and its base, the reviewed commit, the merge method, and the merge authority, and says what each choice does.

![The merge approval card, with the repository, the branch, the reviewed commit, Approve merge, and Deny](assets/screenshots/review-06-light.png#gh-light-mode-only)
![The merge approval card, with the repository, the branch, the reviewed commit, Approve merge, and Deny](assets/screenshots/review-06-dark.png#gh-dark-mode-only)

Select Approve merge, which records your approval of the reviewed commit and asks the worker to merge the branch into the base with the merge method. The worker merges in a temporary worktree, never in your checkout, and only while the branch is still at that commit. Or select Deny, which sends the work back to rework, with the reason you type in its next attempt's brief. A new commit on the branch voids the request. A merge git refuses, such as a push to a protected branch, waits in Needs you as a Held merge, and Retry merge tries it again.

Once the merge lands, the work reads `done`, and work in the same repository that waited for its merge goes on from the merged commit.

### 8. Read the work order's activity

In the sidebar, select Work orders, then Completed, and open the work order, which reads Done. Select the Activity tab. Where the time went splits the elapsed time into the agent's time, time waiting for a person, and other waits. Timeline shows each round of the work as a bar, and below it each span: when it started and ended, what kind it was, its lane, and what ended it. Delivery record, below the timeline, gives the finished work's outcome, its rounds, how long its delivery, its lane, and its waits for a person took, and its estimated cost.

![The timeline of the merged work order, with its round as a bar and each span below it](assets/screenshots/review-07-light.png#gh-light-mode-only)
![The timeline of the merged work order, with its round as a bar and each span below it](assets/screenshots/review-07-dark.png#gh-dark-mode-only)

### 9. Archive the worktree

A lane's worktree stays on the worker's machine after the lane ends. On the work order's page, select More actions, then Archive the worktree of lane serve-berths…. The dialog says what it does: the worker that ran the lane removes its worktree on its next pass.

![Archive the worktree, for the berths lane](assets/screenshots/review-08-light.png#gh-light-mode-only)
![Archive the worktree, for the berths lane](assets/screenshots/review-08-dark.png#gh-dark-mode-only)

Select Archive the worktree. A worktree that holds work no remote has, such as a commit that was never pushed, is not removed: the dialog shows the worker's reason, and Archive anyway removes it all the same. Archiving never deletes the lane's branch.

### 10. See how often you agree with the review

In Reviews, select Agreement. It shows how often your decisions agreed with the review's verdicts, for the project over the last 30 days and in all time, and below that for each repository, with the merge authority in force there. It is the evidence for raising a repository's merge authority, which you do in its record, and which never rises by itself.

![Reviews, Agreement, with the project's decisions over 30 days and in all time](assets/screenshots/review-09-light.png#gh-light-mode-only)
![Reviews, Agreement, with the project's decisions over 30 days and in all time](assets/screenshots/review-09-dark.png#gh-dark-mode-only)

## Merge at person_merges

At `person_merges`, ropeshift never merges. Once you accept the work, merge its branch when you choose. A lane's branch is `lane/<lane>` in the repository's registered clone, and with review target `branch`, ropeshift pushes nothing before a merge, so merge it in that clone. The Changes tab names the branch and its commit, here `lane/serve-berths`:

```sh
git switch main
git merge --no-ff lane/serve-berths
git push origin main
```

Ropeshift never deletes a branch. Delete a merged branch with git:

```sh
git branch -d lane/serve-berths
```

## Next

[Connect a GitHub repository](github.md) reviews a repository's changes through pull requests on GitHub.
