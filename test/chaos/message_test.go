//go:build chaos

package chaos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/adapters/workerlink"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/contract"
)

// The broken variants of a message's write, each of which the oracle below must catch.
const (
	sabotageResendsMessage = "a restart writes a message again"
	sabotageSplitsMessage  = "a message's write and what it proves in two journal writes"
)

// witnessWrite records one write of a message to a session, which nothing a worker wrote can
// vouch for: a kill can lose the record of it, and a session keeps it either way.
const witnessWrite = "message-write"

// messageProfile restarts nothing, so a session's own end cannot blur a count of its writes.
const messageProfile = "[lifecycle]\nmax_restarts = 0\n"

func applyMessageVariant(variant string) {
	switch variant {
	case sabotageResendsMessage:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageResendsMessage)
	case sabotageSplitsMessage:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageSplitsMessage)
	}
}

// messageDriver is a session that outlives its worker and holds a turn, so it can take a write at
// any moment and admits none: what a kill leaves is what the worker recorded, and nothing else.
type messageDriver struct {
	*lastingDriver
}

func (d *messageDriver) holding() fake.DriverScript {
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{{{Bound: true}, {Hold: true}}}}
}

func (d *messageDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	d.Play(d.holding())
	d.SetIgnoresPeers(true)
	return d.lastingDriver.Start(ctx, spec)
}

func (d *messageDriver) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	lane, generation := laneOf(h.ID), generationOf(h.ID)
	d.Play(d.holding())
	d.SetIgnoresPeers(true)
	d.Leave(lane, generation, lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true})
	return d.AgentDriver.Adopt(ctx, h, from)
}

func (d *messageDriver) Send(ctx context.Context, h ports.SessionHandle, in ports.Input) (ports.SendReceipt, error) {
	if in.Peer {
		if err := d.witness(witnessWrite, in.ID); err != nil {
			return ports.SendReceipt{}, err
		}
	}
	return d.AgentDriver.Send(ctx, h, in)
}

func laneOf(handle string) string {
	lane, _, _ := strings.Cut(handle, "/")
	return lane
}

// roleWorkerMessage runs one lane with one message to it, as passes of a daemon would. The claim,
// the dispatch, and the send are all safe to repeat, and the journal says how far a kill got.
func roleWorkerMessage(c *child) error {
	applyMessageVariant(c.spec.Variant)
	journal, err := sqlite.OpenJournal(c.spec.Journal)
	if err != nil {
		return err
	}
	defer journal.Close()
	if err := journal.Migrate(c.ctx); err != nil {
		return err
	}
	client := api.NewUnixClient(c.spec.Socket).Project(ledger.DefaultProjectName)
	defer client.Close()
	dir := filepath.Dir(c.spec.Journal)
	driver := &messageDriver{lastingDriver: &lastingDriver{AgentDriver: fake.NewAgentDriver(), dir: dir, log: c.log, journal: journal}}
	profiles := fake.NewProfileSource()
	profiles.PutUser(c.spec.Profile)
	subject := worker.New(workerlink.New(client.Client), journal, clock.System{}, worker.Config{
		Name:      "child",
		JoinToken: func(context.Context) (string, error) { return c.spec.Key, nil },
		Report:    func(err error) { fmt.Fprintln(os.Stderr, "worker:", err) },
		Lanes: &worker.LaneRuntime{
			Driver:      driver,
			Provisioner: worker.Provisioner{Profiles: profiles, Worktrees: lastingWorktrees{dir: dir, log: c.log}},
			Secrets:     fake.SecretResolver{},
			Repository:  func(context.Context, string, string) (string, error) { return "/repository", nil },
		},
	})
	// Run's own start is the only one this process makes, so it opens one epoch. A start of its own, to
	// learn the worker's ID, would open a second when Run began: a retry with nothing left to do ends Run
	// before it stored that epoch, and the closing upload went out under the first, which the ledger refuses.
	running, stop := context.WithCancel(c.ctx)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = subject.Run(running)
	}()
	err = serveMessage(c, subject, journal, client, ended)
	stop()
	<-ended
	if err != nil {
		return err
	}
	return subject.Upload(c.ctx)
}

