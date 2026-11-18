package server_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
)

// spendEdit is what a Settings form of the web UI publishes, and what the CLI then reports of it. The web tests send the same
// bodies, from the same file, so the page and the ledger agree on what a save writes.
type spendEdit struct {
	Body     string              `json:"body"`
	Shows    map[string]string   `json:"shows"`
	Channels map[string][]string `json:"channels"`
}

func readSpendEdits(t *testing.T) map[string]spendEdit {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(goldenDir, "spend-edits.json"))
	if err != nil {
		t.Fatal(err)
	}
	var edits map[string]spendEdit
	if err := json.Unmarshal(raw, &edits); err != nil {
		t.Fatal(err)
	}
	return edits
}

// A save of the Spend section is a version of the policy as the CLI writes one, and `spend show` reports it: the web UI's
// body is the body of the policy the CLI would publish with the same keys set, and the limits of the project follow it
// at once (FR-SPEND-18).
func TestTheSpendSectionPublishesAVersionSpendShowReports(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	edit := readSpendEdits(t)["spend"]
	limitedProject(t, d)
	want := authority.DefaultSpendPolicy()
	for key, value := range map[string]string{"project_daily_limit": "30", "project_weekly_limit": "50", "waiting_alert_share": "0.4"} {
		var err error
		if want, err = want.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	want.Version = 3
	if edit.Body != want.Body() {
		t.Fatalf("the form writes\n%s\nand the policy writes\n%s", edit.Body, want.Body())
	}
	if _, err := d.client.PublishPolicy(ctx, edit.Body); err != nil {
		t.Fatal(err)
	}
	view, err := d.client.Spend(ctx)
	if err != nil || view.Project.Version != 3 {
		t.Fatalf("after the save the project reads %+v: %v", view.Project, err)
	}
	for key, value := range edit.Shows {
		if got := spendKey(view.Project, key); got != value {
			t.Errorf("spend show reports %s as %s, and the form saved %s", key, got, value)
		}
	}
	if math.Abs(view.ProjectSpend.Day.HardLimit-30) > 1e-9 || view.ProjectSpend.Day.Past() {
		t.Errorf("the day's limit follows the new version at once, and reads %+v", view.ProjectSpend.Day)
	}
}

// A save of the installation's Spend section is a version of the installation's policy that `spend show` reports under the
// installation's keys.
func TestTheInstallationSpendSectionPublishesAVersionSpendShowReports(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	edit := readSpendEdits(t)["installation"]
	before := authority.InstallationSpendPolicy{Version: 2, OrchestratorClass: "large", OrchestratorDailyWarn: 20}
	if _, err := d.client.PublishPolicy(ctx, before.Body()); err != nil {
		t.Fatal(err)
	}
	want, err := before.Set("orchestrator_class", "small")
	if err != nil {
		t.Fatal(err)
	}
	if want, err = want.Set("orchestrator_daily_warn", "40"); err != nil {
		t.Fatal(err)
	}
	want.Version = 3
	if edit.Body != want.Body() {
		t.Fatalf("the form writes\n%s\nand the policy writes\n%s", edit.Body, want.Body())
	}
	if _, err := d.client.PublishPolicy(ctx, edit.Body); err != nil {
		t.Fatal(err)
	}
	view, err := d.client.Spend(ctx)
	if err != nil || view.Installation.Version != 3 {
		t.Fatalf("after the save the installation reads %+v: %v", view.Installation, err)
	}
	for key, value := range edit.Shows {
		if got := spendKey(view.Installation, key); got != value {
			t.Errorf("spend show reports %s as %s, and the form saved %s", key, got, value)
		}
	}
}

// The channel choice of the Notifications section is a version of the notify policy with its spend_channels, which is what
// publishing the policy file does, and each kind reaches the channels it chose and no others (FR-SPEND-19).
func TestTheChannelChoiceOfTheNotificationsSectionIsWhatThePolicyFilePublishes(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	edit := readSpendEdits(t)["notify"]
	before := "[notify_policy]\nversion = 2\ndesktop = true\nwebhook_url = \"https://hooks.example.com/ropeshift\"\n\n[notify_policy.spend_channels]\nspend_hard_limit = [\"desktop\"]\n"
	if _, err := d.client.PublishPolicy(ctx, before); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.PublishPolicy(ctx, edit.Body); err != nil {
		t.Fatal(err)
	}
	versions, err := d.client.PoliciesOf(ctx, authority.PolicyNotify)
	if err != nil || len(versions) != 3 || versions[0].Version != 3 {
		t.Fatalf("the notify policy holds %+v: %v", versions, err)
	}
	policy, err := authority.ReadNotifyPolicy(versions[0])
	if err != nil {
		t.Fatal(err)
	}
	for kind, channels := range edit.Channels {
		if got := policy.SpendChannels[kind]; !slices.Equal(got, channels) {
			t.Errorf("%s reaches %v, and the form chose %v", kind, got, channels)
		}
		for _, channel := range []string{authority.NoticeChannelDesktop, authority.NoticeChannelWebhook} {
			if policy.Sends(kind, channel) != slices.Contains(channels, channel) {
				t.Errorf("%s on the %s is %v, and the form chose %v", kind, channel, policy.Sends(kind, channel), channels)
			}
		}
	}
	if policy.Sends("spend_soft_limit", authority.NoticeChannelWebhook) {
		t.Errorf("a kind the form chose nothing for reaches the webhook")
	}
}
