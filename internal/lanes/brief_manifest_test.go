package lanes

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// manifestExample stands for the manifest template the entry package renders and passes in. It is a marker
// here, because the lanes package never imports the templates.
const manifestExample = "+++\nkind = \"repositories\"\nid = \"manifest-marker\"\n+++\n\nThe manifest marker.\n"

// A planning lane writes the manifest of a project's code repositories, so its brief shows the format after the
// others, and says where a path in it points.
func TestAPlanningBriefShowsTheManifestAfterTheOtherFormats(t *testing.T) {
	for name, spec := range map[string]BriefSpec{
		"a planning brief": {WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan, ManifestTemplate: manifestExample},
		"an adopt brief":   {WorkOrder: "wo", Title: "Adopt it", Kind: workorders.KindPlan, ManifestTemplate: manifestExample, Adopt: true},
	} {
		brief := Brief(spec)
		manifest := strings.Index(brief, manifestExample)
		if manifest < 0 {
			t.Errorf("%s leaves out the manifest", name)
			continue
		}
		last := planning.FormatExamples[len(planning.FormatExamples)-1]
		if after := strings.Index(brief, last.Text) + len(last.Text); manifest < after {
			t.Errorf("%s shows the manifest at %d, before the last format ends at %d", name, manifest, after)
		}
		if strings.Count(brief, manifestExample) != 1 {
			t.Errorf("%s shows the manifest %d times", name, strings.Count(brief, manifestExample))
		}
		for _, want := range []string{
			"relative to the planning repository's clone", "inside the folder that holds it",
		} {
			if !strings.Contains(brief, want) {
				t.Errorf("%s says nothing of %q", name, want)
			}
		}
	}
}

// With no manifest passed, a brief shows none and says nothing of one.
func TestABriefWithNoManifestTemplateSaysNothingOfAManifest(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan})
	for _, unwanted := range []string{"manifest", "inside the folder that holds it"} {
		if strings.Contains(brief, unwanted) {
			t.Errorf("a brief with no manifest holds %q", unwanted)
		}
	}
}

// Work that plans nothing is told neither the manifest nor where plan files go.
func TestAWorkBriefIsToldNothingOfTheManifestOrTheIntakeDirectory(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange, ManifestTemplate: manifestExample, IntakeDirectory: "plans"})
	for _, unwanted := range []string{manifestExample, "Intake reads plan files", "Keep plan files in"} {
		if strings.Contains(brief, unwanted) {
			t.Errorf("a brief for work that plans nothing holds %q", unwanted)
		}
	}
}

// Both planning briefs say where intake reads, from the daemon's --dir: under that directory when it has one,
// and else in plan/, with what intake reads of the whole repository.
func TestAPlanningBriefSaysWhereIntakeReads(t *testing.T) {
	const (
		withDirectory = "Intake reads plan files only under `plans/`; write every plan file there"
		withNone      = "Keep plan files in `plan/`; intake reads every committed `.md` file whose first line is `+++`"
	)
	for name, adopt := range map[string]bool{"a planning brief": false, "an adopt brief": true} {
		for directory, want := range map[string]string{"plans": withDirectory, "plans/": withDirectory, "": withNone} {
			brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan, IntakeDirectory: directory, Adopt: adopt})
			if !strings.Contains(brief, want) {
				t.Errorf("%s with directory %q leaves out %q", name, directory, want)
			}
			other := withNone
			if want == withNone {
				other = "Intake reads plan files only under"
			}
			if strings.Contains(brief, other) {
				t.Errorf("%s with directory %q also says %q", name, directory, other)
			}
			if strings.Contains(brief, "plans//") {
				t.Errorf("%s with directory %q doubles the slash", name, directory)
			}
		}
	}
}
