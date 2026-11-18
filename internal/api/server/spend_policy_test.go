package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/test/apitest"
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

// A delegate's change to a spend setting is a confirmation card showing the policy before and after, nothing changes
// until a member confirms it, and no delegate policy can exempt it (ADR-0024). The installation's keys take the same
// road, and a caller with no member's session, as an agent has, is refused before anything reads the body.
func TestADelegatesSpendChangeIsACardNoDelegatePolicyExempts(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	policy, err := authority.DefaultSpendPolicy().Set("hard_multiplier", "3")
	if err != nil {
		t.Fatal(err)
	}
	policy.Version = 2
	installation, err := authority.DefaultInstallationSpendPolicy().Set("orchestrator_daily_warn", "40")
	if err != nil {
		t.Fatal(err)
	}
	installation.Version = 2
	cards := map[string]authority.Approval{}
	operations := map[string]string{"project": authority.OpPolicyPublish, "installation": authority.OpInstallationSpendPolicyPublish}
	for name, body := range map[string]string{"project": policy.Body(), "installation": installation.Body()} {
		_, err := delegate.PublishPolicy(ctx, body)
		cards[name] = preparedCard(t, err)
		detail := cardDetail(t, cards[name])
		if detail.Operation != operations[name] || !strings.Contains(string(detail.Changes), "hard_multiplier") && name == "project" {
			t.Errorf("the %s card is %+v", name, detail)
		}
		if !strings.Contains(string(detail.Changes), "version = 2") || !strings.Contains(string(detail.Changes), "version = 1") {
			t.Errorf("the %s card does not show the policy before and after: %s", name, detail.Changes)
		}
	}
	for _, kind := range []authority.PolicyKind{authority.PolicySpend, authority.PolicyInstallationSpend} {
		if versions, err := d.Client.PoliciesOf(ctx, kind); err != nil || len(versions) != 1 {
			t.Errorf("a delegate's call changed %s to %+v: %v", kind, versions, err)
		}
	}
	exempt := "[delegate_policy]\nversion = 2\nexempt = [\"policy.publish\"]\n"
	if _, err := d.Client.PublishPolicy(ctx, exempt); err == nil {
		t.Error("a delegate policy that exempts publishing a policy was published")
	}
	if _, err := delegate.PublishPolicy(ctx, policy.Body()); err == nil {
		t.Error("a delegate's second call ran")
	}
	for name, card := range cards {
		if _, err := d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true}); err != nil {
			t.Fatalf("the member could not confirm the %s card: %v", name, err)
		}
	}
	spend, err := d.Client.PoliciesOf(ctx, authority.PolicySpend)
	if err != nil || len(spend) != 2 || spend[0].AuthoredBy == created.ID {
		t.Fatalf("after the card the project's spend policy holds %+v: %v", spend, err)
	}
	read, err := authority.ReadSpendPolicy(spend[0])
	if err != nil || read.HardMultiplier != 3 {
		t.Errorf("the policy in force reads %+v: %v", read, err)
	}
	if versions, err := d.Client.PoliciesOf(ctx, authority.PolicyInstallationSpend); err != nil || len(versions) != 2 {
		t.Errorf("after the card the installation's policy holds %+v: %v", versions, err)
	}
}

// No agent can move a spend setting: a caller that holds no member's session, as a lane or a worker does not, is
// refused for the project's policy and for the installation's.
func TestNoAgentCanMoveASpendSetting(t *testing.T) {
	d := apitest.Start(t)
	credential, _ := joinWorker(t, d)
	policy, err := authority.DefaultSpendPolicy().Set("hard_multiplier", "9")
	if err != nil {
		t.Fatal(err)
	}
	policy.Version = 2
	installation := authority.DefaultInstallationSpendPolicy()
	installation.Version = 2
	for name, setup := range map[string]func(*api.ProjectClient) *api.ProjectClient{
		"a caller with nothing":               func(c *api.ProjectClient) *api.ProjectClient { return c },
		"the worker's credential":             func(c *api.ProjectClient) *api.ProjectClient { return c.WithCredential(credential) },
		"a lane's token where a session goes": withSession("a-lane-token"),
		"the credential where a session goes": withSession(credential),
	} {
		plain := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
		t.Cleanup(plain.Close)
		caller := setup(plain)
		for body, text := range map[string]string{"project": policy.Body(), "installation": installation.Body()} {
			t.Run(name+"/"+body, func(t *testing.T) {
				_, err := caller.PublishPolicy(t.Context(), text)
				refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
			})
		}
	}
	if versions, err := d.Client.PoliciesOf(t.Context(), authority.PolicySpend); err != nil || len(versions) != 1 {
		t.Errorf("a refused call left the project's history at %+v: %v", versions, err)
	}
}

// The installation's spend policy is published through the same route, kept apart from the project's, and bounded
// the same way: a version states both keys, a class or an amount outside its range is refused with the key named, and
// a project's key in its table is an unknown setting.
func TestTheInstallationSpendPolicyIsPublishedAndBounded(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	versions, err := d.client.PoliciesOf(ctx, authority.PolicyInstallationSpend)
	if err != nil || len(versions) != 1 || versions[0].Body != authority.BuiltInInstallationSpendPolicyBody {
		t.Fatalf("an installation starts on %+v: %v", versions, err)
	}
	good := "[installation_spend_policy]\nversion = 2\norchestrator_class = \"small\"\norchestrator_daily_warn = 40\n"
	if _, err := d.client.PublishPolicy(ctx, good); err != nil {
		t.Fatal(err)
	}
	versions, err = d.client.PoliciesOf(ctx, authority.PolicyInstallationSpend)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 || versions[0].Kind != authority.PolicyInstallationSpend {
		t.Fatalf("versions %+v: %v", versions, err)
	}
	read, err := authority.ReadInstallationSpendPolicy(versions[0])
	if err != nil || read.OrchestratorClass != "small" || read.OrchestratorDailyWarn != 40 {
		t.Errorf("the policy reads %+v: %v", read, err)
	}
	if project, err := d.client.PoliciesOf(ctx, authority.PolicySpend); err != nil || len(project) != 1 {
		t.Errorf("the project's spend policy holds %+v: %v", project, err)
	}
	for name, bad := range map[string]string{
		"a class that does not exist": "[installation_spend_policy]\nversion = 3\norchestrator_class = \"huge\"\norchestrator_daily_warn = 0\n",
		"a negative amount":           "[installation_spend_policy]\nversion = 3\norchestrator_class = \"standard\"\norchestrator_daily_warn = -1\n",
		"a key left out":              "[installation_spend_policy]\nversion = 3\norchestrator_class = \"standard\"\n",
		"no version":                  "[installation_spend_policy]\norchestrator_class = \"standard\"\norchestrator_daily_warn = 0\n",
		"a project's key":             "[installation_spend_policy]\nversion = 3\norchestrator_class = \"standard\"\norchestrator_daily_warn = 0\nhard_multiplier = 3\n",
		"a version not newer":         "[installation_spend_policy]\nversion = 2\norchestrator_class = \"standard\"\norchestrator_daily_warn = 5\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.client.PublishPolicy(ctx, bad)
			refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		})
	}
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
