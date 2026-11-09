package parking

import (
	"strings"
	"time"
)

// Words says what an expression waits for in a sentence a person reads, such as "When question q-1
// is answered, or when a person resumes it". A time reads in UTC, because a stored expression names
// no time zone.
func (e Expr) Words() string { return "When " + e.root.flat().sentence(true) }

// ShortWords says the same in the few words a terminal line has room for, such as "question q-1 or
// a person".
func (e Expr) ShortWords() string { return e.root.flat().short(true) }

// flat folds a term into a parent of its own kind, and a composite of one term into that term, so
// the words say no grouping the evaluator does not need.
func (n node) flat() node {
	if len(n.kids) == 0 {
		return n
	}
	var kids []node
	for _, kid := range n.kids {
		kid = kid.flat()
		if kid.term == n.term {
			kids = append(kids, kid.kids...)
		} else {
			kids = append(kids, kid)
		}
	}
	if len(kids) == 1 {
		return kids[0]
	}
	n.kids = kids
	return n
}

// sentence says a term after "When". A choice at the top repeats "when" before each way out, and a
// choice inside a whole reads as "either".
func (n node) sentence(top bool) string {
	switch n.term {
	case termAnyOf:
		parts := sentences(n.kids)
		if top {
			return strings.Join(parts, ", or when ")
		}
		return "either " + strings.Join(parts, " or ")
	case termAllOf:
		return list(sentences(n.kids), holdsComposite(n.kids))
	}
	return n.clause()
}

func sentences(kids []node) []string {
	out := make([]string, 0, len(kids))
	for _, kid := range kids {
		out = append(out, kid.sentence(false))
	}
	return out
}

func holdsComposite(kids []node) bool {
	for _, kid := range kids {
		if len(kid.kids) != 0 {
			return true
		}
	}
	return false
}

// list joins the parts of a whole. A comma goes before "and" when the list is long, or when a part
// is itself a choice, so its "or" does not run into the "and".
func list(parts []string, comma bool) string {
	switch {
	case len(parts) == 1:
		return parts[0]
	case len(parts) == 2 && !comma:
		return parts[0] + " and " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
}

func (n node) clause() string {
	key := n.fact.Key
	switch n.term {
	case termAt:
		return "the time reaches " + stamp(n.at)
	case string(QuestionAnswered):
		return "question " + key + " is answered"
	case string(ApprovalDecided):
		return "approval " + key + " is decided"
	case string(DecisionRecorded):
		return "a decision on " + key + " is recorded"
	case string(UsageReset):
		return "the usage limit on " + key + " resets"
	case string(SigninRestored):
		return "the sign-in on " + key + " is restored"
	case string(WorkOrderDone):
		return "work order " + key + " is done"
	case string(ExternalCheck):
		return "check " + key + " reports back"
	case string(HumanResume):
		return "a person resumes it"
	}
	return n.term
}

// short says a term in a terminal's words. A composite inside another stands in parentheses.
func (n node) short(top bool) string {
	var joint string
	switch n.term {
	case termAnyOf:
		joint = " or "
	case termAllOf:
		joint = " and "
	default:
		return n.shortClause()
	}
	parts := make([]string, 0, len(n.kids))
	for _, kid := range n.kids {
		parts = append(parts, kid.short(false))
	}
	if top {
		return strings.Join(parts, joint)
	}
	return "(" + strings.Join(parts, joint) + ")"
}

func (n node) shortClause() string {
	key := n.fact.Key
	switch n.term {
	case termAt:
		return stamp(n.at)
	case string(QuestionAnswered):
		return "question " + key
	case string(ApprovalDecided):
		return "approval " + key
	case string(DecisionRecorded):
		return "a decision on " + key
	case string(UsageReset):
		return "a usage reset on " + key
	case string(SigninRestored):
		return "a sign-in on " + key
	case string(WorkOrderDone):
		return "work order " + key
	case string(ExternalCheck):
		return "check " + key
	case string(HumanResume):
		return "a person"
	}
	return n.term
}

func stamp(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04 UTC") }
