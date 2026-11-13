//go:build !linux && !darwin

package machine

import "ropeshift.com/ropeshift/internal/lanes"

// readVolumes reads nothing on a system this package has no statfs for.
func readVolumes([]lanes.Volume) []lanes.VolumeSample { return nil }
