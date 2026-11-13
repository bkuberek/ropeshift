// Package machine reads what a machine has free and what its processes use, from the system's own
// reports. It reads files on Linux and runs the system's tools on macOS, because the standard library
// has no portable call and neither needs cgo.
package machine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// Paths of what this package reads. A tool is named absolutely, so PATH chooses nothing.
const (
	procDir      = "/proc"
	procMeminfo  = "/proc/meminfo"
	procLoadavg  = "/proc/loadavg"
	procStat     = "/proc/stat"
	procPressure = "/proc/pressure/memory"
	vmStat       = "/usr/bin/vm_stat"
	sysctl       = "/usr/sbin/sysctl"
	ps           = "/bin/ps"
)

const (
	bytesPerMB  = 1 << 20
	readTimeout = 5 * time.Second
	// userHZ is the unit of a process's CPU time in /proc/<pid>/stat. The kernel fixes it at 100 for user
	// space on every architecture Linux runs on (USER_HZ), and reading sysconf would need cgo.
	userHZ = 100
)

// Reader reads the machine it runs on.
type Reader struct {
	Processes ports.ProcessRunner
	// GOOS names the platform. Empty takes this build's.
	GOOS string
	// Cores counts the CPUs. Zero takes what this process sees.
	Cores int
}

func (r Reader) cores() int64 {
	if r.Cores > 0 {
		return int64(r.Cores)
	}
	return int64(runtime.NumCPU())
}

// Reading reads memory and load.
func (r Reader) Reading(ctx context.Context) (lanes.MachineReading, error) {
	goos := r.goos()
	switch goos {
	case "linux":
		return r.readLinux()
	case "darwin":
		return r.readDarwin(ctx)
	}
	return lanes.MachineReading{}, fmt.Errorf("no reading on %s", goos)
}

func (r Reader) readLinux() (lanes.MachineReading, error) {
	meminfo, err := os.ReadFile(procMeminfo)
	if err != nil {
		return lanes.MachineReading{}, err
	}
	free, err := ParseMeminfo(meminfo)
	if err != nil {
		return lanes.MachineReading{}, err
	}
	loadavg, err := os.ReadFile(procLoadavg)
	if err != nil {
		return lanes.MachineReading{}, err
	}
	load, err := ParseLoad(string(loadavg))
	if err != nil {
		return lanes.MachineReading{}, err
	}
	return lanes.MachineReading{Known: true, FreeMemoryMB: free, FreeCPUPercent: FreeCPU(load, r.cores())}, nil
}

func (r Reader) readDarwin(ctx context.Context) (lanes.MachineReading, error) {
	stats, err := r.output(ctx, vmStat)
	if err != nil {
		return lanes.MachineReading{}, err
	}
	free, err := ParseVMStat(stats)
	if err != nil {
		return lanes.MachineReading{}, err
	}
	loadavg, err := r.output(ctx, sysctl, "-n", "vm.loadavg")
	if err != nil {
		return lanes.MachineReading{}, err
	}
	load, err := ParseLoad(strings.Trim(strings.TrimSpace(string(loadavg)), "{} "))
	if err != nil {
		return lanes.MachineReading{}, err
	}
	return lanes.MachineReading{Known: true, FreeMemoryMB: free, FreeCPUPercent: FreeCPU(load, r.cores())}, nil
}

// output runs one tool to its end, and a tool that hangs is an incomplete read.
func (r Reader) output(ctx context.Context, path string, args ...string) ([]byte, error) {
	var out bytes.Buffer
	process, err := r.Processes.Start(ctx, ports.ProcessSpec{Path: path, Args: args, Stdout: &out})
	if err != nil {
		return nil, err
	}
	finished := make(chan error, 1)
	go func() {
		_, err := process.Wait()
		finished <- err
	}()
	select {
	case err := <-finished:
		return out.Bytes(), err
	case <-time.After(readTimeout):
		_ = process.Signal(ports.SignalKill)
		return nil, errors.New(path + " did not answer")
	}
}

