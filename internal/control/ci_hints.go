package control

import (
	"sync"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
)

// The board of CI hints (FR-SPEND-20). A signed webhook delivery for a head puts a hint on it, and each worker's
// heartbeat takes the hints after the last one that worker heard. A hint carries no state and the read decides, so
// the board is memory and nothing else: a restart loses the hints on it, and the schedule, which works alone,
// reads what they would have.
const (
	// CIHintsHeld is how many hints the board keeps. The oldest go first.
	CIHintsHeld = 256
	// ciHintLife is how long a hint is handed out. A worker that was away longer reads on its schedule.
	ciHintLife = 10 * time.Minute
)

type boardHint struct {
	hint ledger.CIHint
	at   time.Time
}

// CIHints is the board of CI hints.
type CIHints struct {
	now func() time.Time

	mu    sync.Mutex
	seq   int64
	items []boardHint
}

// NewCIHints returns an empty board. Now is the clock the hints' lives run on. The first hint is numbered from the
// clock's millisecond, so a board that came after a restart numbers above every hint the board before it made,
// and a worker's mark from the earlier run is below them all.
func NewCIHints(now func() time.Time) *CIHints {
	return &CIHints{now: now, seq: now().UnixMilli()}
}

// Note puts a hint on the board for a head of a repository. An empty head names every pull request of it.
func (h *CIHints) Note(binding ledger.ForgeBinding, head string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	h.items = append(h.items, boardHint{hint: ledger.CIHint{Seq: h.seq, Binding: binding.String(), Head: head}, at: h.now()})
	if extra := len(h.items) - CIHintsHeld; extra > 0 {
		h.items = append([]boardHint(nil), h.items[extra:]...)
	}
}

// Since lists the hints after a mark that are still alive, oldest first, and the board's own mark, which a worker
// sends back with its next heartbeat.
func (h *CIHints) Since(after int64) ([]ledger.CIHint, int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	var out []ledger.CIHint
	for _, item := range h.items {
		if item.hint.Seq > after && now.Sub(item.at) < ciHintLife {
			out = append(out, item.hint)
		}
	}
	return out, h.seq
}
