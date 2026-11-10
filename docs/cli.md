# Command line

`ropeshift` is a client of the versioned API, so it reaches the ledger the same way every other surface does. It finds the control plane's socket from the installation's paths, or from `--socket`.

A command that changes something needs a member's session, and a read on the local socket does not. With no session, the command asks for the passphrase on the terminal, for that one command. `eval "$(ropeshift login)"` holds a short-lived session in one shell instead. No file holds it, and no lane inherits it. The passphrase comes only from the controlling terminal, never from stdin, a flag, or a variable, so a process with no terminal cannot sign in.

Every command takes `--json`. With it, a result is one JSON document on stdout, and a failure is `{"error": {...}}` on stderr with the same exit code.

## Help

`ropeshift help`, `ropeshift --help`, and `ropeshift -h` list every command by group, each with one line, on stdout, and exit 0. `ropeshift help <command>` and `ropeshift <command> --help` print one command's usage, what it does, and its flags, and exit 0. A group's first word lists the commands under it, so `ropeshift help work` lists every `work` command.

`ropeshift` with no command prints the same list on stderr and exits 2: a missing command is a usage error, as an unknown one is. An unknown command or flag exits 2 and names `ropeshift help`. `ropeshift --version` is `ropeshift version`, and `ropeshift --version --json` prints its JSON.

`ropeshiftd` answers the same forms for its own commands: `solo`, `help`, and `version`.

## Commands

