package server_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestGoldenNotices writes what the Audit view's Notices tab reads, from a real ledger: a notice a channel took
// at its first attempt, one it took at its second after refusing the first, which it may have heard twice, and
// one a channel has refused every time. A ledger that told no channel anything lists nothing.
func TestGoldenNotices(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	desktop, webhook := fake.NewNotifier("desktop"), fake.NewNotifier("webhook")
	notices := control.NewNotices(d.Store, func(authority.NotifyPolicy) []ports.Notifier { return []ports.Notifier{desktop, webhook} }).RetryingAfter(0)
	actor := ledger.Actor{Kind: ledger.ActorSystem, ID: "notify-tick"}
	since := clock.Now()
	waiting := []ledger.Notice{
		ledger.NewNotice(apitest.Scope, ledger.NoticeQuestions, "", "batch-1", since).In("harbor"),
		ledger.NewNotice(apitest.Scope, ledger.NoticeWork, "blocked", "wo-1", since+1000).In("harbor"),
		ledger.NewNotice(apitest.Scope, ledger.NoticeApproval, "", "approval-1", since+2000).In("harbor"),
	}
	empty, err := d.Client.Notices(t.Context())
	if err != nil || len(empty) != 0 {
		t.Fatalf("a ledger that told nothing lists %+v: %v", empty, err)
	}
	holdsGolden(t, "notices-empty", steady(pretty(t, api.NoticeList{Notices: empty})))

	deliver := func(waiting []ledger.Notice) {
		t.Helper()
		clock.Advance(time.Minute)
		// A channel that refuses ends the tick with its refusal, and the other channel has still been told.
		_ = notices.Deliver(t.Context(), apitest.Scope, waiting, actor)
	}
	webhook.Fail(errString("the webhook answered 503"))
	deliver(waiting[:2])
	// The first attempt at the second notice is a channel's refusal. This one takes it at its second attempt.
	desktop.Fail(errString("the desktop channel is not running"))
	deliver(waiting[2:])
	desktop.Fail(nil)
	deliver(waiting[2:])
	deliver(waiting[:2])

	lines, err := d.Client.Notices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var twice, refused int
	for _, line := range lines {
		twice += btoi(line.Delivered && line.Attempts > 1)
		refused += btoi(!line.Delivered && line.Failure != "")
	}
	if len(lines) != 6 || twice == 0 || refused == 0 {
		t.Fatalf("the notices read %+v", lines)
	}
	holdsGolden(t, "notices", steady(pretty(t, api.NoticeList{Notices: lines})))
}

type errString string

