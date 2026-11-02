package contract

import (
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// AutonomyChecks are what the ledger promises about a worker's autonomy (FR-ORCH-6). The claim table
// runs every level and window through the one transaction every claim takes, and the first checks
// write past every use case, because the refusal is the store's.
var AutonomyChecks = []Check{
	{"AU1", "the store refuses a run window no member opened", checkStoreRefusesAgentWindow},
	{"AU2", "the store refuses an acknowledgement no member made", checkStoreRefusesAgentAcknowledgement},
	{"AU3", "no principal but a member opens, closes, or acknowledges anything", checkOnlyAMemberHoldsAutonomy},
	{"AU4", "a claim follows the level and the window", checkClaimTable},
	{"AU5", "a member claims at any level", checkMemberClaimsAtAnyLevel},
	{"AU6", "a window that closed by itself or by a member admits nothing", checkWindowEnds},
	{"AU7", "unattended on a subscription needs a member's word", checkUnattendedNeedsAcknowledgement},
	{"AU8", "a worker that came back on a subscription falls back at the claim", checkSignInChangeFallsBack},
	{"AU9", "an acknowledgement of other text is refused", checkStaleStatement},
	{"AU10", "work a wake brought back goes on outside a window", checkWokenWorkIgnoresWindow},
	{"AU11", "attended holds a worker to the policy's lanes", checkAttendedBoundsLanes},
	{"AU12", "a claim that was granted stands when its window closes", checkGrantedClaimReplays},
	{"AU13", "every change replays from the log", checkAutonomyReplays},
	{"AU14", "an upload cannot make a window or an acknowledgement", checkUploadForgesNothing},
	{"AU15", "a retry opens one window, and a second is refused while one stands", checkWindowRetry},
	{"AU16", "only a member publishes the policy, and a default is no acknowledgement", checkOnlyMemberPublishesAutonomy},
	{"AU17", "a wake reads no work this worker cannot claim", checkDigestFollowsStanding},
	{"AU18", "an orchestrator at manual works only the ladder", checkManualKeepsToTheLadder},
	{"AU19", "a woken lane past the day's respawns waits for a member", checkWokenLaneIsBounded},
	{"AU20", "an orchestrator's respawn past the day's bound is refused", checkOrchestratorRespawnIsBounded},
	{"AU21", "a worker reads the bound its level holds it to", checkDesiredCarriesTheBound},
	{"AU22", "a new window counts what the last one left", checkAWindowCountsWhatTheLastLeft},
}

// subscriptionManifest is a worker on a person's own sign-in.
var subscriptionManifest = ledger.Manifest{Drivers: []ledger.DriverManifest{{
	Driver: "claude-code", Present: true, Qualified: true, SignIn: ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedIn},
}}}

var theAgents = []ledger.Actor{
	{Kind: ledger.ActorLane, ID: "lane-1"}, {Kind: ledger.ActorOrchestrator, ID: "worker-1"},
	{Kind: ledger.ActorWorker, ID: "worker-1"}, {Kind: ledger.ActorSystem, ID: "tick"},
}

// autonomyRig is one registered worker, on a sign-in a check chooses.
type autonomyRig struct {
	r     *Rig
	store ports.Store
	plane *control.Workers
	ops   *control.Autonomy
	auth  ledger.WorkerAuth
	epoch int64
}

func newAutonomyRig(r *Rig, manifest ledger.Manifest) (*autonomyRig, error) {
	a := &autonomyRig{r: r, store: r.Store()}
	a.plane = control.NewWorkers(a.store, r.Scope, control.WorkerOptions{HeartbeatMs: statusHeartbeatMs, LeaseTTLMs: TTLMs})
	a.ops = control.NewAutonomy(a.store)
	token, err := a.plane.CreateJoinToken(r.Ctx, theMember)
	if err != nil {
		return nil, err
	}
	worker := r.ID("worker")
	credential, err := a.plane.Register(r.Ctx, ledger.Registration{JoinToken: token, WorkerID: worker, Name: "w"})
	if err != nil {
		return nil, err
	}
	a.auth = ledger.WorkerAuth{WorkerID: worker, Credential: credential.Credential}
	return a, a.signIn(manifest)
}

