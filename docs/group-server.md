# Run a group server

> **Preview.** Group mode starts only when `ROPESHIFT_GROUP_MODE=preview` is in the environment of every command below. Without it, each one exits with "group mode is a preview in this release; set ROPESHIFT_GROUP_MODE=preview to run it", and `ropeshiftd help` does not list them. A preview carries no compatibility promise: a flag, a stored record, or the join format may change between two releases, with no way to carry an installation made now across the change. Keep nothing in a preview server that you cannot recreate.

A group server is the control plane on its own, on Postgres: the API, the worker plane, and the web UI on one listener. Each machine that runs work runs a worker of its own, which dials the server, so no machine opens a port for it. Solo mode, where one daemon runs both, is unchanged and ignores the variable.

The listener serves plaintext on a loopback address, and TLS on any other, or plaintext to a reverse proxy that terminates TLS: [choose a TLS mode](#choose-a-tls-mode) says which, and where a certificate comes from. A worker joins with a join string, which carries the URL it reaches the server at and what it trusts there, so a worker runs on any machine that reaches the server: [join a worker](#join-a-worker) says how.

## What a server does and does not do

- **It runs every control-plane tick solo mode runs**, such as leases, intake, wakes, approvals, delivery, forecasts, and acceptance, apart from the ones that act on a worker's machine: desktop notifications and the telemetry of a worker's lanes.
- **It reads no repository and shows no pane.** The server keeps no clone and has no terminal. A read that solo mode makes from a clone, such as a document, a lane's base, or a repository's registration, is refused with the code `repository_reads_unavailable`, and a view of a lane's terminal with `pane_relay_unavailable`. Each refusal names the read. Work that needs a repository therefore does not run on a preview server yet.
- **It holds no agent.** It never runs an agent CLI and holds no agent credential. Lanes run on workers.
- **Every request carries a credential.** A worker's credential opens the worker plane and nothing else, a join token opens the registration of one worker, a member's session as a bearer token in the `Ropeshift-Session` header opens the API, and the web UI's session cookie with its CSRF token opens the routes the web UI uses. Only the web UI's own files and the route that signs a member in need none. A bearer session never sets or changes the passphrase: the control socket sets it, and the web UI changes it with the passphrase in force. Wrong guesses at a passphrase or a join token are [limited per address](#limit-wrong-guesses).
- **It keeps secrets out of the process list and its output.** The database's connection string and a worker's join string reach the commands through `ROPESHIFT_DATABASE` and `ROPESHIFT_JOIN`. An argument is visible to every process on the machine, so the help never shows a value the environment holds. A join token is a credential, and no log, error, or help shows one or the join string that holds it. The fingerprint of an authority is public, as the authority's certificate is.

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

A worker joins with a join string, which a member makes, which expires, and which joins one worker. A join string is one line that holds a format version, the URL the worker reaches the server at, the fingerprint of the server's certificate authority when the server is its own, and a join token. It is the only thing a worker needs, from any machine that reaches the server.

1. In the second terminal, make a join string. The command prints it once, and the server keeps only a digest of the token in it.

   ```sh
   ropeshift worker token
   ```

   Workers and resources in the web UI makes one too, with Create join token. Solo mode makes none: its one worker is the daemon's own, and a solo installation shows no join string.

   A join string expires 24 hours after it is made. `--expires-in` names another lifetime, up to 30 days, as a duration such as `72h`, and 30 days is `720h`.

   ```sh
   ropeshift worker token --expires-in 72h
   ```

   The command prints the string alone on its output, so a pipe carries nothing else, and says on its error stream when the token expires and its ID. With `--json` it prints all of that, and the token and the string with it. A member revokes a token that no worker used with `ropeshift worker token-revoke <id>`, or with Revoke join token in Workers and resources, which asks for the ID. A token that expired or was revoked is refused with `join_token_expired` or `join_token_revoked`, and a wrong secret for the same token is a bad token, so only whoever holds a token learns that it ended. Each counts as a wrong guess for the address that sent it, like any wrong token ([limit wrong guesses](#limit-wrong-guesses)). A token a worker used cannot be revoked, because that worker holds a credential from it.

   The record of a join token names the member who made it. The worker that registers with it has no owner of its own yet: nothing records one on the worker.

   A join string is a credential, and it is not spent when a worker joins with it. Until a worker joins, whoever holds the string can join a worker with it. After a worker joined, whoever holds the string and that worker's ID can ask for the worker's credential again, and the server then issues a new one and ends the old, until the worker opens its first session or the token expires, whichever is first. That window is there so a worker whose first answer was lost can join again, and it is short, because a worker opens its first session as it starts. After it the server refuses the used token as it refuses any spent token. Give a string only to the machine that is to join, and make another for the next machine.

2. In a third terminal, give the worker directories of its own, and join with the string. The string is a secret until the join spends its token, so it goes in the environment and not in an argument. The worker keeps the server's address, the authority it pinned, and its own credential in its journal, which only its user can read. Joining prints the worker's ID and the server's URL, and never the credential, the join string, or the token in it.

   ```sh
   export ROPESHIFT_GROUP_MODE=preview
   export ROPESHIFT_STATE_DIR="$HOME/ropeshift-group/worker/state"
   export ROPESHIFT_RUN_DIR="$HOME/ropeshift-group/worker/run"
   export ROPESHIFT_TELEMETRY=127.0.0.1:0
   export ROPESHIFT_JOIN='<join string>'
   ropeshiftd worker join --name first-worker
   unset ROPESHIFT_JOIN
   ```

   A join string given as the one argument works too, and giving it both ways is refused. A string that is not a join string, or is of another version, is refused with a sentence that says which and never repeats the string.

   The worker trusts the server in one of two ways, and refuses a server that fails the one its string asks for, before the join token leaves the machine:

   - **A string with a fingerprint pins the server's own authority.** The worker trusts the authority whose certificate has that fingerprint, among those the server sends, and no root of the system's, and checks that this authority signed the server's certificate, for the host name in the URL and for the time. A server that does not hold that authority is refused with "does not chain to the certificate authority this worker pinned", whatever name it presents.
   - **A string with no fingerprint verifies through the system's roots**, as a browser does, for a server behind a proxy or with a certificate you supplied. A server whose certificate no root vouches for is refused with "not trusted by this machine's system roots".

   An `http://` URL is a loopback address on the worker's own machine, which a server on loopback with no TLS makes. The client follows no redirect and reads no proxy from the environment.

   A worker registers once and joins one server: joining again exits with an error that names the server it joined, spends no token, and changes nothing, and a worker that must join another removes its state directory first. A token the server refuses leaves no credential and no server address behind. A join refused because the server was not trusted never sent its token, so the string is still good once the trust is right.

3. Start the worker. It runs this machine's lanes, terminal, checkouts, drivers, hooks, and MCP on its own worker socket, and talks to the server over the link it joined with, pinning the same authority at every start.

   ```sh
   ropeshiftd worker
   ```

4. In the second terminal, list the server's workers. The new worker appears with the drivers it found on its machine, and its heartbeats keep it observed.

   ```sh
   ropeshift worker list
   ```

   Stop the worker with Ctrl C, and the server shows it as last reported once its heartbeats stop.

### Join from another machine

The join string carries everything the worker needs, so a worker on another machine joins a server that serves TLS the same way. Start the server on an address the worker reaches, with a name for the certificate it makes, and make the string on the server:

```sh
ropeshiftd server --listen 0.0.0.0:7400 --name ropeshift.lan
ropeshift worker token
```

On the worker's machine, give the string to the join:

```sh
export ROPESHIFT_GROUP_MODE=preview
export ROPESHIFT_JOIN='<join string>'
ropeshiftd worker join --name build-box
```

The string pins the server's authority, so the worker trusts that server and no other, and the machine's own roots decide nothing. The member's browser still needs [the authority's certificate trusted on its device](#use-the-servers-own-authority) to open the web UI, and the worker does not.

### Choose the worker URL

The URL in a join string is where the server's workers reach it, and the server says it in `ropeshiftd: worker url`. `--worker-url` (`ROPESHIFT_WORKER_URL`) sets it. Without it, the server uses what it knows:

| Mode | The worker URL without `--worker-url` |
|------|----------------------------------------|
| A loopback listener with no TLS | `http://` and the address it bound |
| The server's own authority | `https://`, the first `--name`, and the port it bound |
| A reverse proxy | The `--public-url` |
| A supplied certificate | None: the server cannot know the names its certificate holds |

A server that has none makes no join token. `ropeshift worker token` and Create join token refuse with the code `no_worker_url` and say to set `--worker-url`, and the server warns at its start. `--worker-url` is an `https://` URL with a host and a port and no user, path, or query, or an `http://` URL of a loopback address. The server checks it against its transport before it opens the database:

- With its own authority, the URL's host is one of the `--name` values, because the worker verifies the certificate the server makes for those names and no others.
- With its own authority or a supplied certificate, the URL is `https://`, because the server serves TLS. On loopback with no TLS it is `http://`.
- Behind a reverse proxy it is any `https://` URL the proxy serves, the public URL or another name for workers, and the worker verifies it through the system's roots.

## Choose a TLS mode

Plaintext crosses loopback and nothing else. A listener on a loopback address serves plaintext, unless you give it a certificate or a `--name`, which ask for TLS there too. A listener on any other address, such as `0.0.0.0:7400` or a LAN address, serves TLS, and you choose where its certificate comes from. A server off loopback that has neither a name for the certificate it makes nor a certificate of yours refuses to start.

| Your network | Certificate | What you set |
|--------------|-------------|--------------|
| A LAN or a tailnet, with no certificate you manage | One the server makes from its own certificate authority | `--name` |
| A certificate you already have, such as one from a public authority or from [`tailscale cert`](https://tailscale.com/kb/1153/enabling-https) | The files you supply | `--tls-cert` and `--tls-key` |
| A reverse proxy you run, such as nginx, Caddy, or a container platform's proxy, that terminates TLS | The proxy's own | `--tls off`, `--trusted-proxies`, and `--public-url` |

The listener speaks HTTP/1.1 in every mode, over TLS 1.2 or later when it serves TLS. Where members' browsers reach the web UI depends on the mode. Behind a proxy, the web UI answers to the host of `--public-url`, as [run behind a reverse proxy](#run-behind-a-reverse-proxy) says. In the other modes its pages answer to loopback host names in this version, so a browser on another machine is refused with the code `foreign_host`. That is not what protects the listener: the `Host` header is the client's to choose, and a client on your network can send a loopback name. What protects it is that every route but the sign-in page and the route that signs in needs a credential, and that wrong guesses at a passphrase or a join token are counted and refused, as [limit wrong guesses](#limit-wrong-guesses) says.

### Use the server's own authority

1. Start the server on an address off loopback, and name every host name and address that members and workers reach it by. Give `--name` once for each name, or as a comma separated list. A container gives the list in `ROPESHIFT_SERVER_NAMES`.

   ```sh
   ropeshiftd server --listen 0.0.0.0:7400 --name ropeshift.lan --name 192.168.1.5
   ```

   A server off loopback with no name is refused before it opens the database. A name is a host name or an address, with no port and no scheme.

2. At its first start the server makes a certificate authority, valid for ten years, and its key in the `tls` directory of its state directory. It then prints where it serves, where the authority's certificate is, the SHA-256 of that certificate, and where its workers reach it:

   ```text
   ropeshiftd: server https://0.0.0.0:7400
   ropeshiftd: certificate authority /home/you/ropeshift-group/server/state/tls/ca.crt
   ropeshiftd: certificate authority sha256 <64 hexadecimal digits>
   ropeshiftd: worker url https://ropeshift.lan:7400
   ```

   The fingerprint is public, as the authority's certificate is. A worker pins it from the join string, so a person who is handed a join string by a route that might be tampered with compares the fingerprint in it with this one, which is the line a server prints at every start. `openssl x509 -noout -fingerprint -sha256 -in ca.crt` prints the same value, in capital letters with colons.

3. The server issues its own certificate from that authority for the names. The certificate lasts 90 days, and the server issues the next one when 30 days are left, with no restart. The authority, the certificate, and the keys stay where they are across a restart, and the keys are readable only by the server's user. A change of `--name` issues a new certificate from the same authority.

4. A browser warns about the certificate until you trust `ca.crt` on the device. The authority's key stays on the server and signs only the server's own certificate, but a device that trusts the authority accepts anything that key signs, so trust it on devices you own and no others.

The authority never replaces itself. A server whose authority has expired, or whose `ca.key` is missing, exits with an error that names the file. To replace the authority, stop the server, remove `ca.crt` and `ca.key` from the `tls` directory, and start it again. Every worker that pinned the old authority refuses the server until it joins again, from a state directory with nothing in it and a join string made after the replacement.

### Supply a certificate

1. Give the server the files of a certificate and its key. A container gives them in `ROPESHIFT_TLS_CERT` and `ROPESHIFT_TLS_KEY`.

   ```sh
   ropeshiftd server --listen 0.0.0.0:7400 --tls-cert /etc/ropeshift/tls.crt --tls-key /etc/ropeshift/tls.key
   ```

   Both files are PEM: the certificate, with its chain after it, and the key. A pair that does not load, or whose key does not belong to its certificate, is refused at start. The certificate holds its own names, so `--name` is refused beside it. A supplied certificate is served on a loopback address too.

2. The server reads the files again when the modification time or the size of either changes, checked as each connection arrives, so a renewal needs no restart. A file that is a link is followed. A replacement that does not load, such as a certificate written before its key, leaves the pair in use served, and the server's standard error says so once. The next change to either file is read again.

3. The certificate has to chain to roots that the clients trust, and cover the names they reach the server by.

## Run behind a reverse proxy

Use this when a proxy you run already terminates TLS for a public name, as the proxy of a container platform or an nginx or a Caddy does, and the server sits behind it on a network only the proxy reaches. The server serves plaintext to the proxy and serves the web UI at the proxy's name.

1. List the proxies. `--trusted-proxies` names the addresses and CIDR ranges of every proxy that connects to the server, repeated or comma separated, and `ROPESHIFT_TRUSTED_PROXIES` gives the same in a container. List the hop that records the client's address in `X-Forwarded-For`, and every hop between it and the server. Otherwise one proxy's address stands for every client behind it, and the limit on wrong guesses counts them as one. When the proxy and the server share a container network whose addresses change across restarts, name that network's range. A proxy on the server's own machine connects from `127.0.0.1` or from `::1`, so list the one it uses. A listener on every address of both families, which `[::]` and an empty host are, is reached at both, and a proxy on the machine may use either, so list both. A listener on every IPv4 address (`0.0.0.0`) or on `localhost` is reached at `127.0.0.1`, and one on `[::1]` at `::1`. A server whose listener loopback can reach, and that lists none of the loopback addresses it is reached at, starts with a warning that names the ones missing, because it would read nothing a proxy on its machine forwards: every member would sign in from one address and share one limit on wrong guesses. A proxy on another machine is right to leave loopback out, and the warning is then about a proxy you do not run.

2. Say where members reach the proxy. `--public-url` (`ROPESHIFT_PUBLIC_URL`) is the https origin of the proxy's public name, such as `https://ropeshift.example`, with a port only when it is not 443.

3. Start the server with TLS off.

   ```sh
   ropeshiftd server --listen 10.0.0.9:7400 --tls off --trusted-proxies 10.0.0.5/32 --public-url https://ropeshift.example
   ```

   The server here is at `10.0.0.9` and its proxy is at `10.0.0.5`. The server prints `ropeshiftd: server http://10.0.0.9:7400` and the public URL it was given. It refuses to start with TLS off off loopback unless both settings are given, and it refuses a public URL that is not an `https://` origin, or has a path, a user, or a query. The address `--listen` binds is the one the proxy reaches, so on a shared host bind the proxy's network and no wider.

   List only the proxy. Every address in `--trusted-proxies` may connect in plaintext and say who a client is, so a range such as `10.0.0.0/8` trusts every host in it, and any of them can send a request with an `X-Forwarded-For` of its choice. Name the proxy's own address, as `10.0.0.5/32` does, and name a range only when the proxy's address changes within it. A range is no wider than a `/8` of IPv4 addresses or a `/16` of IPv6 addresses, and a wider one is refused with the limit, because every address in it may say who a client is. A site's own block of IPv6 addresses is a `/48` or narrower, and the private `fd00::/8` is wider than the limit, so write the `/48` the network holds.

4. Set the proxy to terminate TLS, keep the `Host` header the browser sent, add the client's address to `X-Forwarded-For`, say `https` in `X-Forwarded-Proto`, and pass streams on as they are written. A page of the web UI holds a stream open for its live updates, so a proxy that buffers a response, or closes a connection that is silent for a minute, breaks it. The nginx and Caddy examples below do each.

### What the server takes from a proxy

- **Plaintext only from loopback and listed addresses.** A connection from any other address is closed as it is accepted, before the server reads a byte from it, so it never sees a credential or a body. The plaintext hop belongs on a network that only the proxy reaches.
- **Headers only from a listed peer.** The server reads `X-Forwarded-For` and `X-Forwarded-Proto` from a connection whose address is listed, and ignores them from every other. A connection from loopback that is not listed is served, and its headers are ignored: list `127.0.0.1` when the proxy runs on the server's own machine. `Forwarded` and `X-Real-IP` are never read.
- **The client is the rightmost address in `X-Forwarded-For` that is not listed.** A proxy adds the address it was reached from at the end of the list, so a client can write what it likes at the start and no further. The server reads every `X-Forwarded-For` line, splits them on commas, and takes an address with a port or in brackets as the address. It stops at an entry that is no address, such as a name or an empty entry, and takes the last listed hop it passed as the client. The limit on wrong guesses counts by this client.
- **A proxy that says its client used plain http is refused.** A request whose nearest proxy sent `X-Forwarded-Proto: http` is answered with 403 and the code `insecure_transport`, whatever route it is for, because a passphrase in it crossed a network in the clear. A request with no `X-Forwarded-Proto` is served: the header is not required. A listed address is also the address of whoever else connects from it, such as a worker or an operator on the server's own machine when `127.0.0.1` is listed for a proxy there, and none of them sends the header, so a server that required it would refuse them. What the header guards is a proxy that also accepts plain http, and a proxy set up as the examples below say sends the scheme it was reached by, which the server refuses when it is `http`. Set `X-Forwarded-Proto` in the proxy, and a proxy that sends it wrongly is the proxy's to fix.
- **Strict entries.** Each entry of `--trusted-proxies` is an address or a CIDR range, and nothing else: no name, no port, no zone. A range with bits set past its prefix, such as `10.0.0.5/24`, is refused with the range it means, and so is a range wider than a `/8` of IPv4 addresses or a `/16` of IPv6 addresses, among them `0.0.0.0/0` and `::/0`, which would trust every client to say who it is.

### What workers do behind a proxy

A worker reaches the server through the proxy at the worker URL, which is the public URL unless `--worker-url` names another name the proxy serves. A join string from a server behind a proxy carries no fingerprint, because the server holds no authority of its own, so the worker verifies the proxy's certificate through the system's roots, and the certificate has to chain to roots the worker's machine trusts. A worker is a client of the proxy like a browser is, and the limit on wrong guesses counts its address like a browser's.

### What the server does with the public URL

- The web UI answers to the host of the public URL, in any case, and to loopback host names. A request for another host is refused with `foreign_host`. The proxy therefore passes the `Host` header on as the browser sent it.
- A page served under the public host is that origin's alone: a request there whose `Origin` header is anything but the public origin is refused with `foreign_origin`. On loopback the origin is the loopback origin of the name used.
- The session and token cookies are `Secure` when they are set for a request to the public host and the public URL is `https`. A request on loopback, from the person at the server's own machine, sets cookies that are not `Secure`, because a browser sends no `Secure` cookie back over http.

### An nginx example

```nginx
server {
    listen 443 ssl;
    server_name ropeshift.example;
    # ssl_certificate and ssl_certificate_key name your certificate.

    location / {
        proxy_pass http://10.0.0.9:7400;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_read_timeout 1h;
    }
}
```

`proxy_set_header Host $http_host` keeps the `Host` header exactly as the browser sent it, port included, where nginx sends the upstream's own by default. `$host` is a host name, and nginx documents the port as a variable of its own, so a public URL with a port, such as `https://ropeshift.example:8443`, would be refused with `foreign_host` behind it. `$proxy_add_x_forwarded_for` is the client's own `X-Forwarded-For` with `$remote_addr` added at the end. `proxy_buffering off` and a long `proxy_read_timeout` keep streams open, where nginx buffers a response by default and closes a connection that is silent for 60 seconds. List nginx's address in `--trusted-proxies`. Sources: [`ngx_http_proxy_module`](https://nginx.org/en/docs/http/ngx_http_proxy_module.html).

### A Caddy example

```caddyfile
ropeshift.example {
    reverse_proxy 10.0.0.9:7400 {
        flush_interval -1
    }
}
```

Caddy terminates TLS for the name, passes the `Host` header on unchanged, sets `X-Forwarded-For` and `X-Forwarded-Proto`, and ignores the `X-Forwarded-*` headers a client sends unless the client's address is one of Caddy's own `trusted_proxies`. `flush_interval -1` turns response buffering off and flushes after each write. List Caddy's address in `--trusted-proxies`. Sources: [`reverse_proxy`](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy).

## Limit wrong guesses

The server counts every wrong guess at a secret by the address the connection came from: a wrong passphrase at the sign-in page, in a change of the passphrase, or in a call that signs in with a session, and a wrong join token at a worker's registration, which an expired or a revoked join token is too. After 10 wrong guesses within 15 minutes, the address is refused for 15 minutes. A refusal is HTTP 429 with the code `too_many_attempts` and a `Retry-After` header that says how many seconds remain, and it covers the right passphrase too. A right passphrase clears the address's count. A join token that works clears nothing, and a worker that holds its credential is not refused, because a credential is no guess.

- `--signin-max-failures` and `--signin-lockout` set the two numbers, one for how many wrong guesses and one for how long they count and how long a refusal lasts. Both must be above zero.
- The address is the one the TCP connection came from, and behind a reverse proxy it is the client the proxy names, as [run behind a reverse proxy](#run-behind-a-reverse-proxy) says. A header changes the address only when a listed proxy sends it. Clients that reach the server through one machine that is not a listed proxy, such as a NAT, share that machine's address and count together.
- An IPv6 address counts with the rest of its /64, because one host holds a whole /64 and could take a new address from it for every guess. An IPv4 address counts as itself, and so does an IPv4-mapped IPv6 address (`::ffff:0:0/96`), which counts as the IPv4 address. Other IPv6 forms of an IPv4 address are not that address: every client behind NAT64's `64:ff9b::/96` shares one count, and so does every link-local client on `fe80::/64`.
- A guess that fails for any reason after the check counts as wrong, such as a store that is down or a client that leaves once its passphrase was checked. Only a request that proves nothing is not counted: no passphrase is set, the session a change needs was gone, the new passphrase is outside its bounds, or another change won.
- Guesses sent at once count as wrong until each is judged, so a client cannot get more guesses than the limit by sending them together.
- The counts live in the server's memory and a restart clears them. The server tracks at most 10,000 addresses at a time, and lets go of the longest idle one that is not refused when it needs room.
- The control socket is no client address and is never limited.
- A passphrase check is a password hash, which costs on purpose, so the server runs one at a time and lets no more than 16 wait their turn. An address has two checks in the line at most, whatever its limit on wrong guesses is, and a third is refused at once with 429 and `Retry-After: 1`. So a few addresses cannot fill the line and leave a member with nothing but a refusal: it takes eight addresses to fill it. A check that finds the line full is refused with 503 and `Retry-After: 1`. Neither refusal is counted against anybody. The limit per address bounds what one address costs, and the line bounds what every address costs together. It does not stop many addresses from guessing at once: each gets its own count, and the line turns away what it cannot take.

## Settings

| Setting | Flag | Variable | Default |
|---------|------|----------|---------|
| Postgres connection string, which holds a password: give it in the variable | `--database` | `ROPESHIFT_DATABASE` | None: required |
| The one listener's address | `--listen` | `ROPESHIFT_LISTEN` | `127.0.0.1:7400` |
| The host names and addresses of the certificate the server makes, repeated or comma separated | `--name` | `ROPESHIFT_SERVER_NAMES` | None: required off loopback with no supplied certificate |
| The file of a certificate to serve, with its key | `--tls-cert` | `ROPESHIFT_TLS_CERT` | None |
| The file of that certificate's key | `--tls-key` | `ROPESHIFT_TLS_KEY` | None |
| `off` serves plaintext to the reverse proxies listed, which terminate TLS | `--tls` | `ROPESHIFT_TLS` | `on` |
| The addresses and CIDR ranges of the reverse proxies, repeated or comma separated | `--trusted-proxies` | `ROPESHIFT_TRUSTED_PROXIES` | None: required with `--tls off` off loopback |
| The https origin members' browsers use behind a reverse proxy | `--public-url` | `ROPESHIFT_PUBLIC_URL` | None: required with `--trusted-proxies` |
| The URL workers reach the server at, which join strings carry | `--worker-url` | `ROPESHIFT_WORKER_URL` | [By mode](#choose-the-worker-url) |
| A worker's join string, given to `ropeshiftd worker join` | The one argument | `ROPESHIFT_JOIN` | None: required to join |
| How long a join token lives, given to `ropeshift worker token` | `--expires-in` | None | `24h`, and at most `720h` |
| Wrong guesses one client address may make within the lockout | `--signin-max-failures` | `ROPESHIFT_SIGNIN_MAX_FAILURES` | `10` |
| How long wrong guesses count, and how long a refused address stays refused | `--signin-lockout` | `ROPESHIFT_SIGNIN_LOCKOUT` | `15m` |
| The server's state directory | None | `ROPESHIFT_STATE_DIR` | [By platform](cli.md#the-daemon) |
| The server's run directory, which holds its control socket | None | `ROPESHIFT_RUN_DIR` | [By platform](cli.md#the-daemon) |

`ropeshiftd help server` lists the tick intervals too. The adapter's warnings, such as a schema the server dropped at its start because no project named it, go to the server's standard error.
