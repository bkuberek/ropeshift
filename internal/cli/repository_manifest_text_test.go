package cli

import (
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// What a manifest's registration reports, as `repository add --manifest` and a plan's approval print it, never
// breaks a line or reaches the terminal as a control sequence, whatever the API or the manifest file sent.
func TestManifestTextPrintsEveryFieldAsPlainText(t *testing.T) {
	added := func(text string) []api.RepositoryAdded {
		return []api.RepositoryAdded{
			{Name: text, ManifestPath: text, Path: text, Remote: text, Outcome: ledger.Registered},
			{Name: text, ManifestPath: text, Path: text, Remote: text, Outcome: ledger.RegistrationRefused, Reason: text},
			{Name: text, ManifestPath: text, Path: text, Remote: text, Outcome: ledger.NotAttempted, Reason: text},
		}
	}
	for _, wouldBe := range []bool{false, true} {
		sameShape(t, manifestText(added(hostile), wouldBe, "  "), manifestText(added("x"), wouldBe, "  "))
	}
}

// A path a manifest names prints with its spaces as they are.
func TestManifestTextKeepsThePathsSpaces(t *testing.T) {
	got := manifestText([]api.RepositoryAdded{{Name: "api", ManifestPath: "repos/My  Api", Path: "/w/My  Api", Outcome: ledger.Registered}}, false, "")
	if want := "registered\tapi\trepos/My  Api\t/w/My  Api\t-\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