// signIn opens a session on a manifest, which is how a worker says what it stands on.
func (a *autonomyRig) signIn(manifest ledger.Manifest) error {
	session, err := a.plane.OpenSession(a.r.Ctx, ledger.SessionRequest{Auth: a.auth, Protocol: ledger.ProtocolVersion, Manifest: manifest})
	a.epoch = session.Epoch
	return err
}

func (a *autonomyRig) worker() string { return a.auth.WorkerID }

func (a *autonomyRig) claim(id string) error {
	_, err := a.plane.Claim(a.r.Ctx, a.auth, workorders.ClaimRequest{WorkOrder: id, ClaimID: a.r.ID("claim"), TTLMs: TTLMs})
	return err
}

func (a *autonomyRig) open() error {
	_, err := a.ops.OpenWindow(a.r.Ctx, a.r.Scope, a.worker(), a.r.ID("window"), 0, theMember)
	return err
}

func (a *autonomyRig) set(level authority.Level) error {
	return a.ops.SetLevel(a.r.Ctx, a.r.Scope, a.worker(), level, theMember)
}

func (a *autonomyRig) acknowledge() error {
	return a.ops.AcknowledgeRisk(a.r.Ctx, a.r.Scope, a.worker(), authority.RiskDigest(), theMember)
}

// past is a write past every use case, with the event every transaction needs, so that only a rule
// of the store's own can refuse it.
func (a *autonomyRig) past(write func(ports.WriteTx) error) error {
	return a.store.Write(a.r.Ctx, a.r.Scope, "past-the-use-case", func(tx ports.WriteTx) error {
		if err := write(tx); err != nil {
			return err
		}
		_, err := tx.Append(a.r.noteDraft(a.r.ID("note")))
		return err
	})
}

func checkStoreRefusesAgentWindow(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	window := func(actor ledger.Actor) func(ports.WriteTx) error {
		return func(tx ports.WriteTx) error {
			return tx.Workers().InsertWindow(authority.RunWindow{
				ID: r.ID("forged"), WorkerID: a.worker(), OpenedBy: actor.ID, OpenedKind: string(actor.Kind), OpenedAt: tx.Now(), ClosesAt: tx.Now() + 60_000,
			})
		}
	}
	for _, agent := range theAgents {
		if err := a.past(window(agent)); err == nil {
			return Violatedf("the store took a window from %s", agent.Kind)
		}
	}
	if err := a.past(window(theMember)); err != nil {
		return fmt.Errorf("the same write from a member failed, so nothing above proved a refusal: %w", err)
	}
	return nil
}

func checkStoreRefusesAgentAcknowledgement(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	acknowledgement := func(actor ledger.Actor) func(ports.WriteTx) error {
		return func(tx ports.WriteTx) error {
			return tx.Workers().SaveAutonomy(authority.WorkerAutonomy{
				WorkerID: a.worker(), Level: authority.LevelUnattended, AcknowledgedBy: actor.ID,
				AcknowledgedKind: string(actor.Kind), AcknowledgedDigest: authority.RiskDigest(), AcknowledgedAt: tx.Now(),
			})
		}
	}
	for _, agent := range theAgents {
		if err := a.past(acknowledgement(agent)); err == nil {
			return Violatedf("the store took an acknowledgement from %s", agent.Kind)
		}
	}
	if err := a.past(acknowledgement(theMember)); err != nil {
		return fmt.Errorf("the same write from a member failed, so nothing above proved a refusal: %w", err)
	}
	return nil
}

