package lanes

import "strings"

// Nudge is the one message a lane that ended a turn with no outcome receives. An idle
// lane with no outcome is an abnormality.
const Nudge = "You ended your turn with no outcome. Call the " + ToolLaneReport +
	" tool now, once, with the outcome of this lane."

// BriefSpec is what a first prompt is built from.
type BriefSpec struct {
	WorkOrder string
	Title     string
	Statement string
	Criteria  []string
}

// Brief is a lane's first prompt until context packs replace it: the contract, and
// how the lane ends.
func Brief(spec BriefSpec) string {
	var out strings.Builder
	out.WriteString("You are working in a ropeshift lane on work order " + spec.WorkOrder + ": " + spec.Title + ".\n")
	if statement := strings.TrimSpace(spec.Statement); statement != "" {
		out.WriteString("\n" + statement + "\n")
	}
	if len(spec.Criteria) > 0 {
		out.WriteString("\nAcceptance criteria:\n")
		for _, criterion := range spec.Criteria {
			out.WriteString("- " + criterion + "\n")
		}
	}
	out.WriteString("\nWhen the work is done, or cannot go on, call the " + ToolLaneReport +
		" tool once with its outcome, then end your turn.\n")
	return out.String()
}

// Restarted is what a resumed generation is told, because a resume alone starts no turn.
const Restarted = "Your session ended unexpectedly and was restarted. Continue this work order from where it stopped, " +
	"and call lane_report when it is done or cannot go on."
