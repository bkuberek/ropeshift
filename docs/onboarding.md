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

A project of many repositories can keep a manifest of them in its planning repository: a file of kind `repositories` with a `[[repository]]` table for each repository, giving its `name` in the project, its `remote`, the `path` of its clone relative to the planning repository's clone, such as `../api`, and a `description`. A plan lists the manifest in its `documents` as `repositories:<id>`, so approving the plan covers its version. Intake reads a manifest only from the planning repository, and reports each repository of the project the manifest does not list; ropeshift removes none, and you remove one with `ropeshift repository remove`. A manifest never sets the review target, the merge method, `start_from`, the forge binding, or the merge authority: those stay your settings, and any key the format does not have is a problem `source list` reports, with no effect.

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

The web UI shows the same pane, on the lane's page at `/lane?id=<lane>`. A planning work order's row in Work orders leads there, with Open the planning session, and `plan start` prints the page's address beside the `lane attach` command when the web UI is on. The page needs a signed-in session. It shows the pane's screen with its colours while the page is open, and follows the lane through a park, a resume, or a restart to the pane of its next generation; while the lane is parked, it says so. Type a line in the field and send it, with Enter after it or without, and press Enter, Escape, Tab, Backspace, the arrow keys, Ctrl-C, or Ctrl-D with the buttons; with the field empty, Enter, Escape, and the up and down arrows go straight to the session. A terminal attached with `ropeshift lane attach` sees the same conversation at the same time. The page does not count as an attached terminal, so with no terminal attached, the agent's questions go to Needs you. A planning session also has a page of its own at `/projects/<project>/planning?lane=<lane>`. It names the session, its work order, its lane, and its worker, shows the pane with Detach and Attach, and beside the pane shows the plan the session has committed so far, read from the lane's branch: the documents added or changed, the milestones, the contracts with their criterion and work order counts, the draft decisions, the questions nobody has answered, and, when a blocking question is open, why the version is not submitted. Only committed files show, and `ropeshift lane outline <lane>` prints the same. A parked session shows its last screen, labeled parked, and Resume. Start planning session on the Roadmap, Plans, and Planning pages starts a session as `plan start` does, with the goal as text or a file read in the browser, the planning repository, and the worker.

A planning lane's questions are yours. While you are attached, the agent asks in the conversation: its question tool shows in your terminal, and you answer there. With nobody attached, or when the worker cannot tell, the question tool is refused and the agent asks through `ask`, which puts the question in Needs you as a product question, with no orchestrator to decide it or assume an answer for it. The worker tells the lane's hook who is attached about once a second, so a question asked in the moment after you attach or detach can go either way.

When the planning lane has finished, the plan waits for you. `ropeshift plan show <plan>` prints the plan, and under it the request to approve it: what the version holds, how it differs from the last version you approved, and each answer you gave in the conversation that the plan drafts. `ropeshift plan approve <plan>` approves that version in one step. It records each drafted answer as your decision, passes the contract review of the work the plan's contracts cover, and accepts the planning work order, so there is no review to pass for each work order and no acceptance to give for the plan. A work order whose contract has a question still open waits for the question, and the definition of ready still applies. A change to any document or contract of an approved plan is a new version with its own request, which shows what changed; work under a contract that did not change goes on. The approval is also the review of the planning branch the lane left, and at `person_approves` and `review_approves` ropeshift merges that branch at the approved commit once intake has recorded the plan, as [a planning branch](#a-planning-branch) describes; at `person_merges`, the default, you merge it yourself. `ropeshift top` and the Needs you view list each plan that waits, and each work order whose contract waits for a review that no plan stands behind. In the web UI, a plan's card in Needs you shows the same request, with the difference and each drafted answer, and approves or denies it; the plan approval page, at `/projects/<project>/plan?id=<plan>&version=<n>`, shows it in full, with the contracts the version adds or changes and a link to the difference between the versions of each document it covers, and approves it, or declines it with a reason; a version a newer one replaced is void, and a version already decided is read-only; a contract review's card leads to the contract, and passes the review or sends the work back to planning with a reason. The planning lane cannot approve the plan it wrote, and neither can an orchestrator: only you can, with a session.

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

