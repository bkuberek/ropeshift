package contract

import (
	"context"
	"fmt"
	"sync"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// blindStore is a Store whose transactions cannot read what an estimate is made of or says: the delivery records,
// the estimates and their sets, the forecasts, and the installation's pool. Reading any of them in a transaction
// that holds no leave to ends that transaction with an error naming the transaction and what it reached, so a
// decision that read one, whatever it did with it, fails the check that ran it (FR-EST-12). A transaction a member
// or the control plane makes to estimate, plan, or forecast reads them by right, and a Leave says which.
type blindStore struct {
	ports.Store
	leave func(op string) bool
	// records says every transaction may read the delivery records, which a planning lane's history is made of, and
	// nothing else.
	records bool
	mu      *sync.Mutex
	reads   *[]string
}

// blinded wraps a store: leave names the transactions that may read what an estimate is made of or says.
func blinded(store ports.Store, leave func(op string) bool) blindStore {
	return blindStore{Store: store, leave: leave, mu: &sync.Mutex{}, reads: new([]string)}
}

// blindedToEstimates is blinded to everything but the delivery records.
func blindedToEstimates(store ports.Store, leave func(op string) bool) blindStore {
	b := blinded(store, leave)
	b.records = true
	return b
}

// reached says what the blinded transactions read, one line each.
func (b blindStore) reached() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), *b.reads...)
}

func (b blindStore) note(op, what string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	*b.reads = append(*b.reads, fmt.Sprintf("%s read %s", op, what))
}

// sawEstimate is what a blinded accessor panics with, so the transaction's wrapper can end it with an error.
type sawEstimate struct{ what string }

// catching runs a transaction's function and turns a read of something forbidden into the transaction's error.
func (b blindStore) catching(op string, run func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			saw, ok := p.(sawEstimate)
			if !ok {
				panic(p)
			}
			b.note(op, saw.what)
			err = fmt.Errorf("the transaction %q read %s", op, saw.what)
		}
	}()
	return run()
}

func (b blindStore) Write(ctx context.Context, scope ledger.WorkspaceID, op string, fn func(ports.WriteTx) error) error {
	if b.leave(op) {
		return b.Store.Write(ctx, scope, op, fn)
	}
	return b.Store.Write(ctx, scope, op, func(tx ports.WriteTx) error {
		return b.catching(op, func() error { return fn(blindWrite{WriteTx: tx, records: b.records}) })
	})
}

func (b blindStore) Read(ctx context.Context, scope ledger.WorkspaceID, fn func(ports.ReadTx) error) error {
	if b.leave("read") {
		return b.Store.Read(ctx, scope, fn)
	}
	return b.Store.Read(ctx, scope, func(tx ports.ReadTx) error {
		return b.catching("read", func() error { return fn(blindRead{ReadTx: tx, records: b.records}) })
	})
}

// blindWrite and blindRead hold a transaction apart from the delivery read models.
type blindWrite struct {
	ports.WriteTx
	records bool
}

func (b blindWrite) Deliveries() ports.DeliveryReader {
	if b.records {
		return b.WriteTx.Deliveries()
	}
	panic(sawEstimate{"the delivery records"})
}
func (blindWrite) Estimates() ports.EstimateWriter { panic(sawEstimate{"the estimates"}) }
func (blindWrite) Forecasts() ports.ForecastReader { panic(sawEstimate{"the forecasts"}) }
func (blindWrite) Pool() ports.PoolReader          { panic(sawEstimate{"the installation pool"}) }

type blindRead struct {
	ports.ReadTx
	records bool
}

func (b blindRead) Deliveries() ports.DeliveryReader {
	if b.records {
		return b.ReadTx.Deliveries()
	}
	panic(sawEstimate{"the delivery records"})
}
func (blindRead) Estimates() ports.EstimateReader { panic(sawEstimate{"the estimates"}) }
func (blindRead) Forecasts() ports.ForecastReader { panic(sawEstimate{"the forecasts"}) }
func (blindRead) Pool() ports.PoolReader          { panic(sawEstimate{"the installation pool"}) }
