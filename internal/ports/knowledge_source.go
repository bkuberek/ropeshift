package ports

import (
	"context"

	"ropeshift.dev/ropeshift/internal/ledger"
)

// KnowledgeDocument is one document at one version. Key's native ID is where the source
// keeps it, Version names the source's own version, and Digest identifies the content.
// A list leaves Body empty.
type KnowledgeDocument struct {
	Key     ledger.ExternalKey
	Title   string
	Version string
	Digest  string
	Source  string
	Body    string
}

// KnowledgeVersion is one version of one document.
type KnowledgeVersion struct {
	Version string
	Digest  string
}

// KnowledgeSource makes requirements, specs, and designs readable by the UI and by agents
// (FR-INT-8). It reads and versions documents, and holds no record of work.
type KnowledgeSource interface {
	// Describe names the source. Its adapter and instance start every key it returns.
	Describe(ctx context.Context) (SourceDescription, error)
	// List returns every document at its current version, with no body.
	List(ctx context.Context) ([]KnowledgeDocument, error)
	// Read returns one document, at a version or at the current one when version is
	// empty, or ErrNotFound.
	Read(ctx context.Context, key ledger.ExternalKey, version string) (KnowledgeDocument, error)
	// Versions lists one document's versions, newest first, or ErrNotFound.
	Versions(ctx context.Context, key ledger.ExternalKey) ([]KnowledgeVersion, error)
	// Health reports the source's state, and a fetch that failed is a state and not a
	// silent stop.
	Health(ctx context.Context) (SourceHealth, error)
}
