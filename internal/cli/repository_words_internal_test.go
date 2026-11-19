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
	want := "    planned\tweb\trepos/web\t-\t-\tno work order of this version targets web\n" +
		"    skipped\tdocs\trepos/docs\t/work/tt/repos/docs\t-\tnot needed yet\n"
	for _, preview := range []bool{true, false} {
		if got := manifestText(added, preview, "    "); got != want {
			t.Errorf("preview %v prints\n%s\nwant\n%s", preview, got, want)
		}
	}
}
