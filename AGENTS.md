# AGENTS.md

This file gives coding agents the instructions for working in this repository. It is vendor-neutral on purpose: `CLAUDE.md` only imports it.

## The repository

Ropeshift is a self-hosted software factory that runs supervised agent CLIs from a work ledger. It is fair source under FSL-1.1-ALv2 ([LICENSE.md](LICENSE.md)); never call it open source. The repository is public, so nothing committed may hold a local path, a private name, or a reference to private planning.

It is one Go module, `ropeshift.com/ropeshift`, that builds one binary. The binary is named `ropeshift`, and `ropeshiftd` is a link to it: the name a process starts under picks the command. The web UI is embedded in the binary.

## Layout

The module follows a hexagonal layout, and `test/rules` classifies every package into a layer:

| Path | Layer | What it holds |
|------|-------|---------------|
| `cmd/ropeshift`, `cmd/ropeshiftd` | Main | Thin mains that call `internal/entry` |
| `internal/entry` | Entry | The shared main, and the only package that wires adapters to ports |
| `internal/authority`, `contextpacks`, `lanes`, `ledger`, `measures`, `messages`, `parking`, `planning`, `questions`, `workorders` | Domain | The rules of the product, with no process, file, network, clock, or entropy access |
| `internal/ports` | Ports | The interfaces the application needs, each with a fake |
| `internal/control`, `internal/worker` | Application | The control plane and the worker |
| `internal/adapters/...` | Adapters | Claude Code, SQLite, git, tmux, launchd, systemd, OpenTelemetry, and the other outside systems |
| `internal/api`, `internal/api/server` | API | The versioned API's types and client, and its HTTP handler |
| `internal/cli`, `hook`, `mcpserver`, `shim`, `top` | Clients | The command line, hook, MCP server, session shim, and terminal view |
| `internal/crashpoint`, `internal/version` | Support | Leaf packages |
| `web/` | Web UI | The TypeScript application. `make web-build` writes it to `web/static`, which `main` never holds apart from a placeholder page |
| `test/` | Suites | `rules`, `contract`, `chaos`, `bench`, `service`, `exit`, `install`, and `real`, with `doubles`, `apitest`, `gittest`, `pgtest`, and `bundle` beside them |

`docs/` holds the user and maintainer documentation, `scripts/` the release scripts, `packaging/` the Homebrew formula template, and `install.sh` the install script.

## Dependency rules

`test/rules` reads the source, so a broken rule fails `make check`:

- A main imports only `internal/entry`.
- Domain and port packages import only the domain and a short list of pure standard library packages, and no third-party module.
- The application imports the domain, ports, and support packages. The control plane and the worker do not import each other.
- Only `internal/entry` imports adapters. An adapter imports the domain, ports, other adapters, support packages, and the API client.
- Clients reach the ledger only through the API or the worker's socket. No path from a client reaches the ports, the application, an adapter, or the API server, and no path from the hook reaches an adapter.
- Every port has a fake, there is one authorization function, and every Go module and web package must carry a license the license check allows.

## Build and test

You need Go at the version `go.mod` names, Node.js and npm for the web UI, and golangci-lint. GoReleaser and Docker are needed only for the release targets.

| Target | When to run it |
|--------|----------------|
| `make check` | Before every pull request. It runs formatting, vet, lint, the race-enabled tests, the web tests and typecheck, the license check, and the release configuration check, and it starts no agent session |
| `make test`, `make vet`, `make lint`, `make web-test` | One part of `make check` while you iterate |
| `make cross` | After a change that could break a platform: it builds for macOS and Linux on arm64 and amd64 |
| `make chaos` | After a change to crash recovery, leases, or the journal: it kills real processes at random points |
| `make check-postgres` | After a change to the store: the contract suite on a private Postgres |
| `make bench`, `make sizing` | When a change can move performance. Never beside each other or beside `make chaos` |
| `make service-test`, `make service-container` | After a change to the service manager adapters. The second needs Docker |
| `make snapshot`, `make install-test`, `make exit-container` | After a change to packaging, `install.sh`, or the first run. They need GoReleaser and Docker |
| `make real`, `make qualify` | Only when a person asks by name |

`make real` and `make qualify` start real agent CLI sessions on the signed-in account. They never run in continuous integration. The real-session suites compile only with the `realsession` build tag, skip unless `ROPESHIFT_REAL_SESSIONS=1` is set, count every turn before it runs, and stop at the first failure with no retry. Every other target puts a `claude` canary first on `PATH` that fails the run if anything executes it, so a test that needs an agent uses the fake CLI in `test/doubles`.

## Tests first

Write the test that shows a behavior change, run it, and see it fail for the reason you expect before you change the code. Then make it pass, and keep `make check` passing. A defect fix starts with a test that reproduces the defect. Tests in `make check` use fakes and doubles, never a real agent session or a network service.

## Conventions

- Use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/): `feat:`, `fix:`, `docs:`, `test:`, `chore:`, `ci:`, and `style:`. Release notes are built from these types, so choose the type a user would expect.
- Keep each commit to one coherent change. Do not add AI attribution or `Co-Authored-By` trailers.
- Write code comments, documentation, and messages in English, in a professional engineering tone, with no emojis and no marketing language.
- In Markdown, put a blank line before and after every heading, list, code block, and table, give every code block a language, and use relative links that resolve.
- Keep documents current: describe the present behavior, and leave change history to git. Cite a source URL for every external fact.
- Mention Claude Code, Codex, and opencode only as the CLIs ropeshift drives, never with their branding as ropeshift's own.

## Branches and releases

`main` is the one long-lived branch, and every change reaches it through a pull request. Stability comes from the version, not from a branch: while release-please's `prerelease` setting is `true`, every release from `main` is a pre-release, and a `Release-As:` footer moves to the next stage or to a stable release. Do not create release branches or push tags by hand: when a person merges the release pull request, release-please tags the merge commit `v<version>` and creates the release, and the release workflow builds and uploads its artifacts with GoReleaser in the same run. That tag is the release's only tag. `go install` is not an install channel for now: every channel is a GoReleaser artifact, with the web UI GoReleaser builds embedded. `scripts/release.sh` is only the fallback for [releasing by hand](docs/releasing.md#releasing-by-hand) when GitHub Actions is unavailable. Every pull request merges with a merge commit, never a squash or a rebase, so every commit in a pull request must be a Conventional Commit: each one reaches the changelog. A pull request's title may be a Conventional Commit, but GitHub copies the title into the merge commit's message, and release-please then counts the change twice, so merge with a plain description: `gh pr merge N --merge --body "Merges pull request #N."`, or on the pull request's web page replace the merge commit's description with a plain sentence ([release notes](docs/releasing.md#release-notes)). [Releasing](docs/releasing.md#branches-and-stability) describes the whole model.

## Never

- Never commit secrets: `.env` files, API keys, tokens, private keys, or credentials of any kind. Unstage any you find and say so.
- Never commit build output: `dist/` and the web build under `web/static` stay untracked.
- Never run an agent CLI's sign-in command, and never edit an agent CLI's user-level settings, such as its settings or credential files in your home directory. Ropeshift never reads, stores, relays, or proxies agent credentials, and the work in this repository must not either.
- Never run a real agent CLI session outside `make real` or `make qualify`, and run those only when a person asks.
