# Qualification

Ropeshift drives the unmodified Claude Code CLI. A few of the surfaces it uses are not in the vendor's documentation, so each one is used only on a version where a check of it passed. This page says what is checked, where the results come from, and what a lane does without a surface.

## Checked surfaces

Each surface has one check and one use. A surface whose check failed, or could not run, is not used.

| Surface | Check | Without it |
|---------|-------|------------|
| `socket_line` | A message on the session's own socket opens a turn: a `UserPromptSubmit` hook names its first line, and a headless replay carries its `uuid` | No message from anyone else reaches a session, and a pane's own input is typed. An answer to a question the lane waits on parks the lane on every question it waits on, and resumes it at once when no other of them is open, so the answer reaches it in the first prompt of its next generation. A lane parks when its turn ends and never in the turn, and a worker that restarts after it settled the answer parks the lane when it starts. Any other message reads `expired` with the reason `undeliverable`, or `ledger_fact` when it bears a decision the next generation is told of. A version that passed this check has no such cause: a message its session cannot take just then, for want of a socket or a state it could read, reads `channel_unavailable` and degrades nothing |
| `receipts` | A session that refuses messages answers a reply address with `expired` and `refused` | A refusal is not read, and a message waits until it expires |
| `peer_message_hold` | A session that holds messages reports the hold in its stream | Nothing: no state follows from a hold |
| `socket_dir` | `CLAUDE_CODE_TMPDIR` moves the socket into the lane's own directory, within the path limit | The socket stays where the CLI puts it |
| `unified_windows` | A `rate_limit_event` carries a window's `utilization`. A probe on an API key does not check it, because the figures belong to a subscription | The near-limit watch reads the top-level figures only |
| `command_lifecycle` | The stream reports it | Nothing: the design does not need it |
| `print_keeps_login` | On a subscription, `system/init` reports `apiKeySource` `none` and a `messaging_socket_path`, and a `rate_limit_event` arrives | A subscription has no headless lane, and a new lane its profile sends headless starts in tmux |
| `start_screens` | A pane in a new directory reaches the prompt through the start check | Nothing beyond the start check, which stops on any screen it does not know |
| `max_budget_usd` | A headless session started with `--max-budget-usd` far below the cost of its one turn ends that turn with a result of subtype `error_max_budget_usd` | A headless generation starts with no budget cap of its own, and the worker alone holds the lane to its work order's hard limit |
| `stream_usage` | A headless session run with `--include-partial-messages` writes a `stream_event` `message_start` that carries the tokens of an API call and a `message_delta` that carries its output | A turn's spend is seen from its assistant messages when they carry usage, and otherwise not until the turn ends, so a long turn can pass its work order's hard limit before the worker sees it |

The start check answers the trust dialog with the key its focus calls for, and only once the same frame has stood for half a second. Claude Code 2.1.329 puts the focus back on `No, exit` about 150 ms after the dialog first paints and refuses an Enter until then, so an earlier key has no effect there.

A dialog that still shows its focus off `Yes, I trust this folder` two seconds after a key gets its navigation again, as End when that option is the last and as Home when it is the first. An arrow wraps and is never repeated. End and Home leave the focus on that option however many land, so a key that was only slow to repaint does no harm. No key repeats on a screen the check does not know, or where neither End nor Home reaches the option.

Enter goes only to a focus that stood on that option, and it goes once. A dialog that takes no key ends the check at its timeout, and so does one that does not act on its Enter: it loses the key, and its focus stays on `Yes, I trust this folder`, or it refuses the key and puts its focus back on `No, exit`. Neither gets a second Enter, because a late first one could answer the next dialog, and neither gets any other key. The daemon's output then says that the trust dialog did not take its Enter, and which of the two it showed, where it otherwise says only that the start stopped on an unknown screen. A fresh start then parks the lane on the screen it stopped on, for a person, and a resume falls back to a new generation.

On Claude Code 2.1.330, a message on the socket still opens a turn, and its `UserPromptSubmit` hook names its first line, but the headless replay of it carries a `uuid` the CLI gives it, and the line's own ID appears only as `origin.msg_id`, on a line that names a reply address. Its `peer_message_hold` lines name a `message_uuid` of the CLI's own too. So the checks of `socket_line` and `peer_message_hold` fail on that version, and the first of them puts it in `degraded_mode`.

