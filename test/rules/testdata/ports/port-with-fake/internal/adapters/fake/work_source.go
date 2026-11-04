package fake

import "ropeshift.dev/ropeshift/internal/ports"

// WorkSource is the fake.
type WorkSource struct{}

// Poll does nothing.
func (WorkSource) Poll() error { return nil }

var _ ports.WorkSource = WorkSource{}