| Command | Does |
|---------|------|
| `ropeshift top [--once] [--width N]` | Watches the fleet: what needs a person first, in the order the Needs you view holds, then each lane in that order with its state, reachability, and whether its value is `observed` or `last_reported`, then the workers. A parked lane says what it waits for, such as `waits for question q-1 or a person`. It follows the ledger's events and reads again at least every 5 seconds, and it changes nothing: it names the command for what the cursor stands on. Text from agents and repositories prints with control characters replaced. With no terminal, or with `--once`, it prints one frame, and `--json` prints one document. A printed frame is as wide as the terminal on stdout, and 120 columns when stdout is not a terminal, such as a pipe or a file. `--width N` sets its width in columns, and clips each line to it |
| `ropeshift health` | Says the control plane answered and read its ledger, and the revision it stands at. It exits 9 when nothing answers |
| `ropeshift version [--json]` | Prints the version, the commit, the build date, the Go version, and the platform. It needs no daemon, and `ropeshiftd version` prints the same under the daemon's name. `--version` is the same command. See [versioning](versioning.md#what-a-binary-reports) |
| `ropeshift help [<command>]` | Lists every command by group, or prints one command's usage, what it does, and its flags. See [help](#help) |
| `ropeshift work create --title T [--criterion C]...` | Records a work order and the contract it pins, in `contract_review`. With `--kind plan --repository R`, its lane writes repository files on its branch, and intake reads them at the commit a member accepts. The files are goals, plans, requirements, specs, architecture documents, experience designs, milestones, and contracts, and intake keeps a numbered version of each document for every committed change. A contract pins each document it cites to the version at its commit, and a criterion that serves no requirement of its plan keeps the work from `ready`. A contract names the milestone of its plan it belongs to, which every contract of a plan with milestones must do, and the shared contracts of its plan it uses, and a milestone names the milestones it depends on. Intake makes every work order of a milestone wait for every work order of the milestones it depends on, and every work order of a contract wait for the work orders of each shared contract it uses. A reference to a milestone or shared contract the plan does not hold is a problem, and intake records nothing from that file. A plan whose milestones, shared contracts, or work orders depend on each other in a loop is refused whole, and the problem names the loop. `source list` names R, and reports a file intake could not read |
| `ropeshift work list [--state S] [--worker W]` | Lists work orders. With `--worker`, it lists only ready work that worker's manifest can run |
| `ropeshift work show <id>` | Shows one work order. `base` names the branch its contract file asked to start from, and `waiting` names why ready work is not claimed: `waiting_for_merge` for a dependency whose accepted work is not yet in the base branch, `dependencies_diverge` for dependencies whose accepted work is on separate lines, and `base_unavailable` for a base branch that could not be fetched or does not exist. The reason clears by itself once it no longer holds, and a claim that meets it is refused with exit 4 and the same words |
| `ropeshift work review <id>` | Records a passed review, and the definition of ready decides the move to `ready`. Work whose contract version an approved plan version lists needs no review of its own, because approving the plan passed it |
| `ropeshift work acceptance <id>` | Shows what a work order's evidence leaves to do, with every artifact and check result linked to it |
| `ropeshift work accept <id>` | Moves completed work to `done`. A failed check cannot be accepted, and a criterion no check maps to needs a member |
| `ropeshift work rework <id> --reason R` | Sends completed work back to `ready` for another attempt. Its evidence stays on record and stops counting |
| `ropeshift work claim <id> --worker W [--claim-id C]` | Claims a work order for a worker. A retry with the same claim ID returns the grant |
| `ropeshift work release <id> --claim-id C --fence N` | Gives back a claim whose lane has not started |
| `ropeshift work write <id> --claim-id C --fence N [--write-id W] [--lane L --generation G] --payload P` | Stores one event under a claim's fence. A retry with the same write ID stores one event, and a stale fence exits 5 with the current one in the message |
| `ropeshift work reassign <id> --reason R` | Ends a lapsed holder's ownership, which moves the fence |
| `ropeshift work cancel <id> --reason R` | Ends a work order for good, from any state that is not terminal, which only a member can do. Any claim ends and the fence moves, so a lane can write nothing more. A parked lane ends at once. A lane with a process stops through the guard when its worker hears, saves what it holds to a ref, and reads as last reported until then. Canceling work that is already canceled changes nothing, and finished or failed work exits 4 |
| `ropeshift work send-back <id> --reason R` | Returns a work order in review to `planning`, because its contract must change. It voids the review, and the contract's next version returns the work to review |
| `ropeshift work resolve <id> --reason R` | Returns blocked work to `ready` with its review standing, once whatever stopped the lane is fixed |
| `ropeshift work replan <id> --reason R` | Returns blocked work to `planning`, which voids its review, because its contract must change. Only a member can take work out of `blocked` |
| `ropeshift plan start [--goal TEXT \| --goal-file PATH] [--worker ID] [--repository R] [--no-attach]` | Opens a planning session and attaches your terminal to it. It makes the calls a member makes by hand, each one recorded as yours: it creates a `plan` work order whose statement is the goal, passes its contract review, claims it on a worker, and starts the lane, then waits for the lane's pane and runs `lane attach`. The goal is optional, and a session with none starts with the planning agent asking what the plan is for. The worker is `--worker`, or the only worker registered, or the only one of several whose word is fresh, and otherwise the command lists them and stops. The repository is `--repository`, or the one the daemon reads. With no terminal, with `--no-attach`, or with `--json`, it starts the session and prints the lane, the `lane attach` command that joins it, and the session's page in the web UI, at the address `ROPESHIFT_WEB` or the default names, unless that is `off`. It exits 7 when the review names what still holds the work back, and the work order it recorded stays |
| `ropeshift plan list` | Lists plans |
| `ropeshift plan show <id>` | Shows one plan with its goal, each contract's newest version, and their work orders. When a request to approve the plan stands, it also prints that request: the version, who asked, how the version differs from the last one a member approved, and every draft decision it would decide. The difference names the documents, milestones, and contracts the version adds, changes, and removes, and for a contract that changed, the criteria and work orders it adds, changes, and removes |
| `ropeshift plan approve <id> [--version N]` | Approves the plan version that waits, in one step, as the member who read it. The version is bound to its digest, so a version that changed since `plan show` is refused. Approval decides every draft decision of the version as your decision, with a citation to the plan version, and passes the contract review of each work order whose contract version the approved version lists at the same digest, with you as the reviewer. The definition of ready still applies to each, so a question still open keeps the work in review. For a plan a planning lane left on its branch, approval also accepts the lane's `plan` work order, and intake reads that commit on its next tick. `--version` must name the version that waits, and an approved version approves again as the same answer. Only a member with a session can approve a plan; the planning lane that wrote it cannot |
| `ropeshift plan review <id> [--id I]` | Starts a contract review a lane can run, ready for a member to claim. Its lane reads the plan and each contract's newest version, and everything it asks reaches the ladder together when it ends. Its questions are about the plan, so a blocking one holds all of the plan's work. Accepting the review passes the review of the work whose versions it read |
| `ropeshift requirement list` | Lists each requirement's newest version |
| `ropeshift requirement get <key>` | Shows one requirement's newest version, by the key a contract names |
| `ropeshift question list [--category C] [--status S] [--scope S] [--text T]` | Finds questions. `--scope` names what a question is about, and `--text` ranks the best match first |
| `ropeshift question show <id>` | Shows one question |
| `ropeshift question record --text T [--category C]... [--subject ID] [--subject-kind K] [--blocking]` | Records a question as it is, in `open`. Categories come from `category list` |
| `ropeshift question dismiss <id>` | Closes a question with no answer. It stays on record |
| `ropeshift question answer <id> --answer A [--rationale R] [--citation C]... [--id I]` | Answers a question that still waits, which records a decision. It works with no orchestrator running, needs no ladder trace, and ends any hold. Every lane attached to the question hears the answer: a running lane as a message, and a parked lane in its next generation's first prompt, which the same answer wakes. A question is answered once, and a second answer exits 4 |
| `ropeshift question batches [--category C]` | Shows the batches that reached members and still wait, each question with its options, recommendation, and citations. Nothing is assigned to anyone. A question from a planning lane skips the ladder and reaches this list at the next batch tick, whatever the window, with the categories its lane named and the options it gave, if any. No orchestrator can take, decide, or assume it, and a member answers it with `question answer` like any other |
| `ropeshift decision list [--category C] [--status S] [--scope S] [--text T]` | Finds decisions, and an overturned decision stays findable |
| `ropeshift decision show <id>` | Shows one decision |
| `ropeshift decision record --statement S [--rationale R] [--scope S] [--category C]... [--citation C]... [--reversibility R] [--question Q] [--assumed] [--supersedes D] [--no-conflict D]...` | Records a decision, or with `--assumed` a reversible assumption. The answer lists decisions with an overlapping scope. `--supersedes` replaces one, with no rework |
| `ropeshift decision overturn <id> --statement S [--rationale R] [--citation C]...` | Reverses a decision in one transaction: rework for each completed work order, a notice for each lane, and the outcome on its experience record |
| `ropeshift policy list [--kind K]` | Lists the versions of a policy, newest first. `--kind` is `decision`, the default, `lane`, `delegation`, `autonomy`, or `notify` |
| `ropeshift policy show <version> [--kind K]` | Prints one version as its author wrote it |
| `ropeshift policy publish --file F` | Publishes a new version of the policy its table names, `[decision_policy]`, `[lane_policy]`, `[delegation_policy]`, `[autonomy_policy]`, or `[notify_policy]`. A body names its version, and it must be newer than the one in force. Only a member can |
| `ropeshift approval list [--state S]` | Lists requests, each with what it asks and how it stands |
| `ropeshift approval show <id>` | Shows one request: its call, the lane that asked or, for a plan approval, the plan and version, its digest, and how it stands |
| `ropeshift approval approve <id> --digest D [--reason R]` | Approves the one call a request names, and `--digest` says what you read. It also approves a plan version, the same as `plan approve`, and an orchestrator never can. See [authorization](authorization.md) |
| `ropeshift approval deny <id> --digest D [--reason R]` | Denies it |
| `ropeshift effect list` | Lists the effects a crash left unknown that nothing can observe, such as an input to a session or a profile's hook, each with its worker, lane, and how it stands |
| `ropeshift effect show <id>` | Shows one |
| `ropeshift effect decide <id> --outcome O [--reason R]` | Says what became of one, which only a member can. `happened` and `not_happened` settle it. `retry` lets a run perform it again, so it may happen twice, and an ended lane has nothing left to retry it. An effect is decided once, and a retry a crash cuts short asks again |
| `ropeshift notice list` | Says how each notice stands on each channel: its kind, its attempts, and whether a channel took it. Delivery is at least once, so more than one attempt with a delivery is a notice its channel may have heard twice. See [notifications](notifications.md) |
| `ropeshift telemetry query [--signal S] [--name N] [--lane L] [--since D] [--limit N]` | Reads the worker's local telemetry store, newest first: spans, data points, and the agent's log records, each with its attributes and the lane, work order, and generation its tags name. `--signal` is `span`, `metric`, or `log`, and `--since` takes a duration such as `1h`. It prints 100 records unless `--limit` says otherwise. The local store keeps account attributes, which an export drops. See [telemetry](telemetry.md) |
| `ropeshift telemetry lanes` | Prints each lane's context at its last call, its tokens by type, and the cost its agent reported, which is a lower bound |
| `ropeshift measures [--window D]` | Prints the product measures over a window, `7d` by default, from the ledger alone: interruptions per completed work order beside the overturn rate, rework, and the autonomous decision rate, then the repeated-question rate, repeats prevented, work in progress, flow time, park time by trigger, and cost per outcome by kind. A rate with nothing under it says so. See [measures](measures.md) |
| `ropeshift view <name> [--category C]` | Prints one read model of the web UI, as JSON: `needs-you`, `roadmap`, `plans`, `documents`, `decisions`, `questions`, `in-progress`, `on-hold`, `stuck`, `completed`, `shop-floor`, or `operations`. `--category` narrows `questions` and `decisions`. The `documents` view lists, for each plan, the planning documents its files recorded with every version and where each stands in review, and each requirement of its requirements documents with the criteria that serve it and their work orders. The `roadmap` view lists each plan's milestones in priority order with their progress, the layers of work that can run at the same time, the critical path, and the title, state, size, and milestone of each work order it places. The `plans` view gives each plan its milestones, each contract the ID its file gave it, which another contract's `uses` names, and the plan version whose approval waits, when one does |
| `ropeshift events [--after N] [--limit N]` | Follows the ledger from a revision, one line for each event: what happened and to what, with none of a record's content. With `--limit` it stops after that many |
| `ropeshift category list` | Lists the workspace's categories: the built-in ones and those it added |
| `ropeshift category add <name>` | Adds a category. A name is lowercase words joined by hyphens, and a category cannot be removed |
| `ropeshift search <words> [--kind K]... [--limit N]` | Ranks candidates for a question among decisions, requirements, contracts, and open questions. A score orders one search and means nothing else: a person or an agent judges a match |
| `ropeshift document list` | Lists the Markdown documents of every source, at the commit intake reads |
| `ropeshift document show <path> [--version V] [--instance I]` | Prints one document. `--version` names an earlier version, and `--instance` names the repository when two hold the path |
| `ropeshift document versions <path> [--instance I]` | Lists the versions of one document, newest first |
| `ropeshift document recorded <kind>:<id> [--version N]` | Prints one version of a planning document, such as `spec:checkout-api`, as the ledger stored it when intake read the commit, which stays the same after the file changes. `--version` names an earlier version, and the newest is the default. With `--json` it also lists every version with its digest, the file and commit it came from, when intake recorded it, and whether a plan version a member approved covers it, an approval that waits lists it, or neither |
| `ropeshift lane start <work-order> [--lane L] [--profile F]` | Asks the worker holding the claim to run a lane. It returns when the ledger holds the request, and a retry with the same lane ID is the same request. `--profile` names a TOML file that is this lane's own profile layer, over the built-in, user, and repository layers. It cannot raise the permission mode, change what the machine admits, or change `push_on_stop` |
| `ropeshift source list` | Lists sources, their health, the files they cannot read, and each file whose base moved: a commit that never held the ledger's version and changes the file too is no new version, and merging the two is what settles it. A problem found at a plan's accepted commit names that version and the plan, and stays until a later commit fixes the file: one that holds the plan's version of it, or, for a file nobody could read, one intake takes the file from |
| `ropeshift lane attach <lane>` | Puts your terminal on an interactive lane's pane, on this machine, so you see and type into the agent's session. It needs a terminal and tmux, and it refuses a lane that ended, a lane with no pane (a headless lane, a lane another worker holds, or one that has not opened its pane), and a parked lane. Detaching leaves the lane running: press `C-b d`, or `C-b C-b d` inside tmux, because the status line is off. When the lane's pane goes and the lane moves to a newer generation, as after a hand-off or a resume, the command attaches to the new pane. It waits as long as the lane stays parked, and it ends when you detach, when the lane ends, or when a lane that is not parked has no pane for a minute |
| `ropeshift lane list` | Lists lanes. Each line states the lane's state, liveness, reachability, conditions, and whether the value is `observed` or `last_reported` |
| `ropeshift lane show <lane>` | Shows one lane the same way, and a `base` line with the ref and the commit its branch began at. `--json` adds `as_of`, `reported_by_epoch`, `base_ref`, and `base_commit` |
| `ropeshift lane respawn <work-order>` | Stops a running lane's session, and starts its next generation from that session. It needs a member's session |
| `ropeshift lane reset <work-order>` | Lets the lane its breaker stopped start again, whether crashes stopped it or its generations reached the day's cap. A handoff, a resume, and a respawn count toward that cap, and a lane a handoff stopped goes on from that handoff. Only a member can, because a tripped lane is waiting for a person |
| `ropeshift lane resume <work-order>` | Gives a work order's parked lane a member's word, which wakes a lane that waits on a person. A lane parked on a usage limit or an expired sign-in waits on the worker's word and not on a member's, so this does not move it |
| `ropeshift repository list` | Lists the repositories the daemon has read, each with the branch its lanes start from, how it merges, what dependent work starts from, whether integration is on, and its remote |
| `ropeshift repository show <id>` | Shows one repository's record: its remote and default branch, which the daemon reads from git, and its base branch, forge binding, merge method, integration, and start, which a member sets. `<id>` is the identity `list` prints or any unique prefix of seven characters or more |
| `ropeshift repository set <id> [--base B] [--merge-method merge\|squash\|rebase] [--start-from merged\|accepted] [--integration on\|off]` | Changes the settings a member owns, and only a member can. A flag left out keeps its value, and `--base ""` follows the default branch again. Each change is an event naming the record before and after. `--start-from accepted` is refused for a repository whose merge method is `squash` or `rebase`, and so is changing the merge method to either while it starts from accepted work, because those rewrite the commits the dependent work would be built on. A change that changes nothing records nothing |
| `ropeshift lane archive <lane> [--force]` | Asks the worker that ran an ended lane to remove its worktree. Its branch stays. A lane with `unpushed_work` is refused, and `--force` removes it anyway, which only a member can do. The worker reads the worktree again first, and saves what it finds to a ref. Nothing is pushed unless the user's profile turns `push_on_stop` on, so until then a lane whose work no remote holds reads `unpushed_work`. See [onboarding](onboarding.md#work-stays-on-this-machine) |
| `ropeshift message send <lane> --body B [--kind K] [--id I] [--reply-to R] [--generation G]` | Records a message for a lane's current generation, or for one it names. `--kind` is `instruction`, `notice`, `answer`, or `decision_notice`, and a retry with the same `--id` sends it once. A message grants nothing: it never counts as a permission decision or as consent |
| `ropeshift message show <id>` | Says how far a message got: `queued`, `at_worker`, `handed_off`, `admitted`, `replied`, `refused`, or `expired`, with the reason and any reply. `handed_off` proves only a write, and `admitted` proves the session took it into a turn. Only a reply shows it was read. A message that has not entered a turn dies with its process, and reads `expired` with reason `generation_ended`, or `ledger_fact` when the next generation is told of it |
| `ropeshift message list <lane>` | Lists what was sent to a lane, oldest first |
| `ropeshift fleet show` | Shows the fleet's mode and the caps a claim is held to |
| `ropeshift fleet run`, `pause`, `drain` | Sets the mode for the whole fleet. `pause` takes no new claims and starts no new lanes, and a running lane that crashes still restarts. `drain` also stops new generations. Each lane already running goes on |
| `ropeshift fleet caps [--wip N] [--per-repository N] [--per-worker N]` | Sets what a claim is held to. A flag left out keeps its value, and 0 means no limit. A claim already granted stays |
| `ropeshift login` | Asks for the passphrase on the terminal and prints a session for `eval`. It lasts an hour |
| `ropeshift sign-out` | Ends the session this shell holds, so its token opens nothing again. The token in `ROPESHIFT_MEMBER_TOKEN` is all it needs, so it works with no terminal, and it never asks for the passphrase. With no token, or one whose session expired or already ended, it says there is no session to end and exits 8 |
| `ropeshift passphrase set` | Sets the passphrase on the terminal. The first needs no session, and a later one asks for the current passphrase. A passphrase has at least 8 bytes |
| `ropeshift init` | Sets up this machine for solo mode, and a second run says only what is left. It asks for the first passphrase on the terminal and keeps one already set. It waits for the worker's registration, and with no daemon running it runs one for its own length, because a first start registers the worker. It runs the doctor and says, for each agent CLI, whether to install it, sign in through its vendor's own flow, or nothing, with the level the worker starts at. It writes a sample contract to the installation's directory and keeps one already there. It exits 1 when no agent CLI is usable, after every other step. See [onboarding](onboarding.md) |
| `ropeshift backup <file>` | Copies the ledger to a new file through SQLite's online backup, while the daemon goes on writing. A relative path is read from where the command runs, a file already at the path is never replaced and exits 2, and only the owner can read the copy. It needs a member's session, because the copy holds every record and the passphrase's hash. A control plane with no online backup exits 1 |
| `ropeshift service install [--name N] [-- daemon flags]` | Runs the daemon as a launchd agent on macOS or a systemd user unit on Linux, from the `ropeshiftd` beside this command, and starts it, or restarts it with the new definition. The flags after `--` go to `ropeshiftd solo` as they are. A flag it refuses, or a `--source` that is not an absolute path, exits 2 before anything is written. The definition keeps this shell's `PATH`, locale, and ropeshift's own location variables, because a service manager starts the daemon with almost none, and it holds no other variable. The launchd agent sets `ProcessType` to `Interactive`. The name is `dev.ropeshift.ropeshiftd` on macOS and `ropeshiftd` on Linux by default. See [onboarding](onboarding.md#run-the-daemon-as-a-service) |
| `ropeshift service uninstall [--name N]` | Stops the service and removes its definition. No lane stops: a lane's processes live outside the service, and the daemon adopts them when it starts again. A service that is not installed exits 3 |
| `ropeshift worker doctor` | Reports each agent CLI on this machine: whether it is present, its version, whether that version is qualified, and the kind of its sign-in. It uses the vendor's status command, reads no credential, and needs no daemon. It exits 1 when no CLI is usable |
| `ropeshift worker list` | Lists registered workers, each with its epoch and protocol |
| `ropeshift orchestrator show <worker>` | Shows a worker's orchestrator: what a member asked of it, and how it reads |
| `ropeshift orchestrator start <worker>` | Asks for its orchestrator, resets a breaker that stopped it, or replaces a session that stalled. `view stuck` and `needs-you` list one that tripped or stalled. No agent can ask for one, and no session starts until there is something to judge |
| `ropeshift orchestrator stop <worker>` | Stops its orchestrator, which stops no lane |
| `ropeshift worker token` | Makes a join token for one worker. It is shown once |
| `ropeshift worker autonomy <id>` | Shows a worker's sign-in, the level that holds, any run window, and whether its orchestrator can claim now |
| `ropeshift worker level <id> manual\|attended\|unattended` | Chooses a worker's level. `unattended` on a subscription is refused until a member acknowledges the stated risk for that worker |
| `ropeshift worker risk` | Prints the statement a member acknowledges, and its digest |
| `ropeshift worker acknowledge-risk <id> --digest D` | Records that a member read the statement that digest names, for one worker, as an event. It changes no level, and a changed statement asks again |
| `ropeshift worker window-open <id> [--for D] [--id I]` | Opens a run window, inside which an `attended` worker's orchestrator claims. It lasts 4 hours by default and 12 at most, and closes by itself. A worker holds one at a time |
| `ropeshift worker window-close <id>` | Ends a run window. No lane stops, and work claimed but not started waits for the next window or a member |

`ropeshift mcp` lists 9 tools for a lane and 15 for an orchestrator, by the token it runs with. `lane_stop` ends a stuck session and starts the lane's next generation, because canceling work is a member's.

These commands are for ropeshift or an agent CLI to run, and a person does not need them:

| Command | Does |
|---------|------|
| `ropeshift hook <event>` | Reports one hook event to the worker, and spools it if the worker is down. It always exits 0 |
| `ropeshift mcp` | Serves a lane's tools over stdio, and reaches only the local worker. It takes no arguments, and an argument exits 2 before it serves |
| `ropeshift lane-shim` | Holds a headless session's pipes in a process of its own, so the session outlives its worker. It journals every line, serves that journal on a private socket, and writes each input at most once |
| `ropeshift lane-pane <file>` | Replaces itself with an interactive session in its pane, with the exact environment a private file holds, and removes that file first |
| `ropeshift statusline [--pass]` | Reports one status line input to the worker. It is the driver's half of an interactive lane's status line, and `--pass` prints the input for a person's own command after it in a pipe |

## The daemon

`ropeshiftd solo` runs the control plane and this machine's worker in one process. `ropeshift service install -- <flags>` passes the same flags to it under the service manager. `ropeshiftd help` lists the daemon's commands, and `ropeshiftd solo --help` prints these flags with their defaults.

| Flag | Default | Meaning |
|------|---------|---------|
| `--source <path>` | None | A repository to read work and documents from. Repeat it for each repository |
| `--ref <ref>` | The default branch | The ref intake reads in each source |
| `--dir <dir>` | The whole repository | The directory intake reads in each source |
| `--web <address>` | `127.0.0.1:7317`, or `ROPESHIFT_WEB` | The loopback address of the web UI, or `off` |
| `--telemetry <address>` | `127.0.0.1:7318`, or `ROPESHIFT_TELEMETRY` | The loopback address of the telemetry receiver, or `off`. See [telemetry](telemetry.md) |
| `--intake-tick <duration>` | `30s` | How often sources are read |
| `--notify-tick <duration>` | `30s` | How often members are told what waits. See [notifications](notifications.md) |
| `--telemetry-tick <duration>` | `5s` | How often the ledger is read into spans and metrics |
| `--lease-tick`, `--batch-tick`, `--wake-tick` | `5s`, `5s`, `1s` | How often lapsed leases end, closed question batches reach members, and due wakes fire |
| `--lease-ttl`, `--heartbeat` | `60s`, `15s` | How long a lease lasts, and how often the worker beats |

An installation keeps its ledger, journal, and telemetry store in its state directory, and its sockets in its run directory:

| Directory | macOS | Linux | Moved by |
|-----------|-------|-------|----------|
| State | `~/Library/Application Support/ropeshift` | `$XDG_STATE_HOME/ropeshift`, or `~/.local/state/ropeshift` | `ROPESHIFT_STATE_DIR` |
| Run | `~/.ropeshift/run` | `$XDG_RUNTIME_DIR/ropeshift`, or `~/.ropeshift/run` | `ROPESHIFT_RUN_DIR` |

A second installation on one machine sets both variables and its own `--web` and `--telemetry` addresses.

## Exit codes

| Code | Meaning | What to do |
|------|---------|------------|
| 0 | The command did what it said | Nothing |
| 1 | The control plane could not answer, or the command failed for another reason | Retry, and read the message |
| 2 | The command line or the request is invalid | Fix the command |
| 3 | The record does not exist | Check the ID |
| 4 | The ledger's state refused this: a claim was lost, work was not lapsed, the move has no edge, or a removal would lose work no remote holds | Read the current state |
| 5 | The fence is stale: another holder owns this work | Stop, and read the current fence in the message |
| 6 | The lease lapsed and this holder can return | Return, then retry |
| 7 | The review is recorded, and the definition of ready names what still holds the work back | Resolve what it names. The work order moves on its own |
| 8 | This actor may not take this step, or the step needs a member's session and has none | Use an actor that may, or sign in on a terminal |
| 9 | Nothing answered on the socket | Start the daemon, or check `--socket` |
