package lanes

import (
	"fmt"
	"strconv"
)

// The values of a workspace's fleet mode, as a worker reads them from desired state.
const (
	FleetRun   = "run"
	FleetPause = "pause"
	FleetDrain = "drain"
)

// Why a worker holds a lane it was asked to start. Each reaches a person as a note on the
// work order, and the lane starts when the reason goes away.
const (
	HeldFleetPaused   = "fleet_paused"
	HeldFleetDraining = "fleet_draining"
	HeldWorkerFull    = "worker_full"
	HeldSignInFull    = "sign_in_full"
	HeldMemory        = "memory_floor"
	HeldCPU           = "cpu_floor"
	HeldUsageLimit    = "usage_limit"
	HeldNearLimit     = "near_limit"
	HeldSignedOut     = "signed_out"
	// HeldQualifying says the installed CLI version has no probe's result on this machine yet.
	HeldQualifying = "qualifying"
)

// Admission holds what only a machine can see (FR-LANE-16). Zero leaves a cap off. These
// protect the machine and the sign-in, so they come from its owner: a repository or a lane can
// lower a lane cap for its own lanes, and can raise none and change nothing else (ADR-0021).
type Admission struct {
	MaxLanes          int64 `toml:"max_lanes" json:"max_lanes"`
	MaxLanesPerSignIn int64 `toml:"max_lanes_per_sign_in" json:"max_lanes_per_sign_in"`
	MinFreeMemoryMB   int64 `toml:"min_free_memory_mb" json:"min_free_memory_mb"`
	MinFreeCPUPercent int64 `toml:"min_free_cpu_percent" json:"min_free_cpu_percent"`
}

// CheckAdmission refuses a layer past the user's that raises a lane cap or changes anything else this
// machine admits. A cap is lower when it is set where the user's is not, or smaller than the user's; no
// cap where the user set one is higher.
func CheckAdmission(user, layered Admission) error {
	for _, c := range []struct {
		name          string
		user, layered int64
	}{
		{"max_lanes", user.MaxLanes, layered.MaxLanes},
		{"max_lanes_per_sign_in", user.MaxLanesPerSignIn, layered.MaxLanesPerSignIn},
	} {
		if c.layered < 0 || (c.user > 0 && (c.layered == 0 || c.layered > c.user)) {
			return fmt.Errorf("%w: a layer past the user's can lower %s and cannot raise it from %s to %s", ErrBadProfile, c.name, capWords(c.user), capWords(c.layered))
		}
	}
	if user.MinFreeMemoryMB != layered.MinFreeMemoryMB || user.MinFreeCPUPercent != layered.MinFreeCPUPercent {
		return fmt.Errorf("%w: a layer past the user's cannot change what this machine admits", ErrBadProfile)
	}
	return nil
}

// capWords says a cap as a person reads it.
func capWords(value int64) string {
	if value == 0 {
		return "no cap"
	}
	return strconv.FormatInt(value, 10)
}

// MachineReading is what a machine has free. Known is false when it could not be read,
// which is no evidence of a machine under pressure.
type MachineReading struct {
	Known          bool
	FreeMemoryMB   int64
	FreeCPUPercent int64
}

// AdmissionFacts is what one start is judged on. Running counts this worker's lanes, which
// in phase 1 share one driver, so it is also what a sign-in holds (D10).
type AdmissionFacts struct {
	Mode    string
	Running int64
	Limits  Admission
	Machine MachineReading
	// SignedOut, Limited, and NearLimit are what this worker knows of its sign-in.
	SignedOut, Limited, NearLimit bool
	// Woken says this lane is one that parked, which near the limit has work to finish. Probe says
	// it is the one lane sent to find out whether a limit still stands.
	Woken, Probe bool
}

// Admit names what holds a new lane back, and is empty when nothing does.
func Admit(f AdmissionFacts) string {
	switch f.Mode {
	case FleetPause:
		return HeldFleetPaused
	case FleetDrain:
		return HeldFleetDraining
	}
	switch {
	case f.SignedOut:
		return HeldSignedOut
	case f.Limited && !f.Probe:
		return HeldUsageLimit
	case f.NearLimit && !f.Woken:
		return HeldNearLimit
	case f.Limits.MaxLanes > 0 && f.Running >= f.Limits.MaxLanes:
		return HeldWorkerFull
	case f.Limits.MaxLanesPerSignIn > 0 && f.Running >= f.Limits.MaxLanesPerSignIn:
		return HeldSignInFull
	case !f.Machine.Known:
		return ""
	case f.Machine.FreeMemoryMB < f.Limits.MinFreeMemoryMB:
		return HeldMemory
	case f.Machine.FreeCPUPercent < f.Limits.MinFreeCPUPercent:
		return HeldCPU
	}
	return ""
}

// AdmitsRestart says whether a crashed lane may have a next generation. A pause keeps what
// is running, and a drain lets it end.
func AdmitsRestart(mode string) bool { return mode != FleetDrain }
