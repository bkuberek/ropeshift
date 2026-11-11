# API

Every surface of ropeshift is a client of one versioned HTTP API: the command line, `ropeshift top`, the web UI, and the worker. On the local machine the API listens on the control plane's socket, which only its owner can reach, and the web UI reaches a closed list of its routes through the web listener.

## Routes

An installation holds one or more projects, and the API names the project in the path of every route on a project's records:

| Routes | Under | What they reach |
|--------|-------|-----------------|
| The installation's | `/v1/` | The registry of projects, members and their sessions, the passphrase, workers with their autonomy, run windows, and orchestrators, the worker's telemetry, the health of the control plane, and the protocol a worker speaks with its credential |
| A project's | `/v1/projects/{project}/` | Everything else: work orders, lanes, plans, requirements, documents, questions, decisions, policies, approvals, effects, notices, repositories, the fleet's mode and caps, the read models, the event stream, measures, and backups |

`{project}` is the project's name, as `ropeshift project list` prints it. A name the installation does not hold answers 404 with the code `no_project`, and a record of another project answers 404 with `not_found`, because a project's routes read only that project's ledger.

`GET /v1/projects` lists the projects, each with its state and the identity of each repository it holds, and `GET /v1/projects/{project}` shows one with its database and its repository records.

A worker claims work on its own route, `POST /v1/workers/{id}/claims`, with the work order in the body.

## Routes with no project

Until the release after the one that added projects, the daemon still answers each project route at its old path with no project, such as `/v1/work-orders`, for the installation's first project, `default` on an installation that upgraded from one ledger. Each answer carries the header `Deprecation: true`. These routes keep a worker one protocol version behind and a script written before projects working through one upgrade. No surface of ropeshift calls them, and the release after removes them.
