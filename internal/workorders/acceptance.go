package workorders

import (
	"errors"

	"ropeshift.com/ropeshift/internal/ledger"
)

// ErrNotAcceptable means a check failed, so nothing can accept this attempt.
var ErrNotAcceptable = errors.New("a check failed, so this work cannot be accepted")

// EventEvidenceRecorded records check results that arrived with no change of state.
const EventEvidenceRecorded = "work_order.evidence_recorded"

// EvidenceSource says who stands behind a check result.
type EvidenceSource string

// The sources of a check result.
const (
	// SourceForge is a source the lane did not write, such as CI on the branch's commit.
	SourceForge EvidenceSource = "forge"
	// SourceLane is the lane's own word.
	SourceLane EvidenceSource = "lane"
)

// CheckStatus is how a check ended.
type CheckStatus string

// The ends of a check.
const (
	CheckPassed CheckStatus = "passed"
	CheckFailed CheckStatus = "failed"
)

// Evidence is one check result on record, linked to the work order it speaks for. Fence
// names the attempt: a lane's end moves the fence, so an attempt's evidence shares one.
type Evidence struct {
	ID         string         `json:"id"`
	WorkOrder  string         `json:"work_order_id"`
	Lane       string         `json:"lane_id"`
	Fence      int64          `json:"fence"`
	ArtifactID string         `json:"artifact_id,omitempty"`
	Criterion  string         `json:"criterion_id,omitempty"`
	Name       string         `json:"name"`
	Status     CheckStatus    `json:"status"`
	Source     EvidenceSource `json:"source"`
	DetailsURI string         `json:"details_uri,omitempty"`
	CreatedAt  int64          `json:"created_at"`
}

// Standing is where one criterion stands.
type Standing string

// The standings of a criterion, from the acceptance table.
const (
	// StandingCovered counts by itself: a forge result passed.
	StandingCovered Standing = "covered"
	// StandingFailed means a check that maps to the criterion failed.
	StandingFailed Standing = "failed"
	// StandingSecondPrincipal means only the lane says the criterion holds.
	StandingSecondPrincipal Standing = "second_principal"
	// StandingMember means no check maps to the criterion.
	StandingMember Standing = "member"
)

// Need is the least a principal must bring for a work order to be accepted.
type Need string

// The needs of an assessment, weakest first.
const (
	NeedNothing         Need = "nothing"
	NeedSecondPrincipal Need = "second_principal"
	NeedMember          Need = "member"
)

// CriterionStanding is one criterion and where it stands.
type CriterionStanding struct {
	Criterion string   `json:"criterion_id"`
	Standing  Standing `json:"standing"`
}

// Assessment is what an attempt's evidence says about a contract's criteria.
type Assessment struct {
	Criteria []CriterionStanding `json:"criteria"`
	// Failed names every check that failed, mapped to a criterion or not.
	Failed []string `json:"failed,omitempty"`
	Needs  Need     `json:"needs"`
}

// Assess follows the acceptance table: it compares a contract's criteria with evidence the
// lane did not write by itself. A failure anywhere in the attempt stands, whatever else
// passed, and a work order with no criteria has nothing evidence could cover.
func Assess(criteria []string, evidence []Evidence) Assessment {
	out := Assessment{Criteria: make([]CriterionStanding, 0, len(criteria)), Needs: NeedNothing}
	if len(criteria) == 0 {
		out.Needs = NeedMember
	}
	for _, id := range criteria {
		standing := standingOf(id, evidence)
		out.Criteria = append(out.Criteria, CriterionStanding{Criterion: id, Standing: standing})
		out.Needs = strongest(out.Needs, needOf(standing))
	}
	for _, row := range evidence {
		if row.Status != CheckPassed {
			out.Failed = append(out.Failed, row.Name)
			out.Needs = NeedMember
		}
	}
	return out
}

func standingOf(criterion string, evidence []Evidence) Standing {
	standing := StandingMember
	for _, row := range evidence {
		if row.Criterion != criterion {
			continue
		}
		switch {
		case row.Status != CheckPassed:
			return StandingFailed
		case row.Source == SourceForge:
			standing = StandingCovered
		case standing == StandingMember:
			standing = StandingSecondPrincipal
		}
	}
	return standing
}

func needOf(standing Standing) Need {
	switch standing {
	case StandingCovered:
		return NeedNothing
	case StandingSecondPrincipal:
		return NeedSecondPrincipal
	}
	return NeedMember
}

func strongest(a, b Need) Need {
	if a == NeedMember || b == NeedMember {
		return NeedMember
	}
	if a == NeedSecondPrincipal || b == NeedSecondPrincipal {
		return NeedSecondPrincipal
	}
	return NeedNothing
}

// AcceptanceView is a work order's standing, with everything linked to it. Assessment reads
// the current attempt, and Artifacts and Checks hold every attempt's.
type AcceptanceView struct {
	WorkOrder  string            `json:"work_order_id"`
	State      State             `json:"state"`
	Assessment Assessment        `json:"assessment"`
	Artifacts  []ledger.Artifact `json:"artifacts"`
	Checks     []Evidence        `json:"checks"`
}
