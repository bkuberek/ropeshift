package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
)

// The statuses of a plan and a contract.
const (
	StatusActive = "active"
)

// The event subject kinds of this package.
const (
	SubjectGoal        = "goal"
	SubjectPlan        = "plan"
	SubjectContract    = "contract"
	SubjectRequirement = "requirement"
	SubjectLink        = "external_link"
	SubjectDependency  = "dependency"
	SubjectBinding     = "source_binding"
	SubjectDocument    = "document"
)

// The events that record planning data. Each holds the whole record, so a replay
// needs nothing else.
const (
	EventGoalRecorded       = "goal.recorded"
	EventPlanRecorded       = "plan.recorded"
	EventContractVersion    = "contract.version_recorded"
	EventRequirementVersion = "requirement.version_recorded"
	EventDocumentVersion    = "document.version_recorded"
	EventPlanVersion        = "plan.version_recorded"
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

// Goal is what plans pursue.
type Goal struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Status          string   `json:"status"`
	SuccessMeasures []string `json:"success_measures,omitempty"`
	Statement       string   `json:"statement,omitempty"`
	Source          string   `json:"source,omitempty"`
}

// Requirement is one version of one requirement. Key is what a contract names, and ID
// stays the same across versions.
type Requirement struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Key     string `json:"key"`
	Text    string `json:"text"`
	Source  string `json:"source,omitempty"`
	Digest  string `json:"digest"`
}

// RequirementDigest identifies one requirement's content. A file holds many requirements,
// so its blob hash would call all of them changed when one changed.
func RequirementDigest(key, text string) string {
	sum := sha256.Sum256([]byte(key + "\x00" + text))
	return DigestSHA256 + hex.EncodeToString(sum[:])
}

// ContentDigest names content a source gave no digest for, from the parts that make it up.
func ContentDigest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return DigestSHA256 + hex.EncodeToString(sum[:])
}

