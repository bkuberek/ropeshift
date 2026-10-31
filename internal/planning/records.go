package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/ledger"
)

// The statuses of a plan and a contract.
const (
	StatusActive = "active"
)

// The event subject kinds of this package.
const (
	SubjectPlan       = "plan"
	SubjectContract   = "contract"
	SubjectLink       = "external_link"
	SubjectDependency = "dependency"
	SubjectBinding    = "source_binding"
)

// The events that record planning data. Each holds the whole record, so a replay
// needs nothing else.
const (
	EventPlanRecorded       = "plan.recorded"
	EventContractVersion    = "contract.version_recorded"
	EventLinkRecorded       = "external_link.recorded"
	EventDependencyRecorded = "dependency.recorded"
	EventBindingChanged     = "source_binding.changed"
)

// DigestGitBlob and DigestSHA256 prefix a digest with its kind, so a comparison
// across sources cannot mistake one hash for another (ADR-0013).
const (
	DigestGitBlob = "gitblob:"
	DigestSHA256  = "sha256:"
)

// Plan is what one or more contracts belong to.
type Plan struct {
	ID     string `json:"id"`
	GoalID string `json:"goal_id,omitempty"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
}

// Contract is one version of a contract. Source names where this version came
// from, so a version read from a repository names its commit.
type Contract struct {
	ID           string      `json:"id"`
	Version      int64       `json:"version"`
	PlanID       string      `json:"plan_id"`
	Title        string      `json:"title"`
	Digest       string      `json:"digest"`
	Source       string      `json:"source,omitempty"`
	Statement    string      `json:"statement,omitempty"`
	Criteria     []Criterion `json:"criteria,omitempty"`
	Requirements []string    `json:"requirements,omitempty"`
	Decisions    []string    `json:"decisions,omitempty"`
	Status       string      `json:"status"`
}

// DependencyKind says what a dependency does to the definition of ready.
type DependencyKind string

// DependencyBlocking holds a work order back until the dependency is done.
const DependencyBlocking DependencyKind = "blocking"

// Dependency is one edge between work orders. A source names its work orders by
// native ID, and the named one may not exist yet, so the edge keeps the name and
// the source, and the definition of ready resolves it. Ledger work names another
// work order by ID and has no source.
type Dependency struct {
	WorkOrder string         `json:"work_order"`
	DependsOn string         `json:"depends_on"`
	Adapter   string         `json:"adapter,omitempty"`
	Instance  string         `json:"instance,omitempty"`
	Kind      DependencyKind `json:"kind"`
}

// Key returns the external key of a dependency that names one.
func (d Dependency) Key() (ledger.ExternalKey, bool) {
	if d.Adapter == "" {
		return ledger.ExternalKey{}, false
	}
	return ledger.ExternalKey{Adapter: d.Adapter, Instance: d.Instance, NativeID: d.DependsOn}, true
}

// The health states of a binding. A credential expiring or a fetch failing is a
// state and not a silent stop.
const (
	HealthOK          = "ok"
	HealthFetchFailed = "fetch_failed"
	HealthBadFiles    = "bad_files"
)

// SourceProblem is one thing a source could not read.
type SourceProblem struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

// SourceBinding is one configured source, with how far intake has read it.
type SourceBinding struct {
	ID       string          `json:"id"`
	Port     string          `json:"port"`
	Adapter  string          `json:"adapter"`
	Instance string          `json:"instance"`
	Cursor   string          `json:"cursor,omitempty"`
	Health   string          `json:"health"`
	Detail   string          `json:"detail,omitempty"`
	Problems []SourceProblem `json:"problems,omitempty"`
}

// PortWork is the `port` of a binding that reads work.
const PortWork = "work"

// LedgerDigest identifies a contract that lives only in the ledger, by its content.
func LedgerDigest(c Contract) (string, error) {
	raw, err := json.Marshal(struct {
		Title     string      `json:"title"`
		Statement string      `json:"statement"`
		Criteria  []Criterion `json:"criteria"`
	}{c.Title, c.Statement, c.Criteria})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return DigestSHA256 + hex.EncodeToString(sum[:]), nil
}

// RecordEvent is the event that records one planning record.
func RecordEvent(eventType, subjectKind, subjectID string, record any, actor ledger.Actor) (ledger.EventDraft, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{
		Type:    eventType,
		Actor:   actor,
		Subject: ledger.Subject{Kind: subjectKind, ID: subjectID},
		Data:    data,
	}, nil
}

// Book is what the planning events replay to.
type Book struct {
	Plans        map[string]Plan
	Contracts    map[string]Contract
	Links        map[string]ledger.ExternalLink
	Dependencies map[string]Dependency
}

// ContractKey identifies a contract version in a Book.
func ContractKey(id string, version int64) string {
	return fmt.Sprintf("%s@%d", id, version)
}

// LinkKey identifies a link in a Book.
func LinkKey(link ledger.ExternalLink) string {
	return link.RecordKind + ":" + link.Key.String()
}

// DependencyKey identifies a dependency in a Book.
func DependencyKey(d Dependency) string {
	return d.WorkOrder + "->" + d.DependsOn
}

// ErrReplay means the log holds a planning event that cannot be read or that
// breaks the records' own rules.
var ErrReplay = errors.New("planning replay")

// Replay rebuilds the planning records from events, in revision order. Versions of
// one contract must count up from 1, because a gap means a version was lost.
func Replay(events []ledger.Event) (Book, error) {
	book := Book{
		Plans:        map[string]Plan{},
		Contracts:    map[string]Contract{},
		Links:        map[string]ledger.ExternalLink{},
		Dependencies: map[string]Dependency{},
	}
	for _, e := range events {
		if err := book.apply(e); err != nil {
			return Book{}, fmt.Errorf("%w: rev %d: %v", ErrReplay, e.Rev, err)
		}
	}
	return book, nil
}

func (b Book) apply(e ledger.Event) error {
	switch e.Type {
	case EventPlanRecorded:
		var plan Plan
		if err := json.Unmarshal(e.Data, &plan); err != nil {
			return err
		}
		b.Plans[plan.ID] = plan
	case EventContractVersion:
		var contract Contract
		if err := json.Unmarshal(e.Data, &contract); err != nil {
			return err
		}
		if contract.Version > 1 {
			if _, ok := b.Contracts[ContractKey(contract.ID, contract.Version-1)]; !ok {
				return fmt.Errorf("contract %s version %d follows no version", contract.ID, contract.Version)
			}
		}
		b.Contracts[ContractKey(contract.ID, contract.Version)] = contract
	case EventLinkRecorded:
		var link ledger.ExternalLink
		if err := json.Unmarshal(e.Data, &link); err != nil {
			return err
		}
		b.Links[LinkKey(link)] = link
	case EventDependencyRecorded:
		var dependency Dependency
		if err := json.Unmarshal(e.Data, &dependency); err != nil {
			return err
		}
		b.Dependencies[DependencyKey(dependency)] = dependency
	}
	return nil
}
