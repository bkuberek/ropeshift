package cli

// ForecastText, LookBackText, and CalibrationText are what `plan forecast`, `measures delivery`, and `measures
// estimates` print, and EstimateLines the estimate and baseline lines of `work show`.
var (
	EstimateLines   = estimateLines
	ForecastText    = forecastText
	LookBackText    = lookBackText
	CalibrationText = calibrationText
)
