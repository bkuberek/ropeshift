# Modes: solo, group, and cluster

Ropeshift is designed to run in three modes. Team size changes configuration, not concepts: every mode is designed to use the same versioned API. Solo mode is the one built in this release, and every guide of ropeshift is a guide to it.

| Mode | What it is | Status | Target version |
|------|------------|--------|----------------|
| Solo | One binary on one machine, with SQLite, no Docker, and no account | Built; every guide uses it | Every release |
| Group | A self-hosted server on Postgres, run with Docker Compose on a LAN or tailnet, with workers on several machines that dial out and several members | Designed, not built | `v0.2.0` |
| Cluster | A Helm chart and Terraform modules for your own cloud | Designed, not built | A 1.x minor release |

## What to use today

Use solo mode. Install ropeshift on the machine that runs your work and follow [Get started with the web UI](getting-started.md). Several projects, several repositories, and many lanes at once on one machine are all solo mode: [Run several projects at once](several-projects.md) covers them.

## Solo

Solo mode is one binary on one machine. The daemon, the web UI at `http://127.0.0.1:7317`, the worker that runs lanes, and the SQLite ledgers all live on that machine. Ropeshift opens no network connection of its own: the agent CLI it drives reaches its provider, and a GitHub repository is reached through your own `gh` sign-in. A member signs in to the web UI with one passphrase.

## Group

Group mode separates the control plane from the machines that do the work. A server holds the API, the ledger on Postgres, and the web UI. Each machine that runs lanes runs a worker that dials the server, so a laptop opens no inbound port. Several members use one installation.

Group mode is not built as a product. A preview of its server starts only when `ROPESHIFT_GROUP_MODE=preview` is set, carries no compatibility promise, and does not yet run work that needs a repository. It is not a way to run ropeshift today. [Run a group server](group-server.md) describes the preview for people who test it.

The guides of a group installation ship with each part of it as that part is built.

## Cluster

Cluster mode runs in your own cloud, installed from a Helm chart with Terraform modules. It is designed and has no build.

## This page

This page changes when a mode's status or its target version does.
