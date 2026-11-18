package server_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestGoldenSpendReads writes what the Spend sections of Settings read, from a real ledger on a clock that stands still:
// a project whose daily limit its lanes are past and whose weekly limit they are under, with an open work order, the
// spend policy versions it holds, and the installation's. A test of the sections runs on what the API serves.
func TestGoldenSpendReads(t *testing.T) {
	d := startTalkWith(t, apitest.Options{Clock: apitest.NewClock().Dialect})
	ctx := t.Context()
	if _, err := d.client.CreateWorkOrder(ctx, d.workIn("wo-spend", "Add the retry budget")); err != nil {
		t.Fatal(err)
	}
	limitedProject(t, d)
	installation := authority.InstallationSpendPolicy{Version: 2, OrchestratorClass: "large", OrchestratorDailyWarn: 20}
	if _, err := d.client.PublishPolicy(ctx, installation.Body()); err != nil {
		t.Fatal(err)
	}
	notify := "[notify_policy]\nversion = 2\ndesktop = true\nwebhook_url = \"https://hooks.example.com/ropeshift\"\n\n[notify_policy.spend_channels]\nspend_hard_limit = [\"desktop\"]\n"
	if _, err := d.client.PublishPolicy(ctx, notify); err != nil {
		t.Fatal(err)
	}
	view, err := d.client.Spend(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]any{"spend": view}
	kinds := map[string]authority.PolicyKind{
		"policies-spend": authority.PolicySpend, "policies-installation-spend": authority.PolicyInstallationSpend, "policies-notify": authority.PolicyNotify,
	}
	for name, kind := range kinds {
		versions, err := d.client.PoliciesOf(ctx, kind)
		if err != nil {
			t.Fatal(err)
		}
		reads[name] = api.PolicyList{Versions: versions}
	}
	for name, value := range reads {
		holdsGolden(t, name, steadyTimes(steady(pretty(t, value))))
	}
}
