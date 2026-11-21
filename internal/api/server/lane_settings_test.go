package server_test

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// heldChange is what a lane's worktree changed of its settings: a PermissionRequest hook, and a summary line that
// holds a credential's shape, which the ledger takes out.
func heldChange() lanes.SettingsChange {
	return lanes.SettingsChange{
		Files: []string{".claude/settings.json"}, Keys: []string{"hooks"}, Digest: "sha256:held",
		Summary: []string{`.claude/settings.json hooks.PermissionRequest: added`, `.claude/settings.json apiKeyHelper: changed "echo sk-ant-` + `api03-abcdefghijklmnopqrstuvwxyz0123456789"`},
	}
}

// A lane held on its own settings is one Needs you item in the security category, which names the lane, the keys, and
// a redacted summary, recommends the repository's settings, and offers the three choices. It answers the resume its
// parked work would raise, and the floor names the change as what the lane waits on.
func TestALaneHeldOnItsSettingsIsOneSecurityItem(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	held := w.Running("wo-held", "Held work")
	held.Upload(ledger.UploadLaneSettingsHeld, heldChange())
	held.Park(lanes.HumanWake())
	var items []api.NeedItem
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedLaneSettings || (item.Work != nil && item.Work.WorkOrder != nil && item.Work.WorkOrder.ID == "wo-held") {
			items = append(items, item)
		}
	}
	if len(items) != 1 || items[0].Kind != api.NeedLaneSettings || items[0].ID != held.ID {
		t.Fatalf("Needs you holds %+v", items)
	}
	need := items[0].LaneSettings
	if need.Category != api.CategorySecurity || need.Lane != held.ID || need.WorkOrder == nil || need.WorkOrder.ID != "wo-held" ||
		need.Recommendation != lanes.SettingsBase || need.RecommendationRationale == "" || !slices.Equal(need.Actions, api.LaneSettingsActions) {
		t.Errorf("the item is %+v", need)
	}
	if !slices.Equal(need.Change.Keys, []string{"hooks"}) || need.Change.Digest != "sha256:held" || len(need.Change.Summary) != 2 {
		t.Errorf("the change is %+v", need.Change)
	}
	for _, line := range need.Change.Summary {
		if strings.Contains(line, "abcdefghijklmnopqrstuvwxyz0123456789") {
			t.Errorf("the summary keeps a credential: %s", line)
		}
	}
	if hold := floorLane(t, d, held.ID).SettingsHold; hold == nil || hold.Digest != "sha256:held" {
		t.Errorf("the floor names the lane's hold as %+v", hold)
	}
	if hold := floorLane(t, d, w.Running("wo-free", "Free work").ID).SettingsHold; hold != nil {
		t.Errorf("a lane held on nothing names %+v", hold)
	}
}

// A member's choice through the API ends the hold: running with either settings wakes the lane, and stop blocks its
// work. A choice for another change is refused.
func TestAMembersChoiceEndsTheHold(t *testing.T) {
	t.Parallel()
	for choice, state := range map[string]string{lanes.SettingsBase: "ready", lanes.SettingsLane: "ready", lanes.SettingsStop: "blocked"} {
		t.Run(choice, func(t *testing.T) {
			t.Parallel()
			d := apitest.Start(t)
			w := apitest.NewWorld(t, d)
			held := w.Running("wo-held", "Held work")
			held.Upload(ledger.UploadLaneSettingsHeld, heldChange())
			held.Park(lanes.HumanWake())
			if _, err := d.Client.DecideLaneSettings(t.Context(), "wo-held", api.DecideLaneSettings{Lane: held.ID, Digest: "sha256:other", Choice: choice}); err == nil {
				t.Error("a choice for another change was taken")
			}
			decided, err := d.Client.DecideLaneSettings(t.Context(), "wo-held", api.DecideLaneSettings{Lane: held.ID, Digest: "sha256:held", Choice: choice})
			if err != nil || decided.State != state {
				t.Fatalf("the choice left %+v (%v), want %s", decided, err, state)
			}
			for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
				if item.Kind == api.NeedLaneSettings {
					t.Errorf("the hold stays after the choice: %+v", item)
				}
			}
		})
	}
}

// scriptChange is a hold that comes from a script a hook of the repository runs, which the lane changed.
func scriptChange() lanes.SettingsChange {
	return lanes.SettingsChange{
		Files: []string{"scripts/check.sh"}, Keys: []string{"hooks"}, Scripts: []string{"scripts/check.sh"}, Digest: "sha256:script",
		Summary: []string{"scripts/check.sh: changed, and a hook in .claude/settings.json runs it"},
	}
}

// A hold that comes from a script a hook runs offers no restore, because putting the script back would revert the
// lane's work: the item offers running with the lane's version or stopping, recommends stopping, and the control plane
// refuses a restore with its own code, while the lane's own version is still taken. A hold from a settings file still
// offers and takes the restore.
func TestAHoldFromAScriptAHookRunsOffersNoRestore(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	script := w.Running("wo-script", "Script work")
	script.Upload(ledger.UploadLaneSettingsHeld, scriptChange())
	script.Park(lanes.HumanWake())
	settings := w.Running("wo-settings", "Settings work")
	settings.Upload(ledger.UploadLaneSettingsHeld, heldChange())
	settings.Park(lanes.HumanWake())
	needs := map[string]*api.LaneSettingsNeed{}
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedLaneSettings {
			needs[item.ID] = item.LaneSettings
		}
	}
	if need := needs[script.ID]; need == nil || !slices.Equal(need.Actions, []string{api.ActSettingsLane, api.ActSettingsStop}) ||
		need.Recommendation != lanes.SettingsStop || !slices.Equal(need.Change.Scripts, []string{"scripts/check.sh"}) {
		t.Errorf("a script's hold is offered as %+v", need)
	}
	if need := needs[settings.ID]; need == nil || !slices.Equal(need.Actions, api.LaneSettingsActions) || need.Recommendation != lanes.SettingsBase {
		t.Errorf("a settings file's hold is offered as %+v", need)
	}

	_, err := d.Client.DecideLaneSettings(t.Context(), "wo-script", api.DecideLaneSettings{Lane: script.ID, Digest: "sha256:script", Choice: lanes.SettingsBase})
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusConflict || refused.Detail.Code != api.CodeSettingsNotRestorable ||
		!strings.Contains(refused.Detail.Message, "scripts/check.sh") {
		t.Errorf("a restore of a script's hold gave %v", err)
	}
	if decided, err := d.Client.DecideLaneSettings(t.Context(), "wo-script", api.DecideLaneSettings{Lane: script.ID, Digest: "sha256:script", Choice: lanes.SettingsLane}); err != nil || decided.State != "ready" {
		t.Errorf("running a script's hold with the lane's version left %+v (%v)", decided, err)
	}
	if decided, err := d.Client.DecideLaneSettings(t.Context(), "wo-settings", api.DecideLaneSettings{Lane: settings.ID, Digest: "sha256:held", Choice: lanes.SettingsBase}); err != nil || decided.State != "ready" {
		t.Errorf("a restore of a settings file's hold left %+v (%v)", decided, err)
	}
}
