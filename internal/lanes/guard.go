package lanes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// WorktreeFindings is what a stop must account for. It is read before anything changes, so
// a guard acts on what is there and not on what a lane reported.
type WorktreeFindings struct {
	// Branch is empty on a detached head, which has nothing to push.
	Branch string
	Head   string
	// Dirty says a tracked or an untracked file differs from HEAD.
	Dirty bool
	// DirtyPaths names those files.
	DirtyPaths []string
	// Remote names where a push goes, and empty means none is configured.
	Remote string
	// Ahead counts the commits only this branch holds: the remote lacks them, or with no
	// remote, every other branch does.
	Ahead int64
}

// GuardPlan is what a stop does before it can leave a worktree behind (FR-LANE-14).
type GuardPlan struct {
	Push         bool
	Snapshot     bool
	PushSnapshot bool
}

// GuardRules is what a guard may do beside what it reads.
type GuardRules struct {
	// Fenced says the lane's lease went.
	Fenced bool
	// Push says the user chose to send a lane's work to its remote (push_on_stop).
	Push bool
}

// PlanGuard reads the design's table. A fenced lane leaves its branch alone, because that
// branch may no longer be its to move, and a ref holds everything it had. A detached head
// has no branch at all, so only a ref keeps its commits reachable. A remote the user did not
// choose to push to is no place to keep work, so without that choice the table reads as for no
// remote: the branch and every ref stay on this machine.
func PlanGuard(found WorktreeFindings, rules GuardRules) GuardPlan {
	fenced := rules.Fenced
	remote := found.Remote != "" && rules.Push
	detached := found.Branch == "" && found.Ahead > 0
	var plan GuardPlan
	plan.Push = remote && found.Ahead > 0 && !fenced && !detached
	plan.Snapshot = found.Dirty || detached || (fenced && found.Ahead > 0)
	plan.PushSnapshot = plan.Snapshot && remote
	return plan
}

// GuardActs is what one guard did.
type GuardActs struct {
	Plan   GuardPlan
	Fenced bool
	// Ref and Commit name the snapshot it made, if it made one.
	Ref    string
	Commit string
	// SnapshotPushed says the snapshot reached the remote.
	SnapshotPushed bool
	// Detail says what failed.
	Detail string
}

// GuardResult is how a stop left a worktree. Unpushed means a removal could lose work, so
// only a member's forced command can make one.
type GuardResult struct {
	Unpushed bool   `json:"unpushed_work,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Commit   string `json:"commit,omitempty"`
	Pushed   bool   `json:"pushed,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// SettleGuard decides from a second reading, and not from whether an act returned, so a
// push that seemed to work and left the commits behind still reads as not pushed. A snapshot
// counts only once it left this machine.
func SettleGuard(after WorktreeFindings, acts GuardActs) GuardResult {
	saved := acts.Ref != "" && acts.SnapshotPushed
	commitsHeld := saved && (acts.Fenced || acts.Plan.Snapshot && after.Branch == "")
	commitsLeft := after.Ahead > 0 && !commitsHeld
	return GuardResult{
		Unpushed: commitsLeft || (after.Dirty && !saved),
		Ref:      acts.Ref, Commit: acts.Commit, Pushed: acts.SnapshotPushed, Detail: acts.Detail,
	}
}

// UnreadGuard is the result of a worktree a guard could not read. A read that failed is no
// evidence of a clean worktree, so nothing may remove this one.
func UnreadGuard(err error) GuardResult { return GuardResult{Unpushed: true, Detail: err.Error()} }

// SalvageRef names what a fenced lane leaves.
func SalvageRef(lane string, fence int64) string {
	return fmt.Sprintf("refs/ropeshift/salvage/%s/f%d", lane, fence)
}

// CheckpointRef names what a stop leaves of a generation's uncommitted changes.
func CheckpointRef(lane string, generation int64) string {
	return fmt.Sprintf("refs/ropeshift/checkpoints/%s/%d", lane, generation)
}

// HeadsRef names a branch.
func HeadsRef(branch string) string { return "refs/heads/" + branch }

// PortPool is the block of ports a worker hands out (FR-LANE-15).
type PortPool struct {
	Base int64
	Size int64
}

// DefaultPortPool sits under the ephemeral range of both platforms.
var DefaultPortPool = PortPool{Base: 20000, Size: 10000}

// Allocation is what one lane holds beside its worktree. It lives in the journal, so a
// restart knows what a lane that crashed still held.
type Allocation struct {
	Lane           string
	PortBase       int64
	Ports          int64
	ComposeProject string
}

// AllocationRequest asks for a lane's resources.
type AllocationRequest struct {
	Lane           string
	Ports          int64
	ComposeProject string
	Pool           PortPool
}

// NextPortBase finds the lowest free block. It reports false when none fits.
func NextPortBase(held []Allocation, pool PortPool, ports int64) (int64, bool) {
	if ports <= 0 {
		return 0, true
	}
	for base := pool.Base; base+ports <= pool.Base+pool.Size; {
		clear := true
		for _, other := range held {
			if other.Ports > 0 && base < other.PortBase+other.Ports && other.PortBase < base+ports {
				base, clear = other.PortBase+other.Ports, false
				break
			}
		}
		if clear {
			return base, true
		}
	}
	return 0, false
}

// composeDigestSize is how much of a lane's digest separates two names that fold alike.
const composeDigestSize = 6

// ComposeProject names a lane's compose project. Compose takes lower-case letters, digits,
// hyphens, and underscores, and a name that needed changing keeps a piece of the lane's
// digest, so two lanes never share a project.
func ComposeProject(prefix, lane string) string {
	var name strings.Builder
	for _, c := range strings.ToLower(lane) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			name.WriteRune(c)
		} else {
			name.WriteByte('-')
		}
	}
	project := prefix + "-" + name.String()
	if name.String() == lane {
		return project
	}
	sum := sha256.Sum256([]byte(lane))
	return project + "-" + hex.EncodeToString(sum[:])[:composeDigestSize]
}
