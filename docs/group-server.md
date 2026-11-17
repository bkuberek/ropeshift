# Run a group server

> **Preview.** Group mode starts only when `ROPESHIFT_GROUP_MODE=preview` is in the environment of every command below. Without it, each one exits with "group mode is a preview in this release; set ROPESHIFT_GROUP_MODE=preview to run it", and `ropeshiftd help` does not list them. A preview carries no compatibility promise: a flag, a stored record, or the join format may change between two releases, with no way to carry an installation made now across the change. Keep nothing in a preview server that you cannot recreate.

A group server is the control plane on its own, on Postgres: the API, the worker plane, and the web UI on one listener. Each machine that runs work runs a worker of its own, which dials the server, so no machine opens a port for it. Solo mode, where one daemon runs both, is unchanged and ignores the variable.

The listener serves plaintext on a loopback address, and TLS on any other: [choose a TLS mode](#choose-a-tls-mode) says where its certificate comes from. A worker joins over a loopback `http://` address in this version, so a worker and its server run on one machine.

## What a server does and does not do

- **It runs every control-plane tick solo mode runs**, such as leases, intake, wakes, approvals, delivery, forecasts, and acceptance, apart from the ones that act on a worker's machine: desktop notifications and the telemetry of a worker's lanes.
- **It reads no repository and shows no pane.** The server keeps no clone and has no terminal. A read that solo mode makes from a clone, such as a document, a lane's base, or a repository's registration, is refused with the code `repository_reads_unavailable`, and a view of a lane's terminal with `pane_relay_unavailable`. Each refusal names the read. Work that needs a repository therefore does not run on a preview server yet.
- **It holds no agent.** It never runs an agent CLI and holds no agent credential. Lanes run on workers.
- **Every request carries a credential.** A worker's credential opens the worker plane and nothing else, a join token opens the registration of one worker, a member's session as a bearer token in the `Ropeshift-Session` header opens the API, and the web UI's session cookie with its CSRF token opens the routes the web UI uses. Only the web UI's own files and the route that signs a member in need none. A bearer session never sets or changes the passphrase: the control socket sets it, and the web UI changes it with the passphrase in force. Wrong guesses at a passphrase or a join token are [limited per address](#limit-wrong-guesses).
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

   The connection string goes in the variable or in `--database`, not both, and the server never prints it: a string that does not parse is refused with a fixed message.

   A server that moves its state directory names its run directory too, because its control socket lives there. Without `ROPESHIFT_RUN_DIR` beside `ROPESHIFT_STATE_DIR`, the server exits with status 2 before it makes anything. That catches the usual mistake of moving only the state, and it is not a guarantee: a server whose `XDG_STATE_HOME` differs from a solo daemon's, with the same run directory, takes a different lock and the same control socket. What the server does guarantee is that it never binds over a socket something answers on. It checks just before it binds, so a daemon that starts in that instant is not covered. Run a preview server and a solo installation as separate users, or give each its own `ROPESHIFT_RUN_DIR`.

2. Start the server on its default address.

   ```sh
   ropeshiftd server
   ```

   The address comes from `--listen` or `ROPESHIFT_LISTEN`, and the default is `127.0.0.1:7400`. The listener serves plaintext on a loopback address, which no other machine reaches. The server migrates the database on the first start, makes a project named `default`, and prints where it serves:

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

## Choose a TLS mode

Plaintext crosses loopback and nothing else. A listener on a loopback address serves plaintext, unless you give it a certificate or a `--name`, which ask for TLS there too. A listener on any other address, such as `0.0.0.0:7400` or a LAN address, serves TLS, and you choose where its certificate comes from. A server off loopback that has neither a name for the certificate it makes nor a certificate of yours refuses to start.

| Your network | Certificate | What you set |
|--------------|-------------|--------------|
| A LAN or a tailnet, with no certificate you manage | One the server makes from its own certificate authority | `--name` |
| A certificate you already have, such as one from a public authority or from [`tailscale cert`](https://tailscale.com/kb/1153/enabling-https) | The files you supply | `--tls-cert` and `--tls-key` |

The listener speaks HTTP/1.1 over TLS 1.2 or later in both modes. The web UI's pages answer to loopback host names in this version, so a browser on another machine is refused with the code `foreign_host`. That is not what protects the listener: the `Host` header is the client's to choose, and a client on your network can send a loopback name. What protects it is that every route but the sign-in page and the route that signs in needs a credential, and that wrong guesses at a passphrase or a join token are counted and refused, as [limit wrong guesses](#limit-wrong-guesses) says.
### Use the server's own authority

1. Start the server on an address off loopback, and name every host name and address that members and workers reach it by. Give `--name` once for each name, or as a comma separated list. A container gives the list in `ROPESHIFT_SERVER_NAMES`.

   ```sh
   ropeshiftd server --listen 0.0.0.0:7400 --name ropeshift.lan --name 192.168.1.5
   ```

   A server off loopback with no name is refused before it opens the database. A name is a host name or an address, with no port and no scheme.

2. At its first start the server makes a certificate authority, valid for ten years, and its key in the `tls` directory of its state directory. It then prints where the authority's certificate is and the SHA-256 of that certificate, which a client that verifies the server pins:

   ```text
   ropeshiftd: server https://0.0.0.0:7400
   ropeshiftd: certificate authority /home/you/ropeshift-group/server/state/tls/ca.crt
   ropeshiftd: certificate authority sha256 <64 hexadecimal digits>
   ```

3. The server issues its own certificate from that authority for the names. The certificate lasts 90 days, and the server issues the next one when 30 days are left, with no restart. The authority, the certificate, and the keys stay where they are across a restart, and the keys are readable only by the server's user. A change of `--name` issues a new certificate from the same authority.

4. A browser warns about the certificate until you trust `ca.crt` on the device. The authority's key stays on the server and signs only the server's own certificate, but a device that trusts the authority accepts anything that key signs, so trust it on devices you own and no others.

The authority never replaces itself. A server whose authority has expired, or whose `ca.key` is missing, exits with an error that names the file. To replace the authority, stop the server, remove `ca.crt` and `ca.key` from the `tls` directory, and start it again. Every client that pinned the old fingerprint must pin the new one.

### Supply a certificate

1. Give the server the files of a certificate and its key. A container gives them in `ROPESHIFT_TLS_CERT` and `ROPESHIFT_TLS_KEY`.

   ```sh
   ropeshiftd server --listen 0.0.0.0:7400 --tls-cert /etc/ropeshift/tls.crt --tls-key /etc/ropeshift/tls.key
   ```

   Both files are PEM: the certificate, with its chain after it, and the key. A pair that does not load, or whose key does not belong to its certificate, is refused at start. The certificate holds its own names, so `--name` is refused beside it. A supplied certificate is served on a loopback address too.

2. The server reads the files again when the modification time or the size of either changes, checked as each connection arrives, so a renewal needs no restart. A file that is a link is followed. A replacement that does not load, such as a certificate written before its key, leaves the pair in use served, and the server's standard error says so once. The next change to either file is read again.

3. The certificate has to chain to roots that the clients trust, and cover the names they reach the server by.

## Limit wrong guesses

The server counts every wrong guess at a secret by the address the connection came from: a wrong passphrase at the sign-in page, in a change of the passphrase, or in a call that signs in with a session, and a wrong join token at a worker's registration. After 10 wrong guesses within 15 minutes, the address is refused for 15 minutes. A refusal is HTTP 429 with the code `too_many_attempts` and a `Retry-After` header that says how many seconds remain, and it covers the right passphrase too. A right passphrase clears the address's count. A join token that works clears nothing, and a worker that holds its credential is not refused, because a credential is no guess.

- `--signin-max-failures` and `--signin-lockout` set the two numbers, one for how many wrong guesses and one for how long they count and how long a refusal lasts. Both must be above zero.
- The address is the one the TCP connection came from. Clients that reach the server through one machine, such as a NAT or a proxy, share that machine's address and count together, and no header a client sends changes the address.
- Guesses sent at once count as wrong until each is judged, so a client cannot get more guesses than the limit by sending them together.
- The counts live in the server's memory and a restart clears them. The server tracks at most 10,000 addresses at a time, and lets go of the longest idle one that is not refused when it needs room.
- The control socket is no client address and is never limited.
- The sign-in page checks one guess at a time, so each guess costs the server a password hash, and a limit per address bounds only what one address costs. It does not stop many addresses from guessing at once.

## Settings

| Setting | Flag | Variable | Default |
|---------|------|----------|---------|
| Postgres connection string, which holds a password: give it in the variable | `--database` | `ROPESHIFT_DATABASE` | None: required |
| The one listener's address | `--listen` | `ROPESHIFT_LISTEN` | `127.0.0.1:7400` |
| The host names and addresses of the certificate the server makes, repeated or comma separated | `--name` | `ROPESHIFT_SERVER_NAMES` | None: required off loopback with no supplied certificate |
| The file of a certificate to serve, with its key | `--tls-cert` | `ROPESHIFT_TLS_CERT` | None |
| The file of that certificate's key | `--tls-key` | `ROPESHIFT_TLS_KEY` | None |
| Wrong guesses one client address may make within the lockout | `--signin-max-failures` | `ROPESHIFT_SIGNIN_MAX_FAILURES` | `10` |
| How long wrong guesses count, and how long a refused address stays refused | `--signin-lockout` | `ROPESHIFT_SIGNIN_LOCKOUT` | `15m` |
| The server's state directory | None | `ROPESHIFT_STATE_DIR` | [By platform](cli.md#the-daemon) |
| The server's run directory, which holds its control socket | None | `ROPESHIFT_RUN_DIR` | [By platform](cli.md#the-daemon) |

`ropeshiftd help server` lists the tick intervals too. The adapter's warnings, such as a schema the server dropped at its start because no project named it, go to the server's standard error.
