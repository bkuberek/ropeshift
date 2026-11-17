# Run a group server

> **Preview.** Group mode starts only when `ROPESHIFT_GROUP_MODE=preview` is in the environment of every command below. Without it, each one exits with "group mode is a preview in this release; set ROPESHIFT_GROUP_MODE=preview to run it", and `ropeshiftd help` does not list them. A preview carries no compatibility promise: a flag, a stored record, or the join format may change between two releases, with no way to carry an installation made now across the change. Keep nothing in a preview server that you cannot recreate.

A group server is the control plane on its own, on Postgres: the API, the worker plane, and the web UI on one listener. Each machine that runs work runs a worker of its own, which dials the server, so no machine opens a port for it. Solo mode, where one daemon runs both, is unchanged and ignores the variable.

In this version the server and its workers run on one machine, and the listener binds a loopback address. A server on another address is refused: `ropeshiftd server` exits with "a server off loopback needs TLS, which this version does not have".

## What a server does and does not do

- **It runs every control-plane tick solo mode runs**, such as leases, intake, wakes, approvals, delivery, forecasts, and acceptance, apart from the ones that act on a worker's machine: desktop notifications and the telemetry of a worker's lanes.
- **It reads no repository and shows no pane.** The server keeps no clone and has no terminal. A read that solo mode makes from a clone, such as a document, a lane's base, or a repository's registration, is refused with the code `repository_reads_unavailable`, and a view of a lane's terminal with `pane_relay_unavailable`. Each refusal names the read. Work that needs a repository therefore does not run on a preview server yet.
- **It holds no agent.** It never runs an agent CLI and holds no agent credential. Lanes run on workers.
- **Every request carries a credential.** A worker's credential opens the worker plane and nothing else, a join token opens the registration of one worker, a member's session as a bearer token in the `Ropeshift-Session` header opens the API, and the web UI's session cookie with its CSRF token opens the routes the web UI uses. Only the web UI's own files and the route that signs a member in need none. A bearer session never sets or changes the passphrase: the control socket sets it, and the web UI changes it with the passphrase in force.
- **It keeps secrets out of the process list.** The database's connection string and a worker's join token reach the commands through `ROPESHIFT_DATABASE` and `ROPESHIFT_JOIN`. An argument is visible to every process on the machine, so the help never shows a value the environment holds.

## Before you start

You need the `ropeshift` and `ropeshiftd` commands ([getting started](getting-started.md)), a Postgres server you can reach, and a terminal for each process. The steps use directories of their own, so a preview never shares state with a solo installation on the same machine.

## Prepare Postgres

1. Make a database and a role that owns it. The server makes its own schemas inside the database: one for the installation and one for each project.

   ```sh
   createuser --pwprompt ropeshift_group
   createdb --owner ropeshift_group ropeshift_group
   ```

2. Write down the connection string. It is a `postgres://` URL, or a key and value string such as `host=localhost dbname=ropeshift_group user=ropeshift_group`, and it holds the role's password, so treat it as a secret.

## Start the server

1. In a terminal, give the server directories of its own, the connection string, and the preview. The connection string holds a password, so it goes in the environment and not in an argument.

   ```sh
   export ROPESHIFT_GROUP_MODE=preview
   export ROPESHIFT_STATE_DIR="$HOME/ropeshift-group/server/state"
   export ROPESHIFT_RUN_DIR="$HOME/ropeshift-group/server/run"
   export ROPESHIFT_DATABASE='postgres://ropeshift_group:<password>@localhost/ropeshift_group'
   ```

   A server that moves its state directory names its run directory too, because its control socket lives there. Without `ROPESHIFT_RUN_DIR` beside `ROPESHIFT_STATE_DIR`, the server exits with status 2 before it makes anything, so it can never bind the socket of an installation that is the machine's own. It also refuses to start when something already answers on its control socket.

2. Start the server on its default address.

   ```sh
   ropeshiftd server
   ```

   The address comes from `--listen` or `ROPESHIFT_LISTEN`, and the default is `127.0.0.1:7400`. The server migrates the database on the first start, makes a project named `default`, and prints where it serves:

   ```text
   ropeshiftd: serving /home/you/ropeshift-group/server/run/control.sock
   ropeshiftd: server http://127.0.0.1:7400
   ```

   The server holds its state directory, so a second server on the same directories exits with status 3. It runs until you stop it with Ctrl C.

## Set the passphrase

The first passphrase is set on the server's own machine, over its control socket, and never from a browser or over the network.

1. In a second terminal, use the same directories and set the passphrase.

   ```sh
   export ROPESHIFT_STATE_DIR="$HOME/ropeshift-group/server/state"
   export ROPESHIFT_RUN_DIR="$HOME/ropeshift-group/server/run"
   ropeshift passphrase set
   ```

## Sign in

1. Open `http://127.0.0.1:7400` in a browser on the same machine and sign in with the passphrase.
2. Check which mode the server runs in. A group server's health says `"mode": "group"`:

   ```sh
   ropeshift health --json
   ```

## Join a worker

A worker joins with a join token, which a member makes and which works once.

1. In the second terminal, make a join token.

   ```sh
   ropeshift worker token
   ```

2. In a third terminal, give the worker directories of its own, and join with the token. The token is a secret until the join spends it, so it goes in the environment and not in an argument. The worker keeps the server's address and its credential in its journal, which only its user can read. Joining prints the worker's ID and never the credential.

   ```sh
   export ROPESHIFT_GROUP_MODE=preview
   export ROPESHIFT_STATE_DIR="$HOME/ropeshift-group/worker/state"
   export ROPESHIFT_RUN_DIR="$HOME/ropeshift-group/worker/run"
   export ROPESHIFT_TELEMETRY=127.0.0.1:0
   export ROPESHIFT_JOIN='<join token>'
   ropeshiftd worker join --server http://127.0.0.1:7400 --name first-worker
   unset ROPESHIFT_JOIN
   ```

   The address is a plain `http://` URL of a loopback address in this version, with no user, path, or query, and the client follows no redirect from it. A worker registers once and joins one server: joining again exits with an error that names the server it joined, spends no token, and changes nothing, and a worker that must join another removes its state directory first. A token the server refuses leaves no credential and no server address behind.

3. Start the worker. It runs this machine's lanes, terminal, checkouts, drivers, hooks, and MCP on its own worker socket, and talks to the server over HTTP.

   ```sh
   ropeshiftd worker
   ```

4. In the second terminal, list the server's workers. The new worker appears with the drivers it found on its machine, and its heartbeats keep it observed.

   ```sh
   ropeshift worker list
   ```

   Stop the worker with Ctrl C, and the server shows it as last reported once its heartbeats stop.

## Settings

| Setting | Flag | Variable | Default |
|---------|------|----------|---------|
| Postgres connection string, which holds a password: give it in the variable | `--database` | `ROPESHIFT_DATABASE` | None: required |
| The one listener's address | `--listen` | `ROPESHIFT_LISTEN` | `127.0.0.1:7400` |
| The server's state directory | None | `ROPESHIFT_STATE_DIR` | [By platform](cli.md#the-daemon) |
| The server's run directory, which holds its control socket | None | `ROPESHIFT_RUN_DIR` | [By platform](cli.md#the-daemon) |

`ropeshiftd help server` lists the tick intervals too. The adapter's warnings, such as a schema the server dropped at its start because no project named it, go to the server's standard error.
