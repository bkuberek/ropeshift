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

// requireGit refuses a command that reads git in this process when git cannot run ropeshift: older than the minimum,
// or not there. It says the doctor's words and returns ExitError, and ExitOK when git is usable or this build has no
// check. A command calls it before its first git read, and before it writes anything.
func (c *invocation) requireGit() int {
	if section := c.gitSection(); section != nil && !section.Usable() {
		return c.refuse(ExitError, "%s", section.Problem)
	}
	return ExitOK
}

// gitText states the doctor's git section the way a driver's line reads: the release found and the minimum, or
// unusable and the problem with how to fix it.
func gitText(report lanes.GitReport) string {
	if !report.Usable() {
		return fmt.Sprintf("git\tunusable\t%s\n", report.Problem)
	}
	return fmt.Sprintf("git\t%s\tminimum %s\n", report.Found, report.Minimum)
}
