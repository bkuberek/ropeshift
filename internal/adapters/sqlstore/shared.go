package sqlstore

import (
	"database/sql"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
)

// shared is the installation's side of one project transaction (ADR-0021). A project's ledger holds
// none of the records every project shares, so a transaction that reads one runs a transaction on the
// installation database beside its own. The side is one transaction on one connection of the
// installation's pool, whatever the write does with it, so writes that each hold a connection never wait
// for a second one.
//
// The side reads. It begins at its first statement, read-only, and takes no writer lock, so a project
// transaction never waits on the installation's writer, nor holds it. A change to a shared record commits
// through the Installation, in a transaction of its own.
//
// The one exception is the repository index, which a project's write keeps current as it registers a
// repository, so the refusal of a repository another project holds is decided under the index's lock. The
// first statement that writes the index ends a read-only side, which has changed nothing, and begins a
// writing one in its place, and every read after it runs there. The side commits just before the
// project's: a crash between the two leaves an entry whose record is missing, which a retry in the same
// project completes and the daemon's next start drops when it rebuilds the index from the records. A crash
// never leaves a record that holds no entry.
//
// The project's transaction and the side are idle in turn: each waits while the other runs a statement. A
// server that ends a session left idle inside a transaction (idle_in_transaction_session_timeout) ends the
// side when a project statement runs longer than that timeout between two uses of the side, and the write
// fails. The side begins at its first use and ends when the write's function returns, before the project's
// commit, so a write keeps it only as long as the function needs it.
type shared struct {
	installation *Installation
	tx           *sql.Tx
	writing      bool
	reads        *session
	writes       *session
}

// shared returns the installation's side of a transaction, or nil for a store with no installation.
func (s *Store) shared() *shared {
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
	if sh.reads == nil {
		sh.reads = sh.derive(own, false)
	}
	return sh.reads, ledger.InstallationScope
}

// index returns where a write keeps the installation's repository index, or nil for a store with no
// installation.
func (sh *shared) index(own *session) *session {
	if sh == nil {
		return nil
	}
	if sh.writes == nil {
		sh.writes = sh.derive(own, true)
	}
	return sh.writes
}

func (sh *shared) derive(own *session, write bool) *session {
	se := &session{store: own.store, ctx: own.ctx, id: own.id, mode: own.mode, op: own.op}
	se.begin = func() error { return sh.open(se, write) }
	return se
}

// open puts a session of the side on the side's transaction, and begins it if the side has none or one that
// cannot write for a session that does. It runs before each statement of the session, because the
// transaction a session ran on may have given way to a writing one.
func (sh *shared) open(se *session, write bool) error {
	if sh.tx != nil && (sh.writing || !write) {
		se.on = sh.tx
		return nil
	}
	if sh.tx != nil {
		// A read-only transaction cannot start to write. It has changed nothing, so it ends before the
		// writing one begins, and the side never holds two connections.
		_ = sh.tx.Rollback()
		sh.tx = nil
	}
	tx, err := sh.installation.core.begin(se.ctx, &sql.TxOptions{ReadOnly: !write})
	if err != nil {
		return fmt.Errorf("the installation database: %w", err)
	}
	sh.tx, sh.writing, se.on = tx, write, tx
	return nil
}

// commit ends the side before the project commits, which is the repository index's turn to hold its
// entries ahead of the records. A side that only read ends with nothing to keep. It reports whether the
// side had written, and a failure of that commit.
func (sh *shared) commit() (wrote bool, err error) {
	if sh == nil || sh.tx == nil {
		return false, nil
	}
	tx := sh.tx
	sh.tx = nil
	if !sh.writing {
		_ = tx.Rollback()
		return false, nil
	}
	return true, tx.Commit()
}

// rollback ends the installation's side, if it began and did not commit.
func (sh *shared) rollback() {
	if sh != nil && sh.tx != nil {
		_ = sh.tx.Rollback()
		sh.tx = nil
	}
}
