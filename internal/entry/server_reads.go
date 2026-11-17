package entry

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// A group server runs in a container with no clone and no terminal beside it, and the control plane never
// holds a worker's adapters (FR-DEP-7). Each read that solo mode makes from a clone on its own machine, and
// each pane it reaches through the worker's terminal, is wired here to a refusal that names the read, so a
// member sees a typed error and never an empty answer. Mirrors of each repository's remote replace the
// repository reads, and the worker's relay replaces the panes.

// What each refusal names of the read it refuses.
const (
	readDocuments       = "documents"
	readStarts          = "lane starts and bases"
	readBranches        = "a lane's branch"
	readCheckouts       = "repository checkouts"
	readManifests       = "a repository manifest"
	readPanes           = "a lane's pane"
	readWorkSource      = "a repository's work source"
	readAcceptedCommits = "an accepted commit"
)

// unavailableRead is one wiring field of serverReads, what its refusal names, and the reason it carries.
type unavailableRead struct {
	Field  string
	Read   string
	Reason error
}

// unavailableReads lists every field of serverReads with the refusal it gets, so G2c and G2e replace a known
// list, and a test calls each refusal and holds the list to the fields.
var unavailableReads = []unavailableRead{
	{"knowledge", readDocuments, ports.ErrRepositoryReadsUnavailable},
	{"starts", readStarts, ports.ErrRepositoryReadsUnavailable},
	{"branches", readBranches, ports.ErrRepositoryReadsUnavailable},
	{"checkouts", readCheckouts, ports.ErrRepositoryReadsUnavailable},
	{"parseManifest", readManifests, ports.ErrRepositoryReadsUnavailable},
	{"panes", readPanes, ports.ErrPaneRelayUnavailable},
	{"workSource", readWorkSource, ports.ErrRepositoryReadsUnavailable},
	{"acceptedCommits", readAcceptedCommits, ports.ErrRepositoryReadsUnavailable},
}

// serverReads is what a group server wires where solo mode wires a clone or a terminal.
type serverReads struct {
	// knowledge is where documents are read from.
	knowledge []ports.KnowledgeSource
	// starts places the lane of every claim, from a base branch and the commits a repository holds.
	starts *control.Starts
	// branches opens a repository at a lane's branch.
	branches server.BranchSource
	// checkouts is the machine a repository is registered from.
	checkouts ports.Checkouts
	// parseManifest reads a repository manifest a registration names.
	parseManifest func([]byte) (planning.RepositoriesFile, error)
	// panes is the terminal a lane's pane reads and types into.
	panes server.Panes
	// workSource is the source intake reads a repository's work from.
	workSource func(repository ledger.Repository) ports.WorkSource
	// acceptedCommits reads a repository at the commit a member accepted.
	acceptedCommits control.PlanSource
}

// unavailableServerReads wires every read a group server cannot make to its refusal.
func unavailableServerReads() serverReads {
	return serverReads{
		knowledge: []ports.KnowledgeSource{unavailableKnowledge{}},
		starts: &control.Starts{
			Bases: unavailableBases{},
			Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) {
				return "", repositoryRead(readStarts)
			},
		},
		branches: func(context.Context, ledger.WorkspaceID, string, string) (ports.WorkSource, error) {
			return nil, repositoryRead(readBranches)
		},
		checkouts: unavailableCheckouts{},
		parseManifest: func([]byte) (planning.RepositoriesFile, error) {
			return planning.RepositoriesFile{}, repositoryRead(readManifests)
		},
		panes: unavailablePanes{},
		workSource: func(repository ledger.Repository) ports.WorkSource {
			return unavailableWorkSource{what: fmt.Sprintf("%s (%s)", readWorkSource, repository.Name)}
		},
		acceptedCommits: func(context.Context, string, string) (ports.WorkSource, error) {
			return nil, repositoryRead(readAcceptedCommits)
		},
	}
}

// repositoryRead is the refusal of a read a server would make from a clone.
func repositoryRead(read string) error {
	return fmt.Errorf("%w: reading %s: a group server reads no repository on its own machine in this version", ports.ErrRepositoryReadsUnavailable, read)
}

// paneRead is the refusal of a view of a worker's terminal.
func paneRead(read string) error {
	return fmt.Errorf("%w: %s: a group server relays no pane of a worker in this version", ports.ErrPaneRelayUnavailable, read)
}

// unavailableKnowledge reads no document.
type unavailableKnowledge struct{}

func (unavailableKnowledge) Describe(context.Context) (ports.SourceDescription, error) {
	return ports.SourceDescription{}, repositoryRead(readDocuments)
}

