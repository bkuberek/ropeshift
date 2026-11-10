package measures

import (
	"cmp"
	"slices"
)

// The outcomes, decisions, agreements, and sources a calibration record holds, as the measures read them.
const (
	reviewPass     = "pass"
	reviewFail     = "fail"
	decisionAccept = "accept"
	decisionRework = "rework"
	agreed         = "agree"
	disagreed      = "disagree"
	laterDefect    = "later_defect"
)

// thirtyDays is the span of the recent period of review agreement, in milliseconds.
const thirtyDays int64 = 30 * 24 * 60 * 60 * 1000

// RepositoryFacts is what the measures read of one repository: the merge authority level in force now.
type RepositoryFacts struct {
	ID             string
	MergeAuthority string
}

// CalibrationFacts is what the measures read of one member decision on work a review judged (FR-CAL-1).
type CalibrationFacts struct {
	At         int64
	Repository string
	Outcome    string
	Decision   string
	Agreement  string
	Source     string
}

// Agreement is how a member's decisions stood against the review agent's verdicts over one period (FR-CAL-2).
// It describes the past and nothing in it changes a repository's merge authority (FR-CAL-3).
type Agreement struct {
	// Decisions counts every member decision after verdicts, including a defect found later in approved work.
	Decisions int64 `json:"decisions"`
	Agreed    int64 `json:"agreed"`
	Disagreed int64 `json:"disagreed"`
	// Unknown counts decisions after a review that could not judge the work, which agree with nothing.
	Unknown int64 `json:"unknown"`
	// AgreementRate is agreed over agreed and disagreed. A decision with nothing to agree with is in neither.
	AgreementRate Ratio `json:"agreement_rate"`
	// ApprovalsOverturned counts work the review passed that the person sent back, or found a defect in later.
	ApprovalsOverturned int64 `json:"approvals_overturned"`
	// LaterDefects are the approvals overturned by a defect found after the work was accepted.
	LaterDefects int64 `json:"later_defects"`
	// FailuresOverridden counts work the review failed that the person accepted.
	FailuresOverridden int64 `json:"failures_overridden"`
}

// AgreementPeriods reads review agreement over the last 30 days and over all time, ending at the report's
// window.
type AgreementPeriods struct {
	Last30Days Agreement `json:"last_30_days"`
	AllTime    Agreement `json:"all_time"`
}

// RepositoryAgreement is the agreement over one repository, with the merge authority level in force now, so a
// person reads the record beside the level it is evidence for.
type RepositoryAgreement struct {
	Repository     string `json:"repository_id"`
	MergeAuthority string `json:"merge_authority"`
	AgreementPeriods
}

// ReviewAgreement is review agreement for the workspace and for each repository that has a record.
type ReviewAgreement struct {
	Workspace    AgreementPeriods      `json:"workspace"`
	Repositories []RepositoryAgreement `json:"repositories"`
}

func agreement(facts []CalibrationFacts, from, to int64) Agreement {
	var out Agreement
	for _, f := range facts {
		if f.At == 0 || f.At < from || f.At >= to {
			continue
		}
		out.Decisions++
		switch f.Agreement {
		case agreed:
			out.Agreed++
		case disagreed:
			out.Disagreed++
		default:
			out.Unknown++
		}
		if f.Outcome == reviewPass && f.Decision == decisionRework {
			out.ApprovalsOverturned++
			if f.Source == laterDefect {
				out.LaterDefects++
			}
		}
		if f.Outcome == reviewFail && f.Decision == decisionAccept {
			out.FailuresOverridden++
		}
	}
	out.AgreementRate = ratio(out.Agreed, out.Agreed+out.Disagreed)
	return out
}

func periods(facts []CalibrationFacts, now int64) AgreementPeriods {
	return AgreementPeriods{Last30Days: agreement(facts, now-thirtyDays, now), AllTime: agreement(facts, 0, now)}
}

// reviewAgreement reads every period of agreement as of the end of a window.
func reviewAgreement(h History, now int64) ReviewAgreement {
	out := ReviewAgreement{Workspace: periods(h.Calibrations, now), Repositories: []RepositoryAgreement{}}
	byRepository := map[string][]CalibrationFacts{}
	for _, f := range h.Calibrations {
		byRepository[f.Repository] = append(byRepository[f.Repository], f)
	}
	for _, repo := range h.Repositories {
		out.Repositories = append(out.Repositories, RepositoryAgreement{
			Repository: repo.ID, MergeAuthority: repo.MergeAuthority, AgreementPeriods: periods(byRepository[repo.ID], now),
		})
	}
	slices.SortFunc(out.Repositories, func(a, b RepositoryAgreement) int { return cmp.Compare(a.Repository, b.Repository) })
	return out
}
