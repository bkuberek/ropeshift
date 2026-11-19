package practices

import (
	"slices"
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
