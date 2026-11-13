package lanes

// The roles of the volumes a worker watches (FR-OBS-8): the filesystem of its state directory, and the
// filesystems its lanes' worktrees are on.
const (
	VolumeState     = "state"
	VolumeWorktrees = "worktrees"
)

// Volume is a directory whose filesystem a worker watches, and the role it plays.
type Volume struct {
	Role string
	Path string
}

// CPUTicks is a machine's CPU time since it started, busy and in all, in the system's own ticks. One read
// says nothing by itself; two give the share of the time between them that was busy.
type CPUTicks struct {
	Busy  uint64
	Total uint64
}

// LoadAverage is the run queue averaged over one, five, and fifteen minutes.
type LoadAverage struct {
	One, Five, Fifteen float64
}

// MemoryPressure is the share of the last minute, in percent, in which some task, and in which every task,
// waited on memory.
type MemoryPressure struct {
	Some, Full float64
}

// VolumeSample is the space of the filesystem a volume is on. Device names that filesystem, so two
// directories on one count once.
type VolumeSample struct {
	Volume
	Device     uint64
	FreeBytes  int64
	TotalBytes int64
}

// MachineSample is what one read of a machine found. A nil measure is one this machine cannot provide,
// which is never the same as zero, and a volume it could not read is left out.
type MachineSample struct {
	Cores                int64
	CPU                  *CPUTicks
	Load                 *LoadAverage
	MemoryTotalBytes     *int64
	MemoryAvailableBytes *int64
	Pressure             *MemoryPressure
	Volumes              []VolumeSample
}

// ProcessUse is what one process tree has used: the CPU time of every process in it since each started,
// summed, and their resident memory now.
type ProcessUse struct {
	CPUSeconds float64
	RSSBytes   int64
	Processes  int64
}