// serveMessage claims and dispatches the lane once the worker has a session and an ID, then passes until
// the message has an end on record. A claim and a dispatch the first process made are refused again.
func serveMessage(c *child, subject *worker.Worker, journal *sqlite.Journal, client *api.ProjectClient, ended <-chan struct{}) error {
	if err := awaitSession(subject, ended); err != nil {
		return err
	}
	if _, err := apitest.SignIn(c.ctx, client); err != nil {
		return err
	}
	_, err := client.Claim(c.ctx, c.spec.WorkOrder, api.ClaimRequest{
		ClaimID: c.spec.ClaimID, WorkerID: subject.Auth().WorkerID, TTLMs: longTTL,
	})
	var refused *api.Error
	if err != nil && !errors.As(err, &refused) {
		return err
	}
	_, err = client.StartLane(c.ctx, c.spec.WorkOrder, api.StartLane{LaneID: c.spec.Lane})
	if err != nil && !errors.As(err, &refused) {
		return err
	}
	return messagePasses(c, subject, journal, client)
}

// awaitSession waits for the worker's first start to finish, which is when it holds its ID and its epoch.
func awaitSession(subject *worker.Worker, ended <-chan struct{}) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if subject.Session().Epoch != 0 {
			return nil
		}
		select {
		case <-ended:
			return errors.New("the worker ended before it opened a session")
		case <-time.After(5 * time.Millisecond):
		}
	}
	return errors.New("the worker opened no session")
}

// messagePasses waits on a daemon's own loop, until the message's effect has an end or the worker
// has marked one a kill left open, which it will not write again.
func messagePasses(c *child, subject *worker.Worker, journal *sqlite.Journal, client *api.ProjectClient) error {
	draft := messages.Draft{ID: c.spec.Message, To: messages.Target{Lane: c.spec.Lane}, Kind: messages.KindInstruction, Body: "Use two retries."}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := client.Lane(c.ctx, c.spec.Lane); err == nil {
			if _, err := client.SendMessage(c.ctx, draft); err != nil {
				return err
			}
		}
		if err := subject.Sync(c.ctx); err != nil {
			return err
		}
		if _, err := subject.Reconcile(c.ctx); err != nil {
			return err
		}
		effect, err := journal.Effect(c.ctx, messageEffect(c.spec.Lane, c.spec.Message))
		if err == nil && effect.State != lanes.EffectStarted {
			return nil
		}
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		// A variant keys its effects another way, and the message is what says it went out.
		if held, err := journal.Message(c.ctx, c.spec.Message); err == nil && held.State != messages.AtWorker {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("message %s has no end on record", c.spec.Message)
}

func messageEffect(lane, id string) string {
	return lanes.NewEffect(lanes.EffectInput, lane, 1, "", "message/"+id).Key
}

func messageSpec(w *world) (childSpec, error) {
	w.serveOnce()
	id, err := w.rig.Ready(w.store)
	if err != nil {
		return childSpec{}, err
	}
	spec, err := w.workerSpec("worker-message")
	spec.WorkOrder, spec.ClaimID, spec.Lane, spec.Profile, spec.Message = id, w.rig.ID("claim"), w.rig.ID("lane"), messageProfile, w.rig.ID("m")
	return spec, err
}

// writesOf counts what the log no process can rewrite holds of one message.
func writesOf(w *world, id string) (int, error) {
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return 0, contract.Violatedf("%v", err)
	}
	writes := 0
	for _, record := range records {
		if record.Op == witnessWrite && record.Key == messages.InputID(id) {
			writes++
		}
	}
	return writes, nil
}

// writtenOut is the oracle of a message's write, at every step of it. A session keeps what it
// took, so the log counts at most one write. What a worker recorded as done reads as handed off,
// in the journal and then in the ledger. Whatever a kill left unsaid, a sender learns: a message
// nobody can account for expires, because a write that may have happened is never repeated.
func writtenOut(w *world, spec childSpec) error {
	writes, err := writesOf(w, spec.Message)
	if err != nil {
		return err
	}
	if writes > 1 {
		return contract.Violatedf("%d writes of message %s", writes, spec.Message)
	}
	journal, err := sqlite.OpenJournal(spec.Journal)
	if err != nil {
		return err
	}
	defer journal.Close()
	effect, err := journal.Effect(w.rig.Ctx, messageEffect(spec.Lane, spec.Message))
	if err != nil {
		return contract.Violatedf("no effect: %v", err)
	}
	held, err := journal.Message(w.rig.Ctx, spec.Message)
	if err != nil {
		return contract.Violatedf("no message at the worker: %v", err)
	}
	if effect.State == lanes.EffectCompleted && held.State == messages.AtWorker {
		return contract.Violatedf("a write on record left its message unwritten: %+v", held)
	}
	ops := w.rig.NewOps(w.store)
	row, err := ops.ReadMessage(w.rig.Ctx, w.rig.Scope, spec.Message)
	if err != nil {
		return err
	}
	if effect.State == lanes.EffectCompleted && row.State != messages.HandedOff {
		return contract.Violatedf("a write on record reads %s in the ledger", row.State)
	}
	if row.State == messages.HandedOff && writes != 1 {
		return contract.Violatedf("a hand-off with %d writes", writes)
	}
	if row.State == messages.AtWorker {
		if err := comesDue(w, spec.Message); err != nil {
			return err
		}
	}
	return w.rig.CheckMessages(w.store)
}