// ParseMeminfo reads MemAvailable, which is what the kernel says a new process can have.
func ParseMeminfo(raw []byte) (int64, error) {
	for line := range strings.SplitSeq(string(raw), "\n") {
		rest, ok := strings.CutPrefix(line, "MemAvailable:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			break
		}
		kilobytes, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0, err
		}
		return kilobytes * 1024 / bytesPerMB, nil
	}
	return 0, errors.New("no MemAvailable in the report")
}

var (
	pageSizeLine = regexp.MustCompile(`page size of (\d+) bytes`)
	pagesLine    = regexp.MustCompile(`^Pages (free|inactive|speculative|purgeable):\s+(\d+)\.`)
)

// ParseVMStat adds up what macOS can hand out without paging, in megabytes.
func ParseVMStat(raw []byte) (int64, error) {
	available, err := ParseVMStatBytes(raw)
	return available / bytesPerMB, err
}

// ParseVMStatBytes adds up what macOS can hand out without paging: free, inactive, speculative, and
// purgeable pages, in bytes.
func ParseVMStatBytes(raw []byte) (int64, error) {
	size := pageSizeLine.FindSubmatch(raw)
	if size == nil {
		return 0, errors.New("no page size in the report")
	}
	pageBytes, err := strconv.ParseInt(string(size[1]), 10, 64)
	if err != nil {
		return 0, err
	}
	var pages int64
	seen := 0
	for line := range strings.SplitSeq(string(raw), "\n") {
		match := pagesLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		count, err := strconv.ParseInt(match[2], 10, 64)
		if err != nil {
			return 0, err
		}
		pages += count
		seen++
	}
	if seen == 0 {
		return 0, errors.New("no page counts in the report")
	}
	return pages * pageBytes, nil
}

// ParseLoad reads the one-minute load, the first word of a load report.
func ParseLoad(text string) (float64, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return 0, errors.New("an empty load report")
	}
	return strconv.ParseFloat(fields[0], 64)
}

// FreeCPU turns a load into what is left of the machine, in whole percent.
func FreeCPU(load float64, cores int64) int64 {
	if cores <= 0 {
		return 0
	}
	busy := load / float64(cores) * 100
	return max(int64(100-busy), 0)
}

// Sample reads the machine as a whole and the filesystem of each volume. Linux has every measure; macOS
// has no CPU ticks or memory pressure this package reads without cgo, so those stay nil there.
func (r Reader) Sample(ctx context.Context, volumes []lanes.Volume) (lanes.MachineSample, error) {
	switch r.goos() {
	case "linux":
		return r.sampleLinux(volumes), nil
	case "darwin":
		return r.sampleDarwin(ctx, volumes), nil
	}
	return lanes.MachineSample{}, fmt.Errorf("no sample on %s", r.goos())
}

func (r Reader) goos() string {
	if r.GOOS == "" {
		return runtime.GOOS
	}
	return r.GOOS
}

// sampleLinux reads each measure on its own, so one file the kernel lacks costs only that measure.
func (r Reader) sampleLinux(volumes []lanes.Volume) lanes.MachineSample {
	sample := lanes.MachineSample{Cores: r.cores(), Volumes: readVolumes(volumes)}
	if raw, err := os.ReadFile(procStat); err == nil {
		if ticks, err := ParseCPUTicks(raw); err == nil {
			sample.CPU = &ticks
		}
	}
	if raw, err := os.ReadFile(procLoadavg); err == nil {
		if load, err := ParseLoadAverage(string(raw)); err == nil {
			sample.Load = &load
		}
	}
	if raw, err := os.ReadFile(procMeminfo); err == nil {
		if total, available, err := ParseMemory(raw); err == nil {
			sample.MemoryTotalBytes, sample.MemoryAvailableBytes = &total, &available
		}
	}
	if raw, err := os.ReadFile(procPressure); err == nil {
		if pressure, err := ParsePressure(raw); err == nil {
			sample.Pressure = &pressure
		}
	}
	return sample
}

