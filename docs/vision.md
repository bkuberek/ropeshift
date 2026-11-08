# Vision

This document states the problem ropeshift addresses, how it is positioned, the principles that constrain its design, and what it deliberately does not do.

## Summary

Ropeshift is a free, fair-source, self-hosted software factory: project management combined with agentic workforce management and operations. It has four parts: a work ledger, a planning and decision system, a fleet of supervised agents, and a web UI. It wraps agent CLIs and never replaces them. It is not a model client and not an alternative to Claude Code, Codex, or opencode.

One product serves a solo engineer and a team. Team size changes configuration, not concepts.

## Problem

Starting an agent is a solved problem: many launchers create a worktree, open a terminal, and start a session. The harder problems sit around the sessions:

- **Deciding what work exists.** Plans live in trackers, repositories, and people's heads. Agents start from whatever they were told, and nothing checks that the work was ready to start.
- **Keeping one owner per piece of work.** Two sessions on two machines can pick up the same task. Nothing records who holds it, or what happens when a laptop closes.
- **Giving agents the same picture.** Each session rediscovers the goals, requirements, and earlier decisions, or works from a stale copy.
- **Answering questions without stalling the fleet.** Agents stop to ask. The same question reaches a human several times, from different sessions, and most of the answers already exist in the requirements or in earlier decisions. Interrupted agents and repeated questions are the largest source of human toil.
- **Waiting without wasting anything.** A session that needs an answer holds a process and a worktree until someone responds. A usage limit stops a session at an arbitrary point.
- **Showing humans what is happening.** Status is spread across terminals, tracker boards, and chat. Nobody can see what is stuck, what is on hold, and what needs them.

Long-running sessions also fail in smaller ways. Context fills up, sessions stall or crash, permission prompts wait unseen, and detection relies on screen scraping or on files the vendor documents as unstable. Ropeshift handles these too, but they are the supporting layer, not the product.

## Positioning

Ropeshift is a self-hosted software factory. A launcher answers "start an agent in a worktree." A tracker answers "what did we plan." Ropeshift answers "turn planned work into verified outcomes with a fleet of agents, and interrupt a human only when a human is actually needed."

The product sits between the planning tools people already use and the agent CLIs they already run:

- Trackers stay the system of record for what humans plan. Ropeshift reads from them and writes results back, and it also works alone with no tracker.
- Agent CLIs stay the runtime. Ropeshift supervises them and never reimplements their agent loops.
- The ledger is the system of record for execution: who holds which work, what happened, what was asked, what was decided, and what proves it.

### Working vocabulary

Lean manufacturing supplies the vocabulary where it clarifies the design.

| Term | Origin | In ropeshift |
|------|--------|------------|
| Jidoka | [Toyota](https://global.toyota/en/company/vision-and-philosophy/production-system/): "automation with a human touch", in which equipment can "detect the abnormality and stop automatically" so that people need not watch it | A lane stops itself on an abnormality, so one person can tend many lanes |
| Andon | [Toyota](https://global.toyota/en/company/vision-and-philosophy/production-system/): a "problem display board" that lights up to notify workers of an abnormality | A lane raises a question and parks |
| Kanban | [Toyota UK](https://mag.toyota.co.uk/kanban-toyota-production-system/): "conveys information between processes and automatically orders parts as they are used up" | Orchestrators pull work instead of having it pushed to them |
| Poka-yoke | [Toyota UK](https://mag.toyota.co.uk/poka-yoke/): mistake-proofing that helps a person "avoid (yokeru) mistakes (poka)" | Contract review and the definition of ready stop bad work before it starts |
| Work order and shop floor | General manufacturing usage | The unit of dispatch and the live view of the fleet |

The metaphor stops at people. The product has no factory hierarchy and assumes no roles. Every member can answer any question and make any decision.

## Principles

These principles constrain every design decision. A proposal that violates one must say why.

### Wrap native capabilities

Agent CLIs ship supervision features quickly. Claude Code already has background sessions, `claude agents --json`, hooks, stream-json, a per-session messaging socket, and Remote Control. Ropeshift wraps these rather than rebuilding them, and it verifies what it relies on. It never treats a file the vendor documents as unstable, such as transcript JSONL or `~/.claude/jobs/`, as a contract. It feature-detects rather than assumes.

### Never touch agent credentials

Ropeshift never reads, stores, relays, or proxies OAuth tokens, API keys, or session credentials for any agent vendor. Each user signs in to each agent CLI through that vendor's own flow, on the worker that runs it. Where an API key is used, the key stays on the worker and the CLI reads it. Ropeshift stores references to secrets, never values. The control plane holds no agent credentials in any deployment mode. For Claude subscription accounts this is a hard requirement.

### Vendor-neutral

The core knows work orders, lanes, generations, questions, and decisions, not vendors. Vendor behavior lives behind a driver port, and tracker behavior lives behind a source port. Claude Code is the first driver, and Codex and opencode follow. Organization-specific wiring lives in workspace profiles, not in code.

### Local-first in solo mode

Solo mode runs on one machine as one binary, with no network, no account, and no Docker. Every other mode adds infrastructure only because the user chose it. Telemetry is stored locally first and exported only when the user configures an exporter.

### Reliability as a feature

Each daemon is crash-only: it can be killed at any point and restarts from durable state. Hooks never block the agent. Every side effect is recorded before and after it happens, so that after a crash the daemon knows which effects are complete and which are unknown. Respawns are rate-limited by a circuit breaker. An incomplete read is an error, not a negative answer.

### Agent-operable

Every operation a human can perform through the web UI or TUI, an orchestrator agent can perform through the CLI or the MCP server, with machine-readable output. The exceptions are the decisions reserved for humans.

### No LLM in the deterministic control loop

Supervision, leases, scheduling eligibility, wake conditions, and tracker sync are deterministic. Judgment belongs to agents, which run as supervised sessions outside the loop. Planning is a supported workflow, and a planning agent runs as a lane. Learning changes versioned policy and never runs inside the loop.

### Ask a human last

Human attention is the scarcest resource in the system. A question passes a ladder of cheaper answers before it reaches a person, and the same question is never asked twice. Human interruptions per completed work order is the north-star metric, read together with measures that stop it from being gamed.

### Authority cannot be self-granted

An agent cannot approve its own request. A decision binds to an exact artifact and is void when that artifact changes. Learning can tune settings and can never widen authority.

### Free and complete

The product is free, complete enough to run on its own, and requires no service you pay for.

## Non-goals

Ropeshift deliberately does not:

- Include a model client in the control plane or the deterministic core, or act as a model proxy or inference router.
- Offer, broker, pool, or share any vendor login or subscription.
- Replace agent CLIs or reimplement their agent loops, tools, or compaction. Agent CLIs are the only supported runtimes in the early phases. A later built-in runtime for API-key users is an optional worker adapter, not a replacement, and subscription lanes never use it.
- Replace trackers. A tracker stays the system of record for what humans plan.
- Be a general workflow engine. Its state machines exist to run work orders.
- Model an organization. It has no factory hierarchy, and the first release has no roles.
- Require a service you pay for, a vendor account, or a specific network product.
- Collect prompt, response, or tool content in telemetry by default.
