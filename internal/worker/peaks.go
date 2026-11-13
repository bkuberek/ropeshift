package worker

import "ropeshift.com/ropeshift/internal/lanes"

// lanePeaks is the peaks of the lanes this worker ran, oldest first, which the recommendation reads once
// enough lanes have run.
func (w *Worker) lanePeaks() []lanes.LanePeak { return nil }
