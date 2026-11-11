package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

func createReady(t *testing.T, d *apitest.Daemon, id string) workorders.WorkOrder {
	t.Helper()
	ctx := t.Context()
	order, err := d.Client.CreateWorkOrder(ctx, api.CreateWorkOrder{
		ID: id, Title: "Add the retry budget", Statement: "Retry twice.",
		Criteria: []planning.Criterion{{ID: "c1", Text: "It retries"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.State != workorders.ContractReview {
		t.Fatalf("state %s", order.State)
	}
	result, err := d.Client.Review(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != workorders.Ready {
		t.Fatalf("result %+v", result)
	}
	return order
}

func apiError(t *testing.T, err error, status int, code string) *api.Error {
	t.Helper()
	var refused *api.Error
	if !errors.As(err, &refused) {
		t.Fatalf("got %v, want a %s refusal", err, code)
	}
	if refused.Status != status || refused.Detail.Code != code {
		t.Fatalf("got %d %s, want %d %s", refused.Status, refused.Detail.Code, status, code)
	}
	return refused
}

// wantRefused is apiError for a caller that needs no detail.
func wantRefused(t *testing.T, err error, status int, code string) {
	t.Helper()
	_ = apiError(t, err, status, code)
}

func TestHealthReadsTheStore(t *testing.T) {
	d := apitest.Start(t)
	health, err := d.Client.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != "ok" || health.Revision < 1 {
		t.Errorf("health %+v", health)
	}
	if err := d.Store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = d.Client.Health(t.Context())
	wantRefused(t, err, http.StatusServiceUnavailable, api.CodeUnavailable)
}

// Health says where the web UI listens, so a command names a page at the address the daemon bound, and a
// daemon with no web UI says none.
func TestHealthSaysWhereTheWebUIListens(t *testing.T) {
	d := apitest.StartWith(t, apitest.Options{WebAddress: "127.0.0.1:49152"})
	health, err := d.Client.Health(t.Context())
	if err != nil || health.Web != "127.0.0.1:49152" {
		t.Errorf("health %+v: %v", health, err)
	}
	off, err := apitest.Start(t).Client.Health(t.Context())
	if err != nil || off.Web != "" {
		t.Errorf("a daemon with no web UI says %+v: %v", off, err)
	}
}

func TestCreateListShow(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	first := createReady(t, d, "wo-1")
	if first.ContractID == "" || first.ContractVersion != 1 {
		t.Errorf("no pin: %+v", first)
	}
	if _, err := d.Client.CreateWorkOrder(ctx, api.CreateWorkOrder{ID: "wo-2", Title: "Second"}); err != nil {
		t.Fatal(err)
	}
	all, err := d.Client.WorkOrders(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("%d rows: %v", len(all), err)
	}
	ready, err := d.Client.WorkOrders(ctx, workorders.Ready)
	if err != nil || len(ready) != 1 || ready[0].ID != "wo-1" {
		t.Fatalf("ready %+v: %v", ready, err)
	}
	shown, err := d.Client.WorkOrder(ctx, "wo-1")
	if err != nil || shown.Title != "Add the retry budget" || shown.ReviewedBy == "" {
		t.Fatalf("shown %+v: %v", shown, err)
	}
	_, err = d.Client.WorkOrder(ctx, "absent")
	wantRefused(t, err, http.StatusNotFound, api.CodeNotFound)
}

func TestCreateRetryReturnsTheFirstAnswer(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	request := api.CreateWorkOrder{ID: "wo-1", Title: "One", Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}}}
	first, err := d.Client.CreateWorkOrder(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.Client.CreateWorkOrder(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContractID != second.ContractID {
		t.Errorf("a retry made a second contract: %s and %s", first.ContractID, second.ContractID)
	}
	var contracts int
	err = d.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Planning().Contracts()
		contracts = len(all)
		return err
	})
	if err != nil || contracts != 1 {
		t.Errorf("%d contracts: %v", contracts, err)
	}
}

// TestLedgerAloneHoldsWork is FR-LEDGER-3: no source, and work still reaches a
// claim through the definition of ready.
func TestLedgerAloneHoldsWork(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	createReady(t, d, "wo-1")
	var pin planning.Contract
	err := d.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Planning().Contracts()
		if err != nil {
			return err
		}
		pin = all[0]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := d.Client.Claim(ctx, "wo-1", api.ClaimRequest{ClaimID: "claim-1", WorkerID: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Contract == nil || grant.Contract.Digest != pin.Digest || grant.TTLMs != apitest.LeaseTTL {
		t.Errorf("grant %+v, pin %+v", grant, pin)
	}
}

func TestReviewRefusals(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	if _, err := d.Client.CreateWorkOrder(ctx, api.CreateWorkOrder{ID: "bare", Title: "No criteria"}); err != nil {
		t.Fatal(err)
	}
	result, err := d.Client.Review(ctx, "bare")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != workorders.ContractReview || len(result.Failures) != 1 || result.Failures[0].Check != workorders.CheckCriteria {
		t.Errorf("result %+v", result)
	}
	_, err = d.Client.Review(ctx, "absent")
	wantRefused(t, err, http.StatusNotFound, api.CodeNotFound)
}

func TestRacingClaimsHaveOneWinner(t *testing.T) {
	d := apitest.Start(t)
	createReady(t, d, "wo-1")
	const racers = 8
	var wg sync.WaitGroup
	results := make(chan error, racers)
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.Client.Claim(context.Background(), "wo-1", api.ClaimRequest{
				ClaimID: "claim-" + string(rune('a'+i)), WorkerID: "worker-" + string(rune('a'+i)),
			})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
			continue
		}
		wantRefused(t, err, http.StatusConflict, api.CodeLost)
	}
	if wins != 1 {
		t.Errorf("%d winners", wins)
	}
}

func TestClaimRetryReturnsTheGrant(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	createReady(t, d, "wo-1")
	request := api.ClaimRequest{ClaimID: "claim-1", WorkerID: "worker-a", TTLMs: 30_000}
	first, err := d.Client.Claim(ctx, "wo-1", request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.Client.Claim(ctx, "wo-1", request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fence != second.Fence || first.ClaimID != second.ClaimID || !second.Replayed {
		t.Errorf("first %+v, retry %+v", first, second)
	}
}

func TestStaleWriteGetsTheCurrentFence(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	createReady(t, d, "wo-1")
	grant, err := d.Client.Claim(ctx, "wo-1", api.ClaimRequest{ClaimID: "claim-1", WorkerID: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	written, err := d.Client.Write(ctx, "wo-1", api.WriteRequest{WriteID: "write-1", ClaimID: grant.ClaimID, Fence: grant.Fence})
	if err != nil || written.Rev == 0 {
		t.Fatalf("write %+v: %v", written, err)
	}
	if err := d.Client.Release(ctx, "wo-1", grant.ClaimID, api.ReleaseRequest{Fence: grant.Fence}); err != nil {
		t.Fatal(err)
	}
	second, err := d.Client.Claim(ctx, "wo-1", api.ClaimRequest{ClaimID: "claim-2", WorkerID: "worker-b"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Client.Write(ctx, "wo-1", api.WriteRequest{WriteID: "write-2", ClaimID: grant.ClaimID, Fence: grant.Fence})
	refused := apiError(t, err, http.StatusConflict, api.CodeFenced)
	if refused.Detail.CurrentFence != second.Fence || refused.Detail.State != string(workorders.Claimed) {
		t.Errorf("detail %+v, want fence %d", refused.Detail, second.Fence)
	}
}

// TestWriteRetryStoresOneEvent is a write whose answer was lost: the retry finds
// the first write and repeats nothing, even after the holder lost the fence.
func TestWriteRetryStoresOneEvent(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	createReady(t, d, "wo-1")
	grant, err := d.Client.Claim(ctx, "wo-1", api.ClaimRequest{ClaimID: "claim-1", WorkerID: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	request := api.WriteRequest{WriteID: "write-1", ClaimID: grant.ClaimID, Fence: grant.Fence, Payload: []byte("x")}
	first, err := d.Client.Write(ctx, "wo-1", request)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Client.Release(ctx, "wo-1", grant.ClaimID, api.ReleaseRequest{Fence: grant.Fence}); err != nil {
		t.Fatal(err)
	}
	second, err := d.Client.Write(ctx, "wo-1", request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Rev != first.Rev {
		t.Errorf("the retry answered rev %d, the first write rev %d", second.Rev, first.Rev)
	}
}

func TestReleaseAndReassignRefusals(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	createReady(t, d, "wo-1")
	grant, err := d.Client.Claim(ctx, "wo-1", api.ClaimRequest{ClaimID: "claim-1", WorkerID: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	err = d.Client.Release(ctx, "wo-1", grant.ClaimID, api.ReleaseRequest{Fence: grant.Fence - 1})
	wantRefused(t, err, http.StatusConflict, api.CodeFenced)
	err = d.Client.Reassign(ctx, "wo-1", api.ReassignRequest{Reason: "a closed laptop"})
	wantRefused(t, err, http.StatusConflict, api.CodeNotLapsed)
	if err := d.Client.Release(ctx, "wo-1", grant.ClaimID, api.ReleaseRequest{Fence: grant.Fence}); err != nil {
		t.Fatal(err)
	}
	if err := d.Client.Release(ctx, "wo-1", grant.ClaimID, api.ReleaseRequest{Fence: grant.Fence}); err != nil {
		t.Errorf("a repeated release: %v", err)
	}
}

func TestClaimOfUnknownWorkOrder(t *testing.T) {
	d := apitest.Start(t)
	_, err := d.Client.Claim(t.Context(), "absent", api.ClaimRequest{ClaimID: "c", WorkerID: "w"})
	wantRefused(t, err, http.StatusNotFound, api.CodeNotFound)
}

func TestInvalidRequests(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	tests := []struct {
		name string
		call func() error
	}{
		{"a work order with no id", func() error {
			_, err := d.Client.CreateWorkOrder(ctx, api.CreateWorkOrder{Title: "T"})
			return err
		}},
		{"a work order with no title", func() error {
			_, err := d.Client.CreateWorkOrder(ctx, api.CreateWorkOrder{ID: "x"})
			return err
		}},
		{"a size the format does not name", func() error {
			_, err := d.Client.CreateWorkOrder(ctx, api.CreateWorkOrder{ID: "x", Title: "T", Size: "enormous"})
			return err
		}},
		{"a claim with no claim id", func() error {
			_, err := d.Client.Claim(ctx, "x", api.ClaimRequest{WorkerID: "w"})
			return err
		}},
		{"a claim with no worker", func() error {
			_, err := d.Client.Claim(ctx, "x", api.ClaimRequest{ClaimID: "c"})
			return err
		}},
		{"a write with no id", func() error {
			_, err := d.Client.Write(ctx, "x", api.WriteRequest{ClaimID: "c", Fence: 1})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantRefused(t, tt.call(), http.StatusBadRequest, api.CodeInvalid)
		})
	}
}

func TestSourcesList(t *testing.T) {
	d := apitest.Start(t)
	list, err := d.Client.Sources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if list.Sources == nil {
		t.Error("an empty list must encode as an empty array")
	}
}

func TestMalformedBodyIsRefused(t *testing.T) {
	d := apitest.Start(t)
	var raw json.RawMessage = []byte(`{"unknown_field": 1}`)
	err := d.Client.Do(t.Context(), http.MethodPost, apitest.Route("/v1/work-orders"), raw, nil)
	wantRefused(t, err, http.StatusBadRequest, api.CodeInvalid)
}

func TestListenMakesAPrivateSocket(t *testing.T) {
	dir := filepath.Join(apitest.ShortDir(t), "run")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, api.ControlSocketName)
	listener, err := server.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("directory mode %o", mode)
	}
	info, err = os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("socket mode %o", mode)
	}
}

func TestListenRefusesAPathOverTheLimit(t *testing.T) {
	long := "/tmp/" + string(make([]byte, 0)) + filepath.Join(fillPath(120), api.ControlSocketName)
	if _, err := server.Listen(long); err == nil {
		t.Error("a socket path over the limit was accepted")
	}
}

func fillPath(n int) string {
	raw := make([]byte, n)
	for i := range raw {
		raw[i] = 'a'
	}
	return string(raw)
}

func TestErrorsNameTheirActor(t *testing.T) {
	d := apitest.Start(t)
	createReady(t, d, "wo-1")
	var last ledger.Event
	err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		events, err := tx.Events(0, 1000)
		if err != nil {
			return err
		}
		last = events[len(events)-1]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if last.Actor.Kind != ledger.ActorMember {
		t.Errorf("the last event names %+v", last.Actor)
	}
}
