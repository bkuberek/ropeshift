package ledger

// UploadLaneSetup carries what a lane's session loaded at its start, as its agent CLI reported it, with the pieces of
// the member's own CLI setup among them. It passes no fence, because what a session loaded stays true after its lane
// moved on.
const UploadLaneSetup = "lane_setup"

// EventLaneSetup records on a lane what one generation's session loaded at its start.
const EventLaneSetup = "lane.setup_loaded"

// SubjectWorker is what a project's events about one worker of its lanes are about.
const SubjectWorker = "worker"

// EventPersonalSetupAccepted records that a member accepted a piece of their own agent CLI setup for one worker's lanes on
// one CLI version, so the leak is not raised again there.
const EventPersonalSetupAccepted = "worker.personal_setup_accepted"

// SetupAcceptance is what a member accepted: the CLI version, and the kind and name of the piece.
type SetupAcceptance struct {
	Version string `json:"version"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
}
