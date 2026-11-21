package ledger

import (
	"errors"
	"regexp"
	"strings"
)

// The freshness every state carries when it is read (FR-LANE-6, FR-UI-5).
const (
	// FreshnessObserved means a worker confirmed the value within the freshness window.
	FreshnessObserved = "observed"
	// FreshnessLastReported means it is the last value received.
	FreshnessLastReported = "last_reported"
)

// SubjectLane is what a lane's own events are about.
const SubjectLane = "lane"

// EventLaneStatus records a lane's status changing.
const EventLaneStatus = "lane.status"

// EventLaneReset records a member resetting a lane's breaker.
const EventLaneReset = "lane.reset"

// EventLaneRespawn records a request for a lane's next generation.
const EventLaneRespawn = "lane.respawn"

// EventLaneAffinityEased records that a preferred worker's first claim on a woken lane ran out,
// so any capable worker can take it. Its revision is how those workers hear of that.
const EventLaneAffinityEased = "lane.affinity_eased"

// The events of a lane's stop and removal.
const (
	// EventLaneEnded records the end of a lane whose work a member canceled, which belongs to the
	// lane and to no work order: a work order that canceled has no later state to reach.
	EventLaneEnded = "lane.ended"
	// EventLaneGuarded records how a stop left a worktree. It is the one thing a fenced
	// holder may still say, because it adds no ownership and keeps a person's work in view.
	EventLaneGuarded = "lane.guarded"
	// EventLaneArchiveRequested records a request to take a worktree away.
	EventLaneArchiveRequested = "lane.archive_requested"
	// EventLaneArchived records that a worktree went.
	EventLaneArchived = "lane.archived"
)

// The kinds of artifact a lane leaves. A later unit adds the others.
const (
	// ArtifactSalvage is a ref a stop left.
	ArtifactSalvage = "salvage"
	// ArtifactBranch is the final artifact: a member merges it, and ropeshift does not (D17).
	ArtifactBranch = "branch"
	// ArtifactImport is the merge commit the worker made on an adopt lane's branch to import a design repository's
	// history. Its digest is the commit.
	ArtifactImport = "import"
	// ArtifactReport is what a lane's report named.
	ArtifactReport = "report"
	// ArtifactHandoff is a handoff document, and its body lives in the ledger.
	ArtifactHandoff = "handoff"
)

// Artifact is something a lane left. URI names it, and Digest pins its content. Fence names
// the attempt that left it, and a stop's salvage names none.
type Artifact struct {
	ID     string `json:"id"`
	Lane   string `json:"lane_id"`
	Kind   string `json:"kind"`
	URI    string `json:"uri"`
	Digest string `json:"digest,omitempty"`
	Fence  int64  `json:"fence,omitempty"`
	// Change is what a branch changed since its lane's base commit, as the lane's worker measured it, and
	// absent where nothing was measured.
	Change    *ChangeSize `json:"change,omitempty"`
	CreatedAt int64       `json:"created_at"`
}

// ChangeSize is what a lane's branch changed between its base commit and its final commit (FR-OBS-13): the
// files it changed and the lines it added and removed, a binary file counting as a changed file with no
// lines.
type ChangeSize struct {
	Files   int64 `json:"files"`
	Added   int64 `json:"added"`
	Removed int64 `json:"removed"`
}

// LaneGuard is what a worker says about how a stop left a worktree.
type LaneGuard struct {
	UnpushedWork bool   `json:"unpushed_work"`
	Ref          string `json:"ref,omitempty"`
	Commit       string `json:"commit,omitempty"`
	Detail       string `json:"detail,omitempty"`
	// LeftOut names each changed or untracked path the lane's session did not make, which the guard left out of
	// every snapshot of the lane's work and the lane did not commit, and LeftOutMore counts those past the ones
	// named (FR-LANE-20). LeftRef is the ref on the worker's machine that keeps their files, which is never
	// pushed. Unasked names the paths the control plane could not make a question of.
	LeftOut     []string `json:"left_out,omitempty"`
	LeftOutMore int64    `json:"left_out_more,omitempty"`
	LeftRef     string   `json:"left_ref,omitempty"`
	Unasked     []string `json:"unasked,omitempty"`
}

// StateEnded is a lane's last state, which the ledger records for itself.
const StateEnded = "ended"

// StateParked is a lane with no process, which the ledger also records for itself.
const StateParked = "parked"

// LaneStatus is what a worker says about a lane. Every field derives from what happened,
// and no client of the API can write one (FR-LANE-5).
type LaneStatus struct {
	ActualState string   `json:"actual_state"`
	Conditions  []string `json:"conditions,omitempty"`
	Liveness    string   `json:"liveness"`
	// ModelReach and LedgerReach are the lane's own view outward, apart from whether its
	// machine is healthy (FR-LANE-7).
	ModelReach  string `json:"model_reach"`
	LedgerReach string `json:"ledger_reach"`
	// Checking is the agent CLI version the lane's worker is probing, when the lane's process runs that
	// version and the probe has not ended (FR-DRV-2).
	Checking *CLICheck `json:"checking,omitempty"`
}

