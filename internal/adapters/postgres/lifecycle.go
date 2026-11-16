package postgres

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
)

// schemaPrefix keeps a project's schema apart from the installation's and from any schema a person made.
const schemaPrefix = "project_"

// schemaLimit is the longest identifier Postgres keeps.
const schemaLimit = 63

// schemaName names the schema of a project. The ID becomes part of an identifier, so it holds only
// letters, digits, hyphens, and underscores, as it does in a SQLite file's name.
func schemaName(id ledger.WorkspaceID) (string, error) {
	if err := id.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", ledger.ErrInvalidProject, err)
	}
	for _, c := range string(id) {
		if !identifierRune(c) {
			return "", fmt.Errorf("%w: project ID %q holds %q, which a schema name cannot", ledger.ErrInvalidProject, id, c)
		}
	}
	if len(schemaPrefix)+len(id) > schemaLimit {
		return "", fmt.Errorf("%w: project ID %q is longer than the %d characters a schema name allows", ledger.ErrInvalidProject, id, schemaLimit-len(schemaPrefix))
	}
	return schemaPrefix + string(id), nil
}

func identifierRune(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// List returns every project, oldest first.
func (s *State) List(ctx context.Context) ([]ledger.Project, error) {
	return s.Installation.Projects(ctx)
}
