//go:build linux || darwin

package machine

import (
	"golang.org/x/sys/unix"

	"ropeshift.com/ropeshift/internal/lanes"
)

// readVolumes reads the filesystem of each volume with statfs, and the device it is on with stat, so two
// directories on one filesystem can count once. A volume it cannot read is left out.
func readVolumes(volumes []lanes.Volume) []lanes.VolumeSample {
	out := make([]lanes.VolumeSample, 0, len(volumes))
	for _, volume := range volumes {
		var fs unix.Statfs_t
		var st unix.Stat_t
		if unix.Statfs(volume.Path, &fs) != nil || unix.Stat(volume.Path, &st) != nil {
			continue
		}
		// Each field is a different integer type on each system, so every one converts.
		block := int64(fs.Bsize)
		out = append(out, lanes.VolumeSample{
			Volume: volume, Device: uint64(st.Dev), FreeBytes: int64(fs.Bavail) * block, TotalBytes: int64(fs.Blocks) * block,
		})
	}
	return out
}
