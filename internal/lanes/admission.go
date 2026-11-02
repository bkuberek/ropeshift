package lanes

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
)

// Admission holds what only a machine can see (FR-LANE-16). Zero leaves a cap off. These
// protect the machine and the sign-in, so they come from its owner: a repository or a lane
// cannot set them.
type Admission struct {
	MaxLanes          int64 `toml:"max_lanes" json:"max_lanes"`
	MaxLanesPerSignIn int64 `toml:"max_lanes_per_sign_in" json:"max_lanes_per_sign_in"`
	MinFreeMemoryMB   int64 `toml:"min_free_memory_mb" json:"min_free_memory_mb"`
	MinFreeCPUPercent int64 `toml:"min_free_cpu_percent" json:"min_free_cpu_percent"`
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
