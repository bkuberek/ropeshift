package ledger

// The kinds of session a worker measures (FR-OBS-8). Only a lane counts toward the lane caps.
const (
	SessionLane         = "lane"
	SessionOrchestrator = "orchestrator"
	SessionAssistant    = "assistant"
)

// The measures a resource warning names (FR-UI-10).
const (
	WarnDiskFree        = "disk_free"
	WarnMemoryAvailable = "memory_available"
	WarnMemoryPressure  = "memory_pressure"
	WarnCPULoad         = "cpu_load"
)

// The bases of a recommendation: the estimates the profile sets, or the peaks of lanes this worker ran.
const (
	BasisEstimates = "estimates"
	BasisObserved  = "observed"
)

// ResourceReport is what a worker measured of its machine and its sessions at one moment, with the lane
// cap it recommends and the caps in force (FR-OBS-8, FR-OBS-9, FR-PROJ-7). Every measure the machine could
// not provide is absent, never zero.
type ResourceReport struct {
	// IntervalMs is how often the worker measures.
	IntervalMs int64 `json:"interval_ms"`
	// AgeMs is how long before the heartbeat that carried it the worker measured. Only durations cross
	// the wire, so the control plane dates the report by its own clock.
	AgeMs          int64               `json:"age_ms,omitempty"`
	Machine        MachineResources    `json:"machine"`
	Recommendation *LaneRecommendation `json:"recommendation,omitempty"`
	Caps           LaneCaps            `json:"caps"`
	Sessions       []SessionResources  `json:"sessions"`
	Warnings       []ResourceWarning   `json:"warnings,omitempty"`
}

// MachineResources is the machine as a whole. CPUPercent is the share of every core that was busy since
// the measure before, and LoadPerCore the five-minute load over the cores.
type MachineResources struct {
	Cores                int64             `json:"cores,omitempty"`
	CPUPercent           *float64          `json:"cpu_percent,omitempty"`
	Load1                *float64          `json:"load_1,omitempty"`
	Load5                *float64          `json:"load_5,omitempty"`
	Load15               *float64          `json:"load_15,omitempty"`
	MemoryTotalBytes     *int64            `json:"memory_total_bytes,omitempty"`
	MemoryUsedBytes      *int64            `json:"memory_used_bytes,omitempty"`
	MemoryAvailableBytes *int64            `json:"memory_available_bytes,omitempty"`
	MemoryPressureSome   *float64          `json:"memory_pressure_some,omitempty"`
	MemoryPressureFull   *float64          `json:"memory_pressure_full,omitempty"`
	Volumes              []VolumeResources `json:"volumes"`
}

// VolumeResources is one filesystem a worker watches, by the roles it holds: the state directory's, the
// worktrees', or both when they share one. It names no path, because a path says nothing off the machine.
type VolumeResources struct {
	Roles      []string `json:"roles"`
	FreeBytes  int64    `json:"free_bytes"`
	TotalBytes int64    `json:"total_bytes"`
}

// SessionResources is one session's process tree: a lane, the orchestrator, or an assistant. CPUCores is
// the cores it kept busy since the measure before, and is absent on its first measure.
type SessionResources struct {
	Kind      string   `json:"kind"`
	Project   string   `json:"project,omitempty"`
	Lane      string   `json:"lane,omitempty"`
	CPUCores  *float64 `json:"cpu_cores,omitempty"`
	RSSBytes  *int64   `json:"rss_bytes,omitempty"`
	Processes int64    `json:"processes,omitempty"`
}

// LaneRecommendation is the lane cap a worker recommends, and the numbers behind it (FR-OBS-9): what its
// cores and its memory each fit, less the reserve, at what one lane takes.
type LaneRecommendation struct {
	MaxLanes int64 `json:"max_lanes"`
	// Basis says whether one lane's share is the profile's estimates or the peaks of the lanes this worker
	// observed, and ObservedLanes how many lanes those peaks come from.
	Basis              string  `json:"basis"`
	ObservedLanes      int64   `json:"observed_lanes"`
	Cores              int64   `json:"cores"`
	MemoryTotalBytes   *int64  `json:"memory_total_bytes,omitempty"`
	ReserveCPU         float64 `json:"reserve_cpu"`
	ReserveMemoryBytes int64   `json:"reserve_memory_bytes"`
	LaneCPU            float64 `json:"lane_cpu"`
	LaneMemoryBytes    int64   `json:"lane_memory_bytes"`
	ByCPU              int64   `json:"by_cpu"`
	ByMemory           *int64  `json:"by_memory,omitempty"`
	// Reason says the same in words, for a warning a person reads.
	Reason string `json:"reason"`
}

// LaneCaps is what caps this worker's lanes. MaxLanes is the per-worker cap in force, which is the
// recommendation unless a member set one, and zero caps nothing. Set says a member set it.
type LaneCaps struct {
	MaxLanes          int64 `json:"max_lanes"`
	MaxLanesSet       bool  `json:"max_lanes_set"`
	MaxLanesPerSignIn int64 `json:"max_lanes_per_sign_in"`
}

// ResourceWarning is one measure past its threshold. It stops, parks, and refuses nothing (FR-UI-10).
type ResourceWarning struct {
	Measure   string  `json:"measure"`
	Volume    string  `json:"volume,omitempty"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Message   string  `json:"message"`
}
