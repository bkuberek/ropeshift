# API

Every surface of ropeshift is a client of one versioned HTTP API: the command line, `ropeshift top`, the web UI, and the worker. On the local machine the API listens on the control plane's socket, which only its owner can reach, and the web UI reaches a closed list of its routes through the web listener.

## Routes

An installation holds one or more projects, and the API names the project in the path of every route on a project's records:

| Routes | Under | What they reach |
|--------|-------|-----------------|
| The installation's | `/v1/` | The registry of projects, members and their sessions, the passphrase, workers with their autonomy, run windows, and orchestrators, the worker's telemetry, the health of the control plane, the installation's backup, the combined Needs you, and the protocol a worker speaks with its credential |
| A project's | `/v1/projects/{project}/` | Everything else: work orders, lanes, plans, requirements, documents, questions, decisions, policies, approvals, effects, notices, repositories, the fleet's mode and caps, the read models, the event stream, measures, and backups |

A route on a project's records with no project in its path does not exist and answers 404.

`{project}` is the project's name, as `ropeshift project list` prints it. A name the installation does not hold answers 404 with the code `no_project`, and a record of another project answers 404 with `not_found`, because a project's routes read only that project's ledger. A repository another project holds answers 409 with `repository_held`, and the message names that project. A change a repository's record refuses as it stands, such as a name another repository of the project holds, answers 409 with `repository_state`.

`GET /v1/projects` lists the projects, each with its state, the identity of each repository it holds, and how many things wait on a member in it, and `GET /v1/projects/{project}` shows one with its database and its repository records. A member's session takes a project through its lifecycle, and each step records an event in the installation's log:

| Route | Body | Step |
|-------|------|------|
| `POST /v1/projects` | `{"name": "...", "display_name": "..."}` | Create |
| `POST /v1/projects/{project}/rename` | `{"name": "..."}` | Rename |
| `POST /v1/projects/{project}/archive` | `{}` | Archive, once every lane ended |
| `POST /v1/projects/{project}/restore` | `{}` | Restore |
| `POST /v1/projects/{project}/remove` | `{"confirm": "...", "backup": "..."}` or `{"confirm": "...", "no_backup": true}` | Remove an archived project |
| `POST /v1/projects/import` | `{"path": "...", "name": "..."}` | Import a project's backup, keeping its ID |
| `POST /v1/projects/import/upload` | `{"backup": "<base64>", "name": "..."}` | Import a backup the request carries, as a browser uploads it, up to 128 MiB |

An archived project answers every change with 409 `project_archived`, a backup excepted, and a step its state refuses answers 409 `project_state`.

`POST /v1/projects/{project}/work-orders` with `"kind": "plan"` and `"adopt": true` records planning work that adopts an existing project: its lane is a planning lane told to onboard what the project holds, and the worker writes the inventory of the project's repositories into its context folder before it starts. Only planning work adopts, and any other kind answers 400 `invalid`. A project that already has an approved plan answers 409 `already_planned`, because it plans its next work with a planning session.

`POST /v1/projects/{project}/repositories` registers repositories in the project from git clones on the machine of the daemon's worker, and needs a member's session. It takes `{"paths": [...], "depth": N, "dry_run": true}`, where each path is absolute. A path that is the top of a clone is that clone, and any other directory holds the clones among its children, down to `depth` levels, one by default. The answer lists each clone and each path with its `outcome`: `registered`, `known` for a repository the project holds already, or `refused` with a `reason`, such as another project holding the repository or a path that holds no clone. A refusal registers nothing for that clone and stops none of the others, and a dry run records nothing. `POST /v1/projects/{project}/repositories/{repository}/remove` takes a repository out of the project once no open work order targets it, and answers 409 `repository_state` while one does; the record stays as history with state `removed`, and another project may then register it.

