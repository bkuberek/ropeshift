# Onboarding

This page takes one person from an empty machine to a first lane in solo mode, and says what to know before that lane runs. Solo mode runs on one machine, macOS or Linux on arm64 or amd64, with the Claude Code CLI as the agent runtime.

## Install ropeshift

One binary serves both commands, and the name it starts under picks the command: every channel installs `ropeshift` with `ropeshiftd` as a link to it, both on `PATH`, and the web UI built in. Every release is a pre-release until `1.0.0`. Use one channel:

| Channel | Command |
|---------|---------|
| Homebrew | `brew install bkuberek/tap/ropeshift` |
| Install script | `curl -fsSL https://raw.githubusercontent.com/bkuberek/ropeshift/main/install.sh \| sh` |
| Debian and Ubuntu | `sudo dpkg -i ropeshift_<version>_<arch>.deb`, from the [releases page](https://github.com/bkuberek/ropeshift/releases) |
| Fedora and RHEL | `sudo rpm -i ropeshift-<version>-1.<arch>.rpm`, from the same page |

Homebrew may ask you to trust the tap before it reads it. If it does, run `brew trust bkuberek/tap` once.

The install script installs to `~/.local/bin`, and with no `--version` it chooses the newest stable release, or the newest pre-release while no release is stable, and says which. It verifies the archive's SHA-256 against the release's checksums file before it extracts anything. Pass options after `sh -s --`, such as `sh -s -- --version v0.1.0-alpha.1`. [Releasing](releasing.md#the-install-script) lists every option.

The package names carry the version in the packaging system's form, such as `ropeshift_0.1.0~alpha.1_arm64.deb` and `ropeshift-0.1.0~alpha.1-1.aarch64.rpm`. Both packages recommend git and tmux.

Check the installation:

```sh
ropeshift version
```

It prints the release, the commit it was built from, and the platform, as [Versioning](versioning.md#what-a-binary-reports) describes. `ropeshift help` lists every command, and `ropeshift help <command>` prints one command's usage and flags.

Ropeshift also needs git, and tmux for lanes that run in a terminal pane.

### Build from source

`go install` is not an install channel for now, because a binary it builds embeds the placeholder page instead of the web UI ([releasing](releasing.md#install-channels)). A build from source needs no Node.js. To build from a checkout of this repository, with Go at the version `go.mod` names, put both names on `PATH`:

```sh
go build -o ropeshift ./cmd/ropeshift
ln -s ropeshift ropeshiftd
```

A build from a plain checkout serves a placeholder in place of the web UI. `make web-build`, which needs Node.js, writes the application that the next build embeds. Such a build reports its version as `dev`, because only a release build reports a release version ([Versioning](versioning.md)).

## Install and sign in to an agent CLI

Each agent CLI is installed and signed in on each worker, through its vendor's own flow. For Claude Code, that is [Anthropic's sign-in](https://code.claude.com/docs/en/authentication): run `claude` and sign in, or give the CLI an API key it reads itself.

Ropeshift never reads, stores, relays, or proxies an agent CLI's credentials. The CLI keeps its own sign-in, and ropeshift never offers a sign-in of its own. To learn what a machine can run, `ropeshift worker doctor` runs the vendor's status command, [`claude auth status`](https://code.claude.com/docs/en/cli-reference), and keeps only the kind of sign-in it reports: a subscription, an API key, signed out, or unknown. Nothing that names a person or an account leaves that command.

A worker that runs on a subscription is bound to the one person who signed in. A worker that several people share uses an API key.

## Run ropeshift init

`ropeshift init` sets up the machine, and a second run says only what is left.

1. With no daemon running, it runs one for the length of its own run, because a daemon's first start is what registers its worker, and it stops that daemon at its end.
2. It asks for the first passphrase on the terminal, twice. The passphrase comes only from a terminal, never from stdin, a flag, or a variable. A passphrase already set stays as it is.
3. It waits for the worker's registration.
4. It runs the doctor, and says for each agent CLI what to do next: install it, repair it, sign in through its vendor's own flow, or nothing, with the level the worker starts at.
5. It writes a sample contract to the installation's directory, and keeps one already there.

```text
Agent CLIs:
  claude-code: not installed. Install Claude Code, sign in through Anthropic's own flow, and run ropeshift init again.
Ropeshift never reads, stores, relays, or proxies an agent CLI's credentials. Each CLI keeps its own sign-in.
```

It exits 0 when an agent CLI is usable and 1 when none is, after every other step. The installation's directory is `~/Library/Application Support/ropeshift` on macOS, and `$XDG_STATE_HOME/ropeshift` or `~/.local/state/ropeshift` on Linux.

The doctor runs `claude --version` before anything else. A CLI on `PATH` that exits with a failure, ends on a signal, or prints no version has failed that check, and it is not usable: the line names how the check failed and says to run `claude --version` to see why, the doctor does not read its sign-in, and the worker advertises it as not usable, so work that names it is not offered there. A lane started on it anyway fails at its start, and no session runs. `ropeshift worker doctor` reports the same and exits 1.

## Start the daemon and run the sample

```sh
ropeshiftd solo --source /path/to/repository
```

The daemon reads work from the repositories it names, at the default branch, and serves the web UI at `http://127.0.0.1:7317` on loopback only. To add repositories, register their clones with `ropeshift repository add <path>`, which takes a clone or a folder of clones, says what it would do with `--dry-run`, and refuses a repository another project holds. A second product goes in a project of its own: `ropeshift project create <name>`, then `ropeshift repository add <path> --project <name>`. The daemon reads each repository in its project. `ropeshift top` watches the same fleet from a terminal. [The command line](cli.md#the-daemon) lists the daemon's flags and where an installation keeps its files.

To run the sample, copy `sample-contract.md` into the repository, change it to your own work, and commit it. Intake reads it, and its work order waits for a review: `ropeshift work list` names it, and `ropeshift work review <id>` passes the review. A member can then claim it and start its lane, or start the orchestrator and open a run window for it. [The command line](cli.md) lists every step.

## Start a planning session

With the daemon running, one command opens a planning session and puts your terminal in it:

```sh
ropeshift plan start --goal "Replace the checkout page"
```

It makes the calls a member makes by hand, and the ledger records each as yours. It creates a planning work order whose statement is the goal, passes the work order's contract review, claims it on the worker, starts a planning lane, and attaches your terminal to the lane's pane. A shell without a session asks for the passphrase first. `--goal-file` reads the goal from a file, and a session with no goal starts with the planning agent asking what the plan is for. The command plans in the one `--repository` names, or else in the project's planning repository: the one a member named with `ropeshift repository set <name> --planning`, or the project's only repository. With neither, it plans in the one repository the daemon reads. It runs on the one worker registered, or the one `--worker` names.

A planning lane reads every other repository of its project, so it plans against the code as it is. When its claim is granted, ropeshift records each repository's base branch tip, and the worker checks each one out at that commit, with no branch, beside the lane's worktree, in a directory named after the lane with `.reads` after it. The brief names each one. The lane may read them and may not write there: its policy denies an edit, a commit, a push, or a branch change in them, and the worker removes them when the lane ends. A repository whose checkout this machine does not keep, or whose base cannot be read, is named in the brief as one the lane could not read, and the lane starts all the same. A lane never reads a repository of another project.

A planning lane runs in a tmux pane, because you are present by construction. Without tmux on the machine, the work order stops in `blocked` with the reason `no_interactive_mode` and no session runs.

What you type goes to the agent. Detach with `C-b d`, or `C-b C-b d` inside tmux, because the status line is off. Detaching leaves the lane running. To rejoin it, run `ropeshift lane attach <lane>`; `ropeshift lane list` names the lane. The session also goes on through a park and through a restart of the daemon, which adopts the pane again, and `lane attach` follows the lane to the pane of its next generation. With no terminal, `--no-attach` or `--json`, `plan start` starts the session and prints the command that joins it.

The web UI shows the same pane, on the lane's page at `/lane?id=<lane>`. A planning work order's card in In progress or On hold leads there, and `plan start` prints the page's address beside the `lane attach` command when the web UI is on. The page needs a signed-in session. It shows the pane's screen with its colours while the page is open, and follows the lane through a park, a resume, or a restart to the pane of its next generation; while the lane is parked, it says so. Type a line in the field and send it, with Enter after it or without, and press Enter, Escape, Tab, Backspace, the arrow keys, Ctrl-C, or Ctrl-D with the buttons; with the field empty, Enter, Escape, and the up and down arrows go straight to the session. A terminal attached with `ropeshift lane attach` sees the same conversation at the same time. The page does not count as an attached terminal, so with no terminal attached, the agent's questions go to Needs you.

A planning lane's questions are yours. While you are attached, the agent asks in the conversation: its question tool shows in your terminal, and you answer there. With nobody attached, or when the worker cannot tell, the question tool is refused and the agent asks through `ask`, which puts the question in Needs you as a product question, with no orchestrator to decide it or assume an answer for it. The worker tells the lane's hook who is attached about once a second, so a question asked in the moment after you attach or detach can go either way.

When the planning lane has finished, the plan waits for you. `ropeshift plan show <plan>` prints the plan, and under it the request to approve it: what the version holds, how it differs from the last version you approved, and each answer you gave in the conversation that the plan drafts. `ropeshift plan approve <plan>` approves that version in one step. It records each drafted answer as your decision, passes the contract review of the work the plan's contracts cover, and accepts the planning work order, so there is no review to pass for each work order and no acceptance to give for the plan. A work order whose contract has a question still open waits for the question, and the definition of ready still applies. A change to any document or contract of an approved plan is a new version with its own request, which shows what changed; work under a contract that did not change goes on. `ropeshift top` and the Needs you view list each plan that waits, and each work order whose contract waits for a review that no plan stands behind. In the web UI, a plan's card in Needs you shows the same request, with the difference and each drafted answer, and approves or denies it; a contract review's card leads to the contract, and passes the review or sends the work back to planning with a reason. The planning lane cannot approve the plan it wrote, and neither can an orchestrator: only you can, with a session.

## Adopt an existing project

To bring a project that already exists into ropeshift, run one command in its repository with the daemon running:

```sh
cd /path/to/your/repository
ropeshift adopt
```

It makes the calls a member makes by hand, and the ledger records each as yours. When no project holds the repository, it creates one named after the repository's directory, or the one `--project` names. It registers the repository, and every clone under paths you give it, such as `ropeshift adopt ../services` for a folder of clones, as `ropeshift repository add` does. A project of one repository plans in it. A project of several plans in the repository `--planning` names, or the one it already plans in, or else the command asks which one holds the plan. It then starts the adopt session and attaches your terminal, as `ropeshift plan start` does, with the same `--worker` and `--no-attach`. A project that already has an approved plan is refused: plan its next work with `ropeshift plan start`.

The adopt session is a planning session, so everything above holds for it: it runs in a pane, its questions are yours, your answers become draft decisions in the plan, and it survives a detach, a park, and a restart of the daemon. Before its lane starts, the worker writes an inventory of every repository of the project into `.ropeshift/context/inventory.json` beside the lane's context pack: each repository's remote, default branch and tip, every Markdown file with its front matter format and headings, the files already in ropeshift's format and whether intake reads them cleanly, and every branch the base does not hold, with where it left the base and how far ahead and behind it is. No model writes it, so the same commits give the same file. Open pull requests and issues are not read yet, and the inventory says so.

The session's brief asks the agent to:

- Onboard, do not rewrite: add front matter to your existing requirements, specs, architecture notes, and decision records, keep their prose and their IDs, add a `[[requirement]]` table for each requirement a document states under the ID it already has, and move a file only when the format needs it elsewhere, with `git mv`.
- Ask you about anything that belongs to someone else: a file whose front matter belongs to another tool, a document whose meaning is unclear, and two documents that contradict each other.
- Mark what is done: a requirement the code already meets gets `status = "met"` with `evidence` citing files and tests, and a milestone or contract the code already delivers gets `existing = true` and no work orders.
- Plan what remains, with shared contracts first, as any plan does.
- Ask you which in-flight branches to keep: each kept branch becomes a work order with `continues`, whose lane starts from the branch's tip on a branch of its own and never moves the branch, and each branch left out is recorded in the plan's `[[left_out]]` with your reason.
- Write nothing else: the lane commits only on its own branch of the planning repository, and reads every other repository of the project through read-only worktrees.

When the session ends, the plan waits for you, and your default branch is unchanged. `ropeshift approval list` names the request, and `ropeshift approval show <id>` prints it grouped: what exists, each claim with its evidence, which approving it makes your word; what continues; what is new; what is left out, with each reason; and every file the adoption branch added, changed, moved, or deleted. The plan's card in the web UI's Needs you shows the same groups. `ropeshift approval approve <id> --digest <digest>` approves it in one step, with the digest `approval show` printed: it decides the plan's draft decisions as yours, accepts the session's work order, and intake records the plan at the approved commit, so its work orders exist before any merge. Merge the adoption branch yourself once you have approved it, because ropeshift does not merge it; `ropeshift work acceptance <work-order>` names the branch and its commit among the work's artifacts. A work order that continues a branch then starts from that branch's tip.

## Run the daemon as a service

`ropeshift service install` runs the daemon under this machine's service manager from now on, with the flags that follow `--`. It runs the `ropeshiftd` beside the `ropeshift` that installs it, and a source needs its absolute path.

```sh
ropeshift service install -- --source /path/to/repository
```

The install script and the `.deb` and `.rpm` packages install no service, so this command is how a daemon runs as a service on those channels. A Homebrew install can run it through `brew services start ropeshift` instead, with no repository source, and logs to `$(brew --prefix)/var/log/ropeshift.log`. Use one or the other, because two daemons for one installation conflict.

On macOS, it writes a launchd agent named `dev.ropeshift.ropeshiftd` to `~/Library/LaunchAgents` with `ProcessType` set to `Interactive`, because launchd throttles the CPU and I/O of a job with no process class, and loads it. The daemon's output goes to a log named after the agent in the installation's directory. On Linux, it writes a systemd user unit named `ropeshiftd` to `$XDG_CONFIG_HOME/systemd/user`, which is `~/.config/systemd/user` by default, enables it, and starts it, and the daemon's output goes to `journalctl --user`. `--name` chooses another name. Either manager restarts the daemon after a crash. A second install replaces the first, and `ropeshift service uninstall` stops the service and removes its definition.

A stop of the service stops no lane. Each lane's session, and the worker's terminal server, runs outside the service: in a process session of its own on macOS, and in a transient systemd scope on Linux. The daemon that starts next adopts them.

The service keeps this shell's `PATH`, so it finds the agent CLI, git, and tmux where this shell does. It also keeps `LANG`, `LC_ALL`, `LC_CTYPE`, `TZ`, `XDG_STATE_HOME`, and ropeshift's own `ROPESHIFT_STATE_DIR`, `ROPESHIFT_RUN_DIR`, `ROPESHIFT_WEB`, and `ROPESHIFT_TELEMETRY`, and nothing else, so a variable that a profile passes to a lane by `env:` must reach the service another way, such as a systemd drop-in.

On Linux, a user's systemd manager stops when that user's last session ends, and every lane under it stops too, unless lingering is on. Turn it on once, so that lanes keep running after you log out and the daemon starts at boot:

```sh
loginctl enable-linger "$USER"
```

## Before the first lane

### The limit of same-user isolation

In solo mode, lanes run as the same operating system user as you, so the operating system does not separate an agent from your files, your browser, or a session you hold. A passphrase raises the cost of an agent approving its own request and makes an attempt visible. It does not make it impossible. [Authorization](authorization.md#the-limit-of-same-user-isolation) lists what a lane with a shell can reach. Strong separation needs a boundary an agent cannot cross: another operating system user, a container, a virtual machine, or a worker on another machine.

### Repository content can execute

A repository's own configuration can run code. In a headless session, [Claude Code treats the folder as trusted](https://code.claude.com/docs/en/hooks), so hooks committed to a repository run, and an interactive lane accepts the same trust at its start. A lane edits its worktree and commits with no approval request, and a commit runs the repository's commit hooks, so where those hooks run a file in the worktree, or `core.hooksPath` names a directory in it, a lane runs code it wrote at its next commit, with nobody asked. Run lanes on work from outside your team only behind one of the boundaries above.

### Work stays on this machine

When a lane stops, its worker saves what the worktree holds: commits stay on the lane's branch, and uncommitted changes go to a checkpoint ref under `refs/ropeshift/`. By default nothing is pushed, so the branch and its refs exist only on this machine. A lane whose worktree holds commits or changes the remote lacks reads `unpushed_work`, `ropeshift lane archive` needs `--force` to remove its worktree, and a parked lane resumes only on this machine. A lane's branch starts from the repository's [base branch](#where-a-lane-starts) as the remote holds it, never from what your checkout has out, so a push of it sends the lane's commits and, for work that starts from accepted work, that work's commits too.

To have the worker push the lane's branch and its checkpoint refs to the repository's remote when the lane stops and before its worktree is removed, so the work survives the loss of this machine and a parked lane can resume on another, set this in your own profile layer, `profile.toml` in the installation's directory:

```toml
[worktree]
push_on_stop = true
```

Only your layer sets it, because a push sends work off the machine. A repository's `.ropeshift/profile.toml` and a lane's `--profile` layer cannot change it: a layer that sets another value is refused, and the lane does not start. A push never forces and runs no repository hook.

### What a lane does without asking

A lane that writes, which is a change, rework, planning, or adopt lane, works in its own worktree with no approval request. It creates, edits, and deletes files there with the agent's file tools, runs `mkdir`, `rm`, `rmdir`, and `mv` there, and stages and commits on its own branch with `git add`, `git rm`, `git mv`, and `git commit`. Nobody has to be watching for that work to go on.

Everything else still asks, and the request waits for a member:

- A write outside the worktree. Where the lane's hook can read the path, it refuses the call outright, and that includes a path that climbs out with `..` and a file program whose path passes through a link. A file tool that writes through a link that leads out of the worktree asks.
- A push of any kind. A force push, and a push to the default branch or to a branch the lane continues, are refused.
- A move of any other branch, and any command not listed above.
- Every action your profile gates.

A review lane only reads: it cannot edit a file with a tool, commit, push, or move a branch.

Two keys in the `[agent]` table of a profile control this:

```toml
[agent]
permission_mode = "default"
gates = ["Bash(git commit *)", "Edit"]
```

`permission_mode` is the agent's permission mode, `default` unless you set another. A repository's `.ropeshift/profile.toml` and a lane's `--profile` layer cannot raise it. `gates` lists permission rules, in the agent CLI's own syntax, for actions a member must approve. Each becomes a rule that asks in every permission mode, and it overrides what a lane would otherwise do without asking, so the example above makes every commit and every file edit ask. A gate on a file tool takes the `Edit` rule form, because the agent CLI matches file paths only against `Edit` and `Read` rules. A gate can only add requests: no key lets a lane do more without asking than the list above.

### How many lanes run at once

One worker runs the lanes of every project of the installation on this machine, so its caps count the lanes of every project together. Set them in your own profile layer:

```toml
[admission]
max_lanes = 6
max_lanes_per_sign_in = 3
```

`max_lanes` caps the lanes this machine runs, and `max_lanes_per_sign_in` the lanes on one agent sign-in, because every session on a subscription draws from the same usage limits. `0`, or no value, caps nothing. Only lanes count: a worker's orchestrator never does, and a cap never holds it back. A lane a cap holds keeps its claim, its work order says why in a note, and it starts once a lane ends.

A repository's `.ropeshift/profile.toml` and a lane's `--profile` layer can lower either cap for their own lanes, and cannot raise it: a layer that sets a higher value than yours, or no cap where you set one, is refused, and the lane does not start. Each project's own caps, which `ropeshift fleet caps` sets, still hold inside that project, so a cap per worker in one project keeps it from taking every lane on the machine.

### Where a lane starts

A lane's worktree starts from the repository's base branch, and never from the commit your checkout has out. The base branch is the repository's default branch, which the daemon reads from git, until you name another:

```sh
ropeshift repository list
ropeshift repository set 3f2a1b0 --base release/1.2
```

A work order can name its own base with `base = "release/1.2"` in its `[[work_orders]]` table, which overrides the repository's. With a remote, ropeshift fetches the base branch before it grants a claim and starts the lane from the commit the remote holds. With none, it starts from the local branch. If the fetch fails, or the branch does not exist, the work order stays ready with the reason `base_unavailable` on it, and the next claim or the next pass of the daemon's intake tries again. A lane never starts from your checkout instead. The lane records the ref and the commit it started from, and `ropeshift lane show` prints them.

A work order can finish work someone started on a branch with `continues = "feature/retry"` in its `[[work_orders]]` table. Its lane starts from that branch's tip, on a branch of its own, and records where the branch left the work order's base as its base commit, so its review reads the whole change and not only the lane's commits. Its work merges into the base like any other. Ropeshift never moves, rewrites, or deletes the branch it continues, and the lane's policy denies a push to it and any command that moves it or switches to it. With a remote that holds the branch, the lane starts from the remote's copy, and otherwise from the local branch. A branch that neither holds is a problem `source list` reports for the work order, and the work order stays ready with the reason `continued_branch_missing` until the branch is there.

Work that lists another work order in the same repository in `depends_on` starts from that work once it is in the base branch. Until the dependency's accepted commit is an ancestor of the base branch, a claim is refused and the work order reads `waiting_for_merge` and names the dependency, so merge the dependency's branch and the reason clears by itself. A repository can start dependent work from the dependency's accepted commit instead:

```sh
ropeshift repository set 3f2a1b0 --start-from accepted
```

The lane then starts from the accepted commit of the dependency that holds the accepted commits of the others, and a work order whose dependencies are on separate lines of work reads `dependencies_diverge` until a person joins them. Ropeshift refuses `--start-from accepted` for a repository whose merge method is `squash` or `rebase`, because those write new commits and the branch the dependent work was built on is not the one the base then holds. A dependency in another repository gates readiness as it always did and moves nothing here.

### Who reviews a lane's work

An agent does not approve its own work. When a lane completes a `change` or `rework` work order and leaves a commit, ropeshift makes a `review` work order for that commit in the same step. It pins the contract version the work ran on, it is ready at once, and it is claimed and dispatched like any other work: `ropeshift work list` shows it as `Review: <title>`, and `ropeshift work show --json` on it names the work order it judges in `reviews`. A lane that leaves no commit has nothing to review and makes no review. A review is no part of the plan its contract belongs to, so it counts in no milestone and no critical path.

A review's lane has the `reviewer` role, which reads the `writer` context thresholds in your profile unless it names thresholds of its own. It starts at the commit under review in a worktree that has no branch, and the worker leaves the diff from where the reviewed lane began, and the paths it touches, in `.ropeshift/context/review.diff` and `.ropeshift/context/review-files.txt` beside the pack. Its session is read-only: it holds no rule that stages or commits, it may not edit a file with a tool, and it may not commit, push, or move a branch or a tag. A shell command can still write a file in its worktree, which is thrown away: when the lane ends the worker removes the worktree, and the repository holds the same branches it held before. The review lane reports through `lane_report` as any lane does, with a verdict for every criterion of the contract: `pass`, `fail`, or `cannot_judge`, each with its reasons and citations to files and lines, requirement IDs, and documents. A report that leaves a criterion out, or judges one the contract does not have, is refused, and the lane is told which. A verdict is bound to the commit the review read, so a branch that moves, or a later attempt, is never judged by an earlier verdict. A review that completes is `done`, and its verdicts decide the work it judged.

The work it judges waits in `acceptance` while its review is open, and the forge tick leaves it alone. What happens when the verdicts arrive follows the repository's merge authority, which only you set:

```bash
ropeshift repository set 3f2a1b0 --merge-authority review_approves
```

- A criterion the review failed, or a check that failed, sends the work back to `ready` by rule, with the review named in the record. The next attempt's first prompt says what the review found: each failed criterion with the reviewer's reasons and citations. After `max_review_rounds` reviews of one work order, 3 unless a `[review]` table in a profile says otherwise, work the review still fails waits for you instead.
- When every criterion passed, `person_merges`, the default, and `person_approves` leave the decision to you: the work waits in Needs you, with the verdict for each criterion beside Accept and Send back. At `review_approves` the review accepts it, as the system, and the audit names the review.
- A criterion the review could not judge waits for you, unless a passing check from the forge covers it. A check the lane reported about its own work never does.

You can accept or send back the work at any time, with `ropeshift work accept` or `ropeshift work rework`, and doing so cancels a review that is still open. An orchestrator has no tool to accept work: the review is the second reader.

### Claude subscriptions and automated use

Ropeshift is designed to run the unmodified `claude` binary that you installed and signed in to, as your own session. It is an independent project. It is not affiliated with or endorsed by Anthropic, and no vendor has approved this use. Nothing here is legal advice.

Anthropic's rules leave a risk that ropeshift cannot remove:

- Anthropic's [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) prohibit accessing its services "through automated or non-human means, whether through a bot, script, or otherwise", except through an API key or where Anthropic explicitly permits it.
- Its [legal and compliance page](https://code.claude.com/docs/en/legal-and-compliance) says that the advertised usage limits "assume ordinary, individual usage of Claude Code and the Agent SDK", and that Anthropic may enforce its restrictions "without prior notice".
- The same page says its rules do not prevent an end user from signing in to the unmodified Claude Code binary with their own subscription. Running the unmodified binary is required, and it does not by itself settle whether automated use is ordinary use. That is Anthropic's decision.
- Autonomous operation moves further from ordinary use. An orchestrator that claims work and answers questions with nobody present is more automated than a person running sessions side by side, and a fleet that runs overnight is further still.

What that means for you: Anthropic may rate-limit, restrict, or suspend an account, parallel lanes draw on your subscription's limits quickly, as [Anthropic's Help Center](https://support.claude.com/en/articles/15036540) describes for `claude -p`, and the terms can change.

Ropeshift's defaults keep a subscription worker nearer to a person at work, and they do not decide whether its use is ordinary:

- A subscription worker starts at `attended`. Its orchestrator claims work only inside a run window a member opened, which lasts 4 hours by default and closes by itself, and lanes and generations a day are bounded. A sign-in nobody could read starts there too.
- On a machine with tmux, a lane on a subscription runs in a terminal pane by default, so each one stays a Claude Code session you can attach to.
- `unattended` on a subscription needs a member to read the statement `ropeshift worker risk` prints and acknowledge it for that worker. The acknowledgement is a record that a person accepted a risk. It is not permission from anyone, and it does not make automated use acceptable under any vendor's terms.
- For certainty, sign the CLI in with an API key instead, used under the key owner's own agreement with Anthropic. A worker on an API key starts at `unattended`.

These defaults reduce the risk, and they do not remove it. [Authorization](authorization.md) states the levels, and the vendor pages above are the ones to read before the first lane, because they can change.

## Back up a project and the installation

```sh
ropeshift backup /path/to/project-backup.db
ropeshift backup --installation /path/to/installation-backup.db
```

The daemon copies a ledger through SQLite's online backup, so it goes on writing while the copy is made. Each copy is one SQLite file, readable only by its owner, and a file already at the path is never replaced. The first command copies one project, chosen as for any command on a project's records: every record of it, its work, plans, questions, decisions, policies, and repositories, and nothing of another project. `ropeshift project import` registers that copy in another installation with the project's ID. The worker's journal, which holds the lanes on this machine, stays outside the copy.

The second copies the installation database, `installation.db`: the registry of projects, the member, the passphrase's hash, the member sessions, the workers, their autonomy and run windows, and the orchestrators. Restoring a whole installation takes its installation backup and each project's backup. A fresh installation runs on a copy of its first project when the copy is its `ledger.db` before its first start, and asks for a new passphrase at its first sign-in unless its `installation.db` is a copy too.
