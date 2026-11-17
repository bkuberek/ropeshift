//go:build !screenshots

package machine

import "ropeshift.com/ropeshift/internal/lanes"

// pinSample is the sample as read: a normal build reports the machine it runs on. The screenshots build
// reports a fixed one in its place (figures_screenshots.go).
func pinSample(sample lanes.MachineSample) lanes.MachineSample { return sample }

// pinReading is the reading as read, in a normal build.
func pinReading(reading lanes.MachineReading, err error) (lanes.MachineReading, error) {
	return reading, err
}
