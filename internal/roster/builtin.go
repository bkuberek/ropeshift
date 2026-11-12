package roster

import "slices"

// Guide is the built-in routing guide for a coordinating session (FR-AGENT-3).
const Guide = `Hand work to the agents below when it makes the work cheaper or better, and do it yourself when it does not.

- Choose the lightest agent that does the task reliably the first time.
- Give an agent below the standard class a complete written spec: the files, the change, and how to check it.
- Move up one tier only after an agent actually failed at the task, never in anticipation.
- An agent that needs approval for each start waits for that approval, so start one only when the work needs it.
- Use one writing agent at a time for each piece of work, and never two on the same files.
- Read each agent's report, and check its claims against the work before you build on them.`

var (
	readOnlyTools  = Tools{Allow: []string{ToolRead, ToolSearch, ToolContext}, Deny: []string{ToolEdit, ToolShell, ToolWeb, ToolDelegate}}
	checkingTools  = Tools{Allow: []string{ToolRead, ToolSearch, ToolShell, ToolContext}, Deny: []string{ToolEdit, ToolWeb, ToolDelegate}}
	writingTools   = Tools{Allow: []string{ToolRead, ToolSearch, ToolEdit, ToolShell, ToolWeb, ToolContext}, Deny: []string{ToolDelegate}}
	ruleTools      = Tools{Allow: []string{ToolRead, ToolSearch, ToolEdit, ToolShell, ToolContext}, Deny: []string{ToolWeb, ToolDelegate}}
	reasoningTools = Tools{Allow: []string{ToolRead, ToolSearch, ToolEdit, ToolWeb, ToolContext}, Deny: []string{ToolShell, ToolDelegate}}
)

// builtin is the built-in roster, in the order every surface lists it (ADR-0025). Every built-in agent
// is enabled.
func builtin() []Agent {
	agents := definitions()
	for i := range agents {
		agents[i].Enabled = true
	}
	return agents
}

