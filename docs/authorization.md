# Authorization

Ropeshift's aim is that an agent cannot grant itself authority. This page says how, what stops what, and where that stops being true.

## Who can do what

| Principal | Proves itself with | Decides a request |
|-----------|--------------------|-------------------|
| Member | A session from a passphrase | Any request |
| Orchestrator | A token for its session process, through its worker | Only a class a member delegated, never its own, and never a plan or recovery request |
| Lane | A token for its session process, through its worker | None |
| Worker | The worker credential | None |
| Delegate | A delegate credential a member made, on the local socket only | Only a class a member delegated, as an orchestrator, and never a card |

A lane or orchestrator token works only on the worker's local socket. It ends when its process ends. A `clear` keeps the process, so the token goes on, and it acts as the lane's current generation.

Only a member can start an orchestrator, and no session starts until there is something to judge. The first passphrase needs no session, which is safe only because no agent runs before a member asks for one: a lane through a dispatch, or an orchestrator through `ropeshift orchestrator start`. `ropeshift init` sets it as its first step, so on a machine set up that way it exists before any agent can run.

One orchestrator per machine serves every active project of the installation. Each of its tools that acts or reads names the project, and each list it reads labels every item with its project. A call reaches only the named project's ledger, so a record of another project does not resolve, and its ladder lookups find only the question's project's records. A lane's token reaches only its own project.

An orchestrator reads a request in the wake that names it, which states the request's summary and the digest a decision binds to. Only a request a member's delegation covers reaches it, and none it made itself. It reaches the ledger through its worker, and the ledger holds each act to the run a member asked for: a worker's credential with no such run acts for nobody. Its session holds its own tools and none of the vendor's, because it has no repository to work in and nobody to answer a prompt.

A worker's level says how far it goes with nobody there. At `manual`, members claim by hand and an orchestrator only works the question ladder. At `attended`, it claims and starts lanes only inside a run window a member opened. At `unattended`, it claims whenever capacity exists. A subscription starts at `attended`, and so does a sign-in nobody could read. A key starts at `unattended`. Below `unattended`, a worker is held to a bound on lanes and on generations a day, which an `[autonomy_policy]` states.

Only a member opens a window, chooses a level, or acknowledges the risk, and no tool, route, or act gives an agent any of these. A subscription worker reaches `unattended` only after a member acknowledges the stated risk for that worker, as an event naming the text they read. That is a record that a person accepted a risk. It is not permission from anyone, and it does not make automated use acceptable under any vendor's terms. A level is read again at each claim, so a worker that comes back on a subscription falls to `attended`, and so does one whose statement changed. A member's own claim needs none of this, and a lane a wake brought back goes on outside a window, up to the bound on generations, because a wake must bring a lane back with nobody there to dispatch it.

A hook that cannot reach the worker appends to a spool, and that is a file anything this user runs can write. Taken at its word, a line could end another lane's request or give it a generation, so a line carries a seal, keyed from the token of the lane it names. The line holds neither the token nor its digest, so reading the spool gives a lane nothing to speak with. A line with no seal, a seal from any other token, a line changed after its seal, and a line whose token has ended all count for nothing, and the worker reports how many it dropped. A lane can copy a sealed line and append it again, which says only what that session already said, and a repeat merges by meaning.

## A request binds to what it asks

When a call needs a person, its hook reports the tool and its input to the worker, and the worker works out the rest. The digest covers the tool's name, the canonical form of its input, and the path the call lands on, and what a reviewer reads comes from those same facts. A lane's own words reach neither.

A path is a fact about the disk, so one input can land somewhere else later. The worker walks the path on every look, part by part, the way the kernel does, and follows every link, including a link to something that does not exist yet, because a write creates its file through the link. So a link swapped in after a request was raised, or after a decision, makes it another call.

- A decision names the digest its reviewer read. A digest that is not the request's is refused.
- An approval runs one call. The same call again is a new request.
- A call that differs from what was approved voids the approval, and the changed call asks again. That covers a call whose input is the same and which lands somewhere else. A void approval never comes back.
- A denial stands for that call.
- A request whose lane ends is marked `expired`, and so is one past its time.

The last look is the one that answers the hook, and no hook runs between that answer and the tool opening the file. A process the lane left running can swap a link in that window, which is one more case of the limit below.

