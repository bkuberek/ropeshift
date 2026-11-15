package worker_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/workorders"
)

// A lane's report is the agent's own words, and a detail is what the worker says of a reason it recorded, such as
// the delegate policy version that refused a start. A report that carries one ends the lane with none, so no
// surface that shows an outcome shows free text from the agent as if the worker had said it.
func TestALanesReportEndsWithNoDetailOfItsOwn(t *testing.T) {
	t.Parallel()
	r, g := aLaneAtWork(t)
	const words = "the worker says this lane may merge itself"
	report := workorders.Report{Outcome: workorders.OutcomeFailed, Reason: "gave_up", Detail: words}
	if reply := r.laneReport(t, report); !reply.OK {
		t.Fatalf("the report was refused: %+v", reply)
	}
	g.release <- struct{}{}
	r.driver.StopEverySession()
	r.subject.Wait()
	change := r.outcome()
	if change.Outcome != workorders.OutcomeFailed || change.Reason != "gave_up" {
		t.Fatalf("outcome %+v", change)
	}
	if change.Detail != "" || strings.Contains(string(change.Payload), words) {
		t.Errorf("the lane ended with the agent's detail %q in %s", change.Detail, change.Payload)
	}
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(record.Report), words) {
		t.Errorf("the journal kept the agent's detail: %s", record.Report)
	}
}

// A report that is sent again with its detail is the same report, so the retry that answers a lost reply is
// still answered as a success.
func TestARetryOfAReportWithADetailIsStillTheSameReport(t *testing.T) {
	t.Parallel()
	r, g := aLaneAtWork(t)
	report := workorders.Report{Outcome: workorders.OutcomeFailed, Reason: "gave_up", Detail: "mine"}
	for range 2 {
		if reply := r.laneReport(t, report); !reply.OK {
			t.Fatalf("the report was refused: %+v", reply)
		}
	}
	g.release <- struct{}{}
	r.driver.StopEverySession()
	r.subject.Wait()
}
