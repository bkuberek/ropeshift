//go:build screenshots

package screenshots

import "strings"

// The sample's plan, as intake reads it from the orders-api repository: a goal, a plan, requirements, a
// spec, three milestones, and contracts where three use a shared one that each hold one work order.

const planTitle = "Reliable order intake"

const goalFile = `+++
kind = "goal"
id = "reliable-orders"
title = "A reliable orders API"
success_measures = ["Fewer than 0.1% of order requests end in a server error over a week", "Every client error names its cause and what to change"]
+++

The orders API takes requests from the storefront and from partner integrations. A slow upstream service
should cost a request a clear error, never a hung connection, and a client that sends too much should learn
when to try again.
`

// planFile is the plan's own file. The later version drafts a third decision and lists the status page.
func planFile(later bool) string {
	documents := `"requirements:orders", "spec:orders-errors", "architecture:reliability", "milestone:errors", "milestone:resilience", "milestone:observability"`
	out := `+++
kind = "plan"
id = "reliability"
title = "` + planTitle + `"
goal = "reliable-orders"
documents = [` + documents + `]

[[decision]]
id = "throttle-status"
question = "Which status does a throttled client receive?"
answer = "429 Too Many Requests, with a Retry-After header in seconds."
rationale = "Clients and proxies already handle it, and the header tells a client when to try again."

[[decision]]
id = "retry-post"
question = "May a client retry POST /orders?"
answer = "Only with an Idempotency-Key header, which the API stores for 24 hours."
rationale = "Without a key, a retry after a lost response would place the order twice."
`
	if later {
		out += `
[[decision]]
id = "status-page"
question = "Who can read the status page?"
answer = "Anyone on the internal network, with no sign-in."
rationale = "On-call engineers need it while the sign-in service may be down."
`
	}
	return out + `+++

Clear errors first, because every later change returns them. Then the calls to fulfillment and payments,
and last what on-call engineers need to follow one request.
`
}

const requirementsFile = `+++
kind = "requirements"
id = "orders"
title = "What the orders API must do"
plan = "reliability"

[[requirement]]
id = "ORD-1"
text = "Every error response uses one documented shape, with a code and a detail."

[[requirement]]
id = "ORD-2"
text = "Each public error code is documented with its cause and what a client should change."

[[requirement]]
id = "ORD-3"
text = "A call to an upstream service has a deadline, and an idempotent call retries within a budget."

[[requirement]]
id = "ORD-4"
text = "A client that sends too much learns when it may try again."

[[requirement]]
id = "ORD-5"
text = "Session signing keys rotate without signing anyone out."

[[requirement]]
id = "ORD-6"
text = "Every log line of a request carries its request ID."

[[requirement]]
id = "ORD-7"
text = "A load balancer can ask whether an instance is ready to take traffic."
+++

What the orders API must do for the storefront, for partners, and for the engineers who run it.
`

// specFile is the error spec. The later version adds the section on retries.
func specFile(later bool) string {
	out := `+++
kind = "spec"
id = "orders-errors"
title = "Error responses"
plan = "reliability"
requirements = ["ORD-1", "ORD-2"]
+++

# Error responses

Every error the orders API returns has the same body: a stable ` + "`code`" + `, a ` + "`detail`" + ` a person can
read, and the request ID.

` + "```json" + `
{"code": "upstream_timeout", "detail": "Fulfillment did not answer within 2 seconds.", "request_id": "req_7Hq2"}
` + "```" + `

## Codes

| Code | Status | Meaning |
| --- | --- | --- |
| ` + "`invalid_order`" + ` | 400 | The order failed validation; the detail names the field. |
| ` + "`rate_limited`" + ` | 429 | The client sent too much; Retry-After says when to try again. |
| ` + "`upstream_timeout`" + ` | 504 | An upstream service did not answer in time. |
`
	if later {
		out += `
## Retries

A client may retry a request that failed with ` + "`upstream_timeout`" + ` when it sent an Idempotency-Key. The API
answers a repeated key with the first response.
`
	}
	return out
}

const architectureFile = `+++
kind = "architecture"
id = "reliability"
title = "How calls to other services stay bounded"
plan = "reliability"
requirements = ["ORD-3", "ORD-4"]
+++

Every outbound call goes through one client that sets a deadline from the request's own, retries an
idempotent call with exponential backoff, and stops at the retry budget. The rate limiter sits in front of
the handlers and keys on the client ID.
`

// milestoneFile writes a milestone. None depends on another, because work in a milestone that depends on
// one still open waits in contract review, and the sample runs work in every milestone at once.
func milestoneFile(id, title, priority, exit string) string {
	out := "+++\nkind = \"milestone\"\nid = \"" + id + "\"\ntitle = \"" + title + "\"\nplan = \"reliability\"\npriority = " + priority + "\n"
	return out + "\n[[exit_criteria]]\nid = \"e1\"\ntext = \"" + exit + "\"\n+++\n\nWhat " + strings.ToLower(title) + " delivers.\n"
}

// contract is one contract of the plan with its one work order.
type contract struct {
	id, title, milestone, statement string
	shared                          bool
	uses                            string
	// criteria are "<id>|<text>|<requirement>".
	criteria []string
	// order is the work order's title and size.
	order, size string
	priority    string
}