func (e errString) Error() string { return string(e) }

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestGoldenLaneMessages writes what the Lane view reads of what was sent to a lane, from a real ledger: a message
// the lane answered, one it took into a turn, one its session refused, one that is still queued, and one that
// outlived its window. A lane nobody wrote to has none.
func TestGoldenLaneMessages(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo-1", "Add the retry budget")
	quiet := w.Running("wo-2", "Trim the retry log")
	actor := ledger.Actor{Kind: ledger.ActorSystem, ID: "message-tick"}
	send := func(id string, kind messages.Kind, body string) {
		t.Helper()
		clock.Advance(time.Second)
		if _, err := d.Client.SendMessage(t.Context(), messages.Draft{ID: id, To: messages.Target{Lane: lane.ID}, Kind: kind, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	report := func(id string, state messages.State, reply string) {
		t.Helper()
		clock.Advance(time.Second)
		lane.Upload(ledger.UploadMessage, messages.Report{ID: id, State: state, Reply: reply})
	}
	send("msg-expired", messages.KindNotice, "The base branch moved.")
	clock.Advance(11 * time.Minute)
	if _, err := control.NewMessages(d.Store, nil).ExpireMessages(t.Context(), apitest.Scope, actor); err != nil {
		t.Fatal(err)
	}
	send("msg-answered", messages.KindInstruction, "Use two retries.")
	for _, state := range []messages.State{messages.AtWorker, messages.HandedOff, messages.Admitted} {
		report("msg-answered", state, "")
	}
	report("msg-answered", messages.Replied, "Two retries, set in the budget.")
	send("msg-admitted", messages.KindInstruction, "Log the budget when it runs out.")
	for _, state := range []messages.State{messages.AtWorker, messages.HandedOff, messages.Admitted} {
		report("msg-admitted", state, "")
	}
	send("msg-refused", messages.KindInstruction, "Stop and wait for review.")
	for _, state := range []messages.State{messages.AtWorker, messages.HandedOff, messages.Refused} {
		report("msg-refused", state, "")
	}
	send("msg-queued", messages.KindInstruction, "Name the budget retry_budget.")

	rows, err := d.Client.LaneMessages(t.Context(), lane.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]messages.State{}
	for _, row := range rows {
		got[row.ID] = row.State
	}
	want := map[string]messages.State{
		"msg-expired": messages.Expired, "msg-answered": messages.Replied, "msg-admitted": messages.Admitted, "msg-refused": messages.Refused, "msg-queued": messages.Queued,
	}
	for id, state := range want {
		if got[id] != state {
			t.Errorf("%s is %q, want %q", id, got[id], state)
		}
	}
	holdsGolden(t, "lane-messages", steady(pretty(t, api.MessageList{Messages: rows})))

	none, err := d.Client.LaneMessages(t.Context(), quiet.ID)
	if err != nil || len(none) != 0 {
		t.Fatalf("a lane nobody wrote to lists %+v: %v", none, err)
	}
	holdsGolden(t, "lane-messages-empty", steady(pretty(t, api.MessageList{Messages: none})))
}

// TestGoldenLaneTelemetry writes what Usage reads of each lane's cost, from the route `telemetry lanes` reads: lanes
// with their tokens, the roster agents that spent some, and the cost their agents reported, a lower bound for a lane
// some of whose cost went unreported and exact for the other. A control plane with no telemetry store says so, and one
// with a store that holds nothing lists no lane.
func TestGoldenLaneTelemetry(t *testing.T) {
	t.Parallel()
	held := fake.NewTelemetry()
	held.Hold(nil, []ports.LaneTelemetry{
		{
			Lane: "lane-wo-running", ContextTokens: 84000, ContextAt: 1_790_000_900_000, LastAt: 1_790_000_950_000, CostUSD: 1.2345, LowerBound: true,
			Tokens: map[string]int64{"input": 120_400, "output": 18_250, "cache_read": 2_400_000, "cache_creation": 61_000},
			Agents: map[string]map[string]int64{"heavy-implementation": {"input": 100_000, "output": 15_000}, "reviewer": {"input": 20_400, "output": 3_250}},
		},
		{Lane: "lane-wo-parked", ContextTokens: 12000, ContextAt: 1_790_000_100_000, LastAt: 1_790_000_100_000, CostUSD: 0.003, Tokens: map[string]int64{"input": 3_000, "output": 400}},
	})
	d := apitest.StartWith(t, apitest.Options{Telemetry: held})
	read, err := d.Client.LaneTelemetry(t.Context())
	if err != nil || !read.Available || len(read.Lanes) != 2 {
		t.Fatalf("the lanes read %+v: %v", read, err)
	}
	if !read.Lanes[0].LowerBound || read.Lanes[1].LowerBound {
		t.Errorf("the lanes' lower bounds read %t and %t, want true and false", read.Lanes[0].LowerBound, read.Lanes[1].LowerBound)
	}
	holdsGolden(t, "telemetry-lanes", steady(pretty(t, read)))

	nothing, err := apitest.StartWith(t, apitest.Options{Telemetry: fake.NewTelemetry()}).Client.LaneTelemetry(t.Context())
	if err != nil || !nothing.Available || len(nothing.Lanes) != 0 {
		t.Fatalf("a store that holds nothing reads %+v: %v", nothing, err)
	}
	holdsGolden(t, "telemetry-lanes-empty", steady(pretty(t, nothing)))

	absent, err := apitest.Start(t).Client.LaneTelemetry(t.Context())
	if err != nil || absent.Available {
		t.Fatalf("a control plane with no store reads %+v: %v", absent, err)
	}
	holdsGolden(t, "telemetry-lanes-unavailable", steady(pretty(t, absent)))
}

// TestGoldenWorkerDrivers writes the shop floor of an installation whose workers each reported a different doctor
// result: a usable subscription, a usable API key on a version the matrix does not hold, no CLI, a CLI that failed
// its version check, a signed-out CLI, and a worker that reported no driver. The Workers view reads it.
func TestGoldenWorkerDrivers(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	w := apitest.NewWorld(t, d)
	driver := func(over func(*ledger.DriverManifest)) ledger.Manifest {
		one := ledger.DriverManifest{Driver: "claude-code", Present: true, Version: "2.1.316", Qualified: true, SignIn: ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedIn}}
		over(&one)
		return ledger.Manifest{Drivers: []ledger.DriverManifest{one}}
	}
	clock.Advance(time.Minute)
	w.Worker("worker-mint", "mint", driver(func(*ledger.DriverManifest) {}))
	clock.Advance(time.Minute)
	w.Worker("worker-pear", "pear", driver(func(one *ledger.DriverManifest) {
		one.Version, one.Qualified, one.SignIn = "2.2.0", false, ledger.SignIn{Kind: ledger.SignInAPIKey, Status: ledger.SignedIn}
	}))
	clock.Advance(time.Minute)
	w.Worker("worker-plum", "plum", driver(func(one *ledger.DriverManifest) {
		*one = ledger.DriverManifest{Driver: "claude-code", Detail: "claude is not on PATH"}
	}))
	clock.Advance(time.Minute)
	w.Worker("worker-fig", "fig", driver(func(one *ledger.DriverManifest) {
		*one = ledger.DriverManifest{Driver: "claude-code", Detail: "claude --version exited with status 1"}
	}))
	clock.Advance(time.Minute)
	w.Worker("worker-oak", "oak", driver(func(one *ledger.DriverManifest) { one.SignIn = ledger.SignIn{Status: ledger.SignedOut} }))
	holdsGolden(t, "shop-floor-drivers", steady(pretty(t, readAny(t, d, api.ViewShopFloor))))
}
