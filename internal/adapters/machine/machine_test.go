package machine_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/machine"
	"ropeshift.com/ropeshift/internal/adapters/osexec"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

func TestParseMeminfo(t *testing.T) {
	got, err := machine.ParseMeminfo([]byte("MemTotal: 16000000 kB\nMemFree: 1000 kB\nMemAvailable:    2048000 kB\n"))
	if err != nil || got != 2000 {
		t.Errorf("free %d: %v", got, err)
	}
	if _, err := machine.ParseMeminfo([]byte("MemTotal: 1 kB\n")); err == nil {
		t.Error("a report with no answer passed")
	}
}

func TestParseVMStat(t *testing.T) {
	report := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               1000.
Pages active:                             9999.
Pages inactive:                           2000.
Pages speculative:                         500.
Pages wired down:                         9999.
Pages purgeable:                           100.
`
	got, err := machine.ParseVMStat([]byte(report))
	if err != nil || got != 56 {
		t.Errorf("free %d: %v", got, err)
	}
	if _, err := machine.ParseVMStat([]byte("nothing\n")); err == nil {
		t.Error("a report with no answer passed")
	}
}

func TestFreeCPU(t *testing.T) {
	tests := []struct {
		load  float64
		cores int64
		want  int64
	}{{0, 8, 100}, {4, 8, 50}, {8, 8, 0}, {50, 8, 0}, {1, 0, 0}}
	for _, tt := range tests {
		if got := machine.FreeCPU(tt.load, tt.cores); got != tt.want {
			t.Errorf("load %v on %d: %d", tt.load, tt.cores, got)
		}
	}
}

func TestParseLoad(t *testing.T) {
	for _, text := range []string{"1.50 2.00 3.00", "1.50 2.00 3.00 1/100 12345\n"} {
		if got, err := machine.ParseLoad(text); err != nil || got != 1.5 {
			t.Errorf("%q: %v, %v", text, got, err)
		}
	}
	if _, err := machine.ParseLoad(""); err == nil {
		t.Error("an empty report passed")
	}
}

func TestThisMachineReads(t *testing.T) {
	reading, err := machine.Reader{Processes: osexec.Runner{}}.Reading(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reading.Known || reading.FreeMemoryMB <= 0 {
		t.Errorf("reading %+v", reading)
	}
}

func TestAPlatformWithNoReadingIsAnError(t *testing.T) {
	if _, err := (machine.Reader{GOOS: "plan9"}).Reading(context.Background()); err == nil {
		t.Error("a platform with no reading passed")
	}
}

func TestParseMemory(t *testing.T) {
	total, available, err := machine.ParseMemory([]byte("MemTotal:       16384000 kB\nMemFree: 1000 kB\nMemAvailable:    2048000 kB\n"))
	if err != nil || total != 16384000*1024 || available != 2048000*1024 {
		t.Errorf("total %d, available %d: %v", total, available, err)
	}
	if _, _, err := machine.ParseMemory([]byte("MemAvailable: 1 kB\n")); err == nil {
		t.Error("a report with no total passed")
	}
}

func TestParseCPUTicks(t *testing.T) {
	stat := "cpu  100 20 30 400 50 6 7 8 9 10\ncpu0 1 2 3 4 5 6 7 8 9 10\nintr 1\n"
	ticks, err := machine.ParseCPUTicks([]byte(stat))
	// The guest columns are inside user and nice already, and idle and iowait are the time nothing ran.
	if err != nil || ticks.Total != 621 || ticks.Busy != 171 {
		t.Errorf("ticks %+v: %v", ticks, err)
	}
	if _, err := machine.ParseCPUTicks([]byte("intr 1\n")); err == nil {
		t.Error("a report with no cpu line passed")
	}
}

func TestParseLoadAverage(t *testing.T) {
	for _, text := range []string{"1.50 2.00 3.00 1/100 12345\n", "{ 1.50 2.00 3.00 }\n"} {
		load, err := machine.ParseLoadAverage(text)
		if err != nil || load.One != 1.5 || load.Five != 2 || load.Fifteen != 3 {
			t.Errorf("%q: %+v, %v", text, load, err)
		}
	}
	if _, err := machine.ParseLoadAverage("1.0 2.0"); err == nil {
		t.Error("a report with two averages passed")
	}
}

func TestParsePressure(t *testing.T) {
	psi := "some avg10=1.00 avg60=12.50 avg300=3.00 total=100\nfull avg10=0.50 avg60=4.25 avg300=1.00 total=50\n"
	pressure, err := machine.ParsePressure([]byte(psi))
	if err != nil || pressure.Some != 12.5 || pressure.Full != 4.25 {
		t.Errorf("pressure %+v: %v", pressure, err)
	}
	if _, err := machine.ParsePressure([]byte("some avg10=1.00\n")); err == nil {
		t.Error("a report with no minute average passed")
	}
}

func TestParseMemsize(t *testing.T) {
	if got, err := machine.ParseMemsize([]byte("17179869184\n")); err != nil || got != 17179869184 {
		t.Errorf("memsize %d: %v", got, err)
	}
	if got, err := machine.ParseVMStatBytes([]byte("Mach Virtual Memory Statistics: (page size of 4096 bytes)\nPages free: 10.\n")); err != nil || got != 40960 {
		t.Errorf("available %d: %v", got, err)
	}
}

func TestParseProcStat(t *testing.T) {
	// A command name can hold spaces and parentheses, so the fields count from the last one.
	line := "4242 (tmux: server (1)) S 4000 4242 4242 0 -1 4194560 100 0 0 0 250 150 0 0 20 0 1 0 100 1000000 300 18446744073709551615"
	stat, err := machine.ParseProcStat(line, 100, 4096)
	if err != nil || stat.PID != 4242 || stat.PPID != 4000 || stat.CPUSeconds != 4 || stat.RSSBytes != 300*4096 {
		t.Errorf("stat %+v: %v", stat, err)
	}
	if _, err := machine.ParseProcStat("4242 (short) S 1", 100, 4096); err == nil {
		t.Error("a short line passed")
	}
	// A child the process has reaped keeps its CPU time in the tree, in cutime and cstime, so a build's
	// compilers still count once they exit.
	reaped := "4242 (make) S 4000 4242 4242 0 -1 4194560 100 0 0 0 250 150 30 20 20 0 1 0 100 1000000 300 18446744073709551615"
	if stat, err := machine.ParseProcStat(reaped, 100, 4096); err != nil || stat.CPUSeconds != 4.5 {
		t.Errorf("reaped children: %+v: %v", stat, err)
	}
}

// TestADarwinTreeCountsReapedChildren asks ps to add each process's exited children to its time, which is
// what cutime and cstime give on Linux.
func TestADarwinTreeCountsReapedChildren(t *testing.T) {
	runner := fake.NewProcessRunner()
	runner.Install("/bin/ps", func(spec ports.ProcessSpec, _ <-chan struct{}) int {
		_, _ = io.WriteString(spec.Stdout, "  10     1   2048     0:30.00\n  11    10   1024     0:15.00\n")
		return 0
	})
	trees, err := machine.Reader{Processes: runner, GOOS: "darwin"}.ProcessTrees(context.Background(), []int{10})
	if err != nil || trees[10].CPUSeconds != 45 {
		t.Fatalf("trees %+v: %v", trees, err)
	}
	spawned := runner.Spawned()
	if len(spawned) != 1 || !slices.Contains(spawned[0].Args, "-S") {
		t.Errorf("ran %+v", spawned)
	}
}

func TestParsePS(t *testing.T) {
	out := "    1     0  12000   1:02:03.50\n  500     1   2048     0:01.25\n  501   500    512  2-00:00:00\nnot a row\n"
	stats, err := machine.ParsePS([]byte(out))
	if err != nil || len(stats) != 3 {
		t.Fatalf("stats %+v: %v", stats, err)
	}
	if stats[0].PID != 1 || stats[0].CPUSeconds != 3723.5 || stats[0].RSSBytes != 12000*1024 {
		t.Errorf("first %+v", stats[0])
	}
	if stats[1].PPID != 1 || stats[1].CPUSeconds != 1.25 {
		t.Errorf("second %+v", stats[1])
	}
	if stats[2].CPUSeconds != 2*86400 {
		t.Errorf("third %+v", stats[2])
	}
}

func TestSumTrees(t *testing.T) {
	stats := []machine.ProcessStat{
		{PID: 1, PPID: 0, CPUSeconds: 100, RSSBytes: 1},
		{PID: 10, PPID: 1, CPUSeconds: 2, RSSBytes: 200},
		{PID: 11, PPID: 10, CPUSeconds: 3, RSSBytes: 300},
		{PID: 12, PPID: 11, CPUSeconds: 4, RSSBytes: 400},
		{PID: 20, PPID: 1, CPUSeconds: 5, RSSBytes: 500},
	}
	got := machine.SumTrees(stats, []int{10, 20, 99})
	if use := got[10]; use.CPUSeconds != 9 || use.RSSBytes != 900 || use.Processes != 3 {
		t.Errorf("tree of 10: %+v", use)
	}
	if use := got[20]; use.CPUSeconds != 5 || use.Processes != 1 {
		t.Errorf("tree of 20: %+v", use)
	}
	if _, ok := got[99]; ok {
		t.Error("a root that is gone was answered")
	}
}

func TestThisMachineSamples(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	reader := machine.Reader{Processes: osexec.Runner{}}
	dir := t.TempDir()
	sample, err := reader.Sample(context.Background(), []lanes.Volume{{Role: lanes.VolumeState, Path: dir}, {Role: lanes.VolumeWorktrees, Path: filepath.Join(dir, "gone")}})
	if err != nil {
		t.Fatal(err)
	}
	if sample.Cores <= 0 || sample.CPU == nil || sample.CPU.Total == 0 || sample.Load == nil || sample.MemoryTotalBytes == nil || sample.MemoryAvailableBytes == nil {
		t.Errorf("sample %+v", sample)
	}
	if len(sample.Volumes) != 1 || sample.Volumes[0].Role != lanes.VolumeState || sample.Volumes[0].FreeBytes <= 0 || sample.Volumes[0].TotalBytes < sample.Volumes[0].FreeBytes {
		t.Errorf("volumes %+v", sample.Volumes)
	}
	own := os.Getpid()
	trees, err := reader.ProcessTrees(context.Background(), []int{own, 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if use, ok := trees[own]; !ok || use.RSSBytes <= 0 || use.Processes < 1 {
		t.Errorf("own tree %+v", trees)
	}
	if _, ok := trees[1<<30]; ok {
		t.Error("a process that does not exist was answered")
	}
}

func TestAPlatformWithNoSampleIsAnError(t *testing.T) {
	reader := machine.Reader{GOOS: "plan9"}
	if _, err := reader.Sample(context.Background(), nil); err == nil {
		t.Error("a platform with no sample passed")
	}
	if _, err := reader.ProcessTrees(context.Background(), []int{1}); err == nil {
		t.Error("a platform with no process reads passed")
	}
}
