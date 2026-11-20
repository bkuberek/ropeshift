package ledger

// UploadLaneSetup carries what a lane's session loaded at its start, as its agent CLI reported it, with the pieces of
// the member's own CLI setup among them. It passes no fence, because what a session loaded stays true after its lane
// moved on.
const UploadLaneSetup = "lane_setup"

// EventLaneSetup records on a lane what one generation's session loaded at its start.
const EventLaneSetup = "lane.setup_loaded"
