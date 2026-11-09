// Command licensereport prints one line per module in the build list and exits
// non-zero if any module fails the license rule.
package main

import (
	"fmt"
	"os"

	"ropeshift.com/ropeshift/test/rules"
)

func main() {
	modules, err := rules.ListModules(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	report, violations := rules.CheckLicenses(modules)
	web, webViolations, err := rules.CheckWebLicenses("web/package-lock.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	violations = append(violations, webViolations...)
	for _, line := range append(report, web...) {
		fmt.Println(line)
	}
	fmt.Printf("%d modules, %d web packages, %d violations\n", len(modules), len(web), len(violations))
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, v)
	}
	if len(violations) > 0 {
		os.Exit(1)
	}
}
