package api

import (
	"context"
	"net/http"
	"slices"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// NeedLaneSettings is a lane whose worktree changed the project settings its agent CLI reads, in a key that runs code
// or widens access, which parked before its session started: one item for each lane, in the security category, which
// only a member answers (ADR-0040).
const NeedLaneSettings = "lane_settings"

// CategorySecurity is the category of a lane held on its own settings, the workspace's built-in security category,
// which keeps it with a member: a choice about what runs outside every rule, never an orchestrator's.
const CategorySecurity = questions.CategorySecurity

// What a member can do about a lane held on its own settings, the recommended choice first, each naming the command
// that does the same: `lane settings --run base`, `--run lane`, or `--run stop`.
const (
	ActSettingsBase = "settings_base"
	ActSettingsLane = "settings_lane"
	ActSettingsStop = "settings_stop"
)

// LaneSettingsActions lists what a read model can offer about a lane held on its own settings, so every surface can be
// held to knowing each one.
var LaneSettingsActions = []string{ActSettingsBase, ActSettingsLane, ActSettingsStop}

// LaneSettingsActionsFor is what a hold offers: every action, less running with the repository's settings for a hold
// that offers no restore (lanes.SettingsChange.Restorable).
func LaneSettingsActionsFor(change lanes.SettingsChange) []string {
	if change.Restorable() {
		return slices.Clone(LaneSettingsActions)
	}
	return []string{ActSettingsLane, ActSettingsStop}
}

// CodeSettingsNotRestorable refuses a choice to run a lane with the repository's settings when its hold comes from a
// script a hook or a setting of the repository runs, which a restore would revert with the lane's work.
const CodeSettingsNotRestorable = "settings_not_restorable"

// LaneSettingsNeed is a lane held on its own settings: the lane and its work, what it changed, which is what a member's
// choice binds to, the category that keeps it with a member, the choice recommended (lanes.SettingsAdvice) with why, and
// the actions it offers (LaneSettingsActionsFor).
type LaneSettingsNeed struct {
	Lane                    string                `json:"lane"`
	WorkOrder               *workorders.WorkOrder `json:"work_order"`
	Category                string                `json:"category"`
	Change                  lanes.SettingsChange  `json:"change"`
	Recommendation          string                `json:"recommendation"`
	RecommendationRationale string                `json:"recommendation_rationale"`
	Actions                 []string              `json:"actions"`
}

// DecideLaneSettings is a member's choice for a lane held on its own settings: the lane, the digest of the change they
// read, and the choice, one of lanes.SettingsChoices.
type DecideLaneSettings struct {
	Lane   string `json:"lane"`
	Digest string `json:"digest"`
	Choice string `json:"choice"`
}

// LaneSettingsDecided is what a choice left: the work order and the state it is in.
type LaneSettingsDecided struct {
	WorkOrder string `json:"work_order"`
	State     string `json:"state"`
	Choice    string `json:"choice"`
}

// DecideLaneSettings records a member's choice for a lane held on its own settings.
func (p *ProjectClient) DecideLaneSettings(ctx context.Context, workOrder string, in DecideLaneSettings) (LaneSettingsDecided, error) {
	var out LaneSettingsDecided
	err := p.do(ctx, http.MethodPost, workOrderPath(workOrder, "lane-settings"), in, &out)
	return out, err
}
