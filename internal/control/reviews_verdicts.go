package control

import (
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// oneLine turns each line break of a citation into a space, so a citation stays on one line.
var oneLine = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\u2028", " ", "\u2029", " ")

// redactedVerdicts copies a report's verdicts with the shapes of credentials taken out of their reasons and citations,
// each citation held to one line, and the reasons held to their bound afterwards, because a marker can be longer than
// what it replaces. A verdict is model text that reaches the ledger, its events, and a pull request, so it is redacted
// before any of them keeps it, as a note is. Redaction comes first, because a credential can span lines.
func redactedVerdicts(verdicts []workorders.CriterionVerdict) []workorders.CriterionVerdict {
	if len(verdicts) == 0 {
		return nil
	}
	out := make([]workorders.CriterionVerdict, len(verdicts))
	for i, v := range verdicts {
		out[i] = v
		out[i].Reasons = clipText(ledger.RedactCredentials(v.Reasons), workorders.MaxVerdictReasons)
		out[i].Citations = nil
		for _, citation := range v.Citations {
			out[i].Citations = append(out[i].Citations, strings.TrimSpace(oneLine.Replace(ledger.RedactCredentials(citation))))
		}
	}
	return out
}