func checkOnlyAMemberHoldsAutonomy(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	if err := a.open(); err != nil {
		return err
	}
	for _, agent := range theAgents {
		attempts := map[string]func() error{
			"open": func() error {
				_, err := a.ops.OpenWindow(r.Ctx, r.Scope, a.worker(), r.ID("window"), 0, agent)
				return err
			},
			"close":       func() error { return a.ops.CloseWindow(r.Ctx, r.Scope, a.worker(), agent) },
			"acknowledge": func() error { return a.ops.AcknowledgeRisk(r.Ctx, r.Scope, a.worker(), authority.RiskDigest(), agent) },
			"set":         func() error { return a.ops.SetLevel(r.Ctx, r.Scope, a.worker(), authority.LevelManual, agent) },
		}
		for name, attempt := range attempts {
			if err := attempt(); !errors.Is(err, workorders.ErrForbidden) {
				return Violatedf("%s may %s: %v", agent.Kind, name, err)
			}
		}
	}
	view, err := a.ops.View(r.Ctx, r.Scope, a.worker())
	if err != nil {
		return err
	}
	if view.Window == nil || view.Chosen != "" || view.Acknowledged {
		return Violatedf("a refusal changed something: %+v", view)
	}
	return nil
}

func checkClaimTable(r *Rig) error {
	cases := []struct {
		name     string
		manifest ledger.Manifest
		setup    func(*autonomyRig) error
		want     bool
	}{
		{"a subscription with no window", subscriptionManifest, func(*autonomyRig) error { return nil }, false},
		{"a subscription inside a window", subscriptionManifest, (*autonomyRig).open, true},
		{"a key with no window", UsableManifest, func(*autonomyRig) error { return nil }, true},
		{"manual on a key", UsableManifest, func(a *autonomyRig) error { return a.set(authority.LevelManual) }, false},
		{"manual inside a window", UsableManifest, func(a *autonomyRig) error {
			if err := a.set(authority.LevelManual); err != nil {
				return err
			}
			return a.open()
		}, false},
		{"attended on a key with no window", UsableManifest, func(a *autonomyRig) error { return a.set(authority.LevelAttended) }, false},
		{"attended on a key inside a window", UsableManifest, func(a *autonomyRig) error {
			if err := a.set(authority.LevelAttended); err != nil {
				return err
			}
			return a.open()
		}, true},
		{"unattended on a subscription with the risk acknowledged", subscriptionManifest, func(a *autonomyRig) error {
			if err := a.acknowledge(); err != nil {
				return err
			}
			return a.set(authority.LevelUnattended)
		}, true},
		{"the risk acknowledged, and no level chosen", subscriptionManifest, (*autonomyRig).acknowledge, false},
	}
	for _, tt := range cases {
		a, err := newAutonomyRig(r, tt.manifest)
		if err != nil {
			return err
		}
		id, err := r.Ready(a.store)
		if err != nil {
			return err
		}
		if err := tt.setup(a); err != nil {
			return fmt.Errorf("%s: %w", tt.name, err)
		}
		err = a.claim(id)
		switch {
		case tt.want && err != nil:
			return Violatedf("%s: refused: %v", tt.name, err)
		case !tt.want && !errors.Is(err, workorders.ErrIneligible):
			return Violatedf("%s: claimed: %v", tt.name, err)
		}
	}
	return nil
}

func checkMemberClaimsAtAnyLevel(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	if err := a.set(authority.LevelManual); err != nil {
		return err
	}
	id, err := r.Ready(a.store)
	if err != nil {
		return err
	}
	_, err = r.NewOps(a.store).Claim(r.Ctx, r.Scope, r.Request(id, a.worker()), theMember)
	if err != nil {
		return Violatedf("a member's claim was held to a level: %v", err)
	}
	return nil
}

func checkWindowEnds(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	if _, err := a.ops.OpenWindow(r.Ctx, r.Scope, a.worker(), r.ID("window"), 1000, theMember); err != nil {
		return err
	}
	first, err := r.Ready(a.store)
	if err != nil {
		return err
	}
	if r.DB.ShiftClock == nil {
		return nil
	}
	r.DB.ShiftClock(1000)
	if err := a.claim(first); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("a window past its end admitted a claim: %v", err)
	}
	if err := a.open(); err != nil {
		return err
	}
	if err := a.ops.CloseWindow(r.Ctx, r.Scope, a.worker(), theMember); err != nil {
		return err
	}
	if err := a.claim(first); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("a window a member closed admitted a claim: %v", err)
	}
	return nil
}

