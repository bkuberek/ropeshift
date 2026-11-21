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

// UploadLaneSettingsHeld carries what a lane's worktree changed of the project settings its agent CLI reads, in a key
// that runs code or widens access, which held a generation before its session started (ADR-0040). It passes no fence,
// as what a lane loaded does not.
const UploadLaneSettingsHeld = "lane_settings_held"

// EventLaneSettingsHeld records on a lane the settings change one generation was held on, and
// EventLaneSettingsDecided a member's choice for it.
const (
	EventLaneSettingsHeld    = "lane.settings_held"
	EventLaneSettingsDecided = "lane.settings_decided"
)

// SettingsDecision is a member's choice for a lane whose settings widened what it can run: the generation that was
// held, the digest of the change the member read, and the choice, one of lanes.SettingsChoices.
type SettingsDecision struct {
	Generation int64  `json:"generation"`
	Digest     string `json:"digest"`
	Choice     string `json:"choice"`
}
