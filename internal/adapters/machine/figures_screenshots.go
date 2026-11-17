//go:build screenshots

package machine

import "ropeshift.com/ropeshift/internal/lanes"

// The screenshots build, which make screenshots and make guide-shots capture, reports one fixed machine in
// place of the one it runs on, so every image of the web UI shows the same plausible figures and nothing of
// the machine that captured it (docs/development/screenshots.md). The fixed machine reports what a Mac
// reports of itself: no machine CPU use and no memory pressure. A normal build holds none of this, which
// test/rules checks.
var (
	fixedCores           int64 = 10
	fixedLoad                  = lanes.LoadAverage{One: 1.84, Five: 1.62, Fifteen: 1.47}
	fixedMemory                = fixedSize("32 GiB")
	fixedMemoryUsed            = fixedSize("9.6 GiB")
	fixedDiskFree              = fixedSize("412 GiB")
	fixedDiskTotal             = fixedSize("926 GiB")
	fixedMemoryAvailable       = fixedMemory - fixedMemoryUsed
)

func fixedSize(text string) int64 {
	size, err := lanes.ParseBytes(text)
	if err != nil {
		panic(err)
	}
	return size
}

// pinSample keeps the volumes the read found, each with its device, so two on one filesystem still count
// once, and replaces every figure with the fixed machine's.
func pinSample(read lanes.MachineSample) lanes.MachineSample {
	total, available, load := fixedMemory, fixedMemoryAvailable, fixedLoad
	sample := lanes.MachineSample{Cores: fixedCores, Load: &load, MemoryTotalBytes: &total, MemoryAvailableBytes: &available}
	for _, volume := range read.Volumes {
		volume.FreeBytes, volume.TotalBytes = fixedDiskFree, fixedDiskTotal
		sample.Volumes = append(sample.Volumes, volume)
	}
	return sample
}

// pinReading is what admission reads of the fixed machine, whatever the read found.
func pinReading(lanes.MachineReading, error) (lanes.MachineReading, error) {
	return lanes.MachineReading{
		Known: true, FreeMemoryMB: fixedMemoryAvailable / bytesPerMB, FreeCPUPercent: FreeCPU(fixedLoad.One, fixedCores),
	}, nil
}
