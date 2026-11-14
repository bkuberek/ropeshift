package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/delivery"
)

// TestAnEstimatePolicyPublishesAsAMembersVersion holds `[estimate_policy]` (ADR-0026, FR-EST-6): a project runs
// on the built-in version until a member publishes one, every setting is stated, each version is newer than the
// last, the same body again is a retry, and no version lets a confident estimate rest on fewer than 5 records.
func TestAnEstimatePolicyPublishesAsAMembersVersion(t *testing.T) {
	s := newSession(t)
	var list api.PolicyList
	if err := json.Unmarshal([]byte(s.must("policy", "list", "--kind", "estimate", "--json")), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Versions) != 1 || list.Versions[0].Kind != delivery.PolicyEstimate || list.Versions[0].Version != 1 {
		t.Fatalf("the built-in versions %+v", list.Versions)
	}
	if built, err := delivery.ReadEstimatePolicy(list.Versions[0]); err != nil || built != delivery.DefaultEstimatePolicy() {
		t.Errorf("the built-in policy reads %+v: %v", built, err)
	}
	path := filepath.Join(t.TempDir(), "estimate.toml")
	publish := func(body string) int {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, _ := s.run("policy", "publish", "--file", path)
		return code
	}
	for name, body := range map[string]string{
		"below the floor":    "[estimate_policy]\nversion = 2\nmin_samples = 4\nrecent_samples = 50\nforecast_trials = 2000\nshare_history = false\n",
		"a setting left out": "[estimate_policy]\nversion = 2\nmin_samples = 10\nrecent_samples = 50\nforecast_trials = 2000\n",
		"an unknown setting": "[estimate_policy]\nversion = 2\nmin_samples = 10\nrecent_samples = 50\nforecast_trials = 2000\nshare_history = false\nsamples = 3\n",
	} {
		if code := publish(body); code != cli.ExitUsage {
			t.Errorf("%s: exit %d", name, code)
		}
	}
	good := "[estimate_policy]\nversion = 2\nmin_samples = 12\nrecent_samples = 60\nforecast_trials = 1000\nshare_history = true\n"
	if code := publish(good); code != cli.ExitOK {
		t.Fatalf("a good policy: exit %d", code)
	}
	if code := publish(good); code != cli.ExitOK {
		t.Errorf("the same body again, a retry: exit %d", code)
	}
	for name, body := range map[string]string{
		"an older version":               "[estimate_policy]\nversion = 1\nmin_samples = 20\nrecent_samples = 60\nforecast_trials = 1000\nshare_history = true\n",
		"the same version, another body": "[estimate_policy]\nversion = 2\nmin_samples = 20\nrecent_samples = 60\nforecast_trials = 1000\nshare_history = true\n",
	} {
		if code := publish(body); code != cli.ExitUsage {
			t.Errorf("%s: exit %d", name, code)
		}
	}
	if text := s.must("policy", "show", "2", "--kind", "estimate"); !strings.Contains(text, "share_history = true") {
		t.Errorf("show %q", text)
	}
	if err := json.Unmarshal([]byte(s.must("policy", "list", "--kind", "estimate", "--json")), &list); err != nil {
		t.Fatal(err)
	}
	policy, err := delivery.ReadEstimatePolicy(list.Versions[0])
	if err != nil || len(list.Versions) != 2 || list.Versions[0].Version != 2 || policy != (delivery.EstimatePolicy{Version: 2, MinSamples: 12, RecentSamples: 60, ForecastTrials: 1000, ShareHistory: true}) {
		t.Errorf("the policy in force reads %+v from %+v: %v", policy, list.Versions[0], err)
	}
}