func checkUnattendedNeedsAcknowledgement(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	if err := a.set(authority.LevelUnattended); !errors.Is(err, authority.ErrRiskNotAcknowledged) {
		return Violatedf("no acknowledgement, and the level took: %v", err)
	}
	if err := a.acknowledge(); err != nil {
		return err
	}
	if err := a.set(authority.LevelUnattended); err != nil {
		return err
	}
	view, err := a.ops.View(r.Ctx, r.Scope, a.worker())
	if err != nil {
		return err
	}
	if view.Level != authority.LevelUnattended || !view.Acknowledged {
		return Violatedf("after an acknowledgement: %+v", view)
	}
	return nil
}

// checkSignInChangeFallsBack needs the claim's own guard: the level was chosen on a key, so SetLevel
// had no reason to refuse it, and only the claim can see what the worker stands on now.
func checkSignInChangeFallsBack(r *Rig) error {
	a, err := newAutonomyRig(r, UsableManifest)
	if err != nil {
		return err
	}
	if err := a.set(authority.LevelUnattended); err != nil {
		return err
	}
	if err := a.signIn(subscriptionManifest); err != nil {
		return err
	}
	id, err := r.Ready(a.store)
	if err != nil {
		return err
	}
	if err := a.claim(id); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("a level chosen on a key held on a subscription: %v", err)
	}
	return nil
}

func checkStaleStatement(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	if err := a.ops.AcknowledgeRisk(r.Ctx, r.Scope, a.worker(), "sha256:another", theMember); !errors.Is(err, authority.ErrStaleStatement) {
		return Violatedf("another text was acknowledged: %v", err)
	}
	return nil
}

func checkWokenWorkIgnoresWindow(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	parked, _, err := s.parkedOnLimit(0)
	if err != nil {
		return err
	}
	if err := accepted(s.signalUpload(parking.UsageReset, s.key())); err != nil {
		return err
	}
	a := &autonomyRig{r: r, store: s.store, plane: s.plane, ops: control.NewAutonomy(s.store), auth: s.auth, epoch: s.epoch}
	if err := a.set(authority.LevelAttended); err != nil {
		return err
	}
	fresh, err := r.Ready(a.store)
	if err != nil {
		return err
	}
	if err := a.claim(fresh); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("new work outside a window: %v", err)
	}
	if err := a.claim(parked.grant.WorkOrder); err != nil {
		return Violatedf("a woken lane stayed down outside a window: %v", err)
	}
	return nil
}

// respawnsAtOne holds a worker at one generation a day, which the lane this rig runs has used.
func (a *autonomyRig) respawnsAtOne() error {
	bound := authority.DefaultAutonomyPolicy()
	bound.Version, bound.AttendedMaxRespawnsPerDay = 2, 1
	return a.publish(bound)
}

func checkWokenLaneIsBounded(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	parked, _, err := s.parkedOnLimit(0)
	if err != nil {
		return err
	}
	if err := accepted(s.signalUpload(parking.UsageReset, s.key())); err != nil {
		return err
	}
	a := &autonomyRig{r: r, store: s.store, plane: s.plane, ops: control.NewAutonomy(s.store), auth: s.auth, epoch: s.epoch}
	if err := a.set(authority.LevelAttended); err != nil {
		return err
	}
	if err := a.respawnsAtOne(); err != nil {
		return err
	}
	if err := a.claim(parked.grant.WorkOrder); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("a woken lane went past the day's respawns: %v", err)
	}
	if _, err := r.NewOps(s.store).Claim(r.Ctx, r.Scope, r.Request(parked.grant.WorkOrder, s.auth.WorkerID), theMember); err != nil {
		return Violatedf("a member could not bring it back: %v", err)
	}
	return nil
}

