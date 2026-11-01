# Command line

`ropeshift` is a client of the versioned API, so it reaches the ledger the same way every other surface does. It finds the control plane's socket from the installation's paths, or from `--socket`.

A command that changes something needs a member's session, and a read on the local socket does not. With no session, the command asks for the passphrase on the terminal, for that one command. `eval "$(ropeshift login)"` holds a short-lived session in one shell instead. No file holds it, and no lane inherits it. The passphrase comes only from the controlling terminal, never from stdin, a flag, or a variable, so a process with no terminal cannot sign in.

Every command takes `--json`. With it, a result is one JSON document on stdout, and a failure is `{"error": {...}}` on stderr with the same exit code.

## Commands

| Command | Does |
|---------|------|
| `ropeshift work create --title T [--criterion C]...` | Records a work order and the contract it pins, in `contract_review` |
| `ropeshift work list [--state S] [--worker W]` | Lists work orders. With `--worker`, it lists only ready work that worker's manifest can run |
| `ropeshift work show <id>` | Shows one work order |
| `ropeshift work review <id>` | Records a passed review, and the definition of ready decides the move to `ready` |
| `ropeshift work claim <id> --worker W [--claim-id C]` | Claims a work order for a worker. A retry with the same claim ID returns the grant |
| `ropeshift work release <id> --claim-id C --fence N` | Gives back a claim whose lane has not started |
| `ropeshift work reassign <id> --reason R` | Ends a lapsed holder's ownership, which moves the fence |
| `ropeshift work send-back <id> --reason R` | Returns a work order in review to `planning`, because its contract must change. It voids the review, and the contract's next version returns the work to review |
| `ropeshift plan list` | Lists plans |
| `ropeshift plan show <id>` | Shows one plan with its goal, each contract's newest version, and their work orders |
| `ropeshift requirement list` | Lists each requirement's newest version |
| `ropeshift requirement get <key>` | Shows one requirement's newest version, by the key a contract names |
| `ropeshift question list [--category C] [--status S] [--scope S] [--text T]` | Finds questions. `--scope` names what a question is about, and `--text` ranks the best match first |
| `ropeshift question show <id>` | Shows one question |
| `ropeshift question record --text T [--category C]... [--subject ID] [--subject-kind K] [--blocking]` | Records a question as it is, in `open`. Categories come from `category list` |
| `ropeshift question dismiss <id>` | Closes a question with no answer. It stays on record |
| `ropeshift decision list [--category C] [--status S] [--scope S] [--text T]` | Finds decisions, and an overturned decision stays findable |
| `ropeshift decision show <id>` | Shows one decision |
| `ropeshift decision record --statement S [--rationale R] [--scope S] [--category C]... [--citation C]... [--reversibility R] [--question Q] [--assumed]` | Records a decision, or with `--assumed` a reversible assumption |
| `ropeshift category list` | Lists the workspace's categories: the built-in ones and those it added |
| `ropeshift category add <name>` | Adds a category. A name is lowercase words joined by hyphens, and a category cannot be removed |
| `ropeshift search <words> [--kind K]... [--limit N]` | Ranks candidates for a question among decisions, requirements, contracts, and open questions. A score orders one search and means nothing else: a person or an agent judges a match |
| `ropeshift document list` | Lists the Markdown documents of every source, at the commit intake reads |
| `ropeshift document show <path> [--version V] [--instance I]` | Prints one document. `--version` names an earlier version, and `--instance` names the repository when two hold the path |
| `ropeshift document versions <path> [--instance I]` | Lists the versions of one document, newest first |
| `ropeshift lane start <work-order> [--lane L] [--profile F]` | Asks the worker holding the claim to run a lane. It returns when the ledger holds the request, and a retry with the same lane ID is the same request. `--profile` names a TOML file that is this lane's own profile layer, over the built-in, user, and repository layers. It cannot raise the permission mode or change what the machine admits |
| `ropeshift source list` | Lists sources, their health, and the files they cannot read |
| `ropeshift lane list` | Lists lanes. Each line states the lane's state, liveness, reachability, conditions, and whether the value is `observed` or `last_reported` |
| `ropeshift lane show <lane>` | Shows one lane the same way. `--json` adds `as_of` and `reported_by_epoch` |
| `ropeshift lane reset <work-order>` | Lets the lane its breaker stopped start again. Only a member can, because a tripped lane is waiting for a person |
| `ropeshift lane archive <lane> [--force]` | Asks the worker that ran an ended lane to remove its worktree. Its branch stays. A lane with `unpushed_work` is refused, and `--force` removes it anyway, which only a member can do. The worker reads the worktree again first, and saves what it finds to a ref |
| `ropeshift fleet show` | Shows the fleet's mode and the caps a claim is held to |
| `ropeshift fleet run`, `pause`, `drain` | Sets the mode for the whole fleet. `pause` takes no new claims and starts no new lanes, and a running lane that crashes still restarts. `drain` also stops new generations. Each lane already running goes on |
| `ropeshift fleet caps [--wip N] [--per-repository N] [--per-worker N]` | Sets what a claim is held to. A flag left out keeps its value, and 0 means no limit. A claim already granted stays |
| `ropeshift login` | Asks for the passphrase on the terminal and prints a session for `eval`. It lasts an hour |
| `ropeshift passphrase set` | Sets the passphrase on the terminal. The first needs no session, and a later one asks for the current passphrase. A passphrase has at least 8 bytes |
| `ropeshift worker doctor` | Reports each agent CLI on this machine: whether it is present, its version, whether that version is qualified, and the kind of its sign-in. It uses the vendor's status command, reads no credential, and needs no daemon. It exits 1 when no CLI is usable |
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
| 4 | The ledger's state refused this: a claim was lost, work was not lapsed, the move has no edge, or a removal would lose work no remote holds | Read the current state |
| 5 | The fence is stale: another holder owns this work | Stop, and read the current fence in the message |
| 6 | The lease lapsed and this holder can return | Return, then retry |
| 7 | The review is recorded, and the definition of ready names what still holds the work back | Resolve what it names. The work order moves on its own |
| 8 | This actor may not take this step, or the step needs a member's session and has none | Use an actor that may, or sign in on a terminal |
| 9 | Nothing answered on the socket | Start the daemon, or check `--socket` |