func (unavailableKnowledge) List(context.Context) ([]ports.KnowledgeDocument, error) {
	return nil, repositoryRead(readDocuments)
}

func (unavailableKnowledge) Read(context.Context, ledger.ExternalKey, string) (ports.KnowledgeDocument, error) {
	return ports.KnowledgeDocument{}, repositoryRead(readDocuments)
}

func (unavailableKnowledge) Versions(context.Context, ledger.ExternalKey) ([]ports.KnowledgeVersion, error) {
	return nil, repositoryRead(readDocuments)
}

func (unavailableKnowledge) Health(context.Context) (ports.SourceHealth, error) {
	return ports.SourceHealth{}, repositoryRead(readDocuments)
}

// unavailableBases asks git nothing.
type unavailableBases struct{}

func (unavailableBases) BaseTip(context.Context, string, string) (ports.BaseTip, error) {
	return ports.BaseTip{}, repositoryRead(readStarts)
}

func (unavailableBases) Contains(context.Context, string, string, string) (bool, error) {
	return false, repositoryRead(readStarts)
}

func (unavailableBases) DefaultBranch(context.Context, string) (string, error) {
	return "", repositoryRead(readStarts)
}

func (unavailableBases) Branch(context.Context, string, string) (ports.BaseTip, error) {
	return ports.BaseTip{}, repositoryRead(readStarts)
}

func (unavailableBases) MergeBase(context.Context, string, string, string) (string, error) {
	return "", repositoryRead(readStarts)
}

func (unavailableBases) Changes(context.Context, string, string, string) ([]planning.FileChange, error) {
	return nil, repositoryRead(readStarts)
}

// unavailableCheckouts finds and keeps no clone.
type unavailableCheckouts struct{}

func (unavailableCheckouts) Find(context.Context, []string, int) ([]ports.Clone, []ports.UnreadPath, error) {
	return nil, nil, repositoryRead(readCheckouts)
}

func (unavailableCheckouts) Resolve(context.Context, string) (string, bool, error) {
	return "", false, repositoryRead(readCheckouts)
}

func (unavailableCheckouts) SameFile(context.Context, string, string) (bool, error) {
	return false, repositoryRead(readCheckouts)
}

func (unavailableCheckouts) Ignored(context.Context, string, string) (bool, error) {
	return false, repositoryRead(readCheckouts)
}

func (unavailableCheckouts) Keep(context.Context, ledger.WorkspaceID, string, string) error {
	return repositoryRead(readCheckouts)
}

func (unavailableCheckouts) Forget(context.Context, ledger.WorkspaceID, string) error {
	return repositoryRead(readCheckouts)
}

func (unavailableCheckouts) Of(context.Context, ledger.WorkspaceID) (map[string]string, error) {
	return nil, repositoryRead(readCheckouts)
}

func (unavailableCheckouts) Every(context.Context) ([]string, error) {
	return nil, repositoryRead(readCheckouts)
}

// unavailablePanes reaches no terminal.
type unavailablePanes struct{}

func (unavailablePanes) Screen(context.Context, string, int64) (string, error) {
	return "", paneRead(readPanes)
}

func (unavailablePanes) Send(context.Context, string, int64, server.PaneInput) error {
	return paneRead(readPanes)
}

// unavailableWorkSource reads no work from a repository, and names which one.
type unavailableWorkSource struct{ what string }

func (s unavailableWorkSource) Describe(context.Context) (ports.SourceDescription, error) {
	return ports.SourceDescription{}, repositoryRead(s.what)
}

func (s unavailableWorkSource) Fields(context.Context, ports.ScopeRef) ([]ports.FieldSchema, error) {
	return nil, repositoryRead(s.what)
}

func (s unavailableWorkSource) Poll(context.Context, ports.Cursor) (ports.ChangeBatch, error) {
	return ports.ChangeBatch{}, repositoryRead(s.what)
}

func (s unavailableWorkSource) Read(context.Context, ledger.ExternalKey) (ports.WorkItem, error) {
	return ports.WorkItem{}, repositoryRead(s.what)
}

func (s unavailableWorkSource) Relate(context.Context, string, string) (ports.Lineage, error) {
	return "", repositoryRead(s.what)
}

func (s unavailableWorkSource) VerifyDelivery(context.Context, ports.InboundDelivery) (ports.ChangeHint, error) {
	return ports.ChangeHint{}, repositoryRead(s.what)
}

func (s unavailableWorkSource) Apply(context.Context, ports.OutboxItem) (ports.WriteResult, error) {
	return ports.WriteResult{}, repositoryRead(s.what)
}

func (s unavailableWorkSource) Health(context.Context) (ports.SourceHealth, error) {
	return ports.SourceHealth{}, repositoryRead(s.what)
}
