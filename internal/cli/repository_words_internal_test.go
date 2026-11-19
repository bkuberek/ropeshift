package cli

import (
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A planned entry and a skipped creation read as themselves, in a preview and in what an approval recorded.
func TestManifestTextSaysPlannedAndSkipped(t *testing.T) {
	added := []api.RepositoryAdded{
		{Name: "web", ManifestPath: "repos/web", Outcome: ledger.Planned, Reason: "no work order of this version targets web"},
		{Name: "docs", ManifestPath: "repos/docs", Path: "/work/tt/repos/docs", Outcome: ledger.Skipped, Reason: "not needed yet"},
	}
	skipped := "    skipped\tdocs\trepos/docs\t/work/tt/repos/docs\t-\tnot needed yet\n"
	for preview, want := range map[bool]string{
		// A preview says that approving creates nothing for a planned entry, and a record says only why it is planned.
		true:  "    planned\tweb\trepos/web\t-\t-\tno work order of this version targets web, so approving creates nothing; the approval of a version that gives it work creates it\n" + skipped,
		false: "    planned\tweb\trepos/web\t-\t-\tno work order of this version targets web\n" + skipped,
	} {
		if got := manifestText(added, preview, "    "); got != want {
			t.Errorf("preview %v prints\n%s\nwant\n%s", preview, got, want)
		}
	}
}
