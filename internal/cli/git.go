package cli

import (
	"fmt"

	"ropeshift.com/ropeshift/internal/lanes"
)

// gitSection asks the machine which git it runs. A build with no check has no section.
func (c *invocation) gitSection() *lanes.GitReport {
	if c.git == nil {
		return nil
	}
	report := c.git(c.ctx)
	return &report
}

// gitText states the doctor's git section the way a driver's line reads: the release found and the minimum, or
// unusable and the problem with how to fix it.
func gitText(report lanes.GitReport) string {
	if !report.Usable() {
		return fmt.Sprintf("git\tunusable\t%s\n", report.Problem)
	}
	return fmt.Sprintf("git\t%s\tminimum %s\n", report.Found, report.Minimum)
}
