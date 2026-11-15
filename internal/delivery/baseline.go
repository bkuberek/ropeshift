package delivery

import (
	"encoding/json"

	"ropeshift.com/ropeshift/internal/ledger"
)

// EventBaselined records the baseline a plan approval recorded, in the approval's transaction (ADR-0026,
// FR-EST-10).
const EventBaselined = "estimate.baselined"

// SubjectBaseline names a baseline as the subject of an event.
const SubjectBaseline = "estimate_baseline"

// Baseline is what finished work is measured against (ADR-0026, FR-EST-10): the estimate set and the forecasts a
// plan approval showed, recorded in the approval's transaction, or the estimates a work order made outside a plan
// was made with, recorded when it was made, which has no approval. SetID and SetDigest name the set it showed, and
// are empty when it showed none. Forecasts are the forecasts it showed, kept whole, because the forecast tick
// replaces a day's forecast later that day; an approval that showed "forecast pending" has none. Rev is the ledger
// revision its transaction reached. A baseline never changes, and a work order's or a milestone's is the first one
// that holds it.
type Baseline struct {
	ID          string     `json:"id"`
	Approval    string     `json:"approval_id,omitempty"`
	Plan        string     `json:"plan_id,omitempty"`
	PlanVersion int64      `json:"plan_version,omitempty"`
	SetID       string     `json:"estimate_set_id,omitempty"`
	SetDigest   string     `json:"estimate_set_digest,omitempty"`
	Forecasts   []Forecast `json:"forecasts"`
	Rev         int64      `json:"ledger_rev"`
	CreatedAt   int64      `json:"created_at"`
}

// ForecastOf returns the forecast the baseline holds of one of its plan's milestones, or of the plan's own for an
// empty milestone.
func (b Baseline) ForecastOf(milestone string) (Forecast, bool) {
	for _, f := range b.Forecasts {
		if f.Milestone == milestone {
			return f, true
		}
	}
	return Forecast{}, false
}

// baselined is what the event of a baseline says: the approval, the plan version, the set it showed with its
// digest, and the forecasts it showed by ID. The baseline's record holds those forecasts whole.
type baselined struct {
	ID          string   `json:"id"`
	Approval    string   `json:"approval_id"`
	Plan        string   `json:"plan_id,omitempty"`
	PlanVersion int64    `json:"plan_version,omitempty"`
	SetID       string   `json:"estimate_set_id,omitempty"`
	SetDigest   string   `json:"estimate_set_digest,omitempty"`
	Forecasts   []string `json:"forecast_ids"`
}

// BaselineEvent is the event a plan approval appends for the baseline it records, as the member who approved.
func BaselineEvent(b Baseline, actor ledger.Actor) (ledger.EventDraft, error) {
	said := baselined{ID: b.ID, Approval: b.Approval, Plan: b.Plan, PlanVersion: b.PlanVersion, SetID: b.SetID, SetDigest: b.SetDigest, Forecasts: []string{}}
	for _, f := range b.Forecasts {
		said.Forecasts = append(said.Forecasts, f.ID)
	}
	data, err := json.Marshal(said)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{Type: EventBaselined, Actor: actor, Subject: ledger.Subject{Kind: SubjectBaseline, ID: b.ID}, Data: data}, nil
}
