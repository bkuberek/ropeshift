package machine_test

import (
	"context"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/machine"
	"ropeshift.dev/ropeshift/internal/adapters/osexec"
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
