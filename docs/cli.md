# Command line

`ropeshift` is a client of the versioned API, so it reaches the ledger the same way every other surface does. It finds the control plane's socket from the installation's paths, or from `--socket`.

Every command takes `--json`. With it, a result is one JSON document on stdout, and a failure is `{"error": {...}}` on stderr with the same exit code.

## Commands

| Command | Does |
|---------|------|
| `ropeshift work create --title T [--criterion C]...` | Records a work order and the contract it pins, in `contract_review` |
| `ropeshift work list [--state S]` | Lists work orders |
| `ropeshift work show <id>` | Shows one work order |
| `ropeshift work review <id>` | Records a passed review, and the definition of ready decides the move to `ready` |
| `ropeshift work claim <id> --worker W [--claim-id C]` | Claims a work order for a worker. A retry with the same claim ID returns the grant |
| `ropeshift work release <id> --claim-id C --fence N` | Gives back a claim whose lane has not started |
| `ropeshift work reassign <id> --reason R` | Ends a lapsed holder's ownership, which moves the fence |
| `ropeshift lane start <work-order> [--lane L]` | Asks the worker holding the claim to run a lane. It returns when the ledger holds the request, and a retry with the same lane ID is the same request |
| `ropeshift source list` | Lists sources, their health, and the files they cannot read |
| `ropeshift lane list` | Lists lanes. Each line states the lane's state, liveness, reachability, conditions, and whether the value is `observed` or `last_reported` |
| `ropeshift lane show <lane>` | Shows one lane the same way. `--json` adds `as_of` and `reported_by_epoch` |
| `ropeshift worker list` | Lists registered workers, each with its epoch and protocol |
| `ropeshift worker token` | Makes a join token for one worker. It is shown once |

Three commands are for ropeshift or an agent CLI to run, and a person does not need them:

| Command | Does |
|---------|------|
| `ropeshift hook <event>` | Reports one hook event to the worker, and spools it if the worker is down. It always exits 0 |
| `ropeshift mcp` | Serves a lane's tools over stdio, and reaches only the local worker |
| `ropeshift lane-shim` | Holds a headless session's pipes in a process of its own, so the session outlives its worker. It journals every line, serves that journal on a private socket, and writes each input at most once |
| `ropeshift lane-pane <file>` | Replaces itself with an interactive session in its pane, with the exact environment a private file holds, and removes that file first |
| `ropeshift statusline [--pass]` | Reports one status line input to the worker. It is the driver's half of an interactive lane's status line, and `--pass` prints the input for a person's own command after it in a pipe |

## Exit codes

| Code | Meaning | What to do |
|------|---------|------------|
| 0 | The command did what it said | Nothing |
| 1 | The control plane could not answer, or the command failed for another reason | Retry, and read the message |
| 2 | The command line or the request is invalid | Fix the command |
| 3 | The record does not exist | Check the ID |
| 4 | The ledger's state refused this: a claim was lost, work was not lapsed, or the move has no edge | Read the current state |
| 5 | The fence is stale: another holder owns this work | Stop, and read the current fence in the message |
| 6 | The lease lapsed and this holder can return | Return, then retry |
| 7 | The review is recorded, and the definition of ready names what still holds the work back | Resolve what it names. The work order moves on its own |
| 8 | This actor may not take this step | Use an actor that may |
| 9 | Nothing answered on the socket | Start the daemon, or check `--socket` |
