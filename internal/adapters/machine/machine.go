// Package machine reads what a machine has free, from the system's own reports. It reads
// files on Linux and runs the system's tools on macOS, because the standard library has no
// portable call and neither needs cgo.
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
	procMeminfo = "/proc/meminfo"
	procLoadavg = "/proc/loadavg"
	vmStat      = "/usr/bin/vm_stat"
	sysctl      = "/usr/sbin/sysctl"
)

const (
	bytesPerMB  = 1 << 20
	readTimeout = 5 * time.Second
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
	goos := r.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
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

// ParseVMStat adds up what macOS can hand out without paging: free, inactive, speculative,
// and purgeable pages.
func ParseVMStat(raw []byte) (int64, error) {
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
	return pages * pageBytes / bytesPerMB, nil
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

var _ ports.Machine = Reader{}
