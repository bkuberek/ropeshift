package parking

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// ErrUnreadable means this build cannot read an expression. It may come from a newer build,
// so a stored one stops nothing: it stays stored and stays out of evaluation.
var ErrUnreadable = errors.New("this build cannot read this wake expression")

// The bounds of an expression, because every event reads every open wake, and an expression
// must stay a small boolean language (ADR-0008).
const (
	MaxExprBytes = 4 << 10
	MaxDepth     = 8
)

// Kind names a fact a term can ask about.
type Kind string

// The kinds of fact.
const (
	QuestionAnswered Kind = "question_answered"
	ApprovalDecided  Kind = "approval_decided"
	DecisionRecorded Kind = "decision_recorded"
	UsageReset       Kind = "usage_reset"
	SigninRestored   Kind = "signin_restored"
	WorkOrderDone    Kind = "work_order_done"
	ExternalCheck    Kind = "external_check"
	HumanResume      Kind = "human_resume"
)

// The terms that hold no fact.
const (
	termAllOf = "all_of"
	termAnyOf = "any_of"
	termAt    = "at"
)

// namedFacts are the terms that name a record by a string.
var namedFacts = map[Kind]bool{
	QuestionAnswered: true, ApprovalDecided: true, DecisionRecorded: true, UsageReset: true,
	SigninRestored: true, WorkOrderDone: true, ExternalCheck: true,
}

// Fact is one thing the ledger can say.
type Fact struct {
	Kind Kind
	Key  string
}

// Facts says which facts hold. A fact it leaves out does not.
type Facts map[Fact]bool

// node is one term of an expression.
type node struct {
	term string
	fact Fact
	at   int64
	kids []node
}

// Expr is a parsed expression. It holds nothing but what the stored text said, and the lane
// a bare `human_resume` speaks of.
type Expr struct {
	root node
}

func unreadable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnreadable, fmt.Sprintf(format, args...))
}

// Parse reads a stored expression for a lane.
func Parse(text, lane string) (Expr, error) {
	if len(text) > MaxExprBytes {
		return Expr{}, unreadable("%d bytes", len(text))
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return Expr{}, unreadable("%v", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Expr{}, unreadable("more than one document")
	}
	root, err := read(raw, lane, 1)
	if err != nil {
		return Expr{}, err
	}
	return Expr{root: root}, nil
}

func read(raw any, lane string, depth int) (node, error) {
	if depth > MaxDepth {
		return node{}, unreadable("deeper than %d", MaxDepth)
	}
	object, ok := raw.(map[string]any)
	if !ok || len(object) != 1 {
		return node{}, unreadable("a term is an object with one key, and this is %v", raw)
	}
	for name, value := range object {
		switch name {
		case termAllOf, termAnyOf:
			return readComposite(name, value, lane, depth)
		case termAt:
			return readTime(value)
		case string(HumanResume):
			if value != true {
				return node{}, unreadable("%s takes true", name)
			}
			return node{term: name, fact: Fact{Kind: HumanResume, Key: lane}}, nil
		}
		if !namedFacts[Kind(name)] {
			return node{}, unreadable("no term %q", name)
		}
		key, ok := value.(string)
		if !ok || key == "" {
			return node{}, unreadable("%s takes a name", name)
		}
		return node{term: name, fact: Fact{Kind: Kind(name), Key: key}}, nil
	}
	return node{}, unreadable("no term")
}

func readComposite(name string, value any, lane string, depth int) (node, error) {
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		return node{}, unreadable("%s takes a list of terms", name)
	}
	out := node{term: name}
	for _, item := range list {
		kid, err := read(item, lane, depth+1)
		if err != nil {
			return node{}, err
		}
		out.kids = append(out.kids, kid)
	}
	return out, nil
}

func readTime(value any) (node, error) {
	number, ok := value.(json.Number)
	if !ok {
		return node{}, unreadable("%s takes a time", termAt)
	}
	at, err := number.Int64()
	if err != nil {
		return node{}, unreadable("%s takes a whole number of milliseconds", termAt)
	}
	return node{term: termAt, at: at}, nil
}

// Holds is the evaluator: a pure function of the expression, the facts, and the time. A
// timer holds at its time and after it, and never before.
func (e Expr) Holds(facts Facts, now int64) bool { return e.root.holds(facts, now) }

func (n node) holds(facts Facts, now int64) bool {
	switch n.term {
	case termAllOf:
		return !slices.ContainsFunc(n.kids, func(kid node) bool { return !kid.holds(facts, now) })
	case termAnyOf:
		return slices.ContainsFunc(n.kids, func(kid node) bool { return kid.holds(facts, now) })
	case termAt:
		return now >= n.at
	}
	return facts[n.fact]
}

// Facts lists what the expression asks about, each once.
func (e Expr) Facts() []Fact {
	var out []Fact
	e.root.walk(func(n node) {
		if n.fact != (Fact{}) && !slices.Contains(out, n.fact) {
			out = append(out, n.fact)
		}
	})
	return out
}

// Names says the expression asks about a kind of fact, which is how an event finds the wakes
// it could touch.
func (e Expr) Names(kind Kind) bool {
	named := false
	e.root.walk(func(n node) { named = named || n.fact.Kind == kind })
	return named
}

func (n node) walk(visit func(node)) {
	visit(n)
	for _, kid := range n.kids {
		kid.walk(visit)
	}
}

// NextTime is the earliest time ahead of now, which `next_eval_at` becomes after a false
// evaluation. It reports false when only an event can change the answer.
func (e Expr) NextTime(now int64) (int64, bool) {
	var next int64
	found := false
	e.root.walk(func(n node) {
		if n.term == termAt && n.at > now && (!found || n.at < next) {
			next, found = n.at, true
		}
	})
	return next, found
}

// FirstTime is the earliest time in the expression, past or not, which `next_eval_at` starts
// from.
func (e Expr) FirstTime() (int64, bool) {
	var first int64
	found := false
	e.root.walk(func(n node) {
		if n.term == termAt && (!found || n.at < first) {
			first, found = n.at, true
		}
	})
	return first, found
}