// comesDue says the expiry tick will reach a message nobody can account for, which is how its
// sender learns. A crash run keeps real time, so it reads the list a later tick will read.
func comesDue(w *world, id string) error {
	return w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		due, err := tx.Messages().Due(now + (11 * time.Minute).Milliseconds())
		if err != nil {
			return err
		}
		if !slices.Contains(due, id) {
			return contract.Violatedf("no tick will tell a sender of message %s: %v", id, due)
		}
		return nil
	})
}

func workerMessageCase(name string, steps ...string) crashCase {
	return crashCase{name: name, op: "worker.message", steps: steps, prepare: messageSpec, settled: writtenOut}
}

func roleSendMessage(c *child) error {
	draft := messages.Draft{ID: c.spec.Message, To: messages.Target{Lane: c.spec.Lane}, Kind: messages.KindNotice, Body: "A notice."}
	_, err := c.ops.Send(c.ctx, c.scope, draft, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
	return err
}

func sendMessageCase() crashCase {
	return crashCase{
		name: "send a message", op: "message.send", steps: withSteps("inserted"),
		prepare: func(w *world) (childSpec, error) {
			holder, err := w.running(longTTL)
			if err != nil {
				return childSpec{}, err
			}
			spec := holderSpec("send-message", holder)
			spec.Message = w.rig.ID("m")
			return spec, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, messages.EventChanged, spec.Message, 1); err != nil {
				return err
			}
			return w.rig.CheckMessages(w.store)
		},
	}
}

func roleExpireMessages(c *child) error {
	_, err := c.ops.ExpireMessages(c.ctx, c.scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "tick"})
	return err
}

func expireMessagesCase() crashCase {
	return crashCase{
		name: "expire a message", op: "message.expire", steps: withSteps("listed"),
		prepare: func(w *world) (childSpec, error) {
			holder, err := w.running(longTTL)
			if err != nil {
				return childSpec{}, err
			}
			draft := messages.Draft{ID: w.rig.ID("m"), To: messages.Target{Lane: holder.Lane}, Kind: messages.KindNotice, Body: "Soon gone."}
			if _, err := control.NewMessages(w.store, messages.Windows{messages.KindNotice: {StaleAfter: time.Millisecond, ExpireAfter: 2 * time.Millisecond}}).
				Send(w.rig.Ctx, w.rig.Scope, draft, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			time.Sleep(10 * time.Millisecond)
			spec := holderSpec("expire-messages", holder)
			spec.Message = draft.ID
			return spec, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, messages.EventChanged, spec.Message, 2); err != nil {
				return err
			}
			row, err := w.rig.NewOps(w.store).ReadMessage(w.rig.Ctx, w.rig.Scope, spec.Message)
			if err != nil {
				return err
			}
			if row.State != messages.Expired || row.Reason != messages.ReasonTimedOut {
				return contract.Violatedf("%+v", row)
			}
			return w.rig.CheckMessages(w.store)
		},
	}
}

func roleRollMessages(c *child) error {
	_, err := c.ops.Handoff(c.ctx, c.scope, c.holder(), c.actor)
	return err
}