func checkOrchestratorRespawnIsBounded(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if _, err := s.plane.SetOrchestrator(r.Ctx, s.auth.WorkerID, ledger.OrchestratorRunning, theMember); err != nil {
		return err
	}
	a := &autonomyRig{r: r, store: s.store, plane: s.plane, ops: control.NewAutonomy(s.store), auth: s.auth, epoch: s.epoch}
	if err := a.set(authority.LevelAttended); err != nil {
		return err
	}
	if err := a.respawnsAtOne(); err != nil {
		return err
	}
	lane := ledger.Orchestrator{ID: ledger.OrchestratorOf(a.worker()), Run: 1}.Lane()
	call := ledger.OrchestratorCall{Auth: a.auth, Epoch: a.epoch, Lane: lane, WorkOrder: s.grant.WorkOrder}
	if _, err := a.plane.Orchestrate(r.Ctx, ledger.VerbStop, call); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("a respawn went past the day's bound: %v", err)
	}
	if err := control.NewLeases(s.store).RespawnLane(r.Ctx, r.Scope, s.grant.WorkOrder, theMember); err != nil {
		return Violatedf("a member's respawn was held to it: %v", err)
	}
	return nil
}

func checkDesiredCarriesTheBound(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	read := func() (ledger.DesiredState, error) {
		return a.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: a.auth})
	}
	state, err := read()
	if err != nil {
		return err
	}
	if want := authority.DefaultAutonomyPolicy().AttendedMaxRespawnsPerDay; state.MaxRespawnsPerDay != want {
		return Violatedf("an attended worker reads %d, want %d", state.MaxRespawnsPerDay, want)
	}
	keyed, err := newAutonomyRig(r, UsableManifest)
	if err != nil {
		return err
	}
	state, err = keyed.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: keyed.auth})
	if err != nil {
		return err
	}
	if state.MaxRespawnsPerDay != 0 {
		return Violatedf("an unattended worker reads a bound of %d", state.MaxRespawnsPerDay)
	}
	return nil
}

func checkAttendedBoundsLanes(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	if err := a.open(); err != nil {
		return err
	}
	bound := authority.DefaultAutonomyPolicy()
	bound.Version, bound.AttendedMaxLanes = 2, 1
	if err := a.publish(bound); err != nil {
		return err
	}
	first, err := r.Ready(a.store)
	if err != nil {
		return err
	}
	second, err := r.Ready(a.store)
	if err != nil {
		return err
	}
	if err := a.claim(first); err != nil {
		return err
	}
	if err := a.claim(second); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("a second lane past the bound: %v", err)
	}
	digest, err := a.plane.WakeDigest(r.Ctx, ledger.WakeRequest{Auth: a.auth, Epoch: a.epoch})
	if err != nil {
		return err
	}
	if len(digest.Items) != 0 {
		return Violatedf("a wake for work past the bound: %+v", digest.Items)
	}
	return nil
}

// checkAWindowCountsWhatTheLastLeft holds a person's window to what they opened it for: work an
// orchestrator passed on in one window is news again in the next, and an item that kept its stamp
// would never wake it.
func checkAWindowCountsWhatTheLastLeft(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	if r.DB.ShiftClock == nil {
		return nil
	}
	if _, err := r.Ready(a.store); err != nil {
		return err
	}
	key := func() (string, error) {
		digest, err := a.plane.WakeDigest(r.Ctx, ledger.WakeRequest{Auth: a.auth, Epoch: a.epoch})
		if err != nil {
			return "", err
		}
		if len(digest.Items) != 1 {
			return "", Violatedf("digest %+v", digest.Items)
		}
		return digest.Items[0].Key(), nil
	}
	if err := a.open(); err != nil {
		return err
	}
	first, err := key()
	if err != nil {
		return err
	}
	if err := a.ops.CloseWindow(r.Ctx, r.Scope, a.worker(), theMember); err != nil {
		return err
	}
	r.DB.ShiftClock(1000)
	if err := a.open(); err != nil {
		return err
	}
	second, err := key()
	if err != nil {
		return err
	}
	if first == second {
		return Violatedf("two windows read one item: %s", first)
	}
	return nil
}

