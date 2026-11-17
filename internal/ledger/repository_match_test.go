package ledger

import (
	"errors"
	"testing"
)

func lookupOf(records ...Repository) RepositoryLookup {
	return func(id string) (Repository, bool, error) {
		for _, record := range records {
			if record.ID == id {
				return record, true, nil
			}
		}
		return Repository{}, false, nil
	}
}

func removedRecord(id string) Repository {
	record := NewRepository(id)
	record.State = RepositoryRemoved
	return record
}

func TestMatchRepositoryReadsRecordsInOrder(t *testing.T) {
	tests := []struct {
		name      string
		first     string
		roots     []string
		preferred []string
		records   []Repository
		want      string
		earlier   bool
	}{
		{"the first-parent root's record wins over the lowest root's", "c", []string{"a", "c"}, nil,
			[]Repository{NewRepository("a"), NewRepository("c")}, "c", false},
		{"the lowest root's record when the first-parent root has none", "c", []string{"a", "c"}, nil,
			[]Repository{NewRepository("a")}, "a", true},
		{"no record at all", "c", []string{"a", "c"}, nil, nil, "", false},
		{"a removed record is never matched by the lowest root", "c", []string{"a", "c"}, nil,
			[]Repository{removedRecord("a")}, "", false},
		{"a removed record of the first-parent root is still its record", "c", []string{"a", "c"}, nil,
			[]Repository{removedRecord("c")}, "c", false},
		{"a record the caller keeps at the clone wins over the first-parent root's", "c", []string{"a", "b", "c"}, []string{"b"},
			[]Repository{NewRepository("b"), NewRepository("c")}, "b", true},
		{"a preferred ID that is no root of the clone is ignored", "c", []string{"a", "c"}, []string{"z"},
			[]Repository{NewRepository("z"), NewRepository("c")}, "c", false},
		{"a preferred record that is removed is skipped", "c", []string{"a", "c"}, []string{"a"},
			[]Repository{removedRecord("a"), NewRepository("c")}, "c", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, found, earlier, err := MatchRepository(tt.first, tt.roots, tt.preferred, lookupOf(tt.records...))
			if err != nil || found != (tt.want != "") || record.ID != tt.want || earlier != tt.earlier {
				t.Errorf("matched %q (found %v, earlier %v, %v), want %q (earlier %v)", record.ID, found, earlier, err, tt.want, tt.earlier)
			}
		})
	}
}

func TestMatchRepositoryReportsALookupFailure(t *testing.T) {
	failure := errors.New("the store is down")
	_, _, _, err := MatchRepository("c", []string{"a", "c"}, nil, func(string) (Repository, bool, error) { return Repository{}, false, failure })
	if !errors.Is(err, failure) {
		t.Errorf("the failure reads %v", err)
	}
}
