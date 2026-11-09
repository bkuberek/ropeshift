package contract

import (
	"slices"
	"sync"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
)

// Trace collects the statements a Store ran.
type Trace struct {
	mu      sync.Mutex
	entries []sqlstore.TraceEntry
}

// Record appends one entry.
func (t *Trace) Record(e sqlstore.TraceEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = append(t.entries, e)
}

// Entries returns a copy of what was recorded.
func (t *Trace) Entries() []sqlstore.TraceEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.entries)
}

// The statements that may open a write transaction, and the one that follows.
var (
	revisionStatements = []string{sqlstore.StatementTakeRevision, sqlstore.StatementCreateWorkspace}
	clockStatement     = sqlstore.StatementReadClock
)

// CheckRevisionFirst proves the order SQLite alone cannot show (D7): the first
// statement of every write transaction takes the workspace revision, and database
// time is read right after it. A renewal and a wake's next evaluation are the two
// transactions that skip the revision, and they must then hold no revision statement.
func CheckRevisionFirst(entries []sqlstore.TraceEntry) (int, error) {
	byTx := map[uint64][]sqlstore.TraceEntry{}
	var order []uint64
	for _, e := range entries {
		if e.Mode != sqlstore.ModeWrite {
			continue
		}
		if _, seen := byTx[e.Tx]; !seen {
			order = append(order, e.Tx)
		}
		byTx[e.Tx] = append(byTx[e.Tx], e)
	}
	for _, id := range order {
		statements := byTx[id]
		op := statements[0].Op
		if IsRenewal(op) {
			for _, e := range statements {
				if slices.Contains(revisionStatements, e.Statement) {
					return len(order), Violatedf("%s took a revision", op)
				}
			}
			continue
		}
		if !slices.Contains(revisionStatements, statements[0].Statement) {
			return len(order), Violatedf("%s ran %s before the revision", op, statements[0].Statement)
		}
		if len(statements) > 1 && statements[1].Statement != clockStatement {
			return len(order), Violatedf("%s ran %s before reading the clock", op, statements[1].Statement)
		}
	}
	return len(order), nil
}

// IsRenewal names the transactions that hold no revision.
func IsRenewal(op string) bool { return op == sqlstore.OpRenew || op == sqlstore.OpWakeNextEval }
