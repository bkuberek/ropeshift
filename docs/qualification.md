# Qualification

Ropeshift drives the unmodified Claude Code CLI. A few of the surfaces it uses are not in the vendor's documentation, so each one is used only on a version where a check of it passed. This page says what is checked, where the results come from, and what a lane does without a surface.

## Checked surfaces

Each surface has one check and one use. A surface whose check failed, or could not run, is not used.

| Surface | Check | Without it |
|---------|-------|------------|
| `socket_line` | A message on the session's own socket opens a turn: a `UserPromptSubmit` hook names its first line, and a headless replay carries its `uuid` | No message from anyone else reaches a session, and a pane's own input is typed |
| `receipts` | A session that refuses messages answers a reply address with `expired` and `refused` | A refusal is not read, and a message waits until it expires |
| `peer_message_hold` | A session that holds messages reports the hold in its stream | Nothing: no state follows from a hold |
| `socket_dir` | `CLAUDE_CODE_TMPDIR` moves the socket into the lane's own directory, within the path limit | The socket stays where the CLI puts it |
| `unified_windows` | A `rate_limit_event` carries a window's `utilization` | The near-limit watch reads the top-level figures only |
| `command_lifecycle` | The stream reports it | Nothing: the design does not need it |
| `print_keeps_login` | On a subscription, `system/init` reports `apiKeySource` `none` and a `messaging_socket_path`, and a `rate_limit_event` arrives | A subscription has no headless lane, and its lanes run in tmux |
| `start_screens` | A pane in a new directory reaches the prompt through the start check | Nothing beyond the start check, which stops on any screen it does not know |

A version where any surface ropeshift relies on did not pass runs in `degraded_mode`. The lane shows the condition, and the driver uses documented surfaces in its place. `command_lifecycle` and `peer_message_hold` are reported and never degrade a version, and the login check counts only on a subscription.

## Where results come from

The release ships a matrix in the binary. It holds versions, surfaces, and results, and nothing else. `ropeshift worker doctor` reads it, so it can say whether the installed version is qualified with no daemon running.

A running worker does not rely on the matrix alone, because the settings and plugins on a machine are part of what must work. When a lane is about to start a process on a version this worker has not probed, the worker runs a probe on that machine first:

- three short headless sessions, under `accept`, `refuse`, and `hold`, with at most one turn each, apart from a second turn when the first message is never taken;
- one pane in a new directory, which reaches the prompt and takes no turn, when the worker has a terminal.

Every lane that reaches the version waits for the same probe, and a person sees the lane held as `qualifying` when the probe takes longer than a second. The result is kept in the worker's journal, recorded in the ledger as `worker.driver_qualified`, and handed to the driver. A probe that cannot run leaves every surface unobserved, so the version runs degraded rather than holding work.

## When the CLI updates itself

A running process keeps the version it started on. A new process reads the installed version at its launch:

- A version the last probe named uses that probe's result.
- Any other version uses no undocumented surface, whether the last probe passed or failed, until its own probe ends.
- The worker probes the new version before the next process on it starts.
- A session that a restarted worker takes over uses the result for the version it started on, not the installed one.

So an update never moves a lane between qualified and degraded without a probe's result for the version it runs.
