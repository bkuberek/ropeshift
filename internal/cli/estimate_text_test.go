package cli_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
)

// A work order's estimate and its baseline, and a plan approval's estimates, show in the web UI in the words
// `work show` and `plan forecast` print, which this holds to the same goldens the web tests read.

func TestTheWorkEstimateGoldensPrintAsTheWebTestsRead(t *testing.T) {
	for _, name := range []string{"work-estimate", "work-estimate-low", "work-estimate-open", "work-estimate-none", "work-estimate-absent"} {
		var view api.WorkEstimate
		readGolden(t, name+".json", &view)
		holdsText(t, name+".cli.txt", cli.EstimateLines(view.Estimate, view.Baseline))
	}
}

func TestThePlanApprovalGoldenPrintsAsTheWebTestsRead(t *testing.T) {
	var view api.PlanView
	readGolden(t, "plan-estimates.json", &view)
	holdsText(t, "plan-estimates.cli.txt", cli.ForecastText(*view.Approval.Forecast))
}