func (c contract) file() string {
	out := "+++\nkind = \"contract\"\nid = \"" + c.id + "\"\ntitle = \"" + c.title + "\"\nplan = \"reliability\"\nmilestone = \"" + c.milestone + "\"\n"
	if c.shared {
		out += "shared = true\n"
	}
	if c.uses != "" {
		out += "uses = [\"" + c.uses + "\"]\n"
	}
	for _, criterion := range c.criteria {
		parts := strings.Split(criterion, "|")
		out += "\n[[criteria]]\nid = \"" + parts[0] + "\"\ntext = \"" + parts[1] + "\"\nrequirements = [\"" + parts[2] + "\"]\n"
	}
	priority := c.priority
	if priority == "" {
		priority = "2"
	}
	out += "\n[[work_orders]]\nid = \"" + c.id + "-work\"\ntitle = \"" + c.order + "\"\npriority = " + priority + "\nsize = \"" + c.size + "\"\n"
	return out + "+++\n\n" + c.statement + "\n"
}

// The work orders by the lane each runs on, so the population and the fixture name the same ones.
const (
	orderErrorShape  = "Return every error in one documented shape"
	orderErrorDocs   = "Document the public API error codes"
	orderTimeouts    = "Set upstream timeouts on every outbound call"
	orderRetries     = "Retry idempotent requests with backoff"
	orderRateLimit   = "Add per-client rate limits"
	orderSigningKeys = "Support two active session signing keys"
	orderRequestIDs  = "Add request IDs to structured logs"
	orderHealth      = "Add a health check endpoint"
	orderStatusPage  = "Publish an internal status page"
)

var contracts = []contract{
	{
		id: "error-shape", title: "One error shape", milestone: "errors", shared: true, order: orderErrorShape, size: "medium", priority: "1",
		statement: "Every handler returns errors through one Problem type, so a client reads one shape.",
		criteria:  []string{"c1|Every handler returns errors as a Problem body|ORD-1", "c2|The shape is documented in the API reference|ORD-1"},
	},
	{
		id: "error-docs", title: "Error code reference", milestone: "errors", uses: "error-shape", order: orderErrorDocs, size: "small",
		statement: "Each public error code has a page with its cause and what a client should change.",
		criteria:  []string{"c1|Each error code has a page with its cause and fix|ORD-2"},
	},
	{
		id: "timeouts", title: "Upstream deadlines", milestone: "resilience", order: orderTimeouts, size: "medium", priority: "1",
		statement: "Every call to fulfillment and payments carries a deadline taken from the request's own.",
		criteria:  []string{"c1|Every outbound call has a deadline|ORD-3"},
	},
	{
		id: "retries", title: "Bounded retries", milestone: "resilience", uses: "error-shape", order: orderRetries, size: "medium",
		statement: "An idempotent call to an upstream service retries with exponential backoff, within a budget.",
		criteria:  []string{"c1|An idempotent call retries at most three times|ORD-3", "c2|A retry reuses the request's idempotency key|ORD-3"},
	},
	{
		id: "rate-limit", title: "Per-client rate limits", milestone: "resilience", uses: "error-shape", order: orderRateLimit, size: "large",
		statement: "Each client ID has a request budget per minute, and a client over it is told when to try again.",
		criteria:  []string{"c1|A client over its limit receives 429 with Retry-After|ORD-4", "c2|Limits are set per client tier|ORD-4"},
	},
	{
		id: "signing-keys", title: "Signing key rotation", milestone: "resilience", order: orderSigningKeys, size: "medium",
		statement: "The API accepts tokens signed with either of two active keys, so a rotation signs nobody out.",
		criteria:  []string{"c1|A token signed with either active key verifies|ORD-5"},
	},
	{
		id: "request-ids", title: "Request IDs in logs", milestone: "observability", order: orderRequestIDs, size: "small",
		statement: "The router assigns a request ID, and every log line of the request carries it.",
		criteria:  []string{"c1|Every log line of a request has its request ID|ORD-6"},
	},
	{
		id: "health-check", title: "Readiness endpoint", milestone: "observability", order: orderHealth, size: "small", priority: "3",
		statement: "GET /healthz answers 200 once the database and the event bus are reachable.",
		criteria:  []string{"c1|A load balancer reads readiness from /healthz|ORD-7"},
	},
}

// laterContract is the contract the plan's second version adds.
var laterContract = contract{
	id: "status-page", title: "Internal status page", milestone: "observability", order: orderStatusPage, size: "small", priority: "3",
	statement: "A page on the internal network shows each dependency's health and the error rate of the last hour.",
	criteria:  []string{"c1|The page shows each dependency's health|ORD-7"},
}

// planFiles is every planning file of the first version, by path.
func planFiles() map[string]string {
	files := map[string]string{
		"plans/reliability/goal.md":         goalFile,
		"plans/reliability/plan.md":         planFile(false),
		"plans/reliability/requirements.md": requirementsFile,
		"plans/reliability/spec-errors.md":  specFile(false),
		"plans/reliability/architecture.md": architectureFile,
		"plans/reliability/m-errors.md":     milestoneFile("errors", "Clear errors", "1", "Every error response has one documented shape"),
		"plans/reliability/m-resilience.md": milestoneFile("resilience", "Resilient calls", "2", "A slow upstream costs a request a clear error, never a hung connection"),
		"plans/reliability/m-observability.md": milestoneFile("observability", "Observability", "3",
			"An on-call engineer can follow one request from the router to fulfillment"),
	}
	for _, c := range contracts {
		files["plans/reliability/"+c.id+".md"] = c.file()
	}
	return files
}

// laterPlanFiles is what the plan's second version changes.
func laterPlanFiles() map[string]string {
	return map[string]string{
		"plans/reliability/plan.md":                     planFile(true),
		"plans/reliability/spec-errors.md":              specFile(true),
		"plans/reliability/" + laterContract.id + ".md": laterContract.file(),
	}
}
