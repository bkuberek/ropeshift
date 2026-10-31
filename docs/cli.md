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
| `ropeshift source list` | Lists sources, their health, and the files they cannot read |

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
