package planning

// FormatExample is one file of the repository file format, as a planning lane is shown it. Intro says
// what the kind is for. The examples describe one project, so the files in them name each other the
// way files written by a lane have to: a plan lists its documents, a document and a contract name their
// plan, and a contract cites documents and serves requirements.
type FormatExample struct {
	Kind  FileKind
	Intro string
	Text  string
}

// FormatExamples is every format a planning lane writes (FR-SESSION-5), in the order intake reads
// them. Tests parse each one and follow every reference between them, so what an agent is told is what
// intake reads.
var FormatExamples = []FormatExample{
	{
		Kind:  FileGoal,
		Intro: "A goal says what success is and how it is measured.",
		Text: `+++
kind = "goal"
id = "reliable-checkout"
title = "Checkout survives a failed call"
success_measures = ["Failed checkouts stay under 1 in 1000"]
+++

Why this goal matters, in prose.
`,
	},
	{
		Kind: FilePlan,
		Intro: "A plan lists every document it holds in documents, each as kind:id. A draft decision records an answer the person " +
			"gave you in conversation, with the question, the answer, and the reason. It binds nothing until the person approves the plan.",
		Text: `+++
kind = "plan"
id = "payments-v2"
title = "Make checkout survive a failed call"
goal = "reliable-checkout"
documents = [
  "requirements:payments",
  "spec:checkout-api",
  "architecture:payments",
  "experience-design:checkout-flow",
  "milestone:m1-reliable-checkout",
]

[[decision]]
id = "d1"
question = "Do retries back off exponentially?"
answer = "Yes, with jitter."
rationale = "The person chose it, and it avoids a retry storm."
blocking = false
+++

How the work is planned, in prose.
`,
	},
	{
		Kind: FileRequirements,
		Intro: "Product requirements stay in [[requirement]] tables, so the prose stays free. A requirement's id is the key a " +
			"criterion names.",
		Text: `+++
kind = "requirements"
id = "payments"
title = "Payments requirements"
plan = "payments-v2"

[[requirement]]
id = "PAY-12"
text = "A failed call retries with the same idempotency key"

[[requirement]]
id = "PAY-13"
text = "A retry budget bounds every call"
+++

Context for the requirements, in prose.
`,
	},
	{
		Kind:  FileSpec,
		Intro: "A technical spec lists the requirement ids it addresses.",
		Text: `+++
kind = "spec"
id = "checkout-api"
title = "Checkout API"
plan = "payments-v2"
requirements = ["PAY-12", "PAY-13"]
+++

The calls the checkout service accepts and how each one fails, in prose.
`,
	},
	{
		Kind:  FileArchitecture,
		Intro: "An architecture document is written the same way.",
		Text: `+++
kind = "architecture"
id = "payments"
title = "Payments architecture"
plan = "payments-v2"
requirements = ["PAY-12"]
+++

The services that take part in a payment and who owns each, in prose.
`,
	},
	{
		Kind:  FileExperienceDesign,
		Intro: "An experience design also lists assets: paths inside the repository to the images or files it shows.",
		Text: `+++
kind = "experience-design"
id = "checkout-flow"
title = "Checkout flow"
plan = "payments-v2"
requirements = ["PAY-13"]
assets = ["docs/design/checkout-flow.png"]
+++

What the person sees when a payment fails, in prose.
`,
	},
	{
		Kind: FileMilestone,
		Intro: "A milestone has a priority, and a lower one comes first. depends_on lists the ids of milestones that must finish " +
			"before it, and each exit criterion says how the milestone is known to be done.",
		Text: `+++
kind = "milestone"
id = "m1-reliable-checkout"
title = "Reliable checkout"
plan = "payments-v2"
priority = 1
depends_on = []

[[exit_criteria]]
id = "e1"
text = "Checkout succeeds under one injected upstream failure"
+++

What this milestone delivers and why it comes first, in prose.
`,
	},
	{
		Kind: FileContract,
		Intro: "A shared contract is one that other contracts build on. It is marked shared = true, and every contract that builds " +
			"on it lists its id in uses. The work of a shared contract is built first.",
		Text: `+++
kind = "contract"
id = "payments-client"
title = "Payments client"
plan = "payments-v2"
milestone = "m1-reliable-checkout"
shared = true
documents = ["architecture:payments"]

[[criteria]]
id = "c1"
text = "Every payment call goes through one client that carries an idempotency key"
requirements = ["PAY-12"]

[[work_orders]]
id = "client-base"
title = "Build the payments client"
repo = "."
priority = 1
size = "medium"
needs = { driver = "claude-code" }
+++

What the shared client must offer the contracts that use it, in prose.
`,
	},
	{
		Kind: FileContract,
		Intro: "A contract belongs to one milestone of its plan and cites the documents it depends on in documents. Each criterion " +
			"lists the requirement ids it serves. uses lists the ids of the shared contracts of the plan this one builds on, and " +
			"each of its work orders waits for theirs. Each work order sets repo, priority, size, depends_on, and needs, and may set base to start from a branch other than " +
			"its repository's base branch.",
		Text: `+++
kind = "contract"
id = "checkout-retry"
title = "Retry failed checkout calls"
plan = "payments-v2"
milestone = "m1-reliable-checkout"
shared = false
uses = ["payments-client"]
documents = ["spec:checkout-api", "architecture:payments"]
decisions = []

[[criteria]]
id = "c1"
text = "A failed call retries twice with the same idempotency key"
requirements = ["PAY-12"]

[[criteria]]
id = "c2"
text = "No call retries past the retry budget"
requirements = ["PAY-13"]

[[work_orders]]
id = "retry-budget"
title = "Add the retry budget"
repo = "."
priority = 2
size = "medium"
depends_on = []
needs = { driver = "claude-code" }
+++

The statement of what this work must deliver, in prose.
`,
	},
}