func (r Reader) sampleDarwin(ctx context.Context, volumes []lanes.Volume) lanes.MachineSample {
	sample := lanes.MachineSample{Cores: r.cores(), Volumes: readVolumes(volumes)}
	if raw, err := r.output(ctx, sysctl, "-n", "vm.loadavg"); err == nil {
		if load, err := ParseLoadAverage(string(raw)); err == nil {
			sample.Load = &load
		}
	}
	if raw, err := r.output(ctx, sysctl, "-n", "hw.memsize"); err == nil {
		if total, err := ParseMemsize(raw); err == nil {
			sample.MemoryTotalBytes = &total
		}
	}
	if raw, err := r.output(ctx, vmStat); err == nil {
		if available, err := ParseVMStatBytes(raw); err == nil {
			sample.MemoryAvailableBytes = &available
		}
	}
	return sample
}

// ProcessTrees reads every process once and sums each root's tree, so a process that started between two
// reads cannot be counted twice.
func (r Reader) ProcessTrees(ctx context.Context, roots []int) (map[int]lanes.ProcessUse, error) {
	var stats []ProcessStat
	var err error
	switch r.goos() {
	case "linux":
		stats, err = readProcStats()
	case "darwin":
		var raw []byte
		if raw, err = r.output(ctx, ps, "-A", "-o", "pid=,ppid=,rss=,time="); err == nil {
			stats, err = ParsePS(raw)
		}
	default:
		err = fmt.Errorf("no process reads on %s", r.goos())
	}
	if err != nil {
		return nil, err
	}
	return SumTrees(stats, roots), nil
}

// readProcStats reads every process /proc lists. A process that ends between the listing and its read
// is skipped, because it is gone.
func readProcStats() ([]ProcessStat, error) {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil, err
	}
	page := int64(os.Getpagesize())
	stats := make([]ProcessStat, 0, len(entries))
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile(procDir + "/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		if stat, err := ParseProcStat(string(raw), userHZ, page); err == nil {
			stats = append(stats, stat)
		}
	}
	return stats, nil
}

// ParseMemory reads MemTotal and MemAvailable, in bytes.
func ParseMemory(raw []byte) (total, available int64, err error) {
	values := map[string]int64{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok || (key != "MemTotal" && key != "MemAvailable") {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kilobytes, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0, 0, err
		}
		values[key] = kilobytes * 1024
	}
	total, hasTotal := values["MemTotal"]
	available, hasAvailable := values["MemAvailable"]
	if !hasTotal || !hasAvailable {
		return 0, 0, errors.New("no MemTotal or MemAvailable in the report")
	}
	return total, available, nil
}

// ParseCPUTicks reads the machine's line of /proc/stat. The guest columns are already inside user and nice,
// so the total stops at steal, and idle and iowait are the time nothing ran.
func ParseCPUTicks(raw []byte) (lanes.CPUTicks, error) {
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 || fields[0] != "cpu" {
			continue
		}
		var values [8]uint64
		for i := range values {
			value, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return lanes.CPUTicks{}, err
			}
			values[i] = value
		}
		var total uint64
		for _, value := range values {
			total += value
		}
		return lanes.CPUTicks{Busy: total - values[3] - values[4], Total: total}, nil
	}
	return lanes.CPUTicks{}, errors.New("no cpu line in the report")
}

// ParseLoadAverage reads the first three words of a load report: /proc/loadavg, or macOS's vm.loadavg,
// which wraps them in braces.
func ParseLoadAverage(text string) (lanes.LoadAverage, error) {
	fields := strings.Fields(strings.NewReplacer("{", " ", "}", " ").Replace(text))
	if len(fields) < 3 {
		return lanes.LoadAverage{}, errors.New("a load report with fewer than three averages")
	}
	var values [3]float64
	for i := range values {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return lanes.LoadAverage{}, err
		}
		values[i] = value
	}
	return lanes.LoadAverage{One: values[0], Five: values[1], Fifteen: values[2]}, nil
}

// ParsePressure reads the one-minute averages of the some and full lines of /proc/pressure/memory.
func ParsePressure(raw []byte) (lanes.MemoryPressure, error) {
	found := map[string]float64{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for _, field := range fields[1:] {
			value, ok := strings.CutPrefix(field, "avg60=")
			if !ok {
				continue
			}
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return lanes.MemoryPressure{}, err
			}
			found[fields[0]] = parsed
		}
	}
	some, hasSome := found["some"]
	if !hasSome {
		return lanes.MemoryPressure{}, errors.New("no one-minute average in the pressure report")
	}
	return lanes.MemoryPressure{Some: some, Full: found["full"]}, nil
}

