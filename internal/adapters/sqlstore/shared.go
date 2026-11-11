package sqlstore

import (
	"database/sql"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
)

// shared is the installation's side of one project transaction (ADR-0021). A project's ledger holds
// none of the records every project shares, so a transaction that reads or writes one runs a second
// transaction on the installation database. That one begins at its first statement, so a transaction
// that touches no shared record never waits on the installation's writer lock, and it commits just
// before the project's own. The two files have no commit in common: a crash between the two commits
// keeps the shared change without the project's event that names it.
type shared struct {
	installation *Installation
	readOnly     bool
	tx           *sql.Tx
	se           *session
}

// shared returns the installation's side of a transaction, or nil for a store with no installation.
func (s *Store) shared(readOnly bool) *shared {
	if s.installation == nil {
		return nil
	}
	return &shared{installation: s.installation, readOnly: readOnly}
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

// begin starts the transaction on the installation database, once.
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

// changed reports that a statement touched a shared record.
func (sh *shared) changed() bool {
	return sh != nil && sh.se != nil && sh.se.changed
}

// commit commits the installation's side, if it began.
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
