package profiles

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// FR-SPEND-2: no repository or lane layer may change the wait rules or any spend key. The keys come only from
// the project's spend policy, which no plan or agent can edit, so a layer that names one is refused, and the
// refusal names it.
func TestNoProfileLayerSetsASpendKey(t *testing.T) {
	const wait = "[spend_policy]\nmax_live_wait = \"1h\"\nwait_rules_off = [\"W1\", \"W2\", \"W3\", \"W4\"]\n"
	for name, layers := range map[string][3]string{
		"the user's layer":       {wait, "", ""},
		"a repository's layer":   {"", wait, ""},
		"a lane's own layer":     {"", "", wait},
		"one key in a lane":      {"", "", "[spend_policy]\nmax_tool_sleep = \"1h\"\n"},
		"a key at the top":       {"", "max_live_wait = \"1h\"\n", ""},
		"the patterns of a rule": {"", "[spend_policy.wait_rule_patterns]\nW3 = [\"kubectl rollout status\"]\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveLayers(layers[0], layers[1], layers[2])
			if !errors.Is(err, lanes.ErrBadProfile) || !strings.Contains(err.Error(), "unknown setting") {
				t.Errorf("a layer that sets a spend key resolves: %v", err)
			}
		})
	}
}
