package lanes

import "strings"

// HookLaneCreated is the profile hook that runs once, when a lane's worktree exists.
const HookLaneCreated = "on_lane_created"

// laneNameToken is what a hook's arguments use for a lane's name.
const laneNameToken = "{lane.name}"

// ExpandHook fills a hook's arguments. It adds no quoting, because no shell reads them.
func ExpandHook(argv []string, lane string) []string {
	out := make([]string, 0, len(argv))
	for _, word := range argv {
		out = append(out, strings.ReplaceAll(word, laneNameToken, lane))
	}
	return out
}
