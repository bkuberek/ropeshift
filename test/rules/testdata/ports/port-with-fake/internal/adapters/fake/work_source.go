package fake

import "ropeshift.com/ropeshift/internal/ports"

// WorkSource is the fake.
type WorkSource struct{}

// Poll does nothing.
func (WorkSource) Poll() error { return nil }

var _ ports.WorkSource = WorkSource{}