A version where any surface ropeshift relies on did not pass runs in `degraded_mode`. The lane shows the condition, and the driver uses documented surfaces in its place. `command_lifecycle`, `peer_message_hold`, `max_budget_usd`, and `stream_usage` are reported and never degrade a version. The login check and the windows check run on every sign-in but an API key, because a sign-in nobody could read counts as a subscription and the figures belong to a subscription. The login and windows checks on an API key and the screen check on a worker with no terminal cannot run, so they read `unobserved` and no result is asked to pass them there.

## Where results come from

The release ships a matrix in the binary, built from `internal/adapters/claudecode/matrix.json`. It holds versions, surfaces, and results, and nothing else. `ropeshift worker doctor` reads it, so it can say whether the installed version is qualified with no daemon running.

The matrix qualifies Claude Code 2.1.316, 2.1.317, and 2.1.319, each with every surface above passed, except `max_budget_usd` and `stream_usage`, which the release's qualification has not observed, so a version runs with no budget cap until a worker's own probe passes the first, and a worker's own probe is what says whether the second holds on that machine. `make qualify` runs the production probe on the installed CLI and ends with that version's row for the matrix. It starts real sessions, so a maintainer runs it by hand and never in continuous integration.

A running worker does not rely on the matrix alone, because the settings and plugins on a machine are part of what must work. The worker reads the installed version when it starts, once a minute while it runs, and before each lane's process. On a version this worker has not probed, or whose last result has a check that did not pass and whose [wait](#probing-a-version-again) is over, it starts a probe on that machine in the background, with no lane asking, and runs its checks at once:

- three short headless sessions, under `accept`, `refuse`, and `hold`, with at most one turn each, apart from a second turn when the first message is never taken;
- one headless session with a budget cap of a hundredth of a cent, whose one turn the client ends at the cap when the flag works, which costs a fraction of a cent;
- one pane in a new directory, which reaches the prompt and takes no turn, when the worker has a terminal.

