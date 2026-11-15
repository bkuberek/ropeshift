# Get started

This guide takes one small piece of work from an empty machine to accepted work, in solo mode, on macOS or Linux. It uses the sample contract that `ropeshift init` writes, and a git repository of your own. [Onboarding](onboarding.md) explains each step in depth, and what to know before a lane runs. [Before you start](set-up-a-project.md#before-you-start) says which steps need you at a terminal, where ropeshift keeps its state, and what it pushes.

You need:

- git, and tmux for lanes in a terminal pane.
- The Claude Code CLI, installed and signed in through Anthropic's own flow ([agent sign-in](onboarding.md#install-and-sign-in-to-an-agent-cli)).
- A git repository with at least one commit, where an agent may add a section to `README.md`.

## 1. Install ropeshift

Install a release with Homebrew:

```sh
brew install bkuberek/tap/ropeshift
```

Or with the install script, which installs to `~/.local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/bkuberek/ropeshift/main/install.sh | sh
```

Check the installation:

```sh
ropeshift version
```

You see the release, the commit it was built from, and the platform. [Onboarding](onboarding.md#install-ropeshift) lists the `.deb` and `.rpm` packages.

## 2. Set up the machine

```sh
ropeshift init
```

1. It asks for a passphrase on the terminal, twice. You use it to sign in to the web UI and to approve changes.
2. It waits for the worker to register, and prints `Worker: registered as <worker-id>`. Keep that ID.
3. It runs the doctor, and prints a line for each agent CLI under `Agent CLIs:`. A line that says `not installed` or `not signed in` tells you what to do; do it, and run `ropeshift init` again.
4. It writes the sample contract, and prints `Sample contract: wrote <path>`.

It exits 0 once an agent CLI is usable. A run on a subscription says the worker starts at `attended`: read [Claude subscriptions and automated use](onboarding.md#claude-subscriptions-and-automated-use) before the first lane.

## 3. Commit the sample contract

Copy the sample into your repository and commit it. The sample asks for a section of `README.md` that names the command which runs the tests:

```sh
cd /path/to/repository
cp "<path>" sample-contract.md
git add sample-contract.md
git commit -m "chore: add a ropeshift sample contract"
```

`<path>` is the path `ropeshift init` printed. Keep the sample as it is for this first run; change it to your own work later.

## 4. Start the daemon

In a terminal of its own, start the daemon with your repository as its source:

```sh
ropeshiftd solo --source /path/to/repository
```

The daemon keeps running in that terminal. It reads the repository at its default branch, and serves the web UI at `http://127.0.0.1:7317`, on loopback only. [Run the daemon as a service](onboarding.md#run-the-daemon-as-a-service) runs it in the background instead.

## 5. Sign in to the web UI

1. Open `http://127.0.0.1:7317` in a browser on the same machine. You see the Sign in page.
2. Enter the passphrase from step 2, and select Sign in.

You see Needs you, which lists what waits for a person in every project.

A command that changes something asks for the passphrase on the terminal. To type it once, hold a session in your shell:

```sh
eval "$(ropeshift login)"
```

The session lasts an hour, in that shell only.

## 6. Pass the work order's review

Intake reads the repository every 30 seconds by default. It reads the sample, and records its work order in `contract_review`, because no approved plan covers its contract yet.

1. In Needs you, select the work order's Contract review card. It names the contract and its version.
2. Select Pass the review.

The same from the command line:

```sh
ropeshift work list
ropeshift work review <work-order-id>
```

`work list` prints the work order's ID, its state, and its title. Either way, the work order moves to `ready`, and Work orders lists it under Open.

## 7. Run the lane

Start the work on your worker:

1. In Work orders, open the work order, which is `ready`.
2. Select Start. The dialog lists your workers, with the one you have already chosen when there is only one.
3. Select Start in the dialog. It claims the work for the worker, then starts its lane.

If the claim goes through and the lane start is refused, the work stays claimed. The dialog says so, and Start lane tries the lane again.

The same from the command line:

```sh
ropeshift work claim <work-order-id> --worker <worker-id>
ropeshift lane start <work-order-id>
```

The worker starts a Claude Code session in a worktree of its own, on a branch of its own, never in your checkout. To watch it:

```sh
ropeshift lane list
ropeshift lane attach <lane-id>
```

`lane list` names the lane and its state. `lane attach` puts your terminal on the session's pane; press `C-b d` to detach, which leaves the lane running. In the web UI, Floor shows the lane.

`ropeshift worker list` prints the worker's ID again if you need it.

## 8. Run the review

When the lane ends with a commit, ropeshift makes a review work order for that commit, titled `Review: <title>`, and it is `ready` at once. An agent never approves its own work, so a second, read-only lane judges each criterion. Start it as you did the first: open the review work order in Work orders, or find it under Ready to start on Floor, and select Start. From the command line:

```sh
ropeshift work list
ropeshift work claim <review-id> --worker <worker-id>
ropeshift lane start <review-id>
```

The review gives each criterion a verdict: `pass`, `fail`, or `cannot_judge`, with its reasons and citations. A failed criterion sends the work back to `ready` for another attempt, with the review's findings in that attempt's first prompt; start it again.

## 9. Accept the work

When every criterion passes, the work waits for you in Needs you.

![A work order waiting for acceptance, with the review's verdict on each criterion and the Accept button](assets/screenshots/work-order-light.png#gh-light-mode-only)
![A work order waiting for acceptance, with the review's verdict on each criterion and the Accept button](assets/screenshots/work-order-dark.png#gh-dark-mode-only)

1. In Needs you, select the work order. Its card shows the review's verdict on each criterion.
2. Read the verdicts, and select Accept, or Send back for rework with a reason.

The same from the command line:

```sh
ropeshift work show <work-order-id>
ropeshift work accept <work-order-id>
```

`work show` prints a `verdict` line for each criterion. After `work accept`, the work order reads `done`.

A new repository merges nothing by itself: its merge authority is `person_merges`, and its review target is `branch`, so the lane's branch stays on this machine and you merge it when you choose. `ropeshift work acceptance <work-order-id>` names the branch and its commit. [Connect a GitHub repository](github.md) shows how to review through pull requests and let ropeshift merge.

## Next steps

- Let an orchestrator claim ready work, including reviews, instead of claiming each one: [your first plan](first-plan.md#4-let-the-work-run).
- Plan real work with a planning session: [your first plan, end to end](first-plan.md).
- Review and merge through GitHub: [connect a GitHub repository](github.md).
- Read what a lane may do without asking, and the limit of same-user isolation: [before the first lane](onboarding.md#before-the-first-lane).
