# Telemetry

Ropeshift uses OpenTelemetry for system telemetry. Each worker runs an embedded receiver that stores telemetry on its own machine, and nothing leaves that machine unless the standard OTLP exporter settings name an endpoint. Telemetry holds no prompt, response, or tool content unless a person turns content capture on in their own profile layer.

## What a lane's agent is told

At spawn, the driver points each lane's agent at the worker's receiver through its environment, because a repository's settings files cannot turn telemetry on or say where it goes. The variables are Claude Code's, as its [monitoring documentation](https://code.claude.com/docs/en/monitoring-usage) names them:

| Variable | Value |
|----------|-------|
| `CLAUDE_CODE_ENABLE_TELEMETRY` | `1` |
| `OTEL_METRICS_EXPORTER`, `OTEL_LOGS_EXPORTER` | `otlp` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/json` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | The receiver, on loopback |
| `OTEL_EXPORTER_OTLP_HEADERS` | The receiver's token, so only this worker's lanes can write to it |
| `OTEL_METRIC_EXPORT_INTERVAL`, `OTEL_LOGS_EXPORT_INTERVAL` | `10000` and `5000` milliseconds |
| `OTEL_RESOURCE_ATTRIBUTES` | The lane's tags, below |

The tags are `ropeshift.project`, the ID of the lane's project, `ropeshift.workspace`, which holds the same ID, `ropeshift.work_order.id`, `ropeshift.lane.id`, `ropeshift.lane.role`, `ropeshift.generation`, `ropeshift.profile`, `ropeshift.repo`, `ropeshift.branch`, `ropeshift.worker`, and `ropeshift.driver`. A value holds no space or comma: any byte the variable's grammar refuses is percent-encoded. After a `/clear` the agent keeps the generation it started with, because a running process cannot change its environment. Ropeshift's own metrics carry the current generation.

Any `OTEL_*` variable a profile passes through by name is dropped, so only the driver says where telemetry goes and whether content is in it.

## Profile settings

```toml
[telemetry]
traces = false                     # the agent's traces, which are beta
content = false                    # prompts, responses, and tool content
omit_repository = false            # leave ropeshift.repo and ropeshift.branch out
retention_age = "168h"             # the local store keeps 7 days
retention_bytes = 268435456        # and 256 MiB, whichever comes first
export_account_attributes = false  # keep account identifiers on export
```

`content = true` sets the agent's content flags: `OTEL_LOG_USER_PROMPTS`, `OTEL_LOG_ASSISTANT_RESPONSES`, `OTEL_LOG_TOOL_DETAILS`, `OTEL_LOG_TOOL_CONTENT`, and `OTEL_LOG_RAW_API_BODIES`. Only the user's layer can turn it on. A repository's layer or a lane's overrides can turn it off and never on. The last three settings belong to the worker, so only the user's layer sets them.

## The local store

The receiver listens on `127.0.0.1:7318`, and `ROPESHIFT_TELEMETRY` or `ropeshiftd solo --telemetry` moves it, or turns it off with `off`. It takes OTLP over HTTP with JSON on `/v1/traces`, `/v1/metrics`, and `/v1/logs`, with or without gzip. It refuses a request with no token, a request from a browser page, a request that names another host, and protobuf. The token lives in the state directory, readable only by its owner, and survives a restart, because a lane outlives its worker.

The store is a SQLite file of its own in the state directory, apart from the ledger and the journal. It keeps every span, data point, and log record it received, with the account attributes the agent sends, such as `user.email` and `organization.id`. Retention drops the oldest records first, by age and by size.

`ropeshift telemetry query` reads the store with `--json`, and `ropeshift telemetry lanes` and `ropeshift top` show each lane's context at its last call, its tokens by type, and the cost its agent reported. Context, tokens, and cost come from ropeshift's own metrics, which the worker takes from the driver. For a lane ropeshift recorded nothing of, tokens and cost come from the agent's own metrics, and context comes from its request log either way. Reported cost is an estimate from the client and a lower bound: a turn a kill stopped reports nothing.

## Ropeshift's own telemetry

The worker emits ropeshift's spans and metrics into the same store. The lifecycle spans and most metrics are read from the ledger after each change commits, so a span describes only what happened. Every span of a work order shares one trace, whose ID is derived from the work order's ID, so a span written after a restart still joins it.

| Kind | Names |
|------|-------|
| Spans | `ropeshift.work_order`, `ropeshift.claim`, `ropeshift.lane`, `ropeshift.generation`, `ropeshift.effect`, `ropeshift.handoff`, `ropeshift.park`, `ropeshift.approval.wait`, `ropeshift.question.ladder`, `ropeshift.message.send`, `ropeshift.message.inject` |
| Per-lane metrics | `ropeshift.lane.context.tokens`, `ropeshift.lane.context.baseline`, `ropeshift.lane.tokens`, `ropeshift.lane.cost`, `ropeshift.lane.stall.duration`, `ropeshift.lane.respawns`, `ropeshift.approval.wait.duration`, `ropeshift.message.reply.latency` |
| Other metrics | `ropeshift.lane.breaker.trips`, `ropeshift.lane.liveness.unknown`, `ropeshift.approval.pending`, `ropeshift.message.state`, `ropeshift.lease.lapsed`, `ropeshift.effect.duration`, and the product measures as `ropeshift.measure.*` gauges, by window. See [measures](measures.md) |

They carry IDs, states, kinds, and times. A question's text, a message's body, an approval's summary, and an effect's detail stay in the ledger. The ledger is read into spans every 5 seconds, which `ropeshiftd solo --telemetry-tick` changes, and the emitter writes at twice that interval, so a crash loses at most one interval of ropeshift's own telemetry and nothing of the ledger's. A daemon reads from where the ledger stood when it started.

## Export

Ropeshift exports only when the standard settings name an endpoint: `OTEL_EXPORTER_OTLP_ENDPOINT`, or a per-signal `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`, or `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`, read from the daemon's environment. `OTEL_EXPORTER_OTLP_HEADERS` and the per-signal headers apply, and `OTEL_TRACES_EXPORTER=none` and its siblings turn a signal off. The daemon prints where it exports when it starts.

The exporter speaks OTLP over HTTP with JSON, and a daemon configured for `grpc` or `http/protobuf` refuses to start. The [OTLP specification](https://opentelemetry.io/docs/specs/otlp/) says a server should take both encodings, so a backend that takes only protobuf needs an OpenTelemetry Collector in between.

Before anything leaves, the exporter drops every attribute in the `user.` and `organization.` namespaces, wherever it appears, and changes nothing else. `export_account_attributes = true` keeps them. Export forwards the worker's own telemetry and its lanes' telemetry, and a full queue drops what it cannot send, because a lane never waits on a backend.
