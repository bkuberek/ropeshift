package cli_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
)

// A plan's forecasts show in the web UI in the words `plan forecast` prints, which this holds to the same
// goldens the web tests read.

func TestThePlanForecastGoldensPrintAsTheWebTestsRead(t *testing.T) {
	for _, name := range []string{"forecast", "forecast-pending", "forecast-superseded"} {
		var view api.PlanForecast
		readGolden(t, name+".json", &view)
		holdsText(t, name+".cli.txt", cli.ForecastText(view))
	}
}