func (a *autonomyRig) publish(policy authority.AutonomyPolicy) error {
	body := fmt.Sprintf("[autonomy_policy]\nversion = %d\n", policy.Version)
	_, err := a.ops.PublishAutonomyPolicy(a.r.Ctx, a.r.Scope, authority.AutonomyDraft{Body: body, Policy: policy}, theMember)
	return err
}

func checkGrantedClaimReplays(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	if err := a.open(); err != nil {
		return err
	}
	id, err := r.Ready(a.store)
	if err != nil {
		return err
	}
	request := workorders.ClaimRequest{WorkOrder: id, ClaimID: r.ID("claim"), TTLMs: TTLMs}
	if _, err := a.plane.Claim(r.Ctx, a.auth, request); err != nil {
		return err
	}
	if err := a.ops.CloseWindow(r.Ctx, r.Scope, a.worker(), theMember); err != nil {
		return err
	}
	grant, err := a.plane.Claim(r.Ctx, a.auth, request)
	if err != nil || !grant.Replayed {
		return Violatedf("a retry of a granted claim: %+v, %v", grant, err)
	}
	return nil
}

func checkAutonomyReplays(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	for _, step := range []func() error{
		a.acknowledge, func() error { return a.set(authority.LevelUnattended) }, a.open,
		func() error { return a.ops.CloseWindow(r.Ctx, r.Scope, a.worker(), theMember) },
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return r.CheckAutonomy(a.store)
}

// CheckAutonomy compares what a store holds of every worker with what the log replays to, so a row
// no event stands behind is a violation.
func (r *Rig) CheckAutonomy(s ports.Store) error {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	settings, windows, err := authority.ReplayAutonomy(events)
	if err != nil {
		return Violatedf("the log does not replay: %v", err)
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		workers, err := tx.Workers().List()
		if err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		for _, worker := range workers {
			row, err := tx.Workers().Autonomy(worker.ID)
			if err != nil {
				return err
			}
			want := settings[worker.ID]
			want.WorkerID = worker.ID
			if row != want {
				return Violatedf("worker %s reads %+v and the log %+v", worker.ID, row, want)
			}
			open, err := tx.Workers().OpenWindow(worker.ID, now)
			if errors.Is(err, ports.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if _, known := windows[open.ID]; !known {
				return Violatedf("window %s has no history", open.ID)
			}
		}
		for id, replayed := range windows {
			stored, err := tx.Workers().Window(id)
			if err != nil {
				return Violatedf("the log holds window %s and the store does not: %v", id, err)
			}
			if stored != replayed {
				return Violatedf("window %s reads %+v and the log %+v", id, stored, replayed)
			}
		}
		return nil
	})
}

func checkUploadForgesNothing(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	forged := authority.RunWindow{ID: "forged", WorkerID: s.auth.WorkerID, OpenedBy: "member-1", OpenedKind: "member", OpenedAt: 1, ClosesAt: 1 << 62}
	acknowledged := authority.WorkerAutonomy{
		WorkerID: s.auth.WorkerID, Level: authority.LevelUnattended, AcknowledgedBy: "member-1", AcknowledgedKind: "member",
		AcknowledgedDigest: authority.RiskDigest(), AcknowledgedAt: 1,
	}
	for kind, record := range map[string]any{authority.EventWindowOpened: forged, authority.EventRiskAcknowledged: acknowledged, authority.EventAutonomySet: acknowledged} {
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if _, err := s.upload(kind, raw); err != nil {
			return err
		}
	}
	view, err := control.NewAutonomy(s.store).View(r.Ctx, r.Scope, s.auth.WorkerID)
	if err != nil {
		return err
	}
	if view.Window != nil || view.Acknowledged || view.Chosen != "" {
		return Violatedf("an upload changed a worker's autonomy: %+v", view)
	}
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	settings, windows, err := authority.ReplayAutonomy(events)
	if err != nil {
		return err
	}
	if len(settings) != 0 || len(windows) != 0 {
		return Violatedf("a replay reads a forged event: %+v %+v", settings, windows)
	}
	return nil
}

func checkWindowRetry(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	id := r.ID("window")
	first, err := a.ops.OpenWindow(r.Ctx, r.Scope, a.worker(), id, 0, theMember)
	if err != nil {
		return err
	}
	again, err := a.ops.OpenWindow(r.Ctx, r.Scope, a.worker(), id, 0, theMember)
	if err != nil || again != first {
		return Violatedf("a retry: %+v, %v", again, err)
	}
	if err := a.open(); !errors.Is(err, authority.ErrWindowOpen) {
		return Violatedf("a second window: %v", err)
	}
	return nil
}

func checkOnlyMemberPublishesAutonomy(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	open := authority.DefaultAutonomyPolicy()
	open.Version, open.SubscriptionDefault = 2, authority.LevelUnattended
	for _, agent := range theAgents {
		draft := authority.AutonomyDraft{Body: "[autonomy_policy]\nversion = 2\n", Policy: open}
		if _, err := a.ops.PublishAutonomyPolicy(r.Ctx, r.Scope, draft, agent); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s published a policy: %v", agent.Kind, err)
		}
	}
	if err := a.publish(open); err != nil {
		return err
	}
	id, err := r.Ready(a.store)
	if err != nil {
		return err
	}
	if err := a.claim(id); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("a policy default stood for an acknowledgement: %v", err)
	}
	return nil
}

