# Onboarding

This page takes one person from an empty machine to a first lane in solo mode, and says what to know before that lane runs. Ropeshift has no release yet, so it starts from a build of this repository, and the steps describe the first run as it is designed to work.

## Build the binary

One binary serves both commands, and the name it starts under picks the command. Put both names on `PATH`.

```sh
go build -o ropeshift ./cmd/ropeshift
ln -s ropeshift ropeshiftd
```

A build from a plain checkout serves a placeholder in place of the web UI. `make web-build` writes the application that the next build embeds.

## Install and sign in to an agent CLI

Each agent CLI is installed and signed in on each worker, through its vendor's own flow. For Claude Code, that is [Anthropic's sign-in](https://code.claude.com/docs/en/authentication): run `claude` and sign in, or give the CLI an API key it reads itself.

Ropeshift never reads, stores, relays, or proxies an agent CLI's credentials. The CLI keeps its own sign-in, and ropeshift never offers a sign-in of its own. To learn what a machine can run, `ropeshift worker doctor` runs the vendor's status command, [`claude auth status`](https://code.claude.com/docs/en/cli-reference), and keeps only the kind of sign-in it reports: a subscription, an API key, signed out, or unknown. Nothing that names a person or an account leaves that command.

A worker that runs on a subscription is bound to the one person who signed in. A worker that several people share uses an API key.

## Run ropeshift init

`ropeshift init` sets up the machine, and a second run says only what is left.

1. With no daemon running, it runs one for the length of its own run, because a daemon's first start is what registers its worker, and it stops that daemon at its end.
2. It asks for the first passphrase on the terminal, twice. The passphrase comes only from a terminal, never from stdin, a flag, or a variable. A passphrase already set stays as it is.
3. It waits for the worker's registration.
4. It runs the doctor, and says for each agent CLI what to do next: install it, sign in through its vendor's own flow, or nothing, with the level the worker starts at.
5. It writes a sample contract to the installation's directory, and keeps one already there.

```text
Agent CLIs:
  claude-code: not installed. Install Claude Code, sign in through Anthropic's own flow, and run ropeshift init again.
Ropeshift never reads, stores, relays, or proxies an agent CLI's credentials. Each CLI keeps its own sign-in.
```

It exits 0 when an agent CLI is usable and 1 when none is, after every other step. The installation's directory is `~/Library/Application Support/ropeshift` on macOS, and `$XDG_STATE_HOME/ropeshift` or `~/.local/state/ropeshift` on Linux.

## Start the daemon and run the sample

```sh
ropeshiftd solo --source /path/to/repository
```

The daemon reads work from the repositories it names, at the default branch, and serves the web UI at `http://127.0.0.1:7317` on loopback only. `ropeshift top` watches the same fleet from a terminal.

To run the sample, copy `sample-contract.md` into the repository, change it to your own work, and commit it. Intake reads it, and its work order waits for a review: `ropeshift work list` names it, and `ropeshift work review <id>` passes the review. A member can then claim it and start its lane, or start the orchestrator and open a run window for it. [The command line](cli.md) lists every step.

## Before the first lane

### The limit of same-user isolation

In solo mode, lanes run as the same operating system user as you, so the operating system does not separate an agent from your files, your browser, or a session you hold. A passphrase raises the cost of an agent approving its own request and makes an attempt visible. It does not make it impossible. [Authorization](authorization.md#the-limit-of-same-user-isolation) lists what a lane with a shell can reach. Strong separation needs a boundary an agent cannot cross: another operating system user, a container, a virtual machine, or a worker on another machine.

### Repository content can execute

A repository's own configuration can run code. In a headless session, [Claude Code treats the folder as trusted](https://code.claude.com/docs/en/hooks), so hooks committed to a repository run, and an interactive lane accepts the same trust at its start. Where a repository's `core.hooksPath` names a directory in the worktree, a lane that may edit that directory runs its own code at its next commit. Run lanes on work from outside your team only behind one of the boundaries above.

### Claude subscriptions and automated use

Ropeshift is designed to run the unmodified `claude` binary that you installed and signed in to, as your own session. It is an independent project. It is not affiliated with or endorsed by Anthropic, and no vendor has approved this use. Nothing here is legal advice.

Anthropic's rules leave a risk that ropeshift cannot remove:

- Anthropic's [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) prohibit accessing its services "through automated or non-human means, whether through a bot, script, or otherwise", except through an API key or where Anthropic explicitly permits it.
- Its [legal and compliance page](https://code.claude.com/docs/en/legal-and-compliance) says that the advertised usage limits "assume ordinary, individual usage of Claude Code and the Agent SDK", and that Anthropic may enforce its restrictions "without prior notice".
- The same page says its rules do not prevent an end user from signing in to the unmodified Claude Code binary with their own subscription. Running the unmodified binary is required, and it does not by itself settle whether automated use is ordinary use. That is Anthropic's decision.
- Autonomous operation moves further from ordinary use. An orchestrator that claims work and answers questions with nobody present is more automated than a person running sessions side by side, and a fleet that runs overnight is further still.

What that means for you: Anthropic may rate-limit, restrict, or suspend an account, parallel lanes draw on your subscription's limits quickly, as [Anthropic's Help Center](https://support.claude.com/en/articles/15036540) describes for `claude -p`, and the terms can change.

Ropeshift's defaults keep a subscription worker nearer to a person at work, and they do not decide whether its use is ordinary:

- A subscription worker starts at `attended`. Its orchestrator claims work only inside a run window a member opened, which lasts 4 hours by default and closes by itself, and lanes and generations a day are bounded. A sign-in nobody could read starts there too.
- On a machine with tmux, a lane on a subscription runs in a terminal pane by default, so each one stays a Claude Code session you can attach to.
- `unattended` on a subscription needs a member to read the statement `ropeshift worker risk` prints and acknowledge it for that worker. The acknowledgement is a record that a person accepted a risk. It is not permission from anyone, and it does not make automated use acceptable under any vendor's terms.
- For certainty, sign the CLI in with an API key instead, used under the key owner's own agreement with Anthropic. A worker on an API key starts at `unattended`.

These defaults reduce the risk, and they do not remove it. [Authorization](authorization.md) states the levels, and the vendor pages above are the ones to read before the first lane, because they can change.

## Back up the ledger

```sh
ropeshift backup /path/to/ledger-backup.db
```

The daemon copies its ledger through SQLite's online backup, so it goes on writing while the copy is made. The copy is one SQLite file, readable only by its owner, that holds every record and the passphrase's hash. A file already at the path is never replaced. A fresh installation runs on a copy when the copy is its `ledger.db` before its first start. The worker's journal, which holds the lanes on this machine, stays outside the copy.
