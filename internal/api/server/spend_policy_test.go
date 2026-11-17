package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
)

// spendBody is the built-in policy's body at a version, with each named line replaced by the text given, so a
// test states a whole policy and changes only what it is about.
func spendBody(t *testing.T, version string, lines map[string]string) string {
	t.Helper()
	out := strings.Replace(authority.BuiltInSpendPolicyBody, "version = 1\n", "version = "+version+"\n", 1)
	for key, line := range lines {
		var replaced []string
		found := false
		for _, existing := range strings.Split(out, "\n") {
			if strings.HasPrefix(existing, key+" = ") {
				existing, found = line, true
			}
			replaced = append(replaced, existing)
		}
		if !found {
			t.Fatalf("the built-in body has no line for %s", key)
		}
		out = strings.Join(replaced, "\n")
	}
	return out
}

// The spend policy is published through `policy publish` like every other kind. A version states every key, and
// a value outside its range, an unknown key, a key left out, or a repeated spelling is refused with the key named.
func TestTheSpendPolicyIsPublishedAndBounded(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	body := spendBody(t, "2", map[string]string{
		"max_live_wait": `max_live_wait = "10m"`, "wait_rules_off": `wait_rules_off = ["W4"]`,
		"wait_rule_patterns": `wait_rule_patterns = { W3 = ["kubectl rollout status"] }`,
		"hard_multiplier":    "hard_multiplier = 3", "no_history_limit": "no_history_limit = { small = 12.5, medium = 30, large = 80 }",
	})
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
	if read.MaxLiveWait != "10m" || read.MaxToolSleep != "60s" || len(read.WaitRulesOff) != 1 || read.WaitRulePatterns["W3"][0] != "kubectl rollout status" ||
		read.HardMultiplier != 3 || read.NoHistoryLimit.Small != 12.5 || read.NoHistoryLimit.Medium != 30 || read.NoHistoryLimit.Large != 80 {
		t.Errorf("the policy reads %+v", read)
	}
	missing := strings.Replace(spendBody(t, "3", nil), "review_share = 0.5\n", "", 1)
	missingPart := strings.Replace(spendBody(t, "3", nil), "medium = 25.0, ", "", 1)
	for name, bad := range map[string]string{
		"a live wait under a minute":   spendBody(t, "3", map[string]string{"max_live_wait": `max_live_wait = "30s"`}),
		"a rule that does not exist":   spendBody(t, "3", map[string]string{"wait_rules_off": `wait_rules_off = ["W7"]`}),
		"a negative amount":            spendBody(t, "3", map[string]string{"project_daily_limit": "project_daily_limit = -5.0"}),
		"a share past one":             spendBody(t, "3", map[string]string{"review_share": "review_share = 1.5"}),
		"a hard multiplier under one":  spendBody(t, "3", map[string]string{"hard_multiplier": "hard_multiplier = 0.5"}),
		"a repeat limit under two":     spendBody(t, "3", map[string]string{"repeat_limit": "repeat_limit = 1"}),
		"a class that does not exist":  spendBody(t, "3", map[string]string{"assistant_class": `assistant_class = "huge"`}),
		"an unknown key":               spendBody(t, "3", nil) + "max_live_waits = \"5m\"\n",
		"a key left out":               missing,
		"a size left out":              missingPart,
		"no version":                   strings.Replace(spendBody(t, "3", nil), "version = 3\n", "", 1),
		"a version not newer":          spendBody(t, "2", map[string]string{"max_live_wait": `max_live_wait = "7m"`}),
		"two spellings":                spendBody(t, "3", nil) + "Max_Live_Wait = \"6m\"\n",
		"only the keys of a first cut": "[spend_policy]\nversion = 3\nmax_live_wait = \"5m\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.client.PublishPolicy(ctx, bad)
			refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		})
	}
}

// docsSpendPolicyBlocks reads the fenced toml blocks of the spend settings page that open with the project's
// spend policy table.
func docsSpendPolicyBlocks(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "set-up-projects.md"))
	if err != nil {
		t.Fatal(err)
	}
	var blocks []string
	for _, part := range strings.Split(string(raw), "```toml\n")[1:] {
		block, _, _ := strings.Cut(part, "```\n")
		if strings.HasPrefix(block, "[spend_policy]\n") {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// The spend policies the page prints are the ones the product runs: the built-in one is the built-in body, and
// each example states every key and publishes.
func TestTheSpendPolicyExamplesOfTheDocsPublish(t *testing.T) {
	blocks := docsSpendPolicyBlocks(t)
	if len(blocks) < 2 {
		t.Fatalf("the page holds %d spend policies, and it prints the built-in one and an example", len(blocks))
	}
	if blocks[0] != authority.BuiltInSpendPolicyBody {
		t.Errorf("the built-in policy the page prints is not the built-in body:\n%s", blocks[0])
	}
	d := startTalk(t)
	for i, block := range blocks[1:] {
		if _, err := d.client.PublishPolicy(t.Context(), block); err != nil {
			t.Fatalf("example %d does not publish: %v", i+1, err)
		}
		versions, err := d.client.PoliciesOf(t.Context(), authority.PolicySpend)
		if err != nil || len(versions) == 0 {
			t.Fatalf("example %d left versions %+v: %v", i+1, versions, err)
		}
		if _, err := authority.ReadSpendPolicy(versions[0]); err != nil {
			t.Errorf("example %d does not read back: %v", i+1, err)
		}
	}
}