func definitions() []Agent {
	return []Agent{
		{
			Name: "lookup", Kind: KindLookup, Access: AccessReadOnly, Class: ClassSmall, Effort: EffortLow, Approval: ApprovalNone, Tools: readOnlyTools,
			Purpose:      "One fact: a definition, a path, a value, or a version. For a search that needs many files, use explore.",
			Instructions: "Find the one fact asked for, with the fewest reads. Stop as soon as you have it.",
			Report:       "The fact, then the path and line it came from, in at most 50 words.",
		},
		{
			Name: "explore", Kind: KindExplore, Access: AccessReadOnly, Class: ClassSmall, Effort: EffortMedium, Approval: ApprovalNone, Tools: readOnlyTools,
			Purpose:      "Broad read-only search across a codebase or its documents, when the answer needs many files. For one known fact, use lookup. For a diagnosis or a review, use analysis.",
			Instructions: "Search before reading whole files. Report what you found and where, and say what you did not find.",
			Report:       "The answer first, then each finding with its path and line, in at most 300 words.",
		},
		{
			Name: "analysis", Kind: KindAnalysis, Access: AccessReadOnly, Class: ClassLarge, Effort: EffortHigh, Approval: ApprovalNone, Tools: checkingTools,
			Purpose:      "Deep review, debugging, and diagnosis, with evidence. For a plain search, use explore. To judge finished work against its contract, use review.",
			Instructions: "Form a hypothesis, test it against the code and its behavior, and keep only what the evidence supports. Change nothing.",
			Report:       "The finding first, then the evidence for it with paths and lines, then what remains uncertain, in at most 400 words.",
		},
		{
			Name: "reasoning", Kind: KindReasoning, Access: AccessWriting, Class: ClassFrontier, Effort: EffortHigh, Approval: ApprovalPerUse, Tools: reasoningTools,
			Purpose:      "Specs, documents, and hard trade-offs that need the strongest model. It waits for approval on each start. For a diagnosis, use analysis.",
			Instructions: "State the problem and the constraints, weigh the options against them, and write the result where you were asked to.",
			Report:       "The decision or the document's path first, then the trade-offs that decided it, in at most 300 words.",
		},
		{
			Name: "mechanical", Kind: KindMechanical, Access: AccessWriting, Class: ClassSmall, Effort: EffortLow, Approval: ApprovalNone, Tools: ruleTools,
			Purpose:      "Bulk edits that follow a stated rule exactly, such as a rename or a format change. For a change that needs judgment, use light-implementation or implementation.",
			Instructions: "Apply the rule you were given to every place it covers, and nothing else. Stop and report when a case does not fit the rule.",
			Report:       "How many places changed, the files, and every case that did not fit the rule, in at most 150 words.",
		},
		{
			Name: "light-implementation", Kind: KindImplementation, Access: AccessWriting, Class: ClassSmall, Effort: EffortMedium, Approval: ApprovalNone, Tools: writingTools,
			Purpose:      "A small change from a complete written spec that names the files and the check. For an ordinary change with a clear spec, use implementation.",
			Instructions: "Make exactly the change the spec describes, run the check it names, and stop when it passes.",
			Report:       "The files changed and the check's result, in at most 150 words.",
		},
		{
			Name: "implementation", Kind: KindImplementation, Access: AccessWriting, Class: ClassStandard, Effort: EffortMedium, Approval: ApprovalNone, Tools: writingTools,
			Purpose:      "An ordinary change with a clear spec. For a small change with a complete spec, use light-implementation. For a change across many files or with subtle behavior, use heavy-implementation.",
			Instructions: "Read the code the change touches, make the change with its tests, and run the checks that cover it.",
			Report:       "The files changed, the checks run with their results, and anything left undone, in at most 200 words.",
		},
		{
			Name: "heavy-implementation", Kind: KindImplementation, Access: AccessWriting, Class: ClassLarge, Effort: EffortHigh, Approval: ApprovalNone, Tools: writingTools,
			Purpose:      "A change across many files, or with subtle behavior. For an ordinary change, use implementation. For a change that failed at this tier, use deep-implementation.",
			Instructions: "Map what the change touches before editing, keep each step working, and test the behavior that could break.",
			Report:       "The files changed, the checks run with their results, the risks you saw, and anything left undone, in at most 300 words.",
		},
		{
			Name: "deep-implementation", Kind: KindImplementation, Access: AccessWriting, Class: ClassFrontier, Effort: EffortMax, Approval: ApprovalPerUse, Tools: writingTools,
			Purpose:      "A change that failed at the heavy tier, or that no lower tier can do. It waits for approval on each start. Try heavy-implementation first.",
			Instructions: "Find why earlier attempts failed before changing anything, then make the change with tests that prove it.",
			Report:       "Why earlier attempts failed, the files changed, and the checks run with their results, in at most 300 words.",
		},
		{
			Name: "review", Kind: KindReview, Access: AccessReadOnly, Class: ClassLarge, Effort: EffortHigh, Approval: ApprovalNone, Tools: checkingTools,
			Purpose:      "Judging finished work against its contract, criterion by criterion. For a diagnosis of a failure, use analysis.",
			Instructions: "Judge each criterion against the work as it stands, with the evidence for each verdict. Change nothing.",
			Report:       "A verdict per criterion with its evidence, then any defect outside the criteria, in at most 400 words.",
		},
	}
}

// Defaults is the mapping that chooses a lane's agent when its work order names none, by the work
// order's kind and, for a change, its size.
type Defaults struct {
	Plan           string            `json:"plan"`
	ContractReview string            `json:"contract_review"`
	Review         string            `json:"review"`
	Change         map[string]string `json:"change"`
}

// The sizes the default mapping reads. An absent or unknown size counts as medium.
const (
	SizeSmall  = "small"
	SizeMedium = "medium"
	SizeLarge  = "large"
)

// BuiltinDefaults is the default mapping of ADR-0025.
func BuiltinDefaults() Defaults {
	return Defaults{
		Plan: "reasoning", ContractReview: "analysis", Review: "review",
		Change: map[string]string{SizeSmall: "light-implementation", SizeMedium: "implementation", SizeLarge: "heavy-implementation"},
	}
}

// names lists every agent the mapping names, each once, in a stable order.
func (d Defaults) names() []Use {
	uses := []Use{
		{Agent: d.Plan, By: "the default mapping for plan"},
		{Agent: d.ContractReview, By: "the default mapping for contract_review"},
		{Agent: d.Review, By: "the default mapping for review"},
	}
	for _, size := range []string{SizeSmall, SizeMedium, SizeLarge} {
		uses = append(uses, Use{Agent: d.Change[size], By: "the default mapping for a " + size + " change"})
	}
	return slices.DeleteFunc(uses, func(u Use) bool { return u.Agent == "" })
}
