![The ropeshift wordmark above separate hemp yarns that twist into one rope, with a blue marker yarn laid through it.](docs/assets/ropeshift-banner.png)

# ropeshift

Ropeshift is a free, fair-source, self-hosted software factory: project management combined with agentic workforce management and operations. It is designed to turn planned work into verified outcomes with a fleet of supervised agents, and to interrupt a person only when a person is needed. It wraps agent CLIs such as Claude Code, Codex, and opencode. It does not replace them, and it is not a model client.

Website: [ropeshift.com](https://ropeshift.com/)

## Status

In development, with no release yet. This repository holds the concept for now, and code arrives here as the implementation advances. Everything below describes the intended design, and none of it can be installed or run yet.

## What it is

Ropeshift has four parts:

| Part | What it is designed to provide |
|------|--------------------------------|
| Work ledger | The system of record for execution: claims, leases, outcomes, questions, decisions, and evidence. Your tracker stays the system of record for what people plan, and ropeshift also works with no tracker at all |
| Planning and decisions | Plans and versioned contracts, a contract review that finds questions before work starts, and a searchable record of every decision and its rationale |
| Supervised agent fleet | One orchestrator per machine pulls work and exercises judgment. A deterministic daemon keeps each session healthy, and no two machines can own the same work |
| Web UI | Roadmap, plans, decisions, open questions, and what is in progress, on hold, stuck, and completed, with an operator TUI beside it |

## How work flows

1. Work arrives from a tracker, from repository files, or directly in the ledger.
2. A person or a planning agent writes a plan and its contracts.
3. Contract review collects every question it can find, in one batch. Work becomes `ready` only when it passes a definition of ready.
4. An orchestrator claims ready work and starts a lane: one writer in one worktree.
5. When a lane has a question, it climbs a ladder: existing decisions, open questions, a decision within policy, a recorded assumption, and only then a person.
6. A lane that truly cannot proceed parks. It saves its work, stops its process so it uses no quota, and resumes when the answer arrives.
7. The lane ends with a typed outcome, artifacts, and check results, and the ledger writes the result back to the tracker.

The north-star metric is human interruptions per completed work order.

## Ways to run it

Team size changes configuration, not concepts. All planned modes use the same API.

| Mode | How it is designed to run | Store |
|------|---------------------------|-------|
| Solo | One binary on one machine, with no Docker, no account, and no network requirement | SQLite |
| Self-hosted group | Docker Compose on a LAN or tailnet, with workers that dial out, so laptops open no inbound ports | Postgres |
| Cluster | A Helm chart and Terraform modules in your own cloud | Postgres |

## Agent sign-in

Each agent CLI is installed and signed in on each worker, through its vendor's own flow. Ropeshift is designed never to read, store, relay, or proxy agent credentials. A worker that uses a subscription is bound to the person who signed in, and shared workers use API keys.

## Claude subscription accounts

Ropeshift is designed to support Claude subscription accounts by running the unmodified `claude` binary that you installed and signed in to. It is an independent project, and it is not affiliated with or endorsed by Anthropic, OpenAI, or any other vendor.

Anthropic's [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) restrict automated use of subscription accounts, and its [usage limits assume ordinary, individual usage](https://code.claude.com/docs/en/legal-and-compliance). Running the unmodified binary does not by itself resolve that, and autonomous operation moves further from ordinary use. Running automated sessions will carry account risk that you accept.

A subscription worker is designed to start at `attended`, where it claims work only inside a run window a person opened, and to go further only after a member acknowledges the stated risk for that worker. No vendor has approved this use. [Onboarding](docs/onboarding.md#claude-subscriptions-and-automated-use) states the risk in full.

## Documentation

- [Vision](docs/vision.md): the problem, the principles, and the non-goals.
- [Onboarding](docs/onboarding.md): from an empty machine to a first lane, and what to know before it runs.
- [Command line](docs/cli.md): every command and its exit codes.
- [Authorization](docs/authorization.md): who may approve what, and the limit of same-user isolation.
- [Qualification](docs/qualification.md): how each Claude Code version is checked before ropeshift relies on it.
- [Notifications](docs/notifications.md): what a notice says, and where it goes.
- [Telemetry](docs/telemetry.md): what is recorded, where it stays, and when it leaves the machine.
- [Product measures](docs/measures.md): the north star and how each measure is computed.
- [Versioning](docs/versioning.md): how versions are chosen, and what `ropeshift version` reports.
- [Releasing](docs/releasing.md): how a release is built, tested, and published, and each install channel.

## License and contributions

Ropeshift is fair source under the [Functional Source License](https://fsl.software/), FSL-1.1-ALv2. See [LICENSE.md](LICENSE.md). Each version also becomes available under Apache-2.0 two years after its release. Outside code contributions are not open yet.
