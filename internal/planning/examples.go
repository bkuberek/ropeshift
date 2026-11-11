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

// AdoptionExamples are the fields a plan for an existing project adds to the format (ADR-0023): what the
// code already meets and delivers, the in-flight branches the plan finishes, and what it leaves out. They
// describe the same project as FormatExamples, for a brief that onboards existing code to show beside them.
// Tests parse each one.
var AdoptionExamples = []FormatExample{
	{
		Kind: FileRequirements,
		Intro: "A requirement the code already meets says status = \"met\" and cites its evidence as repository:path, the files " +
			"and tests that show it. It needs no criterion. Every other requirement is open, which is the default.",
		Text: `+++
kind = "requirements"
id = "payments"
title = "Payments requirements"
plan = "payments-v2"

[[requirement]]
id = "PAY-11"
text = "Every payment call goes through one client"
status = "met"
evidence = ["api:internal/payments/client.go", "api:internal/payments/client_test.go"]

[[requirement]]
id = "PAY-12"
text = "A failed call retries with the same idempotency key"
+++

Context for the requirements, in prose.
`,
	},
	{
		Kind:  FileMilestone,
		Intro: "A milestone the code already delivers says existing = true, and every milestone that depends on it can start at once.",
		Text: `+++
kind = "milestone"
id = "m0-payments-today"
title = "Payments as they are"
plan = "payments-v2"
priority = 0
existing = true
+++

What the code delivers today, in prose.
`,
	},
	{
		Kind: FileContract,
		Intro: "A contract the code already delivers says existing = true and has no work orders. Mark it shared when new work " +
			"builds on it: every contract that lists it in uses waits for nothing on its account.",
		Text: `+++
kind = "contract"
id = "payments-client"
title = "Payments client"
plan = "payments-v2"
milestone = "m0-payments-today"
shared = true
existing = true

[[criteria]]
id = "c1"
text = "Every payment call goes through one client"
requirements = ["PAY-11"]
+++

What the client offers the contracts that use it, in prose.
`,
	},
	{
		Kind: FileContract,
		Intro: "A work order that finishes an in-flight branch names it in continues. Its lane starts from the branch's tip on a " +
			"branch of its own, its review reads the whole change since the branch left its base, and the branch itself never moves.",
		Text: `+++
kind = "contract"
id = "checkout-retry"
title = "Retry failed checkout calls"
plan = "payments-v2"
milestone = "m1-reliable-checkout"
uses = ["payments-client"]

[[criteria]]
id = "c1"
text = "A failed call retries twice with the same idempotency key"
requirements = ["PAY-12"]

[[work_orders]]
id = "retry-finish"
title = "Finish the retry branch"
repo = "api"
priority = 1
size = "medium"
continues = "feature/checkout-retry"
needs = { driver = "claude-code" }
+++

The statement of what this work must deliver, in prose.
`,
	},
	{
		Kind:  FilePlan,
		Intro: "The plan records each branch or issue it does not carry in a [[left_out]] table, with ref naming it and the reason.",
		Text: `+++
kind = "plan"
id = "payments-v2"
title = "Make checkout survive a failed call"
goal = "reliable-checkout"
documents = ["requirements:payments", "milestone:m0-payments-today", "milestone:m1-reliable-checkout"]

[[left_out]]
ref = "branch:spike/old-checkout"
reason = "An experiment the person abandoned"
+++

How the work is planned, in prose.
`,
	},
}

// Onboarding is an existing document before and after an adopt session onboards it (ADR-0023): the front
// matter goes on top, with a table for each requirement the document states under the ID it already gives,
// and every line the document held stays below it as it was.
type Onboarding struct {
	Before string
	After  string
}

// onboardedProse is the existing document's own text, which onboarding leaves as it is.
const onboardedProse = `# Payments requirements

What the payments service must do, as the team wrote it down.

## PAY-11: One client

Every payment call goes through one client, so retries and logging live in one place.

## PAY-12: Retries

A failed call retries with the same idempotency key.
`

// OnboardedExample is a requirements document the code partly meets, before and after onboarding. Tests
// parse the after and hold its prose to the before.
var OnboardedExample = Onboarding{
	Before: onboardedProse,
	After: `+++
kind = "requirements"
id = "payments"
title = "Payments requirements"
plan = "payments-v2"

[[requirement]]
id = "PAY-11"
text = "Every payment call goes through one client"
status = "met"
evidence = ["api:internal/payments/client.go", "api:internal/payments/client_test.go"]

[[requirement]]
id = "PAY-12"
text = "A failed call retries with the same idempotency key"
+++

` + onboardedProse,
}