// ParseMemsize reads hw.memsize, the bytes of memory a Mac has.
func ParseMemsize(raw []byte) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}

// ProcessStat is one process as the system lists it.
type ProcessStat struct {
	PID, PPID  int
	CPUSeconds float64
	RSSBytes   int64
}

// ParseProcStat reads one /proc/<pid>/stat. A command name can hold spaces and parentheses, so the fields
// count from the last closing parenthesis: the state, then the parent, and utime, stime, and rss further on.
func ParseProcStat(line string, hz, pageBytes int64) (ProcessStat, error) {
	open, closing := strings.IndexByte(line, '('), strings.LastIndexByte(line, ')')
	if open < 1 || closing < open {
		return ProcessStat{}, errors.New("no command in the stat line")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line[:open]))
	if err != nil {
		return ProcessStat{}, err
	}
	fields := strings.Fields(line[closing+1:])
	const ppid, utime, stime, rss = 1, 11, 12, 21
	if len(fields) <= rss {
		return ProcessStat{}, errors.New("a short stat line")
	}
	var values [4]int64
	for i, index := range []int{ppid, utime, stime, rss} {
		if values[i], err = strconv.ParseInt(fields[index], 10, 64); err != nil {
			return ProcessStat{}, err
		}
	}
	return ProcessStat{
		PID: pid, PPID: int(values[0]), CPUSeconds: float64(values[1]+values[2]) / float64(hz), RSSBytes: values[3] * pageBytes,
	}, nil
}

// ParsePS reads `ps -A -o pid=,ppid=,rss=,time=`: resident memory in kilobytes, and CPU time as
// [dd-][hh:]mm:ss[.ss]. A line that does not read is skipped.
func ParsePS(raw []byte) ([]ProcessStat, error) {
	var stats []ProcessStat
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		rss, rssErr := strconv.ParseInt(fields[2], 10, 64)
		seconds, timeErr := parseCPUTime(fields[3])
		if pidErr != nil || ppidErr != nil || rssErr != nil || timeErr != nil {
			continue
		}
		stats = append(stats, ProcessStat{PID: pid, PPID: ppid, CPUSeconds: seconds, RSSBytes: rss * 1024})
	}
	if len(stats) == 0 {
		return nil, errors.New("no process in the listing")
	}
	return stats, nil
}

// parseCPUTime reads [dd-][hh:]mm:ss[.ss].
func parseCPUTime(text string) (float64, error) {
	var days float64
	if before, after, ok := strings.Cut(text, "-"); ok {
		parsed, err := strconv.ParseFloat(before, 64)
		if err != nil {
			return 0, err
		}
		days, text = parsed, after
	}
	parts := strings.Split(text, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("a CPU time of %q", text)
	}
	var seconds float64
	for _, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0, err
		}
		seconds = seconds*60 + value
	}
	return days*86400 + seconds, nil
}

// SumTrees adds up each root and every process descended from it. A root not in the listing is gone, and
// is left out of the answer.
func SumTrees(stats []ProcessStat, roots []int) map[int]lanes.ProcessUse {
	children := map[int][]int{}
	byPID := make(map[int]ProcessStat, len(stats))
	for _, stat := range stats {
		byPID[stat.PID] = stat
		children[stat.PPID] = append(children[stat.PPID], stat.PID)
	}
	out := make(map[int]lanes.ProcessUse, len(roots))
	for _, root := range roots {
		if _, ok := byPID[root]; !ok {
			continue
		}
		var use lanes.ProcessUse
		seen := map[int]bool{}
		queue := []int{root}
		for len(queue) > 0 {
			pid := queue[0]
			queue = queue[1:]
			if seen[pid] {
				continue
			}
			seen[pid] = true
			stat := byPID[pid]
			use.CPUSeconds += stat.CPUSeconds
			use.RSSBytes += stat.RSSBytes
			use.Processes++
			queue = append(queue, children[pid]...)
		}
		out[root] = use
	}
	return out
}

var _ ports.Machine = Reader{}