`POST /v1/projects/{project}/backup` copies one project's ledger, and `POST /v1/installation/backup` copies the installation database. Each takes `{"path": "..."}`, an absolute path on the daemon's machine where no file exists, and needs a member's session. `POST /v1/projects/{project}/backup/download` and `POST /v1/installation/backup/download` take `{}` and answer the same copy in the response, as `application/vnd.sqlite3` with a file name in `Content-Disposition`, for a browser that saves it as a download; nothing of it stays on the daemon's machine.

`GET /v1/audit` reads the installation's log, and `GET /v1/projects/{project}/audit` a project's, newest first: each event with its revision, type, actor, subject, times, and its whole record. `actor` keeps one actor kind, `actor_id` one actor's ID, `kind` the event types that start with its text, `subject` one subject kind, `limit` sets the page from 1 to 200 (50 by default), and `before` starts the page below a revision, which the answer's `next` names, zero when no older event is left. A filtered read looks at 5,000 events at most and answers `"scanned": true` with `next` where it stopped. `GET /v1/members` lists each member that holds a live session, with when each session began and ends, and how many members can sign in; no answer carries a session's token.

A change to a record every project shares, such as a member's sign-in, a worker's registration or autonomy, or an orchestrator, records its event in the installation's log, and no project's log holds it.

## The combined Needs you

`GET /v1/needs-you` lists what waits on a member in every active project, the longest wait first. Each item is the item a project's `needs-you` view holds, with a `project` that names its project's ID, name, and display name. `projects` lists each active project with the revision its ledger stood at and how many of its items wait. A member acts on an item through its own project's routes.

## Delegates and confirmation cards

A delegate is an agent outside ropeshift acting with a member's delegate credential ([authorization](authorization.md#delegation)). It sends the credential in the header `Ropeshift-Delegate`, on the local socket only. Its actor is `delegate`, with the ID `<member ID>/<credential ID>`, so every event it causes names the member it acts for.

A member's session manages the member's own credentials, on the installation's routes:

| Route | Body | Answer |
|-------|------|--------|
| `GET /v1/delegates` | | `{"as_of", "delegates": [...]}`: each credential the member made: its `id` (`dlg_<ulid>`), `label`, `project`, `kind` (`external`), `created_at`, `expires_at`, `last_used_at`, `revoked_at`, and `state` (`active`, `expired`, or `revoked`) |
| `POST /v1/delegates` | `{"project": "...", "label": "...", "expires_in_ms": N}` | 201 with the credential and its `token`, the one time anyone sees it. `project` names an active project, `label` is 1 to 64 characters, holds no control, format, or separator character (a zero-width space, a byte order mark, and a bidirectional control are refused), is kept in its Unicode compatibility-normalized form, and is never `assistant`, in any case and with spaces around it, which is the assistant's own credentials' label (a refusal is 400 `invalid`), and `expires_in_ms` is from one minute to thirty days |
| `POST /v1/delegates/{id}/revoke` | `{}` | The credential, revoked |
| `POST /v1/delegates/{id}/remove` | `{}` | 204, once the credential no longer acts, and 409 `delegate_active` while it does |
| `POST /v1/sessions/end-all` | `{}` | `{"sessions", "delegates", "conversations"}`: every session of the member ends and every credential of theirs that acts is revoked, in one transaction with one event, and then every running conversation of theirs with the assistant stops, in every active project |
| `GET /v1/operations` | | The class table: each operation's `id`, `label`, `class` (`operational`, `consent`, or `member_only`), `exemptable`, and `never_reason` |

The ledger keeps a credential's digest and never its token, and each change to one is an event in the installation's log with the member as its actor. A call that acts records the credential's last use when the one it holds is more than a minute old, as a `delegate.used` event.

These refusals are a delegate's:

| Status | Code | When |
|--------|------|------|
| 400 | `invalid` | A request carries both `Ropeshift-Session` and `Ropeshift-Delegate`, or a consent call carries no `Ropeshift-Why` |
| 401 | `bad_delegate` | The credential is not known, was revoked, or expired, which the message names |
| 403 | `outside_project` | The route is an installation route, or another project's |
| 403 | `member_only` | The operation is a member's alone, such as every route above, a delegate policy, or a card's decision |
| 409 | `delegate_active` | A removal of a credential that still acts |
| 403 | `forbidden` | Any request to the web listener that carries `Ropeshift-Delegate` |

Every changing route names its operation, and a delegate's call takes the operation's class. An operational call runs as the delegate. A consent call runs as the delegate only when the delegate policy in force exempts it, and then records a `delegate.exempted` event naming that policy's version. Any other consent call runs nothing: it records a card, an approval request of kind `confirm`, and answers 202 with `{"card": <approval>}`. It needs the header `Ropeshift-Why`, the delegate's reason, up to 2,000 characters. A call that resolves to several operations, such as a repository change that sets several fields, takes the strictest class, and is exempt only when every consent operation in it is.

A card holds:

| Field | Holds |
|-------|-------|
| `subject` | `<kind>:<id>` of the record the call names |
| `subject_digest` | What the card binds to: the SHA-256 of the record's kind, ID, and the revision of the newest event about it; for a decision of a request, that request's own digest and state; for a policy, the version in force; for a call that creates its record, its arguments, so such a card never goes void |
| `summary` | The operation's label and the subject, such as `Raise merge authority: web` |
| `requested_by` | `delegate:<member ID>/<credential ID>` |
| `detail` | JSON with `operation`, `label`, `method`, `path` (the full project-scoped path), `arguments` (the body as sent), `why`, and `changes`: `{field, from, to}` per field for a repository change or the fleet's caps, `{before, after}` bodies for a policy or a roster change, and `{field, to}` per argument for any other call |

A member decides a card with `POST /v1/projects/{project}/approvals/{id}/decide`, passing the digest they read. Declining, with `"allow": false`, makes it `denied` and runs nothing. Confirming binds the subject again: one that moved makes the card `void` and answers 409 `stale_digest`. Otherwise the card is `approved`, and its exact call runs through the project's own routes as that member's session, with the same checks and events as the member's own call. A 2xx answer makes the card `used`, and any other makes it `failed`, with the API's refusal as its `reason`. `decided_by`, `decided_at`, and `channel` (`web` or `cli`) say who confirmed it, when, and where. The call runs, and its outcome is kept, even when the member's client goes away before the answer. A card still `approved` ten minutes after its confirmation lost its call to a crash, and a sweep makes it `failed` with the reason `interrupted; check the audit`: the project's audit says whether the call ran. A card has `expires_at` 0, and nothing expires it. A delegate's decision of a card answers 403 `member_only`.

Revoking a credential, or signing out of every session, voids the open cards it prepared, and a confirmation of a card whose credential was revoked or removed voids it and answers 409 `request_closed`. A delegate holds at most 20 open cards in a project: its next consent call answers 403 `forbidden` until a member decides one.

| Card state | Means |
|------------|-------|
| `pending` | Open: it waits for a member |
| `approved` | Running: a member confirmed it, and its call is running |
| `used` | Confirmed: its call ran |
| `failed` | Failed to run: the API refused its call |
| `denied` | Declined |
| `void` | Its subject changed before a member confirmed it |
| `expired` | Expired |

The combined Needs you lists each pending card of every active project as an item of kind `confirm`, with a `card` that holds the approval, its parsed `detail`, and the preparing delegate's `label` and `member`. A card also holds `delegate_kind`, `external` or `assistant`, which a surface shows the card's preparer by, never its label. A card the assistant prepared also holds `conversation`, the `id` and `name` of the conversation it came from; once that conversation is deleted, `name` is empty and `deleted` is true, and the card stays. `GET /v1/projects/{project}/approvals/{id}` answers the same `conversation` beside the approval's fields.

A card the assistant prepared stays confirmable after its session stops, because its credential ends with each generation and that says nothing against what it prepared. Revoking that credential by its ID, or signing out of every session, voids its open cards as it voids any delegate's.