func checkDigestFollowsStanding(r *Rig) error {
	a, err := newAutonomyRig(r, subscriptionManifest)
	if err != nil {
		return err
	}
	if _, err := r.Ready(a.store); err != nil {
		return err
	}
	count := func() (int, error) {
		digest, err := a.plane.WakeDigest(r.Ctx, ledger.WakeRequest{Auth: a.auth, Epoch: a.epoch})
		return len(digest.Items), err
	}
	if items, err := count(); err != nil || items != 0 {
		return Violatedf("a wake for work nothing can claim: %d, %v", items, err)
	}
	if err := a.open(); err != nil {
		return err
	}
	if items, err := count(); err != nil || items != 1 {
		return Violatedf("a window opened and no wake came: %d, %v", items, err)
	}
	return nil
}

func checkManualKeepsToTheLadder(r *Rig) error {
	a, err := newAutonomyRig(r, UsableManifest)
	if err != nil {
		return err
	}
	if err := a.set(authority.LevelManual); err != nil {
		return err
	}
	id, err := r.Ready(a.store)
	if err != nil {
		return err
	}
	if _, err := a.plane.SetOrchestrator(r.Ctx, a.worker(), ledger.OrchestratorRunning, theMember); err != nil {
		return err
	}
	lane := ledger.Orchestrator{ID: ledger.OrchestratorOf(a.worker()), Run: 1}.Lane()
	call := ledger.OrchestratorCall{Auth: a.auth, Epoch: a.epoch, Lane: lane, WorkOrder: id, Target: r.ID("target")}
	offered, err := a.plane.Orchestrate(r.Ctx, ledger.VerbCandidates, call)
	if err != nil {
		return err
	}
	var candidates []workorders.WorkOrder
	if err := json.Unmarshal(offered.Result, &candidates); err != nil {
		return err
	}
	if len(candidates) != 0 {
		return Violatedf("candidates at manual: %+v", candidates)
	}
	if _, err := r.NewOps(a.store).Claim(r.Ctx, r.Scope, r.Request(id, a.worker()), theMember); err != nil {
		return err
	}
	for _, verb := range []string{ledger.VerbClaim, ledger.VerbStart, ledger.VerbStop, ledger.VerbAccept} {
		if _, err := a.plane.Orchestrate(r.Ctx, verb, call); !errors.Is(err, workorders.ErrIneligible) {
			return Violatedf("%s at manual: %v", verb, err)
		}
	}
	return nil
}