// CLICheck is a worker's probe of one agent CLI version, as a lane that runs on the version names it. Words
// say it as a person reads it: which version is checked, what that means for the lane, and what ends it.
type CLICheck struct {
	Driver  string `json:"driver"`
	Version string `json:"version"`
	Words   string `json:"words"`
}

// CheckingCLI is the check of a driver's version, with its words, which every surface that shows a check shows as
// they are: the lane's process runs on documented surfaces until the check passes, and a respawn then picks up the
// qualified ones.
func CheckingCLI(driver, version string) *CLICheck {
	return &CLICheck{
		Driver: driver, Version: version,
		Words: "Checking " + VendorOf(driver).Product + " " + version +
			": this lane runs on documented surfaces until the check passes; a respawn then picks up the qualified surfaces",
	}
}

// MaxCheckWords bounds the words of a check a worker reports, in bytes.
const MaxCheckWords = 300

var (
	checkDriver  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	checkVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
)

// Validate says whether a check a worker reported can be kept: its driver is named as a driver is, its version as a
// version is, and its words are one line of plain text within MaxCheckWords.
func (c CLICheck) Validate() error {
	switch {
	case !checkDriver.MatchString(c.Driver):
		return errors.New("the check's driver is no driver name")
	case !checkVersion.MatchString(c.Version):
		return errors.New("the check's version is no version")
	case len(c.Words) > MaxCheckWords:
		return errors.New("the check's words are too long")
	case strings.ContainsAny(c.Words, "\n\t") || PlainText(c.Words) != c.Words:
		return errors.New("the check's words are not one line of plain text")
	}
	return nil
}

// SameCheck reports two lanes' checks that name the same driver and version, or two lanes with none.
func SameCheck(a, b *CLICheck) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Driver == b.Driver && a.Version == b.Version
}

// LaneRow is a lane's status as the ledger holds it.
type LaneRow struct {
	ID         string
	WorkOrder  string
	WorkerID   string
	Generation int64
	Status     LaneStatus
	// ReportedAt is database time, when the status arrived.
	ReportedAt      int64
	ReportedByEpoch int64
	// StartedFence is the fence this lane started under.
	StartedFence int64
	// UnpushedWork says the last stop left work no remote holds.
	UnpushedWork bool
	// Archived says its worktree went.
	Archived bool
	// Hold says where a parked lane can resume.
	Hold ResumeHold
	// ParkWanted says a policy asked this lane to park, and why. Empty asks nothing.
	ParkWanted string
}

// LanePolicies are the versions of the policies a lane started under, which it keeps for its life (ADR-0037): the
// project's spend policy and the installation's. A version published after the lane started never changes what it
// records, so a member reading a lane's history sees the values it ran on. A zero field says the lane started
// before the policy's version was kept.
type LanePolicies struct {
	Spend             int64 `json:"spend,omitempty"`
	InstallationSpend int64 `json:"installation_spend,omitempty"`
}

// The values of a lane's `resume_affinity`.
const (
	// AffinityRequired names work that exists only on one machine.
	AffinityRequired = "required"
	// AffinityPreferred names a machine that holds the session, and a delay lets any other
	// capable worker take the lane.
	AffinityPreferred = "preferred"
)

// ResumeHold is where a parked lane can resume: the worker that holds its session, and how
// strongly that binds it.
type ResumeHold struct {
	Affinity string
	Worker   string
}

// Admits says a worker can take this lane. An empty hold is a preference a delay ran out on.
func (h ResumeHold) Admits(worker string) bool {
	return h.Affinity == "" || h.Worker == worker
}

// HoldOf reads a checkpoint for what binds a lane to its worker. Work no remote holds exists
// only on that machine.
func HoldOf(checkpoint Checkpoint, worker string) ResumeHold {
	if checkpoint.Pushed {
		return ResumeHold{Affinity: AffinityPreferred, Worker: worker}
	}
	return ResumeHold{Affinity: AffinityRequired, Worker: worker}
}

// LaneView is a lane as a client reads it: its status, and how much to believe it.
type LaneView struct {
	ID         string `json:"id"`
	WorkOrder  string `json:"work_order"`
	WorkerID   string `json:"worker_id,omitempty"`
	Generation int64  `json:"generation"`
	LaneStatus
	AsOf            int64  `json:"as_of"`
	Freshness       string `json:"freshness"`
	ReportedByEpoch int64  `json:"reported_by_epoch"`
	// Archived says its worktree went.
	Archived bool `json:"archived,omitempty"`
	// BaseRef and BaseCommit are where the lane's branch began, which review diffs against and
	// integration targets (FR-BASE-4).
	BaseRef    string `json:"base_ref,omitempty"`
	BaseCommit string `json:"base_commit,omitempty"`
	// ContinuesRef and ContinuesCommit are the in-flight branch the lane continues and the tip its branch
	// began at, when it continues one (ADR-0023).
	ContinuesRef    string `json:"continues_ref,omitempty"`
	ContinuesCommit string `json:"continues_commit,omitempty"`
	// Reads are the other repositories of the project the lane reads, at the commits its claim recorded.
	Reads     []RepositoryRead `json:"reads,omitempty"`
	Artifacts []Artifact       `json:"artifacts,omitempty"`
	// Generations lists each run of an agent in this lane, oldest first.
	Generations []Generation `json:"generations,omitempty"`
}
