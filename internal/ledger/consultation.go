package ledger

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/roster"
)

// VerbConsult begins a consultation: an orchestrator asks a stronger read-only roster agent to explore a question it
// cannot settle with confidence, in a headless session of its own (FR-ORCH-9). The ledger chooses the agent, sets the
// budget from `consultation_limit` and the project's daily and weekly limits, and holds the question for the
// consultation until it ends.
const VerbConsult = "consult"

// UploadConsultationEnded carries how a consultation a worker ran ended, with what its agent found and what it cost. It
// belongs to no lane and passes no fence: the question it is about names it.
const UploadConsultationEnded = "consultation_ended"

// The events of a consultation: its start, when the ledger held the question for it, and its end, with the agent, its
// class, the model, the cost, and the agent's options and recommendation.
const (
	EventConsultationStarted = "orchestrator.consultation_started"
	EventConsulted           = "orchestrator.consulted"
)

// SubjectConsultation is what the events of a question's consultations are about, by the question's ID. They are kept
// apart from the question's own events, so the replay of neither reads the other's.
const SubjectConsultation = "consultation"

// WakeConsultation is a consultation that ended on a question that still waits, which its orchestrator decides from.
const WakeConsultation = "consultation"

// ConsultationHolder is who holds a question while a consultation of it runs. It is not the orchestrator, so the
// orchestrator's turn ending lets nothing of it go, and no other holder takes the question until the consultation ends.
func ConsultationHolder(id string) string { return "consultation:" + id }

// ConsultationCitation is how a decision cites a consultation, which the consultation's event explains.
func ConsultationCitation(id string) string { return "consultation:" + id }

// ConsultationStartedID is the ID of the event that records a consultation's start. It is made of the consultation's
// ID, which the worker's call names as its target, so a target that began a consultation is found by it, whatever
// question the consultation is about.
func ConsultationStartedID(id string) string { return "consultation-started-" + id }

// ConsultHold is how long a consultation holds its question. It outlasts the longest session a worker lets a
// consultation run, so a consultation that ends lets the question go itself, and one whose worker went away leaves the
// question held no longer than this (FR-ORCH-9).
const ConsultHold = 45 * time.Minute

// The bounds of what a consultation reports, so its wake stays a wake.
const (
	MaxConsultationOptions = 8
	maxConsultationText    = 4000
	maxConsultationAsk     = 4000
	// MaxConsultationModel is the most bytes the model of a consultation's end holds.
	MaxConsultationModel = 128
)

// ErrInvalidConsultation marks a consultation the ledger must not accept.
var ErrInvalidConsultation = errors.New("invalid consultation")

// ConsultRequest is what an orchestrator asks of a consultation: what to explore, and the agent, when it names one.
type ConsultRequest struct {
	Ask   string `json:"ask"`
	Agent string `json:"agent,omitempty"`
}

// Check refuses a request that says nothing to explore, or too much.
func (r ConsultRequest) Check() error {
	switch {
	case strings.TrimSpace(r.Ask) == "":
		return fmt.Errorf("%w: say what the consultation explores in ask", ErrInvalidConsultation)
	case len(r.Ask) > maxConsultationAsk:
		return fmt.Errorf("%w: ask holds %d bytes, past %d", ErrInvalidConsultation, len(r.Ask), maxConsultationAsk)
	}
	return nil
}

// ConsultRepository is the repository a consultation reads, read-only, at the head of its default branch.
type ConsultRepository struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Base string `json:"base"`
}

// ConsultGrant is what the ledger allows a consultation: the question it is about and its text as an orchestrator
// reads it, the agent it runs, the most it may spend, the repository it reads when the question names one, and when
// its hold on the question ends.
type ConsultGrant struct {
	ID           string             `json:"id"`
	Question     string             `json:"question"`
	QuestionText string             `json:"question_text"`
	Ask          string             `json:"ask"`
	Agent        roster.Agent       `json:"agent"`
	BudgetUSD    float64            `json:"budget_usd"`
	Repository   *ConsultRepository `json:"repository,omitempty"`
	HoldUntil    int64              `json:"hold_until"`
}

// ConsultationRun is what a worker keeps of a consultation it began, from before its session starts until the event that
// carries its end is journaled: the ledger's grant, the project it was granted in, the lane of the orchestrator that
// asked, and what its session had cost when the worker last read it. A worker that starts again reads it to find the
// session a worker that went away left, and to end the consultation with what that session spent.
type ConsultationRun struct {
	Grant        ConsultGrant `json:"grant"`
	Project      string       `json:"project,omitempty"`
	Orchestrator string       `json:"orchestrator"`
	StartedAt    int64        `json:"started_at"`
	CostUSD      float64      `json:"cost_usd,omitempty"`
}

// ConsultStarted is what `consult` answers: the consultation it started, the agent and class it runs, and its budget.
// The answer comes on a later wake.
type ConsultStarted struct {
	Consultation string  `json:"consultation"`
	Question     string  `json:"question"`
	Agent        string  `json:"agent"`
	Class        string  `json:"class"`
	BudgetUSD    float64 `json:"budget_usd"`
	Next         string  `json:"next"`
}

