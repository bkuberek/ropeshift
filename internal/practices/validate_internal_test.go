package practices

import (
	"slices"
	"strings"
	"testing"
)

// grown is a table one version later than the one a report was made on: the real rows, and a practice added by version 2.
func grown() []Practice {
	rows := slices.Clone(table)
	return append(rows, Practice{ID: "added-later", Severity: Low, Since: 2})
}

// statusesOf is a result that names each practice of the rows given, except those skipped, as met.
func statusesOf(rows []Practice, version int, skip ...ID) Result {
	r := Result{TableVersion: version}
	for _, p := range rows {
		if !slices.Contains(skip, p.ID) {
			r.Practices = append(r.Practices, PracticeStatus{ID: p.ID, Status: StatusMet})
		}
	}
	return r
}

// A report one table version behind may lack the practices a later version added, and nothing else.
func TestAReportOneTableVersionBehindMayLackWhatALaterVersionAdded(t *testing.T) {
	rows := grown()
	if err := validate(statusesOf(rows, 1, "added-later"), rows, 2); err != nil {
		t.Errorf("a report that lacks only a practice version 2 added is refused: %v", err)
	}
	if err := validate(statusesOf(rows, 1, CI), rows, 2); err == nil {
		t.Error("a report on version 1 that lacks a practice version 1 held is accepted")
	}
	if err := validate(statusesOf(rows, 1, "added-later", CI), rows, 2); err == nil {
		t.Error("a report that lacks a practice version 1 held, and one later versions added, is accepted")
	}
}

// Secrets is required of every report, whatever its version.
func TestAReportMissingSecretsIsRefusedAtEveryTableVersion(t *testing.T) {
	rows := grown()
	for _, version := range []int{1, 2} {
		if err := validate(statusesOf(rows, version, "added-later", Secrets), rows, 2); err == nil {
			t.Errorf("a report on version %d with no status for secrets is accepted", version)
		}
	}
}

// A report on the current version names every practice of the table.
func TestAReportOnTheCurrentTableVersionNamesEveryPractice(t *testing.T) {
	rows := grown()
	if err := validate(statusesOf(rows, 2), rows, 2); err != nil {
		t.Errorf("a whole report on the current version is refused: %v", err)
	}
	if err := validate(statusesOf(rows, 2, "added-later"), rows, 2); err == nil {
		t.Error("a report on the current version that lacks the practice it added is accepted")
	}
	if err := validate(statusesOf(rows, 2, CI), rows, 2); err == nil {
		t.Error("a report on the current version that lacks a practice is accepted")
	}
}

// A version no table has held is read as the current one, so a report with none is held to the whole table.
func TestAReportOnNoTableVersionIsHeldToTheWholeTable(t *testing.T) {
	rows := grown()
	if err := validate(statusesOf(rows, 0, "added-later"), rows, 2); err == nil {
		t.Error("a report with no table version that lacks a practice is accepted")
	}
}

// Every practice of the table says the version that added it, and none is later than the table.
func TestEveryPracticeSaysTheTableVersionThatAddedIt(t *testing.T) {
	for _, p := range table {
		if p.Since < 1 || p.Since > TableVersion {
			t.Errorf("%s was added by version %d, want one from 1 to %d", p.ID, p.Since, TableVersion)
		}
	}
}

// Only a report one version behind may lack what later versions added: one two or more behind carries the whole table.
func TestAReportTwoTableVersionsBehindCarriesTheWholeTable(t *testing.T) {
	rows := append(grown(), Practice{ID: "added-latest", Severity: Low, Since: 3})
	if err := validate(statusesOf(rows, 2, "added-latest"), rows, 3); err != nil {
		t.Errorf("a report one version behind that lacks only what version 3 added is refused: %v", err)
	}
	if err := validate(statusesOf(rows, 1, "added-latest"), rows, 3); err == nil {
		t.Error("a report two versions behind that lacks a practice is accepted")
	}
	if err := validate(statusesOf(rows, 1, "added-later", "added-latest"), rows, 3); err == nil {
		t.Error("a report two versions behind that lacks what versions 2 and 3 added is accepted")
	}
	if err := validate(statusesOf(rows, 1), rows, 3); err != nil {
		t.Errorf("a whole report two versions behind is refused: %v", err)
	}
}

// A finding ID that Bound clipped to its bound is an ID the grammar accepts, for every practice that can name a long
// subject, so a finding the result lists can be acknowledged. An ID that merely looks clipped is not.
func TestParseFindingIDAcceptsAnIDBoundClipped(t *testing.T) {
	long := strings.Repeat("deep/", 600)
	for name, id := range map[string]string{
		"a secret in a long path":   "secrets:github_token:" + long + "config.yaml:1a2b3c4d5e6f",
		"a tracked env file":        "env-ignored:tracked:" + long + ".env",
		"a lockfile in a deep dir":  "lockfile:npm:" + long,
		"a secret with a long rule": "secrets:" + strings.Repeat("r", 3000) + ":a.yaml:1a2b3c4d5e6f",
	} {
		clipped := clipID(id)
		if len(clipped) >= len(id) || len(clipped) > MaxText {
			t.Fatalf("%s: clipping left %d bytes of %d", name, len(clipped), len(id))
		}
		if _, err := ParseFindingID(clipped); err != nil {
			t.Errorf("%s: the clipped ID %.60q... is refused: %v", name, clipped, err)
		}
	}
	for name, id := range map[string]string{
		"a short ID with a digest tail":  "secrets:github_token:a.yaml~0011223344556677",
		"a clipped ID of no practice":    strings.Repeat("x", MaxText-17) + "~0011223344556677",
		"a clipped ID with a bad digest": "secrets:github_token:" + strings.Repeat("a", MaxText-21-17) + "~00112233445566zz",
		"a secret with no rule":          "secrets::" + strings.Repeat("a", MaxText-9-17) + "~0011223344556677",
		"an ID over the bound":           "secrets:github_token:" + strings.Repeat("a", MaxText) + "~0011223344556677",
	} {
		if _, err := ParseFindingID(id); err == nil {
			t.Errorf("%s: %.60q is accepted", name, id)
		}
	}
}