When the session ends, the plan waits for you, and your default branch is unchanged. `ropeshift approval list` names the request, and `ropeshift approval show <id>` prints it grouped: what exists, each claim with its evidence, which approving it makes your word; what continues; what is new; what is left out, with each reason; and every file the adoption branch added, changed, moved, or deleted. The plan's card in the web UI's Needs you shows the same groups. `ropeshift approval approve <id> --digest <digest>` approves it in one step, with the digest `approval show` printed: it decides the plan's draft decisions as yours, accepts the session's work order, and intake records the plan at the approved commit, so its work orders exist before any merge. The adoption branch then merges as any planning branch does, as [a planning branch](#a-planning-branch) describes: at `person_approves` and `review_approves` ropeshift merges it at the approved commit, and at `person_merges`, the default, you merge it yourself; `ropeshift work acceptance <work-order>` names the branch and its commit among the work's artifacts. A work order that continues a branch then starts from that branch's tip.

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

- A write outside the worktree. Where the lane's hook can read the path, it refuses the call outright, and that includes a path that climbs out with `..` and a file program whose path passes through a link. A file tool that writes through a link that leads out of the worktree asks, or is refused where a generated deny rule names the place it lands.
- A push of any kind. A force push, and a push to the default branch or to a branch the lane continues, are refused.
- A move of any other branch, and any command not listed above.
- Every action your profile gates.

A review lane only reads: it cannot edit a file with a tool, commit, push, or move a branch.

No lane gets a forge credential. Its session starts without `GH_TOKEN`, `GITHUB_TOKEN`, `GH_ENTERPRISE_TOKEN`, or `GITHUB_ENTERPRISE_TOKEN`, with `GH_CONFIG_DIR` pointed at an empty directory of its own, so a `gh` it runs finds no sign-in ([gh environment](https://cli.github.com/manual/gh_help_environment)). A profile whose `secret_ref` names one of those variables is refused, and the lane does not start. Pushes and pull requests are the worker's own steps, never the agent's: see [Review through pull requests](#review-through-pull-requests). Like every lane guardrail, this is not a sandbox.

Two keys in the `[agent]` table of a profile control this:

```toml
[agent]
permission_mode = "default"
gates = ["Bash(git commit *)", "Edit"]
```

`permission_mode` is the agent's permission mode: `default` unless you set `plan`, `acceptEdits`, `dontAsk`, or `auto`. A member's assistant runs in `default` whatever this sets. A repository's `.ropeshift/profile.toml` and a lane's `--profile` layer cannot raise it. No layer, yours included, can choose `bypassPermissions`, which skips every check, or a mode ropeshift does not know: the profile is refused, and the lane does not start. Every session's generated settings also turn the bypass mode off in the agent CLI. `gates` lists permission rules, in the agent CLI's own syntax, for actions a member must approve. Each becomes a rule that asks in every permission mode, and it overrides what a lane would otherwise do without asking, so the example above makes every commit and every file edit ask. A gate on a file tool takes the `Edit` rule form, because the agent CLI matches file paths only against `Edit` and `Read` rules. A gate can only add requests: no key lets a lane do more without asking than the list above.

### How many lanes run at once

One worker runs the lanes of every project of the installation on this machine, so its caps count the lanes of every project together. Set them in your own profile layer:

```toml
[admission]
max_lanes = 6
max_lanes_per_sign_in = 3
```

`max_lanes` caps the lanes this machine runs. With no value, the worker's recommendation caps them; `0` caps nothing, and any positive number stands, with no upper bound. `ropeshift worker caps <id> --max-lanes N --per-sign-in N` sets both in this file and shows the recommendation beside them; a value above the recommendation is saved with a warning that says why it is the recommendation. The worker reads a change at its next start and its next measure, with no restart. `max_lanes_per_sign_in` caps the lanes on one agent sign-in, because every session on a subscription draws from the same usage limits; `0`, or no value, caps nothing. Only lanes count: a worker's orchestrator never does, and a cap never holds it back. A lane a cap holds keeps its claim, its work order says why in a note, and it starts once a lane ends.

The worker recommends a cap from what the machine can run: its cores and its memory, less a reserve for the system, the daemon, the orchestrator, and the assistant, each divided by what one lane takes, and the lower of the two, never below one:

```text
recommended = max(1, min(floor((cores - reserve_cpu) / lane_cpu), floor((memory - reserve_memory) / lane_memory)))
```

What one lane takes starts as the estimates below. Once `observed_min` lanes have run on the worker, it is the highest peak among the last `observed_window` lanes instead: the cores a lane's process tree kept busy, averaged over a minute and counting the children its processes reaped, and its resident memory. A peak never counts as less than half its estimate, so a run of quiet lanes lifts the recommendation at most to twice what the estimates give. These settings are the machine's, so only your own layer sets them:

| Setting | Default | Meaning |
|---------|---------|---------|
| `lane_cpu_estimate` | `1.0` | Cores one lane takes, until enough lanes have run |
| `lane_memory_estimate` | `"2GiB"` | Memory one lane takes, until enough lanes have run |
| `reserve_cpu` | `1.0` | Cores kept for the system, the daemon, the orchestrator, and the assistant |
| `reserve_memory` | `"4GiB"` | Memory kept the same way |
| `observed_window` | `50` | Recent lanes whose peaks replace the estimates |
| `observed_min` | `10` | Lanes that must have run before their peaks apply |

A size is a number of bytes, or a number with `KiB`, `MiB`, `GiB`, or `TiB`, or `KB`, `MB`, `GB`, or `TB`. The defaults are starting values to tune, not measurements. A machine's cores and memory do not change while it runs, so a read that fails keeps what the last one counted. Until a read has counted the cores, the worker recommends one lane and says why, so a machine it cannot read never leaves `max_lanes` uncapped by accident.

A repository's `.ropeshift/profile.toml` and a lane's `--profile` layer can lower either cap for their own lanes, and cannot raise it: a layer that sets a higher value than yours, or no cap where you set one or left the recommendation, is refused, and the lane does not start. Where you left the recommendation, a layer's value holds only up to it. Each project's own caps, which `ropeshift fleet caps` sets, still hold inside that project, so a cap per worker in one project keeps it from taking every lane on the machine.

### What the worker measures

Each worker measures its machine and its sessions every `report_interval`, and sends what it measured to the control plane with its next heartbeat:

- The machine's CPU use and core count, its load averages, its memory in all, used, and available, and its memory pressure where the system provides it.
- The free space on the volumes that hold the state directory and the worktrees of the lanes that run.
- The CPU and resident memory of each session's process tree: each lane and the orchestrator.
- How many lanes hold a place under the lane caps, from which the control plane records when the worker stands at a cap ([capacity intervals](measures.md#capacity-intervals)).

On Linux the worker reads `/proc` for CPU, load, memory, pressure, and each process, and `statfs` for free space; a process's CPU time includes the children it reaped. On macOS it runs `sysctl`, `vm_stat`, and `ps -S`, which adds each process's exited children to its time, and reports no machine CPU use and no memory pressure. A measure the machine cannot provide is reported as unavailable, never as zero.

The `[resources]` table of your own layer sets how often the worker measures and where a measure warns. A warning shows on the machine; it never stops, parks, or refuses work by itself. Only the memory and CPU floors of `[admission]` hold a lane back.

| Setting | Default | Warns when |
|---------|---------|------------|
| `report_interval` | `"15s"` | Not a threshold: how often the worker measures, at least a second |
| `disk_free_warn` | `"10GiB"` | A watched volume has less free space |
| `memory_available_warn` | `0.10` | Available memory is a smaller share of the total |
| `memory_pressure_warn` | `10.0` | Some task waited on memory for more of the last minute, in percent |
| `cpu_load_warn` | `1.5` | The five-minute load per core is higher |

A threshold of `0` turns its warning off. These settings are the machine's, so a repository's or a lane's layer cannot change them.

`ropeshift worker list` and `ropeshift top` show what each worker last measured and how long ago, with each lane's process tree in `top`, and the web UI's Workers page shows the same for every machine and session, with each warning. A worker that has not reported yet says so.

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

The lane then starts from the accepted commit of the dependency that holds the accepted commits of the others, and a work order whose dependencies are on separate lines of work reads `dependencies_diverge` until a person joins them. Ropeshift refuses `--start-from accepted` for a repository whose merge method is `squash` or `rebase`, because those write new commits and the branch the dependent work was built on is not the one the base then holds. A dependency in another repository never moves the start, because a build in one repository cannot use an unmerged branch of another, and starting from accepted work never applies to it: the work is ready only once that dependency is `done`, which is once its merge landed, or once you accepted it in a repository at `person_merges` with review target `branch`, and the lane reads that repository instead.

A lane that changes code reads the other repositories of its project it builds on, read-only. When its claim is granted, ropeshift records each repository a dependency of the work order targets, at the commit ropeshift's merge of that dependency made, or at the repository's base branch tip when ropeshift recorded no merge, as for work you merged by hand. With several dependencies in one repository, it records the merge that holds the others, or the base branch tip when none does. A contract names more repositories for its lanes to read with `reads = ["schema"]`, by their names in the project, and each is recorded at its base branch tip; an entry that is not a repository name, such as `"."`, is a problem `source list` reports, and intake records nothing from that file. The worker checks each one out as it does for [a planning lane](#start-a-planning-session), and the brief names it. A name the project does not hold, a repository this machine keeps no checkout of, or a base that cannot be read is named in the brief as one the lane could not read, and the lane starts all the same. The lane's own repository is its worktree, and no lane reads a repository of another project. The review of the lane's work reads the same repositories at the same commits, so it judges the work against the code its writer saw.

### Who reviews a lane's work

An agent does not approve its own work. When a lane completes a `change` or `rework` work order and leaves a commit, ropeshift makes a `review` work order for that commit in the same step. It pins the contract version the work ran on, it is ready at once, and it is claimed and dispatched like any other work: `ropeshift work list` shows it as `Review: <title>`, and `ropeshift work show --json` on it names the work order it judges in `reviews`. A lane that leaves no commit has nothing to review and makes no review. A review is no part of the plan its contract belongs to, so it counts in no milestone and no critical path.

A review's lane has the `reviewer` role, which reads the `writer` context thresholds in your profile unless it names thresholds of its own. It starts at the commit under review in a worktree that has no branch, and the worker leaves the diff from where the reviewed lane began, and the paths it touches, in `.ropeshift/context/review.diff` and `.ropeshift/context/review-files.txt` beside the pack. Its session is read-only: it holds no rule that stages or commits, it may not edit a file with a tool, and it may not commit, push, or move a branch or a tag. A shell command can still write a file in its worktree, which is thrown away: when the lane ends the worker removes the worktree, and the repository holds the same branches it held before. The review lane reports through `lane_report` as any lane does, with a verdict for every criterion of the contract: `pass`, `fail`, or `cannot_judge`, each with its reasons and citations to files and lines, requirement IDs, and documents. A report that leaves a criterion out, or judges one the contract does not have, is refused, and the lane is told which. A verdict is bound to the commit the review read, so a branch that moves, or a later attempt, is never judged by an earlier verdict. A review that completes is `done`, and its verdicts decide the work it judged.

The work it judges waits in `acceptance` while its review is open, and the forge tick leaves it alone. What happens when the verdicts arrive follows the repository's merge authority, which only you set:

```bash
ropeshift repository set 3f2a1b0 --merge-authority review_approves
```

- A criterion the review failed, or a check that failed, sends the work back to `ready` by rule, with the review named in the record. The next attempt's first prompt says what the review found: each failed criterion with the reviewer's reasons and citations. After `max_review_rounds` reviews of one work order, 3 unless a `[review]` table in a profile says otherwise, work the review still fails waits for you instead.
- When every criterion passed, `person_merges`, the default, leaves the decision to you: with review target `branch`, the work waits in Needs you, with the verdict for each criterion beside Accept and Send back, and through a pull request it waits in `integrating` for you to merge the pull request. At `person_approves` and `review_approves` the work is accepted and waits in `integrating` while ropeshift merges it, as [merging accepted work](#merging-accepted-work) describes: at `person_approves` the merge waits for your approval, and at `review_approves` the review's pass is the approval, as the system, and the audit names the review lane.
- A criterion the review could not judge waits for you, unless a passing check from the forge covers it. A check the lane reported about its own work never does.

You can accept or send back the work at any time, with `ropeshift work accept` or `ropeshift work rework`, and doing so cancels a review that is still open. An orchestrator has no tool to accept work: the review is the second reader.

### Review through pull requests

By default a review reads the lane's branch on this machine, and nothing is pushed. A repository on GitHub can instead review each change through a pull request, so your team sees it while it is reviewed:

```sh
ropeshift repository set widgets --review-target pull_request
```

This needs the [GitHub CLI](https://cli.github.com/), `gh`, installed on the machine whose worker keeps the repository's checkout, on the daemon's `PATH`, and signed in to the repository's host with `gh auth login`, which you run yourself. Ropeshift never signs in, never reads or stores the token, and runs `gh` under your own sign-in. The repository needs a forge binding, `HOST/OWNER/REPO`, which ropeshift takes from a remote such as `git@github.com:acme/widgets.git`; for any other remote, name it with `--forge-binding github.com/acme/widgets`.

The setting takes effect even when no worker has `gh` yet, because the worker's `gh` is not something the control plane can see when you change the setting, and `repository set` warns you instead. Each worker that keeps a checkout of the repository reads `gh auth status --hostname <host>` for it once a minute, or as often as `--forge-tick` says, and `ropeshift repository show` and the Measures page of the web UI show what it found: `ok`, `signed_out` with what `gh` said, or `missing`.

When a `change` or `rework` lane completes with a commit, the worker that ran it, never the agent, pushes the lane's branch to the repository's remote and opens a pull request against the work order's base, with the work order, its contract, and its criteria in the description. A push never forces and runs no repository hook. The review waits, ready but held, as `pull_request_pending` until the pull request is open with the lane's commit as its head, and as `forge_unavailable` while that worker's `gh` is missing or signed out. A push or pull request that fails, such as one branch protection refuses, is tried again after the same interval, and `ropeshift work show` on the review says why it waits. Then the review reads the pull request's head commit.

When the review ends, its verdicts are recorded in the ledger as always, bound to that commit, and the worker posts them on the pull request as one comment: each criterion with its verdict, reasons, and citations, and where the verdicts left the work. It is a comment and not an approving review, because the pull request is opened under your own sign-in and [GitHub does not let the author approve their own pull request](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/reviewing-changes-in-pull-requests/approving-a-pull-request-with-required-reviews). A failed criterion sends the work to rework, which continues the same branch: once the pull request is open, the work keeps its branch, and the worker removes the ended lane's worktree, whose commits the remote now holds, so the next attempt starts from the branch's tip. The worker pushes the new commit to the same branch, which updates the same pull request, the new head is reviewed again, and verdicts on the old head no longer count. `ropeshift work show` prints the pull request, its state, and its head.

At `person_merges`, the default, you decide by merging the pull request on GitHub after reading the verdicts there or with `ropeshift work show`. Once the review passes every criterion, the work waits in `integrating` for your merge, and ropeshift never merges it. The worker that opened the pull request reads it while its work is open, as often as `--forge-tick` says, and when it reads your merge, the work order reaches `done` and the merge is recorded. Your merge counts as accepting the work in [review agreement](measures.md#review-agreement). A pull request you close without merging ends nothing: reopen and merge it, or send the work back with `ropeshift work rework`. At `person_approves` and `review_approves`, ropeshift merges the pull request itself, as the next section describes, and a merge you make on GitHub first still ends the work.

### Merging accepted work

At `person_approves` and `review_approves`, accepted work moves to `integrating`, and ropeshift merges it through one deterministic step of the control plane, never through an agent: no lane, orchestrator, or tool can merge. The step merges only the commit the review judged, only once an approval binds to that commit, and, with review target `pull_request`, only while the pull request's head is that commit and every check the base branch requires is green on it.

- At `review_approves`, the review's pass on every criterion is the approval, recorded with the review lane and its generation.
- At `person_approves`, a merge request waits for you in Needs you and in `ropeshift approval list`, bound to the reviewed commit. `ropeshift approval show <id>` prints the work, the commit, the pull request, and the level, and `ropeshift approval approve <id> --digest <commit>` approves the merge as your decision, the same as `ropeshift work accept` on the work. A new commit voids the request. Denying it, with `--reason`, sends the work to rework with your reason.

The checks a pull request's base branch requires are evidence. The worker that opened the pull request reads them with `gh pr checks --required` each time it reads the pull request ([gh pr checks](https://cli.github.com/manual/gh_pr_checks)): one still running holds the merge, and `ropeshift work show` says which, and one that failed on the reviewed commit sends the work to rework, naming it. A check the base does not require counts for nothing. Then the worker that opened the pull request merges it with `gh pr merge <number> --match-head-commit <commit>` and `--merge`, `--squash`, or `--rebase`, as the repository's merge method names ([gh pr merge](https://cli.github.com/manual/gh_pr_merge)). It reads the pull request first, so a merge repeated after a crash finds it merged and merges nothing twice. It never passes `--admin`, never forces, and never bypasses branch protection.

With review target `branch`, nothing is pushed before the merge, and no forge runs checks on a branch that was never pushed, so the verdicts and any check result on record for the attempt are the gate. The worker that ran the lane merges the branch into the base with git, with the repository's merge method, in a temporary worktree detached at the base's tip, never in your checkout, and only while the branch is still at the reviewed commit. No repository hook runs. With a remote, it pushes the result to the base branch, never forcing; a push the remote refuses, such as a protected branch, holds the work for you. Without one, it moves the local base branch only from the tip it merged onto ([git update-ref](https://git-scm.com/docs/git-update-ref)), and holds the work while your checkout, or any worktree, has the base branch checked out, because moving it would change files under you. The commit the merge makes names the work order and the reviewed commit, so a merge repeated after a crash finds it and merges nothing twice. A team with branch protection uses `pull_request`.

Work merges after the work it depends on, in its own repository or another. Approved work with a dependency that is not `done` waits in `integrating`, and `ropeshift work show` prints a `waiting` line that names each one with its repository and state. The wait is recorded once, as an `integration.waiting` event, and the next pass merges the work once every dependency is done. This holds for a dependency a later plan version adds to work already on its way to merge. At `person_merges`, where you merge the pull request yourself, the wait names the dependencies to merge first. Merges across repositories are not atomic: each lands on its own, in dependency order.

When the merge lands, the work order reaches `done`. When it cannot:

- A conflict, or a branch behind a base that requires an up-to-date branch, sends the work to rework on the same branch. The rework's first prompt names the conflict and asks the lane to merge the current base into the branch and commit; its push moves the same pull request, and the new head is reviewed again.
- A pull request whose head moved past the reviewed commit, because someone pushed to its branch, goes back to review at the new head: an approval of the old head is void, the work returns to `acceptance`, and a review of the new head opens, whose verdicts decide it as before. A review lane on a machine that lacks the new head fetches it from the remote first.
- Anything else the forge or git refuses, such as branch protection, a closed pull request, or a base checked out under you, holds the work in `integrating` with a merge request that says why. Approving it tries the merge again, and denying it sends the work to rework with your reason. A worker that cannot run `gh` leaves the merge requested, and it is tried again once a minute, or as often as `--forge-tick` says.

Every approval and every merge is recorded with who approved (a member, the review lane, or the person on the forge), the level in force, the approved commit, and the merged commit. `ropeshift work show` prints each as an `approved` line, and the work order's page in the web UI shows them on its Pull request and checks tab, with what a merge still waits on.

### A planning branch

The branch a planning or adopt session leaves has no review agent: your plan approval is its review, and the only approval of its merge. It binds to the commit the plan was approved at, and `ropeshift work accept` on the planning work approves nothing.

- With review target `pull_request`, the worker pushes the branch and opens its pull request when the session's lane ends, so you can read the plan there beside its approval.
- At `person_approves` and `review_approves`, approving the plan moves the planning work to `integrating`. Intake first records the plan at the approved commit, so its work orders exist before any merge, and the approval is then recorded as the approval of that commit, naming the plan version. Ropeshift merges the branch through the same step as accepted work, with every required check green through a pull request. A plan on the branch that still waits for you holds the merge.
- At `person_merges`, the default, the approval ends the planning on a branch, and you merge it when you choose. Through a pull request, the planning work waits in `integrating` for your merge on GitHub, which is recorded with the plan approval.
- A head that moved after the approval, because someone pushed to the branch, voids the approval of the old commit: the planning work returns to `acceptance` at the new head, and the plan it holds asks for its own approval, which merges the new head. A new head whose plans did not change asks for no new approval, and waits in `acceptance` for you to send it back with `ropeshift work rework` or merge it yourself.
- A conflict sends the planning work back to the session on the same branch, with the conflict in its brief, to merge the base into the branch; ropeshift never resolves a conflict itself.

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
