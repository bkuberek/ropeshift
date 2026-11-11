# API

Every surface of ropeshift is a client of one versioned HTTP API: the command line, `ropeshift top`, the web UI, and the worker. On the local machine the API listens on the control plane's socket, which only its owner can reach, and the web UI reaches a closed list of its routes through the web listener.

## Routes

An installation holds one or more projects, and the API names the project in the path of every route on a project's records:

| Routes | Under | What they reach |
|--------|-------|-----------------|
| The installation's | `/v1/` | The registry of projects, members and their sessions, the passphrase, workers with their autonomy, run windows, and orchestrators, the worker's telemetry, the health of the control plane, the installation's backup, the combined Needs you, and the protocol a worker speaks with its credential |
| A project's | `/v1/projects/{project}/` | Everything else: work orders, lanes, plans, requirements, documents, questions, decisions, policies, approvals, effects, notices, repositories, the fleet's mode and caps, the read models, the event stream, measures, and backups |

`{project}` is the project's name, as `ropeshift project list` prints it. A name the installation does not hold answers 404 with the code `no_project`, and a record of another project answers 404 with `not_found`, because a project's routes read only that project's ledger. A repository another project holds answers 409 with `repository_held`, and the message names that project.

`GET /v1/projects` lists the projects, each with its state, the identity of each repository it holds, and how many things wait on a member in it, and `GET /v1/projects/{project}` shows one with its database and its repository records. A member's session takes a project through its lifecycle, and each step records an event in the installation's log:

| Route | Body | Step |
|-------|------|------|
| `POST /v1/projects` | `{"name": "...", "display_name": "..."}` | Create |
| `POST /v1/projects/{project}/rename` | `{"name": "..."}` | Rename |
| `POST /v1/projects/{project}/archive` | `{}` | Archive, once every lane ended |
| `POST /v1/projects/{project}/restore` | `{}` | Restore |
| `POST /v1/projects/{project}/remove` | `{"confirm": "...", "backup": "..."}` or `{"confirm": "...", "no_backup": true}` | Remove an archived project |
| `POST /v1/projects/import` | `{"path": "...", "name": "..."}` | Import a project's backup, keeping its ID |

An archived project answers every change with 409 `project_archived`, a backup excepted, and a step its state refuses answers 409 `project_state`.

A change to a record every project shares, such as a member's sign-in, a worker's registration or autonomy, or an orchestrator, records its event in the installation's log, and no project's log holds it.

## The combined Needs you

`GET /v1/needs-you` lists what waits on a member in every active project, the longest wait first. Each item is the item a project's `needs-you` view holds, with a `project` that names its project's ID, name, and display name. `projects` lists each active project with the revision its ledger stood at and how many of its items wait. A member acts on an item through its own project's routes.

## The plane protocol

One worker per machine serves every active project. Version 2 of the plane protocol names the project on every lease, claim, desired-state item, and uploaded event, and on every call a lane or an orchestrator makes through its worker. Desired state lists each project the worker serves with its fleet mode, and the revision of each beside the installation's own, and a worker reads it again when any of them moves. An archived project takes no claim and is offered nothing.

A worker claims work on its own route, `POST /v1/workers/{id}/claims`, with the work order and its project in the body.

A worker one protocol version behind names no project and serves only the default project, the one the routes with no project serve. Once the installation removes that project, such a worker's session is refused with the code `protocol`.

`POST /v1/projects/{project}/backup` copies one project's ledger, and `POST /v1/installation/backup` copies the installation database. Each takes `{"path": "..."}`, an absolute path on the daemon's machine where no file exists, and needs a member's session.

## Routes with no project

Until the release after the one that added projects, the daemon still answers each project route at its old path with no project, such as `/v1/work-orders`, for the installation's first project, `default` on an installation that upgraded from one ledger. Each answer carries the header `Deprecation: true`. These routes keep a worker one protocol version behind and a script written before projects working through one upgrade. Once the installation removes the first project, they answer 404 with the code `no_project`. No surface of ropeshift calls them, and the release after removes them.
