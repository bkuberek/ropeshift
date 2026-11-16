package delivery

import (
	"fmt"
	"strings"
	"time"
)

// VersionNotice writes what the control plane tells a planning lane of the plan version it submitted (FR-EST-12):
// each forecast of the version and each estimate of its work, with its confidence label and the explanation stored
// with it, and the work by the title the plan gave it. It reads only what the estimate and the forecast hold, so a
// tier 6 estimate shows the band it kept, which hides every record of another project, and no explanation says
// more than it did where it was made. A version with no forecast says the forecast is pending, and one with no
// estimate set says no work of it has an estimate.
func VersionNotice(plan string, version int64, set *EstimateSet, forecasts []Forecast, titles map[string]string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Plan version %d of %s: ropeshift's estimates and forecasts for what you submitted. They are estimates drawn from "+
		"finished work, and no promise; each explanation says what it drew on. You size the work and choose its agents; you never write an estimate. "+
		"Read the explanation under each label before you rely on it.\n", version, oneLine(plan))
	out.WriteString("\n### Forecasts\n\n")
	if len(forecasts) == 0 {
		fmt.Fprintf(&out, "Forecast pending: the forecast tick has not forecast version %d yet.\n", version)
	}
	for _, f := range forecasts {
		out.WriteString(f.noticeLine())
	}
	out.WriteString("\n### Estimates\n\n")
	if set == nil || len(set.Estimates) == 0 {
		out.WriteString("No work of this version has an estimate.\n")
		return out.String()
	}
	for _, e := range set.Estimates {
		out.WriteString(e.noticeLine(titles[e.WorkOrder]))
	}
	return out.String()
}

// oneLine reads a title as one line of text, so a title with a line break in it cannot start a section of its own.
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

// noticeLine is one forecast as a line: whose it is, its label, its dates and costs where it has them, and the words it
// was made with. A date or cost that is a lower bound reads "or later" and "at least".
func (f Forecast) noticeLine() string {
	whose := "plan"
	if f.Milestone != "" {
		whose = "milestone " + f.Milestone
	}
	if f.Status != Forecasted {
		return fmt.Sprintf("- %s: %s. %s\n", whose, strings.ReplaceAll(string(f.Status), "_", " "), f.Explanation)
	}
	all, p85 := f.LowerBound.OpensP50(), f.LowerBound.OpensP85()
	return fmt.Sprintf("- %s: %s; p50 %s, p85 %s; cost p50 %s, p85 %s. %s\n", whose, confidenceWords(f.Confidence),
		dateWords(f.EndP50, all), dateWords(f.EndP85, p85), costWords(f.CostP50USD, all), costWords(f.CostP85USD, p85), f.Explanation)
}

// noticeLine is one estimate as a line: its work order, by title where the plan gave one, its label, its band where it
// has one, and the words it was made with. An open band reads "more than" its longest record, and a cost with a lower
// bound "at least".
func (e Estimate) noticeLine(title string) string {
	name := e.WorkOrder
	if title = oneLine(title); title != "" {
		name += fmt.Sprintf(" %q", title)
	}
	if e.Status == NoHistory {
		return fmt.Sprintf("- %s: no history. %s\n", name, e.Explanation)
	}
	open, bound := "", ""
	if e.P85Open {
		open = "more than "
	}
	if e.CostAtLeast {
		bound = "at least "
	}
	return fmt.Sprintf("- %s: %s; delivery p50 %s, p85 %s%s; cost %sp50 $%.2f, p85 %s$%.2f. %s\n", name, confidenceWords(e.Confidence),
		durationWords(e.DeliveryP50Ms), open, durationWords(e.DeliveryP85Ms), bound, e.CostP50USD, open, e.CostP85USD, e.Explanation)
}

// dateWords says a time in epoch milliseconds to the minute in UTC, with "or later" when it is a lower bound.
func dateWords(ms int64, lowerBound bool) string {
	text := time.UnixMilli(ms).UTC().Format("2006-01-02 15:04 UTC")
	if lowerBound {
		text += " or later"
	}
	return text
}

// costWords says an estimated cost, with "at least" when it is a lower bound.
func costWords(usd float64, lowerBound bool) string {
	if lowerBound {
		return fmt.Sprintf("at least $%.2f", usd)
	}
	return fmt.Sprintf("$%.2f", usd)
}

// confidenceWords says a label as a person reads it: "confident", or "low confidence".
func confidenceWords(c Confidence) string {
	if c == Low {
		return "low confidence"
	}
	return string(c)
}