A delegate policy is a versioned policy of kind `delegate`, published with `POST /v1/projects/{project}/policies` as a member, and listed with `GET /v1/projects/{project}/policies?kind=delegate`:

```toml
[delegate_policy]
version = 2
exempt = ["work_order.accept", "work_order.cancel"]
```

`exempt` lists consent operations by ID. A version that lists an unknown ID, an operational or member-only operation, or an operation that is never exempt answers 400 `invalid`, naming each and why. The built-in version exempts nothing.

An event's `actor_for` and `actor_label`, in the event stream and the audit, name the member a delegate acted for and its credential's label, or the credential's ID once its record was removed, and `delegate_kind` names its kind, `external` or `assistant`, until its record is removed. They are absent for every other actor, and `GET /v1/audit?actor=delegate` keeps a delegate's events.

## Conversations

A member talks to one target at a time: their assistant in a project, the orchestrator of a worker that serves the project, a planning session, or a lane ([authorization](authorization.md#delegation)). Every route here is a project's, under `/v1/projects/{project}/`, and needs a member's session; a delegate reads its member's conversations and changes none.

| Route | Body | Answer |
|-------|------|--------|
| `GET /v1/conversations` | | `{"as_of", "conversations", "workers"}`: the member's conversations, newest activity first, and each registered worker with `available` and, when it is not, `reason` |
| `POST /v1/conversations` | `{"id", "name", "worker"}` | 201 with `conversation` and `stopped`, the running conversation it stopped first. `id` is the client's, so a retry makes one; an ID another member's conversation holds, or a deleted conversation held, is refused with 400 `invalid` |
| `GET /v1/conversations/{id}` | | One conversation |
| `POST /v1/conversations/{id}/open` | `{"worker"}` | Starts its session, resuming what it had, with `stopped`, and sends nothing: it is the warm start a client makes on the member's first keystroke, so the session is up when the message goes |
| `POST /v1/conversations/{id}/stop` | `{}` | Stops its session; the conversation stays |
| `POST /v1/conversations/{id}/rename` | `{"name"}` | `name` is 1 to 80 characters |
| `POST /v1/conversations/{id}/delete` | `{}` | 204: its session stops, and its entries, its messages, and its record go in one transaction. Each worker that ran it then removes what it holds of it: the messages to it and its runs' records in its journal, and the directory its sessions ran in. Its actions' events and its cards stay in the ledger. The agent CLI's own record of a session, where the CLI keeps one, is outside ropeshift's control and stays |
| `POST /v1/conversations/{id}/compact` | `{}` | Asks its running session to compact its context with the runtime's own command, and adds a `compacted` marker |
| `POST /v1/conversations/{id}/clear` | `{}` | Ends its running generation and starts the next with an empty context, on the same worker, adds a `cleared` marker, and marks every earlier entry `before_clear`, which no later first prompt holds |
| `GET /v1/conversations/{id}/entries` | | A page of its entries |
| `POST /v1/conversations/{id}/messages` | `{"id", "body", "worker"}` | 201 with `conversation`, `entry`, `message`, and `stopped`: the member's message to the assistant, which opens a stopped conversation first and is delivered once its session runs |
| `GET /v1/conversation-targets` | | What the member can talk to now |
| `GET /v1/lanes/{id}/stream` | | A page of a lane's stream |
| `GET /v1/orchestrators/{worker}/stream` | | A page of a worker's orchestrator's stream in this project |

A conversation holds `id`, `member_id`, `name`, `worker_id`, `worker_name`, `desired_state` (`running` or `stopped`), `actual_state`, `run_lane`, `current_generation`, `stopped_reason`, `created_at`, `last_active_at`, and `open_cards`, the cards it prepared that wait.

| Actual state | Means |
|--------------|-------|
| `starting` | Its session was asked for, and its worker has not reported it running |
| `running` | Its session waits for the member |
| `working` | A turn is running |
| `stopping` | Its session was asked to stop, and its worker has not reported it stopped |
| `stopped` | It has no session |
| `failed` | Its session could not start, which `stopped_reason` names |

At most one of a member's conversations runs in a project: creating, opening, or messaging one stops the running one, with a `stopped` marker. A new conversation is called `New conversation` until the member's first message, which names it with its first 60 characters, cut at a word, unless the member named it. A conversation stops when it sits idle past the assistant policy's `idle_stop`, with an `idle_stopped` marker, and sooner, past its `warm_idle_stop`, while no member message has reached it since its session started or was cleared; closing a view or switching target stops nothing. Reading a conversation, its entries, the list, or the conversation targets starts no session: the history comes from the ledger. Another member's conversation answers 404 `not_found`.

A start runs on a worker that is connected, holds a checkout of every active repository of the project, and runs headless sessions. A start that names a worker takes that one. With none named, it takes the conversation's last worker while it can, else the only one that can; with several that can it answers 400 `invalid` ("Choose a worker: atlas and basalt can run the assistant."), and with none it answers 409 `no_assistant_worker`, naming each worker's reason.

These refusals are the conversation surface's own:

| Status | Code | When |
|--------|------|------|
| 409 | `no_assistant_worker` | No worker can run the project's assistant |
| 409 | `target_not_running` | A compaction or a clear of a conversation whose session is not running, or a message to an orchestrator that is not running |

A conversation's entries, a lane's stream, and an orchestrator's stream share one entry: `id`, `at`, `kind` (`member`, `reply`, `event`, `action`, `card`, `request`, `question`, or `marker`), `from` (`kind`, `id`, `label`, and a delegate's `delegate_kind`), `body`, `message` (a member's message, with its delivery state), `event` (with `actor_for` and `actor_label`), `action`, `approval` (the current approval of a `card` or a `request`), `question` (the current question, its batch, and the `channel` a member's answer came through), `marker`, `assent`, and `before_clear`. A page is `{"entries", "next", "as_of"}`: oldest first within the page, the newest page first. `before` takes a page's `next`, and `limit` is 1 to 500, 100 by default.

- A conversation's entries hold the member's messages, the assistant's replies, the operations it ran as `action` entries, a `card` entry in place of each card it prepared, holding the card as it stands now, and its markers (`started`, `stopped`, `compacted`, `cleared`, `idle_stopped`). A member's message that reads as a yes while a card waits carries `"assent": true`, and is still only a message.
- A lane's stream, for a headless lane or a planning session, holds the messages members and their delegates, the assistant among them, sent it, with their delivery state and the sender a `member` entry's `from` names, its replies, the events about the lane or its work order, its approval requests, and the questions it asked or is attached to.
- An orchestrator's stream holds this project's events its orchestrator caused, the member's messages to it with its replies, and the recovery requests it raised.

A request, a question, or a card resolved anywhere stays at its place in a stream, with its outcome.

`GET /v1/conversation-targets` answers `assistant` (the member's newest conversation or null, the `workers`, `idle_stop_ms`, and `warm_idle_stop_ms`), `orchestrators` (each worker that holds a checkout of an active repository of the project, with its name, state, generation, and the projects it serves), `planning` (the lanes of planning and adopt work that have not ended), and `lanes` (every other lane that has not ended), each lane with its work order and title, worker, generation, run mode, and state.

`POST /v1/projects/{project}/messages` takes `"to": {"kind": "orchestrator", "worker": "..."}` beside a lane's `"to": {"lane": "..."}`; exactly the field of its kind is set. A message to an orchestrator answers 409 `target_not_running` while that orchestrator is not running, and 400 `invalid` when its worker serves no repository of the project. Only a member sends one: a delegate's call, the assistant's included, answers 403 `member_only` and records no card, because an orchestrator holds tools such as `approval_decide` and `lane_start`, and a body that does not say who the message is for takes that class too. An orchestrator's own `reply` is not a message and stays. It reaches the orchestrator as one item of its next wake, with its age, moves to `at_worker` when the worker takes it, `handed_off` when the wake reaches a turn, and `replied` when the orchestrator replies, and it expires on the message window like any other. A message to the assistant goes through its conversation, and `"kind": "assistant"` here answers 400 `invalid`. A message never decides anything: a card or a request stays open whatever a message says.

`POST /v1/projects/{project}/approvals/{id}/decide` and `POST /v1/projects/{project}/questions/{id}/answer` take an optional `place`, `needs_you` or `conversation`. On the web listener, `"place": "conversation"` records the channel `conversation`; otherwise the channel is `web` or `cli`. A member's decision of an approval request records its channel as a card's does, and an answer records it in its decision's event.

The assistant policy is a versioned policy of kind `assistant`, the installation's, published with `POST /v1/projects/{project}/policies` as a member and listed with `GET /v1/projects/{project}/policies?kind=assistant`. `idle_stop` is a duration from one minute to 24 hours, and the built-in version stops an idle session after 15 minutes. `warm_idle_stop`, from one minute to 24 hours and never longer than `idle_stop`, stops a session that no member message has reached since it started, such as one a warm start opened; it is 2 minutes when a version names none. Once a message reaches the session, `idle_stop` holds:

```toml
[assistant]
version = 2
idle_stop = "30m"
warm_idle_stop = "2m"
```

## Searching a project's records

`GET /v1/projects/{project}/views/search` is the read model the web UI's command palette reads. It lists the project's work orders, lanes, plans, contracts, requirements, decisions, questions, planning documents, and repositories, and the calling member's conversations by name as kind `conversation`, each with its kind, ID, title, and state, and it changes nothing. `q` keeps the records whose ID or title holds every word, in any order and any case: an exact ID first, then an ID that begins with the words, then words that each begin a word of the title, then the rest. `category` names one kind. `limit` caps the list, 20 by default when `q` is set, and `more` says the cap cut it. With no `q`, every record is listed.

## Reading a plan by version, and a planning session's outline

`GET /v1/projects/{project}/plans/{id}` answers a plan with its goal, each contract's newest version, their work orders, and the request to approve it: the one that waits, or else the newest. `versions` lists every version of the plan, newest first, each with its digest and the ID and state of the request that stands for it, and none of those when nobody asked about that version. `?version=N` answers with the request of that version, whatever state it is in, and refuses a version the plan never had with 404 and a version that is no whole number from 1 with 400. A request is read by its own ID with `GET /v1/projects/{project}/approvals/{id}`, which is how a first version of a plan is read before the ledger holds the plan.

`GET /v1/projects/{project}/lanes/{id}/outline` reads the plan a planning lane has committed to its branch, at the branch's tip now, in the checkout of the machine the daemon runs on, and compares it with the commit the lane's branch began at. It lists the planning documents added or changed since then, each with its path, kind, and title; the milestones with their priorities; the contracts with their criterion and work order counts; the draft decisions; the work order's questions nobody has answered, with the blocking ones marked; and the request to approve the plan that stands for the tip, or, when none does while a blocking question is open, why the version is not submitted. A branch it cannot read, because the lane runs on another machine or has no commit yet, answers `unread` with the reason and nothing else. A lane that is not a planning lane's is refused with 400. `ropeshift lane outline <lane>` prints the same.

In the documents view, each work order that carries a traced criterion has a `verdict` for that criterion: the latest verdict that speaks for the work order's current attempt and the commit it left, with its outcome and the commit it read. A criterion no review has judged has none.

## The plane protocol

One worker per machine serves every active project. Version 2 of the plane protocol names the project on every lease, claim, desired-state item, and uploaded event, and on every call a lane or an orchestrator makes through its worker. Desired state lists each project the worker serves with its fleet mode, and the revision of each beside the installation's own, and a worker reads it again when any of them moves. An archived project takes no claim and is offered nothing.

A worker claims work on its own route, `POST /v1/workers/{id}/claims`, with the work order and its project in the body.

`GET /v1/workers/{id}/caps` reads a worker's lane caps: `max_lanes`, null where it follows the recommendation and 0 for no cap, `max_lanes_per_sign_in`, 0 for no cap, `effective_max_lanes`, the cap in force, and the `recommendation` the worker last reported, with the numbers behind it and its `reason`. `PUT /v1/workers/{id}/caps` takes `{"max_lanes": N, "max_lanes_per_sign_in": N}` and needs a member's session. A value is any number from 0, with no upper bound, and a `max_lanes` that is null or left out follows the recommendation. The caps live in the user's profile of the worker's machine, which the change edits in place, so the file and the API are one setting. The answer is the caps as they now stand, and carries a `warning`, with the `recommended` value and its `reason`, when `max_lanes` is above the recommendation; the value is saved all the same. A worker that does not run on the daemon's own machine answers 400 `invalid`, because its caps are in the user's profile on that machine. Setting them is member-only: a delegate's call answers 403 `member_only`.

A heartbeat carries a `report` when the worker has measured since its last answered heartbeat: its machine, each session's process tree, the lane cap it recommends with the numbers behind it, the caps in force, and the warnings past its thresholds. The control plane keeps the last report on the worker's record, with no event, as `report` and `reported_at`, dated by its own clock less the report's `age_ms`. A report it cannot keep fails nothing else: the heartbeat still renews and fences the worker's leases, and its answer carries `report_error`, so the worker sends the report again. A report is the worker's word, so the control plane keeps at most 512 sessions, 64 warnings, and 64 volumes of it, each text cut at 1024 bytes, and its interval between a second and a day; a report still larger than 256 KiB is not kept. A measure the machine cannot provide is absent from the report, never zero. `GET /v1/workers` lists each worker with its last report, and `as_of`, the time of the read. Each worker of a project's `shop-floor` view carries the same, and `report_freshness` says whether the report is `observed`, from within three of its intervals or heartbeats, whichever is longer, or `last_reported`. A claim is held to the per-worker cap the last report states, so a change to the cap takes hold without a new session, and to the capacity the session opened with before the first report.

Version 3 adds the assistant's sessions. Desired state's `assistants` lists each running conversation placed on the worker, with its project, conversation, member, run lane, the conversation's generation the run began at, the assistant policy's idle stop, whether it begins with an empty context, the compactions asked of it, and its messages. Each start names a run lane of its own, and the worker runs every start of a conversation in one directory of its own, so a start resumes the vendor's session where it last ran. A resume the vendor refuses starts a fresh generation whose first prompt holds the conversation's newest entries, and the refused session is not tried again. Desired state's `forget` lists each deleted conversation the worker ran, with its project, conversation, and run lanes and no text; once none of those runs goes on, the worker removes the messages to it and its runs' records from its journal and the conversation's directory, and uploads `assistant_forgotten` with the conversation, after which `forget` names it no more. The worker asks `POST /v1/workers/{id}/assistant-credentials`, with the project, conversation, run lane, and the run's generation, for each generation it starts, and the answer holds the credential's token once, with the project's facts and the conversation's newest 50 entries since its last clear for the session's first prompt. It records each reply with `POST /v1/workers/{id}/assistant-entries`, under the vendor message's ID, so a retry records it once. Uploads from an assistant's run carry no work order, and its run's states move its conversation. A worker on version 2 is served, and is told of no assistant.

A worker that speaks version 1, which names no project and serves only the first project, is no longer served. Opening a session at version 1 answers 426 with the code `protocol`, and the message says the version is no longer served and the worker must be upgraded; the error also carries the worker's version and the oldest and newest the control plane accepts. A worker whose session was opened at version 1 before the control plane was upgraded is refused the same way on its next call, until it opens a session at version 2.