// rolloverCase kills the transaction that ends a generation between one message and the next, so
// a torn rollover would show as one moved and one not.
func rolloverCase() crashCase {
	return crashCase{
		name: "a generation's end, with messages on it", op: "message.rollover", steps: []string{"moved"},
		prepare: func(w *world) (childSpec, error) {
			holder, err := w.running(longTTL)
			if err != nil {
				return childSpec{}, err
			}
			ops := w.rig.NewOps(w.store)
			spec := holderSpec("roll-messages", holder)
			for _, kind := range []messages.Kind{messages.KindAnswer, messages.KindInstruction} {
				id := w.rig.ID("m")
				draft := messages.Draft{ID: id, To: messages.Target{Lane: holder.Lane}, Kind: kind, Body: "Before the end."}
				if _, err := ops.Send(w.rig.Ctx, w.rig.Scope, draft, w.rig.Actor); err != nil {
					return childSpec{}, err
				}
				if kind == messages.KindAnswer {
					spec.Message = id
				} else {
					spec.Decision = id
				}
			}
			return spec, nil
		},
		settled: func(w *world, spec childSpec) error {
			ops := w.rig.NewOps(w.store)
			fact, err := ops.ReadMessage(w.rig.Ctx, w.rig.Scope, spec.Message)
			if err != nil {
				return err
			}
			gone, err := ops.ReadMessage(w.rig.Ctx, w.rig.Scope, spec.Decision)
			if err != nil {
				return err
			}
			if fact.Reason != messages.ReasonLedgerFact || gone.Reason != messages.ReasonGenerationEnded {
				return contract.Violatedf("a rollover left %+v and %+v", fact, gone)
			}
			return w.rig.CheckMessages(w.store)
		},
	}
}

// messageCases kills every step of a message, from the ledger's first write to the worker's last.
func messageCases() []crashCase {
	return []crashCase{
		sendMessageCase(),
		expireMessagesCase(),
		rolloverCase(),
		workerMessageCase("a message's write", "taken", "begun", "written", "recorded"),
	}
}

func messageVariants() []variant {
	crash := func(name, step string) func() (crashCase, string) {
		return func() (crashCase, string) { return workerMessageCase(name, step), step }
	}
	return []variant{
		{name: sabotageResendsMessage, check: "crash", crash: crash("a second write", "written")},
		{name: sabotageSplitsMessage, check: "crash", crash: crash("a split record", "split")},
		{name: "a generation's end that leaves its messages", control: control.Sabotage{LeavesMessages: true}, check: "M14"},
		{name: "a stale tick that moves a settled message", control: control.Sabotage{ExpiresSettled: true}, check: "M11"},
		{name: "a worker that speaks for any lane", control: control.Sabotage{ReadsAnyLane: true}, check: "M8"},
		{
			name:     "a move that does not read the state it left",
			sabotage: sqlstore.Sabotage{Rewrite: replace("message.save", " AND state = :from_state", " AND :from_state = :from_state")}, check: "M12",
		},
		{
			name:     "a generation's end that decides only its own messages",
			sabotage: sqlstore.Sabotage{Rewrite: replace("message.unsettled", "AND state IN ('queued', 'at_worker', 'handed_off')", "AND state = 'queued'")}, check: "M14",
		},
		{
			name:     "a tick that expires what a session took",
			sabotage: sqlstore.Sabotage{Rewrite: replace("message.due", "AND state IN ('queued', 'at_worker', 'handed_off')", "AND state IS NOT NULL")}, check: "M10",
		},
		{
			name:     "a message offered past its acknowledgement",
			sabotage: sqlstore.Sabotage{Rewrite: replace("message.queued", "AND state = 'queued'", "AND state IN ('queued', 'at_worker')")}, check: "M5",
		},
		{
			name:     "a fact a binding settled, stated again",
			sabotage: sqlstore.Sabotage{Rewrite: replace("message.facts", "AND carried_in IS NULL", "")}, check: "M16",
		},
		{
			name:     "a binding that forgets what it carried",
			sabotage: sqlstore.Sabotage{Rewrite: replace("message.save", "carried_in = CAST(:carried_in AS BIGINT)", "carried_in = carried_in + 0 * CAST(:carried_in AS BIGINT)")}, check: "M16",
		},
		{
			name:     "a live generation told of its own message",
			sabotage: sqlstore.Sabotage{Rewrite: replace("message.facts", "to_generation <= :ended", "to_generation <= :ended + 1")}, check: "M17",
		},
		{
			name:     "a sequence every sender shares",
			sabotage: sqlstore.Sabotage{Rewrite: replace("message.next_seq", " AND from_kind = :from_kind AND from_id = :from_id", " AND :from_kind = :from_kind AND :from_id = :from_id")}, check: "M2",
		},
	}
}
