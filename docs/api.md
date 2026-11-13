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

`GET /v1/audit` reads the installation's log, and `GET /v1/projects/{project}/audit` a project's, newest first: each event with its revision, type, actor, subject, times, and its whole record. `actor` keeps one actor kind, `kind` the event types that start with its text, `subject` one subject kind, `limit` sets the page from 1 to 200 (50 by default), and `before` starts the page below a revision, which the answer's `next` names, zero when no older event is left. A filtered read looks at 5,000 events at most and answers `"scanned": true` with `next` where it stopped. `GET /v1/members` lists each member that holds a live session, with when each session began and ends, and how many members can sign in; no answer carries a session's token.

A change to a record every project shares, such as a member's sign-in, a worker's registration or autonomy, or an orchestrator, records its event in the installation's log, and no project's log holds it.

## The combined Needs you

`GET /v1/needs-you` lists what waits on a member in every active project, the longest wait first. Each item is the item a project's `needs-you` view holds, with a `project` that names its project's ID, name, and display name. `projects` lists each active project with the revision its ledger stood at and how many of its items wait. A member acts on an item through its own project's routes.

## Searching a project's records

`GET /v1/projects/{project}/views/search` is the read model the web UI's command palette reads. It lists the project's work orders, lanes, plans, contracts, requirements, decisions, questions, planning documents, and repositories, each with its kind, ID, title, and state, and it changes nothing. `q` keeps the records whose ID or title holds every word, in any order and any case: an exact ID first, then an ID that begins with the words, then words that each begin a word of the title, then the rest. `category` names one kind. `limit` caps the list, 20 by default when `q` is set, and `more` says the cap cut it. With no `q`, every record is listed.

## The plane protocol

One worker per machine serves every active project. Version 2 of the plane protocol names the project on every lease, claim, desired-state item, and uploaded event, and on every call a lane or an orchestrator makes through its worker. Desired state lists each project the worker serves with its fleet mode, and the revision of each beside the installation's own, and a worker reads it again when any of them moves. An archived project takes no claim and is offered nothing.

A worker claims work on its own route, `POST /v1/workers/{id}/claims`, with the work order and its project in the body.

`GET /v1/workers/{id}/caps` reads a worker's lane caps: `max_lanes`, null where it follows the recommendation and 0 for no cap, `max_lanes_per_sign_in`, 0 for no cap, `effective_max_lanes`, the cap in force, and the `recommendation` the worker last reported, with the numbers behind it and its `reason`. `PUT /v1/workers/{id}/caps` takes `{"max_lanes": N, "max_lanes_per_sign_in": N}` and needs a member's session. A value is any number from 0, with no upper bound, and a `max_lanes` that is null or left out follows the recommendation. The caps live in the user's profile of the worker's machine, which the change edits in place, so the file and the API are one setting. The answer is the caps as they now stand, and carries a `warning`, with the `recommended` value and its `reason`, when `max_lanes` is above the recommendation; the value is saved all the same. A worker that does not run on the daemon's own machine answers 400 `invalid`, because its caps are in the user's profile on that machine.

A heartbeat carries a `report` when the worker has measured since its last answered heartbeat: its machine, each session's process tree, the lane cap it recommends with the numbers behind it, the caps in force, and the warnings past its thresholds. The control plane keeps the last report on the worker's record, with no event, as `report` and `reported_at`, dated by its own clock less the report's `age_ms`. A measure the machine cannot provide is absent from the report, never zero. `GET /v1/workers` lists each worker with its last report, and `as_of`, the time of the read. Each worker of a project's `shop-floor` view carries the same, and `report_freshness` says whether the report is `observed`, from within three of its intervals or heartbeats, whichever is longer, or `last_reported`. A claim is held to the per-worker cap the last report states, so a change to the cap takes hold without a new session, and to the capacity the session opened with before the first report.

A worker that speaks version 1, which names no project and serves only the first project, is no longer served. Opening a session at version 1 answers 426 with the code `protocol`, and the message says the version is no longer served and the worker must be upgraded; the error also carries the worker's version and the oldest and newest the control plane accepts. A worker whose session was opened at version 1 before the control plane was upgraded is refused the same way on its next call, until it opens a session at version 2.
