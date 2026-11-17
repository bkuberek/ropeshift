package sqlstore

import "database/sql"

// ReplaceProjectCommit makes commit run in place of the commit of every write's transaction of the project,
// and the write fails when it returns an error. A test uses it to fail a project's commit after the
// repository index committed its part. A commit that fails leaves the transaction open for the write to
// roll back, and this does the same.
func (s *Store) ReplaceProjectCommit(commit func() error) {
	s.commit = func(*sql.Tx) error { return commit() }
}
