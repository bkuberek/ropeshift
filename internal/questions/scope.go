package questions

import (
	"errors"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ScopeKind says what kind of record a scope's ID names.
type ScopeKind string

// The kinds of scope. A scope is resolved through the ledger from the record its ID names, never from the
// text of the ID (ADR-0030).
const (
	// ScopePlan binds the plan, every contract of the plan, and every work order under those contracts.
	ScopePlan ScopeKind = "plan"
	// ScopeContract binds the contract and every work order that pins a version of it.
	ScopeContract ScopeKind = "contract"
	// ScopeWorkOrder binds the work order and the rework of it.
	ScopeWorkOrder ScopeKind = "wo"
	// ScopeRepository binds every work order whose repository it is.
	ScopeRepository ScopeKind = "repo"
)

// scopeGrammar is how a refusal names what a scope may be.
const scopeGrammar = "plan:<id>, contract:<id>, wo:<id>, or repo:<id>"

// ErrBadScope marks a scope outside the grammar. It is an invalid request.
var ErrBadScope = errors.New("a scope is " + scopeGrammar)

// ErrUnheldScope marks a scope whose ID names no record of its kind in the ledger, so it would bind nothing. It is
// an invalid request.
var ErrUnheldScope = errors.New("a scope names a record the ledger holds")

// scopeNouns name the record each kind of scope names.
var scopeNouns = map[ScopeKind]string{
	ScopePlan: "plan", ScopeContract: "contract", ScopeWorkOrder: "work order", ScopeRepository: "repository",
}

// Scope is what a decision binds: one record of one kind. The zero Scope binds nothing.
type Scope struct {
	Kind ScopeKind
	ID   string
}

// ParseScope reads `kind:id`, with an ID the ledger accepts. An empty string is the zero Scope, which binds
// nothing, and a decision with no scope is still found by the ladder's lookup.
func ParseScope(text string) (Scope, error) {
	if text == "" {
		return Scope{}, nil
	}
	kind, id, found := strings.Cut(text, ":")
	switch ScopeKind(kind) {
	case ScopePlan, ScopeContract, ScopeWorkOrder, ScopeRepository:
	default:
		found = false
	}
	if !found || !ledger.PlainID(id) {
		return Scope{}, fmt.Errorf("%w: %w: %q", ErrInvalid, ErrBadScope, text)
	}
	return Scope{Kind: ScopeKind(kind), ID: id}, nil
}

// String is the scope as a decision stores it.
func (s Scope) String() string {
	if s.Zero() {
		return ""
	}
	return string(s.Kind) + ":" + s.ID
}

// Zero reports a scope that binds nothing.
func (s Scope) Zero() bool { return s.Kind == "" }

// Unheld is the refusal of a scope whose ID names no record of its kind in the ledger.
func (s Scope) Unheld() error {
	return fmt.Errorf("%w: %w: %q names no %s", ErrInvalid, ErrUnheldScope, s.String(), scopeNouns[s.Kind])
}

// ScopeOf is the scope of a question about a record: what an answer to it binds. A contract review lane's
// question is about its plan, any other lane's about its work order, and a question about nothing binds
// nothing. So does a record whose ID is no plain ID: no scope can name it, and the control plane builds this
// scope itself, so an answer to a question about it records a decision that binds nothing, and is never refused.
func ScopeOf(subjectKind, subjectID string) string {
	if subjectID == "" || !ledger.PlainID(subjectID) {
		return ""
	}
	switch subjectKind {
	case planning.SubjectPlan:
		return Scope{ScopePlan, subjectID}.String()
	case planning.SubjectContract:
		return Scope{ScopeContract, subjectID}.String()
	case workorders.SubjectKind:
		return Scope{ScopeWorkOrder, subjectID}.String()
	}
	return ""
}
