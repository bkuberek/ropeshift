# Notifications

Ropeshift tells members when something needs them. The web UI always does, and it sends nothing anywhere. Anything that leaves the machine is off until a member publishes a policy that turns it on, so with no configuration ropeshift opens no outbound connection for a notice.

## What a notice says

A notice carries these fields and nothing else:

| Field | Holds |
|-------|-------|
| `delivery` | A digest that names the thing that waits. A repeat of the same notice keeps it, so a receiver can drop a second copy. It cannot be turned back into an ID |
| `project` | The name of the project it waits in, which a member chose |
| `attempt` | Which attempt this is, from 1 |
| `kind` | `questions`, `approval`, `effect`, `work`, or one of the CI kinds below |
| `reason` | For work, why it waits: `lapsed`, `blocked`, `failed`, `tripped`, `stalled`, `long_parked`, `worker_behind`, `acceptance`, or `human_resume` |
| `since` | When it began to wait, in milliseconds since the epoch |
| `text` | The project's name, then one fixed sentence for that kind and reason, such as "beta: A lane stopped, and it needs you." |

Apart from its project's name, a notice carries no title, no question, no lane output, no name, no ID, no path, and no repository. Those come from agents, repositories, and trackers, and a notice leaves the machine, so none of that text reaches a notice. To see what waits, a member opens the web UI or runs `ropeshift top`.

## CI notices

CI that asks a person raises a Needs you item and a notice of its own kind. None of them starts an agent, and none names a check, a run, or a branch.

| Kind | Raised when | Text |
|------|-------------|------|
| `ci_not_finished` | A head's required checks have run past the CI timeout: `ci_timeout_multiplier` times the repository's p85, or `ci_timeout_no_history` | "CI has not finished." |
| `ci_queue_stalled` | A required run is still queued past `ci_queue_timeout_multiplier` times the repository's p85 queue time, or `ci_queue_timeout_no_history` | "No runner picked up a CI run." |
| `ci_infrastructure_failure` | An infrastructure conclusion remained after the automatic reruns | "CI failed for a reason an agent cannot fix." |
| `ci_base_red` | A failed check also fails on the base branch's tip | "A check fails on the base branch too." |
| `ci_action_required` | A required check concluded `action_required`, which a person on the forge answers | "A check waits for a person on the forge." |

The same items are in Needs you with their actions, which [the web UI](web-ui.md) and the command line share.

## Channels

| Channel | How it reaches a member | Turned on by |
|---------|-------------------------|--------------|
| `web` | The Needs you view, which an open page reads again when the ledger moves | Nothing: it is always on and sends nothing off the machine |
| `desktop` | `osascript` on macOS and `notify-send` on Linux, with only `text`, on this machine | `desktop = true` |
| `webhook` | An HTTPS POST of the notice as JSON | `webhook_url` |

A member publishes the policy with `ropeshift policy publish --file F`. Only a member can, so no agent can turn a channel on or change where one points.

```toml
[notify_policy]
version = 2
desktop = true
webhook_url = "https://hooks.example.com/ropeshift"
webhook_secret_env = "ROPESHIFT_WEBHOOK_KEY"
```

## Webhooks and secrets

`webhook_url` must be `https`, or `http` to a loopback address. An address with a user, a query, or a fragment is refused, because those parts are where a URL carries a credential, and a URL ends up in logs and proxies. Ropeshift cannot tell a token in a path from a path, so do not use a URL that holds one: a receiver that needs a secret should verify the signature instead.

`webhook_secret_env` names a variable in the daemon's environment, and the ledger holds that name and never the key. With a key set, each delivery carries `Ropeshift-Signature: sha256=<hex>`, the HMAC-SHA256 of the body under that key, so the key itself never crosses the wire. Each delivery also carries `Ropeshift-Delivery`, the same value as `delivery`.

A delivery follows no redirect and uses no proxy, and it goes only to the address a member published. Anything but a 2xx answer counts as a refusal. What a failure records says what went wrong and never where: no address and no key reach a log or the ledger.

## Delivery is at least once

The ledger records each attempt before it is made, and what became of it after. A crash between the two repeats the notice on the next tick, with the same `delivery` and the next `attempt`, so a receiver can hear a notice twice. `ropeshift notice list` shows how many attempts each notice took on each channel, and so does the Notices tab of a project's [Audit view](web-ui.md#audit).

A channel that refuses is asked again after 1, 2, 4, and 8 minutes, up to 5 attempts. Once the thing no longer waits, nothing more is sent about it. One tick sends at most 20 notices, and the rest go on the next. The web UI still shows whatever a channel could not be told.
