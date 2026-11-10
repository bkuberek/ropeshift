package measures_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/measures"
)

const day = 24 * hour

func decided(at int64, repository, outcome, decision, agreement, source string) measures.CalibrationFacts {
	return measures.CalibrationFacts{At: at, Repository: repository, Outcome: outcome, Decision: decision, Agreement: agreement, Source: source}
}

// A history with a known answer for review agreement. Now is day 100, so the last 30 days start at day 70.
func calibrated() measures.History {
	return measures.History{
		Repositories: []measures.RepositoryFacts{
			{ID: "repo-b", MergeAuthority: "person_approves"}, {ID: "repo-a", MergeAuthority: "person_merges"}, {ID: "repo-quiet", MergeAuthority: "review_approves"},
		},
		Calibrations: []measures.CalibrationFacts{
			decided(10*day, "repo-a", "pass", "accept", "agree", "decision"),           // old
			decided(20*day, "repo-a", "pass", "rework", "disagree", "decision"),        // old, an approval overturned
			decided(80*day, "repo-a", "pass", "accept", "agree", "decision"),           // recent
			decided(81*day, "repo-a", "pass", "accept", "agree", "decision"),           // recent
			decided(82*day, "repo-a", "fail", "accept", "disagree", "decision"),        // recent, a failure overridden
			decided(83*day, "repo-a", "fail", "rework", "agree", "decision"),           // recent
			decided(84*day, "repo-a", "cannot_judge", "accept", "unknown", "decision"), // recent, nothing to agree with
			decided(90*day, "repo-a", "pass", "rework", "disagree", "later_defect"),    // recent, a defect found later
			decided(85*day, "repo-b", "pass", "rework", "disagree", "decision"),        // recent
			decided(86*day, "", "pass", "accept", "agree", "decision"),                 // work with no repository
			decided(150*day, "repo-a", "pass", "accept", "agree", "decision"),          // after the window
		},
	}
}

func TestReviewAgreementOverTheLastThirtyDaysAndAllTime(t *testing.T) {
	r := measures.Compute(calibrated(), measures.Window{Name: "7d", From: 93 * day, To: 100 * day})
	workspace := r.ReviewAgreement.Workspace

	// Everything up to now: ten decisions, five agreed, four disagreed, and one nobody could judge.
	all := workspace.AllTime
	if all.Decisions != 10 || all.Agreed != 5 || all.Disagreed != 4 || all.Unknown != 1 {
		t.Errorf("all time %+v", all)
	}
	if got := value(t, all.AgreementRate); all.AgreementRate.Numerator != 5 || all.AgreementRate.Denominator != 9 || got != 5.0/9 {
		t.Errorf("all-time rate %+v: a decision with nothing to agree with is in no rate", all.AgreementRate)
	}
	if all.ApprovalsOverturned != 3 || all.LaterDefects != 1 || all.FailuresOverridden != 1 {
		t.Errorf("all-time overturns %+v", all)
	}

	// The last thirty days leave out days 10 and 20, whatever window the report covers.
	recent := workspace.Last30Days
	if recent.Decisions != 8 || recent.Agreed != 4 || recent.Disagreed != 3 || recent.Unknown != 1 {
		t.Errorf("last 30 days %+v", recent)
	}
	if recent.ApprovalsOverturned != 2 || recent.LaterDefects != 1 || recent.FailuresOverridden != 1 {
		t.Errorf("last 30 days overturns %+v", recent)
	}
}

func TestReviewAgreementPerRepositoryNamesTheLevelInForce(t *testing.T) {
	r := measures.Compute(calibrated(), measures.Window{Name: "7d", From: 93 * day, To: 100 * day})
	byID := map[string]measures.RepositoryAgreement{}
	var order []string
	for _, repo := range r.ReviewAgreement.Repositories {
		byID[repo.Repository] = repo
		order = append(order, repo.Repository)
	}
	if len(order) != 3 || order[0] != "repo-a" || order[1] != "repo-b" || order[2] != "repo-quiet" {
		t.Fatalf("repositories %v, want every repository with a record, by ID", order)
	}
	a := byID["repo-a"]
	if a.MergeAuthority != "person_merges" || a.AllTime.Decisions != 8 || a.Last30Days.Decisions != 6 || a.Last30Days.FailuresOverridden != 1 || a.AllTime.ApprovalsOverturned != 2 {
		t.Errorf("repo-a %+v", a)
	}
	if b := byID["repo-b"]; b.MergeAuthority != "person_approves" || b.AllTime.Decisions != 1 || value(t, b.AllTime.AgreementRate) != 0 {
		t.Errorf("repo-b %+v", b)
	}
	quiet := byID["repo-quiet"]
	if quiet.MergeAuthority != "review_approves" || quiet.AllTime.Decisions != 0 || quiet.AllTime.AgreementRate.Value != nil {
		t.Errorf("a repository nobody has decided after a review reads %+v, and a rate of nothing is no rate", quiet)
	}
}

func TestReviewAgreementOfAnEmptyHistoryHasNoRate(t *testing.T) {
	r := measures.Compute(measures.History{}, measures.Window{Name: "7d", From: 93 * day, To: 100 * day})
	if r.ReviewAgreement.Workspace.AllTime.AgreementRate.Value != nil || r.ReviewAgreement.Workspace.Last30Days.Decisions != 0 {
		t.Errorf("%+v", r.ReviewAgreement)
	}
	if r.ReviewAgreement.Repositories == nil {
		t.Error("repositories encode as null, and a client reads a list")
	}
}
