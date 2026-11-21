package mcpserver

import "ropeshift.com/ropeshift/internal/lanes"

// consultTool asks a stronger read-only roster agent to explore a question (FR-ORCH-9). It returns at once, and the
// answer comes on a later wake, so no call waits on a session.
var consultTool = objectTool(lanes.ToolConsult, "Ask a stronger read-only agent to explore a question you cannot settle with confidence, "+
	"before you escalate it. It starts a session of its own, which reads the records and the question's repository, holds the question, "+
	"and spends within the project's consultation limit and its daily and weekly limits. It returns at once with the consultation's ID: "+
	"end your turn, and a later wake brings the agent's options and recommendation. Decide from them, citing consultation:<id>. "+
	"A question is consulted on once.",
	map[string]any{
		"question": questionField,
		"ask":      textField("What you want explored: the choice, what you weighed, and what you could not settle."),
		"agent":    textField("A read-only roster agent to run, by name. With none, the strongest enabled read-only agent."),
	}, "question", "ask")

// consultReportTool is how a consultation's agent reports what it found, once, at the end of its work.
var consultReportTool = objectTool(lanes.ToolConsultReport, "Report what this consultation found, once, when your exploration is done: "+
	"the options, the one you recommend, and why. Then end your turn.",
	map[string]any{
		"options":        listField("The options you found, at least two, each a short statement of the choice and its consequences."),
		"recommendation": textField("The option you recommend."),
		"rationale":      textField("Why that option, with what you read that supports it."),
	}, "options", "recommendation", "rationale")
