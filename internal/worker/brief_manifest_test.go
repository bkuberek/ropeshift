package worker_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
)

// The worker never imports the templates, so the entry package hands the lane runtime the rendered manifest
// and the directory intake reads, and the first brief of a planning lane carries both (FR-TPL-3).
func TestAPlanningLanesBriefCarriesTheManifestAndTheIntakeDirectory(t *testing.T) {
	t.Parallel()
	r := newPlanningRig(t, func(*fake.AgentDriver) {})
	r.config.ManifestTemplate = "+++\nkind = \"repositories\"\nid = \"manifest-marker\"\n+++\n"
	r.config.IntakeDirectory = "plans"
	r.dispatched()
	r.runWith()
	specs := r.driver.Specs()
	if len(specs) != 1 {
		t.Fatalf("%d sessions", len(specs))
	}
	for _, want := range []string{r.config.ManifestTemplate, "Intake reads plan files only under `plans/`; write every plan file there"} {
		if !strings.Contains(specs[0].Brief, want) {
			t.Errorf("the brief leaves out %q", want)
		}
	}
}

// A lane that plans nothing is told neither.
func TestAChangeLanesBriefIsToldNothingOfTheManifest(t *testing.T) {
	t.Parallel()
	r := newLaneRigOn(t, func(*fake.AgentDriver) {})
	r.config.ManifestTemplate = "manifest-marker"
	r.config.IntakeDirectory = "plans"
	r.dispatched()
	r.runWith()
	specs := r.driver.Specs()
	if len(specs) != 1 {
		t.Fatalf("%d sessions", len(specs))
	}
	for _, unwanted := range []string{"manifest-marker", "Intake reads plan files"} {
		if strings.Contains(specs[0].Brief, unwanted) {
			t.Errorf("a change lane's brief holds %q", unwanted)
		}
	}
}
