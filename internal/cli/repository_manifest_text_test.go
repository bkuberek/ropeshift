package cli

import (
	"strings"
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

// What a plan's approval would register, and the problem that stopped it, print as plain text, though the
// problem can quote the manifest file.
func TestARegistrationPrintsItsProblemAsPlainText(t *testing.T) {
	text := func(problem string) string {
		var out strings.Builder
		writeRegistration(&out, &api.ManifestRegistration{Preview: true, Problem: problem}, "")
		return out.String()
	}
	sameShape(t, text(hostile), text("x"))
}

// What a plan's approval prints of the plan's title, what changed, and the problem with its manifest never breaks
// a line or reaches the terminal as a control sequence.
func TestAnApprovalPrintsAsPlainText(t *testing.T) {
	text := func(s string) string {
		return approvedText(s, 2, s, api.ApprovalDecided{RepositoriesProblem: s})
	}
	sameShape(t, text(hostile), text("x"))
}

// A path a manifest names prints with its spaces as they are.
func TestManifestTextKeepsThePathsSpaces(t *testing.T) {
	got := manifestText([]api.RepositoryAdded{{Name: "api", ManifestPath: "repos/My  Api", Path: "/w/My  Api", Outcome: ledger.Registered}}, false, "")
	if want := "registered\tapi\trepos/My  Api\t/w/My  Api\t-\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
