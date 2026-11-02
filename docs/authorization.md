# Authorization

Ropeshift's aim is that an agent cannot grant itself authority. This page says how, what stops what, and where that stops being true.

## Who can do what

| Principal | Proves itself with | Decides a request |
|-----------|--------------------|-------------------|
| Member | A session from a passphrase | Any request |
| Orchestrator | A token for its session process, through its worker | Only a class a member delegated, and never its own |
| Lane | A token for its session process, through its worker | None |
| Worker | The worker credential | None |

A lane or orchestrator token works only on the worker's local socket. It ends when its process ends. A `clear` keeps the process, so the token goes on, and it acts as the lane's current generation.

## A request binds to what it asks

When a call needs a person, its hook reports the tool and its input to the worker, and the worker works out the rest. The digest covers the tool's name and the canonical form of its input, and what a reviewer reads comes from that same input. A lane's own words reach neither.

- A decision names the digest its reviewer read. A digest that is not the request's is refused.
- An approval runs one call. The same call again is a new request.
- A call that differs from what was approved voids the approval, and the changed call asks again. A void approval never comes back.
- A denial stands for that call.
- A request whose lane ends is marked `expired`, and so is one past its time.

## Where each rule lives

| Rule | Enforced by |
|------|-------------|
| A principal cannot decide its own request | The domain, and a database check on `decided_by` and `requested_by` |
| Only a member or an orchestrator decides | The authorization function, the domain, and a database check on the decider's kind and name |
| A decision names the request's digest | The domain, and a database check |
| Nothing is approved with nobody behind it | A database check |
| A human decision needs a session | The route, which takes only a session the ledger issued |
| A lane's token cannot reach a decision | The worker socket, which takes a decision only from an orchestrator's token |
| A message is never a decision | Nothing in the decision path reads a message |

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
```

A class names a tool and exactly one of a command prefix or a path, and a call that carries the other field goes to a member. A prefix reaches one simple command: any shell syntax, such as `;`, `&&`, `|`, `$(`, or a redirect, sends the request to a member. A prefix admits any arguments, so declare only a command whose arguments cannot start something else: `go test -exec` can. A path is read inside the lane's worktree, with links followed as far as they exist, so a link a lane made under `docs/` leads nowhere a class covers. A path is read when the request is raised, and a link made after that is not seen. Every other request goes to a member, and so does an orchestrator's own. A decision records the version in force, and only a member can publish one.

## The limit of same-user isolation

In solo mode, lanes run as the same operating system user as the person. The operating system does not separate them, so everything above raises the cost of self-approval and makes it visible. It does not make it impossible.

A lane with a shell can, as that user:

- Read another lane's token or a member's session from a process's environment, where the platform shows it.
- Read the worker's credential, and act as that worker's orchestrator for whatever a member delegated.
- Connect to any socket the user can reach, which includes the private terminal server, so a lane in a pane can press the key that answers its own prompt.
- Read and write the ledger and journal files directly. A database check stops a statement that breaks it, and it does not stop one that names a member as the decider. The event log would then disagree with the row, which a replay shows.
- Read the stored passphrase hash, and guess at it offline.
- Change the generated settings and hook commands, past the guardrails that deny that write. A hook is a guardrail and not a sandbox.
- Write to the spool, which carries no token, so a line in it can say anything about any lane. A spooled hook can close a request as `answered`, and it cannot allow a call.

Strong separation needs a boundary an agent cannot cross: another operating system user, a container, a virtual machine, or a worker on another machine. Run a lane on untrusted work only behind one of these.
