# Notifications

Ropeshift tells members when something needs them. The web UI always does, and it sends nothing anywhere. Anything that leaves the machine is off until a member publishes a policy that turns it on, so with no configuration ropeshift opens no outbound connection for a notice.

## What a notice says

A notice carries these fields and nothing else:

| Field | Holds |
|-------|-------|
| `delivery` | A digest that names the thing that waits. A repeat of the same notice keeps it, so a receiver can drop a second copy. It cannot be turned back into an ID |
| `project` | The name of the project it waits in, which a member chose |
| `attempt` | Which attempt this is, from 1 |
| `kind` | `questions`, `approval`, `effect`, `work`, one of the CI kinds below, or one of the spend kinds below |
| `reason` | For work, why it waits: `lapsed`, `blocked`, `failed`, `tripped`, `stalled`, `long_parked`, `worker_behind`, `acceptance`, `human_resume`, `history_missing`, an adoption branch that merged without the design history it imported, or `history_unread`, an adoption branch that merged and whose base could not be checked for that history in two checks in a row. For a spend alert, the kind of thing it is about, such as `project_day` |
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
| `ci_base_unread` | A failed check's base branch could not be read on three reads of the pull request in a row, so nobody can tell whether the base shares the failure | "The base branch's checks cannot be read." |
| `ci_action_required` | A required check concluded `action_required`, which a person on the forge answers | "A check waits for a person on the forge." |

The same items are in Needs you with their actions, which [the web UI](web-ui.md) and the command line share.

## Spend alerts

A spend limit that is reached, and a spend setting that changes, raise an alert of their own kind. Each is an event in the ledger that names its scope, so the web UI and `ropeshift notice list` show every one, and a channel that could not be told leaves it there.

| Kind | Raised when | Reasons it names |
|------|-------------|------------------|
| `spend_soft_limit` | Charged spend reaches a soft limit, once for each limit and scope: a work order, a review, a project's day or week, the assistant's daily warning, `assistant_daily_warn`, or the orchestrator's daily warning, `orchestrator_daily_warn`. A project's day and week are told again for each new day or week, and a limit you raised is told again when its raised soft limit is reached | `work_order`, `review`, `project_day`, `project_week`, `assistant`, `orchestrator` |
| `spend_hard_limit` | Charged spend reaches a hard limit and work parks or stops being claimed, once for each limit and scope | `work_order`, `review`, `project_day`, `project_week` |
| `spend_no_progress` | A lane parks because it made no progress | `lane` |
| `spend_waiting` | Most of a lane's recent spend was waiting | `lane` |
| `spend_policy_changed` | A version of the spend policy or the installation's spend policy is published, or a limit is raised | `project`, `installation`, `limit_raised` |

Nothing in this build raises `spend_no_progress` or `spend_waiting` yet: their kinds, their sentences, and their channels are in place, and they are delivered as every other spend alert is.

The installation's `orchestrator_daily_warn` is a limit for each machine's orchestrator for the UTC day. A machine's orchestrator serves every project on it, so when its spend for the day reaches the limit, each project the machine serves is told once, and each project's `[notify_policy]` chooses the channels. Two machines have two tallies and tell separately, and a spend report counts in the day it reaches the control plane.

A spend alert says what kind of thing reached a limit and nothing else. Its text is one fixed sentence, such as "The project reached its daily spend limit, and no work starts until the day ends or a member raises it.", with the project's name at its head. It holds no amount, no work order, no lane, no setting, and no name. The ledger's event holds those: the limit reached and what was charged, or for `spend_policy_changed` each key with its value before and after and the member who changed it, which you read in the project's log.

By default every spend alert goes to the web UI only, whatever else the policy turns on. You choose the channels of each kind in `[notify_policy]`, and the web UI keeps every alert whatever you choose:

```toml
[notify_policy]
version = 3
desktop = true
webhook_url = "https://hooks.example.com/ropeshift"

[notify_policy.spend_channels]
spend_hard_limit = ["desktop", "webhook"]
spend_policy_changed = ["webhook"]
```

With that policy a hard stop reaches the web UI, the desktop, and the webhook, a soft notice reaches the web UI only, and a changed spend setting reaches the web UI and the webhook. A channel you choose must be turned on above it: `spend_channels` that names the desktop with `desktop` off, or the webhook with no `webhook_url`, is refused, and so is a kind that is not a spend kind, a channel that does not exist, and `email`, which this build has no mailer for. Questions, approvals, work, and CI notices keep going to every channel the policy turns on. A new version takes effect at the next tick, with no restart.

Settings, Notifications chooses the same channels in the web UI: a table of the five kinds with a Desktop and a Webhook box for each, and the web UI marked Always. It publishes the same `[notify_policy.spend_channels]` as the file does, and a save of the webhook or the desktop setting keeps the choice. A box is offered only while the policy turns its channel on, and a choice of a channel that is turned off holds Review change back with the reason the API would give.

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
webhook_secret_env = "ROPESHIFT_HOOK_NOTIFY"
```

## Webhooks and secrets

`webhook_url` must be `https`, or `http` to a loopback address. An address with a user, a query, or a fragment is refused, because those parts are where a URL carries a credential, and a URL ends up in logs and proxies. Ropeshift cannot tell a token in a path from a path, so do not use a URL that holds one: a receiver that needs a secret should verify the signature instead.

`webhook_secret_env` names a variable in the daemon's environment, and the ledger holds that name and never the key. The name starts with `ROPESHIFT_HOOK_`, such as `ROPESHIFT_HOOK_NOTIFY`, so no other variable of the daemon's environment, such as one that holds a database address, can sign a delivery to an address you chose; a policy that names any other variable is refused when it is published. A version published before this rule is still read as it was. With a key set, each delivery carries `Ropeshift-Signature: sha256=<hex>`, the HMAC-SHA256 of the body under that key, so the key itself never crosses the wire. A policy that names `webhook_secret_env` asks for signed deliveries, so when that variable is not set or is empty, no delivery goes out: it fails with a message that names the variable and never a value, and a notice is retried on the next tick once the variable holds a key. A policy that names no variable sends unsigned. Each delivery also carries `Ropeshift-Delivery`, the same value as `delivery`.

A delivery follows no redirect and uses no proxy, and it goes only to the address a member published. Anything but a 2xx answer counts as a refusal. What a failure records says what went wrong and never where: no address and no key reach a log or the ledger.

## Delivery is at least once

The ledger records each attempt before it is made, and what became of it after. A crash between the two repeats the notice on the next tick, with the same `delivery` and the next `attempt`, so a receiver can hear a notice twice. `ropeshift notice list` shows how many attempts each notice took on each channel, and so does the Notices tab of a project's [Audit view](web-ui.md#audit).

A channel that refuses is asked again after 1, 2, 4, and 8 minutes, up to 5 attempts. Once the thing no longer waits, nothing more is sent about it. One tick sends at most 20 notices, and the rest go on the next. The web UI still shows whatever a channel could not be told.