// The ways a consultation ends.
const (
	// ConsultationReported says its agent reported its options and recommendation.
	ConsultationReported = "reported"
	// ConsultationAtCap says the worker stopped its session at its budget.
	ConsultationAtCap = "stopped_at_cap"
	// ConsultationUnreported says its session ended with no report.
	ConsultationUnreported = "no_report"
	// ConsultationFailed says its session could not start or run.
	ConsultationFailed = "failed"
)

var consultationOutcomes = []string{ConsultationReported, ConsultationAtCap, ConsultationUnreported, ConsultationFailed}

// ConsultationReport is what a consultation's agent reports: the options it found, the one it recommends, and why.
type ConsultationReport struct {
	Options        []string `json:"options"`
	Recommendation string   `json:"recommendation"`
	Rationale      string   `json:"rationale"`
}

// Check refuses a report with fewer than two options, no recommendation or rationale, or more than a wake holds.
func (r ConsultationReport) Check() error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidConsultation, fmt.Sprintf(format, args...))
	}
	switch {
	case len(r.Options) < 2:
		return invalid("a report names at least two options, and this names %d", len(r.Options))
	case len(r.Options) > MaxConsultationOptions:
		return invalid("a report names at most %d options, and this names %d", MaxConsultationOptions, len(r.Options))
	case strings.TrimSpace(r.Recommendation) == "":
		return invalid("a report names the option it recommends")
	case strings.TrimSpace(r.Rationale) == "":
		return invalid("a report says why it recommends that option")
	}
	for _, text := range append([]string{r.Recommendation, r.Rationale}, r.Options...) {
		if strings.TrimSpace(text) == "" || len(text) > maxConsultationText {
			return invalid("each option, the recommendation, and the rationale hold text of at most %d bytes", maxConsultationText)
		}
	}
	return nil
}

// ConsultationEnd is the data of EventConsulted: the consultation, the question, the agent it ran with its class and
// approval, the model its session ran on, what it cost, how it ended, and what its agent reported.
type ConsultationEnd struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Agent    string `json:"agent"`
	Class    string `json:"class"`
	// Approval is the agent's approval, per_use for one that asks for each start: a consultation within
	// `consultation_limit` counts as the approved use.
	Approval string  `json:"approval,omitempty"`
	Model    string  `json:"model,omitempty"`
	CostUSD  float64 `json:"cost_usd"`
	Outcome  string  `json:"outcome"`
	Detail   string  `json:"detail,omitempty"`
	ConsultationReport
}

// ConsultationModel makes the model a session reports fit what an end holds: one line of plain text, with each run of
// space a single space, and cut at MaxConsultationModel bytes on a character's edge. The agent CLI wrote it, and it
// stands in the line an orchestrator reads of the consultation.
func ConsultationModel(model string) string {
	model = strings.Join(strings.Fields(PlainText(model)), " ")
	if len(model) <= MaxConsultationModel {
		return model
	}
	cut := MaxConsultationModel
	for cut > 0 && !utf8.RuneStart(model[cut]) {
		cut--
	}
	return strings.TrimSpace(model[:cut])
}

// Check refuses an end that names no consultation, an outcome nobody offers, a cost that is no amount, a model that is
// not one bounded line of plain text, or a report that does not hold together. Only a reported end carries a report.
func (e ConsultationEnd) Check() error {
	switch {
	case !PlainID(e.ID) || !PlainID(e.Question):
		return fmt.Errorf("%w: an end names its consultation and its question", ErrInvalidConsultation)
	case !slices.Contains(consultationOutcomes, e.Outcome):
		return fmt.Errorf("%w: outcome %q", ErrInvalidConsultation, e.Outcome)
	case e.CostUSD < 0 || math.IsNaN(e.CostUSD) || math.IsInf(e.CostUSD, 0):
		return fmt.Errorf("%w: cost %v", ErrInvalidConsultation, e.CostUSD)
	case len(e.Detail) > maxConsultationText:
		return fmt.Errorf("%w: the detail holds more than %d bytes", ErrInvalidConsultation, maxConsultationText)
	case len(e.Model) > MaxConsultationModel:
		return fmt.Errorf("%w: the model holds %d bytes, past %d", ErrInvalidConsultation, len(e.Model), MaxConsultationModel)
	case strings.ContainsAny(e.Model, "\n\t") || PlainText(e.Model) != e.Model:
		return fmt.Errorf("%w: the model is not one line of plain text", ErrInvalidConsultation)
	case e.Outcome == ConsultationReported:
		return e.ConsultationReport.Check()
	case len(e.Options) != 0 || e.Recommendation != "" || e.Rationale != "":
		return fmt.Errorf("%w: only a reported consultation carries a report", ErrInvalidConsultation)
	}
	return nil
}