A session that comes back to an approved call hears that exact call first, with its whole input, because the digest covers every field and a fresh session remembers none of them. A session that makes any other call that asks for permission still voids the approval.

## Where each rule lives

| Rule | Enforced by |
|------|-------------|
| A principal cannot decide its own request | The domain, and a database check on `decided_by` and `requested_by` |
| Only a member or an orchestrator decides | The authorization function, the domain, and a database check on the decider's kind and name |
| A decision names the request's digest | The domain, and a database check |
| Nothing is approved with nobody behind it | A database check |
| A human decision needs a session | The route, which takes only a session the ledger issued |
| Ending a session needs only its token | The sign-out route, which ends the session the call presents, and `ropeshift sign-out`, which never asks for a passphrase |
| A lane's token cannot reach a decision | The worker socket, which takes a decision only from an orchestrator's token |
| A lane's token reaches no orchestrator tool | `ropeshift mcp`, which does not list one, and the worker socket, which refuses one |
| An orchestrator's token reaches no lane's tool | The worker socket, which refuses a report, a wait, an ask, and a reply from it |
| Only a member starts an orchestrator | The route, which takes only a session, and the authorization function |
| An orchestrator runs only what its worker holds | The control plane, which reads the lease before it starts or stops a lane |
| An orchestrator recovers stuck work only within `[recovery_policy]`, and never by a takeover | The control plane, which checks the policy in force in the transaction that acts, and the authorization function, which gives no agent a member's retry, reset, or takeover |
| Only a member decides a recovery request | The authorization function, the domain, and a database check on the decider's kind |
| An orchestrator's breaker reset never overrides a lane's daily cap | The control plane, which refuses a reset of a lane that reads `day_capped`, and the worker, which keeps on the day what such a reset clears from the window |
| An orchestrator cannot cite a record of another project | The plane, which runs each call in the ledger of the project it names, where an ID of another project does not resolve |
| Only a member opens a window or acknowledges the risk | The route, which takes only a session, the authorization function, and a database check on the opener's and the acknowledger's kind |
| A claim no member made follows the level and the window | The claim's transaction, which reads both as it grants |
| `unattended` on a subscription needs a current acknowledgement | The route that sets a level, and the claim, which reads it again |
| A message is never a decision | Nothing in the decision path reads a message |
| A lane that writes creates, edits, and deletes files in its worktree, and stages and commits, with no prompt, and runs nothing else that way | Generated settings: an `Edit` allow rule on the worktree for the file tools, one allow rule each for `mkdir`, `rm`, `rmdir`, and `mv`, and one each for `git add`, `git commit`, `git rm`, and `git mv`. No other git command or program has one, and a review lane holds none |
| An edit through a link that leads out of the worktree asks, or is refused | The agent CLI, which allows a path through a link only when the file it resolves to matches the rule too, and the generated deny rules, which name what lies beside the worktree at each level above it |
| Those programs, and that stage or commit, stay in the lane's worktree | The hook, which refuses an operand outside it or one whose place it cannot read, a file program whose path passes through a link, and a git command after a `cd`, `-C`, `--git-dir`, `--work-tree`, or a `GIT_DIR` that leaves it |
| A push never shares a call with a stage or a commit | The hook, so a push reaches a person however the CLI reads a compound command |
| A push reaches a person in every permission mode, and a force push or a push to a default branch reaches nobody | A generated ask rule on every push, which no allow rule outranks, and the hook and generated deny rules refuse the rest |
| Every action a profile gates asks, in every permission mode | A generated ask rule for each gate, which outranks every allow rule |
| No lane runs in a permission-bypass mode | The profile, which refuses `bypassPermissions` and any mode it does not know in every layer, the driver, which refuses to start or resume a session in it, and generated settings, which turn the mode off in the agent CLI |
| A worker pushes a lane's work only when the user chose it | The profile, where `push_on_stop` is off by default, only the user's layer sets it, and a repository's layer or a lane's overrides that change it are refused. See [onboarding](onboarding.md#work-stays-on-this-machine) |

## The web listener

The web UI serves on a loopback port, and any process on the machine can reach that port, so it holds to more than the socket does. It answers only to its own loopback names on its own port, which refuses a page that rebinds a hostile name to this machine. It serves a closed list of routes, each with a command that does the same, and keeps a worker's routes and the passphrase off that list. Every read needs a session here too, because a port reaches further than a socket only its owner can open.

A session lives in a cookie no page can read, and a change also needs a token derived from it that only this listener's own page can read. A request from any other origin is refused, and that includes another port on this machine, which a same-site cookie does not stop. Signing out ends the session in the ledger, and an open event stream ends with it. Every response forbids inline scripts, so text from a document or an agent cannot run even if a page were to render it.

## A person at the terminal

In a pane the prompt stays on screen while the hook waits, and a person's answer takes effect at once. Ropeshift sees only what follows. A tool that ran closes the request as `answered`, with channel `terminal`, and a turn that ended with no tool closes it the same way. No session proved who answered, so this is a record with nobody's name on it. It grants nothing: only `approved` lets a call run.

Ropeshift sends no keys to a pane with a request open, because an Enter on a permission prompt is an answer.

## Delegation

A member publishes which requests an orchestrator may decide, as a versioned `[delegation_policy]`. The built-in version delegates nothing.

```toml
[delegation_policy]
version = 2

[[delegation_policy.classes]]
tool = "Bash"
command_prefix = "go test"

[[delegation_policy.classes]]
tool = "Edit"
path = "docs/**"

[[delegation_policy.classes]]
tool = "Agent"
agent = "deep-implementation"
```

A class names a tool and exactly one of a command prefix, a path, or an agent. An `agent` class names the `Agent` tool and one roster agent whose approval is per use, and covers a lane's start of that agent alone, as the call's own input names it ([agents](#agents)). A class with a prefix or a path and a call that carries the other field goes to a member. A prefix reaches one simple command: any shell syntax, such as `;`, `&&`, `|`, `$(`, or a redirect, sends the request to a member. A prefix admits any arguments, so declare only a command whose arguments cannot start something else: `go test -exec` can. A class reads where a call lands, inside the lane's worktree, so a link a lane made under `docs/` leads nowhere a class covers. A path with a `..` in it goes to a member, because a tool reads that off the text and the disk may disagree, and so does a file with a second name, which could lie anywhere. Every other request goes to a member, and so does an orchestrator's own. A decision records the version in force, and only a member can publish one.

### Delegates

A delegate is an agent outside ropeshift, such as a CLI assistant, that acts for a member with a delegate credential (ADR-0024). A member makes one with `ropeshift delegate create`, bound to one project and to a life of one minute to thirty days, and the ledger keeps only its digest. The credential reaches only its project's routes on the local socket: every installation route, every other project, and the web listener refuse it. It acts as the actor `delegate`, named `<member>/<credential>`, so every event names the member it acts for. A command that acts on the machine itself rather than through the API alone refuses a delegate's credential with exit 8: `service install`, `service uninstall`, `init`, `worker doctor`, `login`, `passphrase set`, `lane attach`, `plan start`, `adopt`, `backup`, which writes a file the caller names, and `repository add`, which registers clones from paths on this machine. A delegate's command also reads no file on this machine: a flag or a value that names one, such as `lane start --profile`, `agent add --file`, a `@<file>` value, or `policy publish --file`, is refused before the file is read. A member revokes one at any time, and signing out of every session revokes every one of theirs. No lane inherits `ROPESHIFT_DELEGATE_TOKEN`, as none inherits `ROPESHIFT_MEMBER_TOKEN`.

Every changing operation has a class, which `ropeshift operation list` prints:

| Class | A delegate's call |
|-------|-------------------|
| Operational | Runs as the delegate: creating and claiming work, starting and steering lanes, reviewing planning work, retrying work with an attempt left, pausing the fleet, recording a decision the decision policy would let an orchestrator record, registering a repository and changing its settings short of the ones below, deciding a request the delegation policy covers, and sending a message |
| Consent | Records a confirmation card and runs nothing, unless the delegate policy exempts it: accepting work, sending it to rework, canceling it, any other review, a retry with no attempt left, running the fleet or setting its caps, questions and other decisions, changing a forge binding or a merge method, removing a repository, deciding an effect, and typing into a pane, because keys in a pane can answer a prompt no request records |
| Member only | Refused: managing sessions and delegates, the passphrase, the installation's backup, the delegate policy, confirming a card, and every change to a member's conversations with the assistant: starting, opening, stopping, renaming, deleting, compacting, or clearing one, and writing in it |

A card names the exact call, why the delegate proposes it, and what it changes, bound to the subject as it stood, by the identity of the record even when the call named it by a name or a prefix. A member confirms it in Needs you or with `ropeshift confirm`, and the call then runs as that member, through the same checks and events as the member's own. The card is bound again just before its call runs: a subject that changed first makes the card void, and no delegate's word confirms one, its own or another's. Revoking a delegate voids its open cards, and a delegate holds at most 20 open cards in a project, so no agent can bury Needs you.

A versioned `[delegate_policy]` lists the consent operations a delegate runs with no card, and the built-in version lists none. It can never list raising a repository's merge authority, moving its review target to a pull request, changing a policy or a category, changing the agent roster, managing members, worker join tokens, autonomy, or deciding a request outside the delegation policy: a version that lists one is refused. An exemption never applies to an answer or a decision in a one-way category, which stays a card. A delegate decides a request only within the delegation policy, as an orchestrator does.

### The assistant

A member's assistant in a project is a delegate ropeshift runs itself (ADR-0024). Its session runs on a worker, and for each generation of that session the control plane makes a delegate credential of kind `assistant`, labeled `assistant`, bound to that conversation and generation, with no expiry. The worker puts the token in that process's environment as `ROPESHIFT_DELEGATE_TOKEN`, with `ROPESHIFT_PROJECT`, and never writes it to its journal. A call with an assistant credential acts only while its conversation runs that credential's generation: once the generation ends, the conversation stops, or the member signs out of every session, the credential is revoked and refused with 401 `bad_delegate`. Its calls take their class like any delegate's, and each one that ran, or became a card, is an entry of the conversation. A member's list of delegates holds only the credentials the member made.

The assistant reads and searches files only in its own directory and the read-only trees of the project's repositories, uses the context tools, and runs a shell only for commands that start with `ropeshift `. Its rules allow reads under those roots alone, and its hook denies a read, a search, or a listing anywhere else whatever a rule allows, so it cannot read the state directory, a credential, or another process's environment. It has no web tool, and every edit and write tool is denied by rule and by the hook. Its hook also denies every shell command but one `ropeshift` command run alone, with no other command, substitution, variable, or redirect to or from a file, whatever a rule allows. Its session runs in the permission mode that asks, whatever mode the member's profile gives lanes, so a call no rule allows reaches the hook, which denies it with a reason and never raises it as a request. A message to it, as any message, grants nothing: a card waits for a member's confirmation whatever a message says, and conversations are a member's alone, so no delegate starts, deletes, or writes in one.

Each operation names the actions its use case checks, and `Allowed` grants a delegate only what an operational or exemptable consent operation of a project needs. Every member-only and never-exempt operation needs an action no delegate holds, so a defect in the class gate still cannot run one.

## Agents

Every session ropeshift starts receives the agent roster as subagents: agents by kind of work, each with a purpose, a capability class, an effort, an approval setting, and allowed tool categories. On Claude Code the worker passes them with the `--agents` flag on every start and resume, and writes nothing to `~/.claude/agents/` or to a worktree.

- A rendered agent's tools are its allowed categories, less its denied ones, inside its session's generated rules. A read-only agent never holds an edit tool, and in a lane that writes it holds no shell, because the lane's rules let a shell move and delete files with no prompt. A review lane receives read-only agents only, and the orchestrator receives none.
- No rendered agent sets a permission mode, hooks, or MCP servers. Its permission mode is the session's, the lane's deny rules and hooks apply inside it, and no agent may start another: the session sets `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH=1`, and every rendered agent disallows the `Agent` tool.
- Starting an agent whose approval is `per_use` asks in every permission mode, through an `ask` rule `Agent(<name>)`, and raises an ordinary request bound to the call's digest. A member decides it, or an orchestrator under an `agent` class of the delegation policy. A lane that runs as a per-use agent asks nothing to start: its plan's approval or the member who started it decided that.
- The agent a lane runs as sets its model, through the class mapping of the user's profile, and its effort. Its denied categories narrow the lane's rules, and never widen them. A lane's own profile layer still wins.
- Changing the roster or its routing guide is a consent operation: only a member's session runs `ropeshift agent set`, `add`, `disable`, `enable`, `reset`, `revert`, and `guide set` and `reset`, at a scope the command names. The class mapping lives in the user's profile, `[agent.classes]`, which no repository or lane layer can change, so a file a plan or an agent can edit cannot make a cheap agent expensive.

## Recovery

An orchestrator recovers stuck work with no member, within a versioned `[recovery_policy]` that only a member publishes. The built-in version is this one:

```toml
[recovery_policy]
version = 1
restart_stalled = true
max_breaker_resets_per_day = 2
retry_failed = true
replan_blocked = true
max_attempts = 3
```

`max_attempts` is how many attempts a work order gets when it names no bound of its own. A work order's own bound wins, and no work order file sets one yet, so for now every work order has the policy's. A member's retry of work with no attempts left gives that work a bound of its own, one past the attempts it used.

It has four tools for it, each with the project, the work order, and a reason that must not be empty:

| Tool | Does | Within the policy when |
|------|------|------------------------|
| `lane_restart` | Ends a stalled lane's session and starts its next generation, which counts toward the lane's day | `restart_stalled` is true, and the lane runs on this orchestrator's machine |
| `lane_reset` | Resets a tripped breaker, so the lane starts again | The lane runs on this orchestrator's machine, it does not read `day_capped`, and orchestrators reset this work order's breaker fewer than `max_breaker_resets_per_day` times in the last day |
| `work_retry` | Returns failed work to `ready`, and the next lane reads why the last one failed and the reason | `retry_failed` is true, and the work has used fewer attempts than its own bound, or the policy's `max_attempts` when it names none |
| `work_replan` | Sends blocked work to `planning` with the reason and its lane's outcome, where its contract passes review again | `replan_blocked` is true |

A call whose work order or lane is no longer stalled, tripped, failed, or blocked answers with a conflict and records nothing: another actor already moved it. A call outside the policy changes nothing. It raises an approval request of kind `recovery`, bound to the act, its arguments, the reason, and a digest of the work order and lane as they stood, and the call's answer names it. The request waits in Needs you with the reason and the limit it went past, and the same call again finds the same request. Only a member decides one: approving it runs the act as the member's decision, and a retry of work with no attempts left gets one more attempt. The request is void once its work order or lane moves.

Every call that acts or is refused is a `recovery` event naming the act, the work order and its lane, the orchestrator and the generation of its session, the reason, the policy version in force, and the result. A failed lane leaves its work `failed` until someone retries it, so a crash that repeats does not spend every attempt unseen. The worker wakes its orchestrator for failed work, blocked work, and a lane its window tripped. A wake for failed work with no attempts left says so, because a retry of it reaches a member. The policy bounds orchestrators and not members: `ropeshift lane respawn`, `ropeshift lane reset`, `ropeshift work retry`, and `ropeshift work replan` do the same as a member. A takeover is never a recovery act: the control plane's takeover policy or a member reassigns lapsed work.

## The limit of same-user isolation

In solo mode, lanes run as the same operating system user as the person. The operating system does not separate them, so everything above raises the cost of self-approval and makes it visible. It does not make it impossible.

A lane with a shell can, as that user:

- Read another lane's token or a member's session from a process's environment, where the platform shows it.
- Read the worker's credential, and act as that worker's orchestrator for whatever a member delegated.
- Connect to any socket the user can reach, which includes the private terminal server, so a lane in a pane can press the key that answers its own prompt.
- Read and write the ledger and journal files directly. A database check stops a statement that breaks it, and it does not stop one that names a member as the decider. A window or an acknowledgement written that way passes for a member's. The event log would then disagree with the row, which a replay shows. The journal holds each token's digest, which is what a spool line's seal is keyed from.
- Read the stored passphrase hash, and guess at it offline.
- Change the generated settings and hook commands, past the guardrails that deny that write. A hook is a guardrail and not a sandbox.
- Run what it wrote through a repository's own hooks. An edit in the worktree and a commit both run with no prompt, and a commit runs those hooks, so where they run a file in the worktree, or `core.hooksPath` names a directory in it, a lane runs its own code at its next commit.

Strong separation needs a boundary an agent cannot cross: another operating system user, a container, a virtual machine, or a worker on another machine. Run a lane on untrusted work only behind one of these.