// Plan is what one or more contracts belong to.
type Plan struct {
	ID     string `json:"id"`
	GoalID string `json:"goal_id,omitempty"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
}

// PlanVersion is one version of a plan's file: what the plan listed and drafted at one digest. The
// ledger numbers it, and a plan has a new one for each committed change to the file.
type PlanVersion struct {
	PlanID  string `json:"plan_id"`
	Version int64  `json:"version"`
	Digest  string `json:"digest"`
	Source  string `json:"source,omitempty"`
	// Documents are the citations the plan lists, and Decisions the answers it drafts. Drafts bind
	// nothing until a member approves the plan.
	Documents []string        `json:"documents,omitempty"`
	Decisions []DraftDecision `json:"decisions,omitempty"`
}

// DocumentRecord is a document, which stays the same across its versions. Its ID is its reference,
// `<kind>:<id>`, and PlanID is the plan its file names, if any.
type DocumentRecord struct {
	ID     string   `json:"id"`
	Kind   FileKind `json:"kind"`
	PlanID string   `json:"plan_id,omitempty"`
	Title  string   `json:"title"`
}

// DocumentVersion is one version of a document. Body is the Markdown after the front matter, kept so
// that a pack holds what a contract cited after the file has moved on. Fields is the front matter as
// JSON, which holds what the kind says beyond a title and a plan.
type DocumentVersion struct {
	DocumentID string          `json:"document_id"`
	Version    int64           `json:"version"`
	Title      string          `json:"title"`
	Digest     string          `json:"digest"`
	Source     string          `json:"source,omitempty"`
	Body       string          `json:"body,omitempty"`
	Fields     json.RawMessage `json:"fields"`
}

// Decode reads the front matter into the file struct of the document's kind.
func (v DocumentVersion) Decode(into any) error { return json.Unmarshal(v.Fields, into) }

// DocumentRecorded is the event of a new document version. It holds the document as the version
// left it, so a replay needs nothing else.
type DocumentRecorded struct {
	Document DocumentRecord  `json:"document"`
	Version  DocumentVersion `json:"version"`
}

// DocumentPin is the version of a document that a contract version cites, which stays what that
// contract version read after the document moves on.
type DocumentPin struct {
	Document string `json:"document"`
	Version  int64  `json:"version"`
}

// Contract is one version of a contract. Source names where this version came
// from, so a version read from a repository names its commit. Milestone, Shared, and Uses are what the
// file says, kept as written, and Documents pins each citation to the version it read.
type Contract struct {
	ID           string        `json:"id"`
	Version      int64         `json:"version"`
	PlanID       string        `json:"plan_id"`
	Title        string        `json:"title"`
	Digest       string        `json:"digest"`
	Source       string        `json:"source,omitempty"`
	Statement    string        `json:"statement,omitempty"`
	Criteria     []Criterion   `json:"criteria,omitempty"`
	Requirements []string      `json:"requirements,omitempty"`
	Decisions    []string      `json:"decisions,omitempty"`
	Milestone    string        `json:"milestone,omitempty"`
	Shared       bool          `json:"shared,omitempty"`
	Uses         []string      `json:"uses,omitempty"`
	Documents    []DocumentPin `json:"documents,omitempty"`
	Status       string        `json:"status"`
}

// RequirementKeys are the requirement keys a contract version names: those at its own level, which the
// files written before a criterion could name its own use, then those of its criteria. A key appears
// once.
func (c Contract) RequirementKeys() []string {
	keys := slices.Clone(c.Requirements)
	for _, criterion := range c.Criteria {
		for _, key := range criterion.Requirements {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	return keys
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

// SourceProblem is one thing a source could not read. A problem where the source stands is found
// again by each read that moves, so it names no more. A commit a person accepted for a `plan` work
// order is read once, so a problem found there names that work order and the version it read, and it
// stays until a later read of the file settles it (D17).
type SourceProblem struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
	// Conflict says the file could be read, and changed a base that moved.
	Conflict bool `json:"conflict,omitempty"`
	// WorkOrder is the accepted plan whose commit held the file, and Source the version found there.
	WorkOrder string `json:"work_order,omitempty"`
	Source    string `json:"source,omitempty"`
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
	Goals        map[string]Goal
	Plans        map[string]Plan
	Contracts    map[string]Contract
	Requirements map[string]Requirement
	Links        map[string]ledger.ExternalLink
	Dependencies map[string]Dependency
	// Documents are keyed by reference, and the versions of a document and of a plan by ContractKey.
	Documents        map[string]DocumentRecord
	DocumentVersions map[string]DocumentVersion
	PlanVersions     map[string]PlanVersion
}

// ContractKey identifies a contract or requirement version in a Book.
func ContractKey(id string, version int64) string {
	return fmt.Sprintf("%s@%d", id, version)
}

// ParseContractKey reads a key back. An ID may not hold the separator's meaning, so the last one
// counts.
func ParseContractKey(key string) (string, int64, bool) {
	at := strings.LastIndexByte(key, '@')
	if at <= 0 {
		return "", 0, false
	}
	version, err := strconv.ParseInt(key[at+1:], 10, 64)
	if err != nil || version < 1 {
		return "", 0, false
	}
	return key[:at], version, true
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
		Goals:            map[string]Goal{},
		Plans:            map[string]Plan{},
		Contracts:        map[string]Contract{},
		Requirements:     map[string]Requirement{},
		Links:            map[string]ledger.ExternalLink{},
		Dependencies:     map[string]Dependency{},
		Documents:        map[string]DocumentRecord{},
		DocumentVersions: map[string]DocumentVersion{},
		PlanVersions:     map[string]PlanVersion{},
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
	case EventGoalRecorded:
		var goal Goal
		if err := json.Unmarshal(e.Data, &goal); err != nil {
			return err
		}
		b.Goals[goal.ID] = goal
	case EventRequirementVersion:
		var requirement Requirement
		if err := json.Unmarshal(e.Data, &requirement); err != nil {
			return err
		}
		if requirement.Version > 1 {
			if _, ok := b.Requirements[ContractKey(requirement.ID, requirement.Version-1)]; !ok {
				return fmt.Errorf("requirement %s version %d follows no version", requirement.Key, requirement.Version)
			}
		}
		b.Requirements[ContractKey(requirement.ID, requirement.Version)] = requirement
	case EventPlanRecorded:
		var plan Plan
		if err := json.Unmarshal(e.Data, &plan); err != nil {
			return err
		}
		b.Plans[plan.ID] = plan
	case EventPlanVersion:
		var version PlanVersion
		if err := json.Unmarshal(e.Data, &version); err != nil {
			return err
		}
		if version.Version > 1 {
			if _, ok := b.PlanVersions[ContractKey(version.PlanID, version.Version-1)]; !ok {
				return fmt.Errorf("plan %s version %d follows no version", version.PlanID, version.Version)
			}
		}
		b.PlanVersions[ContractKey(version.PlanID, version.Version)] = version
	case EventDocumentVersion:
		var recorded DocumentRecorded
		if err := json.Unmarshal(e.Data, &recorded); err != nil {
			return err
		}
		version := recorded.Version
		if version.Version > 1 {
			if _, ok := b.DocumentVersions[ContractKey(version.DocumentID, version.Version-1)]; !ok {
				return fmt.Errorf("document %s version %d follows no version", version.DocumentID, version.Version)
			}
		}
		b.Documents[recorded.Document.ID] = recorded.Document
		b.DocumentVersions[ContractKey(version.DocumentID, version.Version)] = version
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