Each of them starts with a lane's isolation: `--setting-sources project`, `--strict-mcp-config` with the probe's own MCP configuration, and `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` ([your own agent CLI setup](authorization.md#your-own-agent-cli-setup)). So what the probe qualifies is what a lane runs, and a piece of the member's own setup that reaches a probe session reached it past the same flags, as a leak would reach a lane.

No lane waits for the probe. A lane that starts while it runs starts at once, on documented surfaces, because no probe's result names its version yet, so it runs in `degraded_mode`. While the probe runs, a lane whose process runs on the version carries the check, which `ropeshift lane show`, the lane page, and Planning say in words: `Checking Claude Code <version>`, that the lane runs on documented surfaces until the check passes, and that a respawn then picks up the qualified surfaces. The probe's end clears it. A worker that has probed no version before is the one exception: its driver answers from the shipped matrix, so a version the matrix holds runs on the surfaces the matrix passed, the lane is not degraded, and its check says `Checking Claude Code <version>: this lane runs on the surfaces this release qualified for <version>; a respawn after the check picks up its result`. One probe runs at a time, and every lane on the version shares it. The result is kept in the worker's journal, recorded in the ledger as `worker.driver_qualified`, and handed to the driver, and from then on it decides that version's surfaces for each process that starts. A probe that cannot run leaves every surface unobserved when no result holds for the version, so the version runs degraded, and changes nothing when one does.

## Probing a version again

A result that passed every check a probe runs now holds for its version until the version changes. A probe runs every surface above, except the login check and the windows check on an API key and the screen check on a worker with no terminal. Any other result is probed again: one with a check that failed, one with a check no session reached, one that lacks a check a probe runs now, as a result from an earlier release, from another sign-in, or from a worker with no terminal does, and one a probe that could not run left, which holds no check. The worker probes such a version on a backoff, when it starts and while it runs. A budget check that failed on a version is not run again on it: the check pays for a session of its own on each probe, the flag only backs up the worker's own limit, and the later result keeps the check failed.

The wait after a probe is 1 hour, and doubles with each later probe of the same version, to 2, 4, 8, and 16 hours, and no more than 24 hours after that. It runs from the end of the probe, and the worker's clock reads it.

A probe whose result passed every check it runs ends the wait, and a probe of a version the last probe did not name starts it at 1 hour. A probe that ends in any other way starts the wait too: one that could not run, one whose result could not be kept, and one that ended because the version moved under it. So no version is probed in a loop, and no look at the version, whether the worker's own each minute or a lane's before its process, starts a probe before its wait is over. The wait is in the worker's journal with the record of the last probe, so a worker that starts probes such a version when that wait is over, and a restart neither skips it nor starts it over. The wait goes on doubling across restarts, and a probe that its worker's stop cut short starts none. A worker that starts also holds its probe back until 10 minutes after the last probe it began, which is all the hold there is where that probe left no wait: the worker records each probe in its journal before the probe starts its first session, so a worker that keeps restarting begins one probe in 10 minutes at most. A journal that cannot keep the record starts no probe, and the worker tries again when the wait ends. No probe starts while the sign-in is at a usage limit or signed out either, because its sessions would meet that and its failed or unobserved checks would replace passes: the first look at the version after the limit ends, or the sign-in is back, starts it.

While the probe runs, the last result stays in force: the driver keeps it, a lane's status reads it, and no lane waits. It stays in force when the probe cannot run, as it does after a timeout, and only the wait begins. One probe runs at a time, for the version and for every lane on it.

## Evidence of a check that did not pass

A probe's own directories are removed when it ends. When a check failed or no session reached it, the probe first keeps what the session that ran it showed, in the `probe-evidence/claude-code` directory of the [state directory](onboarding.md#where-ropeshift-keeps-its-state):

| File | Holds | Checks |
|------|-------|--------|
| `accept.stream` | The stream of the session that accepts messages | `socket_line`, `socket_dir`, `unified_windows`, `command_lifecycle`, `stream_usage`, `print_keeps_login` |
| `refuse.stream` | The stream of the session that refuses them | `receipts` |
| `hold.stream` | The stream of the session that holds them | `peer_message_hold` |
| `budget.stream` | The stream of the session with a budget cap | `max_budget_usd` |
| `screens.screen` | The pane's screen as the start check left it | `start_screens` |

A file is kept only when one of its checks did not pass. Only the newest probe's evidence stays: each probe clears what the one before it left when it begins, so a probe that ends with no result, by an error, a timeout, or a version that moved, leaves none, and a probe in which every check passed leaves none either. Only its owner can read the directory and its files, and no file holds more than 256 KiB: a file cut at that bound ends with `[truncated]`.

The files hold what the CLI wrote and showed, and never the environment a session started with, an agent credential, or the settings ropeshift generated for it. A kept screen can show the signed-in account's email address or organization, which is why only its owner can read the directory. A probe keeps nothing where the directory is a link, and replaces a link of its own with a directory. Nothing of the files reaches the ledger, the daemon's log, or an API response.

## When the CLI updates itself

A running process keeps the version it started on. A new process reads the installed version at its launch:

- A version the last probe named uses that probe's result.
- Any other version uses no undocumented surface, whether the last probe passed or failed, until its own probe ends. A lane whose process starts meanwhile starts at once, on documented surfaces.
- The worker probes the new version as soon as it reads it, whether or not a lane is starting, so no lane waits for the probe. After a restart the probe waits out what is left of its version's backoff, and for at least 10 minutes from the last probe the worker began ([probing a version again](#probing-a-version-again)).
- A process keeps the surfaces it started with, so a lane that started on documented surfaces keeps `degraded_mode` after its version's probe passes, and a respawn starts a process on the surfaces the probe passed. A probe that finds a surface failed adds the condition to a lane that is running, and a lane that started on the shipped matrix's surfaces then says it runs on the surfaces this release qualified for its version, not on documented surfaces. A later probe whose result is not degraded clears the condition a probe added, while a lane whose process started on documented surfaces keeps its own until a respawn.
- A session that a restarted worker takes over uses the result for the version it started on, not the installed one.

So an update never moves a lane between qualified and degraded without a probe's result for the version it runs, and no process uses an undocumented surface that no check of its version passed.

The probe starts real sessions on the signed-in account, each for a turn or less, so a daemon that runs for the length of one command does not start one. The daemon `ropeshift init` starts is such a daemon: it registers the worker and is stopped within seconds, and a probe it started would be cancelled before it ended. It qualifies no version of the agent CLI, so it treats the CLI as not qualified: its worker advertises it so, and starts no session of any kind. It takes no work either. Its worker registers and opens its session with the control plane, and then claims nothing, starts, resumes, adopts, and ends no lane, starts no orchestrator, assistant, or consultation, and answers nothing on the worker socket, so a session the stopped service left running spools its hooks as it does while no daemon runs. A `ropeshift init` run while the service is stopped leaves every claim, parked or woken lane, running lane, and conversation as it found them, and the work goes on when the installation's own daemon runs it. A session that reached that daemon's worker anyway would be refused, and its lane would end `blocked` with the reason `cli_not_qualified`, for a member or the orchestrator to send on. A daemon you start yourself always probes.
