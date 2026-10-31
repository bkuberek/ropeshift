// Command licensereport prints one line per module in the build list and exits
// non-zero if any module fails the license rule.
package main

import (
	"fmt"
	"os"

	"ropeshift.dev/ropeshift/test/rules"
)

func main() {
	modules, err := rules.ListModules(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	report, violations := rules.CheckLicenses(modules)
	for _, line := range report {
		fmt.Println(line)
	}
	fmt.Printf("%d modules, %d violations\n", len(modules), len(violations))
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, v)
	}
	if len(violations) > 0 {
		os.Exit(1)
	}
}
