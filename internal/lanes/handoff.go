package lanes

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// HandoffSchema names the contract a handoff follows (FR-LANE-9).
const HandoffSchema = "ropeshift.handoff/v1"

// HandoffContextThreshold is the reason of a handoff the context policy asked for.
const HandoffContextThreshold = "context_threshold"

// MaxNarrativeBytes bounds what an agent writes, because a handoff is read by every later
// generation in its brief.
const MaxNarrativeBytes = 32 << 10

// ErrBadHandoff marks a document or a narrative that does not follow the contract.
var ErrBadHandoff = errors.New("this does not follow the handoff contract")

// ContractRef names the contract version a lane works to.
type ContractRef struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

// PackRef names the context pack a lane read.
type PackRef struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

// CheckFact is one check as the ledger holds it.
type CheckFact struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// HandoffFacts is the part the worker determines. A list the worker could not read stays
// nil and out of the document, because an empty list says there is nothing.
type HandoffFacts struct {
	WorkOrder       string       `json:"work_order"`
	Contract        *ContractRef `json:"contract,omitempty"`
	ContextPack     *PackRef     `json:"context_pack,omitempty"`
	Branch          string       `json:"branch,omitempty"`
	Head            string       `json:"head,omitempty"`
	SnapshotRef     string       `json:"snapshot_ref,omitempty"`
	DirtyPaths      []string     `json:"dirty_paths,omitzero"`
	UnpushedCommits int64        `json:"unpushed_commits"`
	OpenQuestions   []string     `json:"open_questions,omitzero"`
	OpenAssumptions []string     `json:"open_assumptions,omitzero"`
	Checks          []CheckFact  `json:"checks,omitzero"`
	ModelResolved   string       `json:"model_resolved,omitempty"`
}

// Narrative is the part only the agent knows. CarriesNextStep is its statement that this
// holds what the next step needs, and absent or false means it does not (D11).
type Narrative struct {
	Done            []string `json:"done"`
	InProgress      []string `json:"in_progress"`
	NextSteps       []string `json:"next_steps"`
	Risks           []string `json:"risks"`
	CarriesNextStep bool     `json:"carries_next_step"`
}

// Handoff is one document of the contract. A nil Narrative is the failure edge: only the
// deterministic part exists.
type Handoff struct {
	Schema        string       `json:"schema"`
	Lane          string       `json:"lane_id"`
	Generation    int64        `json:"generation"`
	Reason        string       `json:"reason"`
	Deterministic HandoffFacts `json:"deterministic"`
	Narrative     *Narrative   `json:"narrative,omitempty"`
}

// HandoffProgress is how far a lane's handoff got, in the worker's journal, so a restart
// goes on from there.
type HandoffProgress struct {
	Reason string `json:"reason"`
	// DeadlineMs is when the agent's time runs out, on the worker's clock.
	DeadlineMs int64 `json:"deadline_ms"`
	// Narrative is what the agent wrote, once it did.
	Narrative *Narrative `json:"narrative,omitempty"`
	// Refused is why what the agent wrote could not be read, which ends the wait.
	Refused string `json:"refused,omitempty"`
	// Document and Digest are the stored document, once there is one.
	Document []byte `json:"document,omitempty"`
	Digest   string `json:"digest,omitempty"`
	// Begun says the next generation exists, so the document is its brief and nothing is
	// left to finish.
	Begun bool `json:"begun,omitempty"`
	// MidTurn says the instruction must reach a turn already running.
	MidTurn bool `json:"mid_turn,omitempty"`
}

// Degraded says only the deterministic part exists.
func (h Handoff) Degraded() bool { return h.Narrative == nil }

// Seal validates a document and returns its bytes and their digest. The bytes are the
// document's only form, so a digest pins one reading of it.
func (h Handoff) Seal() ([]byte, string, error) {
	if err := h.validate(); err != nil {
		return nil, "", err
	}
	body, err := json.Marshal(h)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(body)
	return body, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (h Handoff) validate() error {
	switch {
	case h.Schema != HandoffSchema:
		return fmt.Errorf("%w: schema %q", ErrBadHandoff, h.Schema)
	case h.Lane == "":
		return fmt.Errorf("%w: no lane", ErrBadHandoff)
	case h.Generation <= 0:
		return fmt.Errorf("%w: generation %d", ErrBadHandoff, h.Generation)
	case h.Reason == "":
		return fmt.Errorf("%w: no reason", ErrBadHandoff)
	case h.Deterministic.WorkOrder == "":
		return fmt.Errorf("%w: no work order", ErrBadHandoff)
	}
	if h.Narrative != nil {
		return h.Narrative.check()
	}
	return nil
}

// ReadHandoff reads a stored document. One a newer build wrote is refused, and not guessed
// at, because a later generation would act on it.
func ReadHandoff(body []byte) (Handoff, error) {
	var out Handoff
	if err := json.Unmarshal(body, &out); err != nil {
		return Handoff{}, fmt.Errorf("%w: %v", ErrBadHandoff, err)
	}
	if err := out.validate(); err != nil {
		return Handoff{}, err
	}
	return out, nil
}

// ParseNarrative reads what an agent wrote. It holds to the schema, because what it passes
// here reaches every later generation as fact.
func ParseNarrative(raw []byte) (Narrative, error) {
	if len(raw) > MaxNarrativeBytes {
		return Narrative{}, fmt.Errorf("%w: %d bytes, and the limit is %d", ErrBadHandoff, len(raw), MaxNarrativeBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var out Narrative
	if err := decoder.Decode(&out); err != nil {
		return Narrative{}, fmt.Errorf("%w: %v", ErrBadHandoff, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Narrative{}, fmt.Errorf("%w: more than one document", ErrBadHandoff)
	}
	if err := out.check(); err != nil {
		return Narrative{}, err
	}
	return out, nil
}

func (n Narrative) check() error {
	said := 0
	for _, list := range [][]string{n.Done, n.InProgress, n.NextSteps, n.Risks} {
		for _, item := range list {
			if strings.TrimSpace(item) == "" {
				return fmt.Errorf("%w: a blank item", ErrBadHandoff)
			}
			said++
		}
	}
	if said == 0 {
		return fmt.Errorf("%w: it says nothing", ErrBadHandoff)
	}
	return nil
}

// Open says a handoff has something left to finish.
func (p *HandoffProgress) Open() bool { return p != nil && !p.Begun }
