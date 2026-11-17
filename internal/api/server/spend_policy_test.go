package server_test

import (
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
)

// The spend policy is published through `policy publish` like every other kind, a missing key reads as its
// default, and a value outside its range, an unknown key, or a repeated spelling is refused with the key named.
func TestTheSpendPolicyIsPublishedAndBounded(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	body := "[spend_policy]\nversion = 2\nmax_live_wait = \"10m\"\nwait_rules_off = [\"W4\"]\n" +
		"wait_rule_patterns = { W3 = [\"kubectl rollout status\"] }\n"
	if _, err := d.client.PublishPolicy(ctx, body); err != nil {
		t.Fatal(err)
	}
	versions, err := d.client.PoliciesOf(ctx, authority.PolicySpend)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 || versions[0].Kind != authority.PolicySpend {
		t.Fatalf("versions %+v: %v", versions, err)
	}
	read, err := authority.ReadSpendPolicy(versions[0])
	if err != nil {
		t.Fatal(err)
	}
	if read.MaxLiveWait != "10m" || read.MaxToolSleep != "60s" || len(read.WaitRulesOff) != 1 || read.WaitRulePatterns["W3"][0] != "kubectl rollout status" {
		t.Errorf("the policy reads %+v", read)
	}
	for name, bad := range map[string]string{
		"a live wait under a minute": "[spend_policy]\nversion = 3\nmax_live_wait = \"30s\"\n",
		"a rule that does not exist": "[spend_policy]\nversion = 3\nwait_rules_off = [\"W7\"]\n",
		"an unknown key":             "[spend_policy]\nversion = 3\nmax_live_waits = \"5m\"\n",
		"no version":                 "[spend_policy]\nmax_live_wait = \"5m\"\n",
		"a version not newer":        "[spend_policy]\nversion = 2\nmax_live_wait = \"7m\"\n",
		"two spellings":              "[spend_policy]\nversion = 3\nmax_live_wait = \"5m\"\nMax_Live_Wait = \"6m\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.client.PublishPolicy(ctx, bad)
			refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		})
	}
}
