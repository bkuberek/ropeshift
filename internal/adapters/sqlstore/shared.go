package sqlstore

import (
	"database/sql"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
)

// shared is the installation's side of one project transaction (ADR-0021). A project's ledger holds
// none of the records every project shares, so a transaction that reads one runs a read transaction on
// the installation database beside its own. It only reads: a change to a shared record commits through
// the Installation, in a transaction of its own. The read begins at its first statement and takes no
// writer lock, so a project transaction never waits on the installation's writer, nor holds it.
//
// The one exception is the repository index, which a project's write keeps current as it registers a
// repository, so the refusal of a repository another project holds is decided under the index's lock.
// That side is a write transaction of its own, which begins only when a write touches the index, and
// commits just before the project's: a crash between the two leaves an entry whose record is missing,
// which a retry in the same project completes and the daemon's next start drops when it rebuilds the
// index from the records. A crash never leaves a record that holds no entry.
type shared struct {
	installation *Installation
	readOnly     bool
	tx           *sql.Tx
	se           *session
}

// shared returns the installation's read side of a transaction, or nil for a store with no installation.
func (s *Store) shared() *shared {
	if s.installation == nil {
		return nil
	}
	return &shared{installation: s.installation, readOnly: true}
}

// indexSide returns the installation's write side of a transaction, for the repository index alone, or
// nil for a store with no installation.
func (s *Store) indexSide() *shared {
	if s.installation == nil {
		return nil
	}
	return &shared{installation: s.installation}
}

// session returns where the shared records of a transaction run, and the scope they carry. With no
// installation they stay in the project's own database, under the transaction's scope. The shared
// session keeps the project transaction's ID, so a trace reads both as one transaction.
func (sh *shared) session(own *session, scope ledger.WorkspaceID) (*session, ledger.WorkspaceID) {
	if sh == nil {
		return own, scope
	}
	if sh.se == nil {
		sh.se = &session{store: own.store, ctx: own.ctx, id: own.id, mode: own.mode, op: own.op, begin: sh.begin}
	}
	return sh.se, ledger.InstallationScope
}

// begin starts the side's transaction on the installation database, once.
func (sh *shared) begin() error {
	if sh.tx != nil {
		return nil
	}
	tx, err := sh.installation.core.begin(sh.se.ctx, &sql.TxOptions{ReadOnly: sh.readOnly})
	if err != nil {
		return fmt.Errorf("the installation database: %w", err)
	}
	sh.tx, sh.se.on = tx, tx
	return nil
}

// commit commits the side, if it began.
func (sh *shared) commit() error {
	if sh == nil || sh.tx == nil {
		return nil
	}
	return sh.tx.Commit()
}

// rollback ends the installation's side, if it began and did not commit.
func (sh *shared) rollback() {
	if sh != nil && sh.tx != nil {
		_ = sh.tx.Rollback()
	}
}
