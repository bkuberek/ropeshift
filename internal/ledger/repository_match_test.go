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

func remoteRecord(id, remote string) Repository {
	record := NewRepository(id)
	record.Remote = remote
	return record
}

// legacyRecord is a record made before a repository's identity was its first-parent root.
func legacyRecord(id string) Repository {
	record := NewRepository(id)
	record.IdentityLegacy = true
	return record
}

func legacyRemoteRecord(id, remote string) Repository {
	record := remoteRecord(id, remote)
	record.IdentityLegacy = true
	return record
}

func TestMatchRepositoryReadsRecordsInOrder(t *testing.T) {
	apartByRemote := func(remote string) func(Repository) bool {
		return func(record Repository) bool { return RemotesDiffer(record.Remote, remote) }
	}
	tests := []struct {
		name      string
		first     string
		roots     []string
		preferred []string
		records   []Repository
		want      string
		earlier   bool
		// apart says a record is another repository than the clone, and is never matched by another root.
		apart func(Repository) bool
	}{
		{"the first-parent root's record wins over a legacy record under a lower root", "c", []string{"a", "c"}, nil,
			[]Repository{legacyRecord("a"), NewRepository("c")}, "c", false, nil},
		{"a legacy record under the lowest root when the first-parent root has none", "c", []string{"a", "c"}, nil,
			[]Repository{legacyRecord("a")}, "a", true, nil},
		{"a record made since is not matched by a root that is not its own", "c", []string{"a", "c"}, nil,
			[]Repository{NewRepository("a")}, "", false, nil},
		{"no record at all", "c", []string{"a", "c"}, nil, nil, "", false, nil},
		{"a removed record is never matched by another root", "c", []string{"a", "c"}, nil,
			[]Repository{func() Repository { r := removedRecord("a"); r.IdentityLegacy = true; return r }()}, "", false, nil},
		{"a removed record of the first-parent root is still its record", "c", []string{"a", "c"}, nil,
			[]Repository{removedRecord("c")}, "c", false, nil},
		{"a record the caller keeps at the clone wins over the first-parent root's", "c", []string{"a", "b", "c"}, []string{"b"},
			[]Repository{NewRepository("b"), NewRepository("c")}, "b", true, nil},
		{"a preferred ID that is no root of the clone is ignored", "c", []string{"a", "c"}, []string{"z"},
			[]Repository{NewRepository("z"), NewRepository("c")}, "c", false, nil},
		{"a preferred record that is removed is skipped", "c", []string{"a", "c"}, []string{"a"},
			[]Repository{removedRecord("a"), NewRepository("c")}, "c", false, nil},
		{"a legacy lowest-root record that is another repository is not matched", "c", []string{"a", "c"}, nil,
			[]Repository{legacyRemoteRecord("a", "git@example.invalid:acme/design.git")}, "", false, apartByRemote("git@example.invalid:acme/workspace.git")},
		{"a legacy lowest-root record with the clone's remote in another spelling is matched", "c", []string{"a", "c"}, nil,
			[]Repository{legacyRemoteRecord("a", "https://example.invalid/acme/app.git")}, "a", true, apartByRemote("git@example.invalid:acme/app.git")},
		{"a legacy lowest-root record with no remote is matched", "c", []string{"a", "c"}, nil,
			[]Repository{legacyRecord("a")}, "a", true, apartByRemote("git@example.invalid:acme/workspace.git")},
		{"a clone with no remote matches a legacy lowest-root record with one", "c", []string{"a", "c"}, nil,
			[]Repository{legacyRemoteRecord("a", "git@example.invalid:acme/design.git")}, "a", true, apartByRemote("")},
		{"a record kept at the clone is matched whatever its remote", "c", []string{"a", "c"}, []string{"a"},
			[]Repository{remoteRecord("a", "git@example.invalid:acme/design.git")}, "a", true, apartByRemote("git@example.invalid:acme/workspace.git")},
		{"the first-parent root's record is matched whatever its remote", "c", []string{"a", "c"}, nil,
			[]Repository{remoteRecord("c", "git@example.invalid:acme/design.git")}, "c", false, apartByRemote("git@example.invalid:acme/workspace.git")},
		{"a legacy record under a higher root when the lowest root has none", "d", []string{"a", "b", "d"}, nil,
			[]Repository{legacyRecord("b")}, "b", true, nil},
		{"the lowest legacy record wins over a higher one", "d", []string{"a", "b", "d"}, nil,
			[]Repository{legacyRecord("a"), legacyRecord("b")}, "a", true, nil},
		{"a record made since under the lowest root is passed over for a legacy one", "d", []string{"a", "b", "d"}, nil,
			[]Repository{NewRepository("a"), legacyRecord("b")}, "b", true, nil},
		{"a removed legacy lowest-root record is passed over for the next root", "d", []string{"a", "b", "d"}, nil,
			[]Repository{func() Repository { r := removedRecord("a"); r.IdentityLegacy = true; return r }(), legacyRecord("b")}, "b", true, nil},
		{"a legacy lowest-root record that is another repository is passed over for the next root", "d", []string{"a", "b", "d"}, nil,
			[]Repository{legacyRemoteRecord("a", "git@example.invalid:acme/design.git"), legacyRemoteRecord("b", "git@example.invalid:acme/workspace.git")}, "b", true,
			apartByRemote("git@example.invalid:acme/workspace.git")},
		{"every other root another repository's", "d", []string{"a", "b", "d"}, nil,
			[]Repository{legacyRemoteRecord("a", "git@example.invalid:acme/design.git"), legacyRemoteRecord("b", "git@example.invalid:acme/tools.git")}, "", false,
			apartByRemote("git@example.invalid:acme/workspace.git")},
		// Two repositories that merged one history are each their own record, and neither is matched to the
		// record the merged history made, which no remote and no kept clone tells apart from either of them.
		{"a record made since for a history another repository merged is not matched", "d", []string{"a", "d"}, nil,
			[]Repository{NewRepository("a")}, "", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, found, earlier, err := MatchRepository(tt.first, tt.roots, tt.preferred, tt.apart, lookupOf(tt.records...))
			if err != nil || found != (tt.want != "") || record.ID != tt.want || earlier != tt.earlier {
				t.Errorf("matched %q (found %v, earlier %v, %v), want %q (earlier %v)", record.ID, found, earlier, err, tt.want, tt.earlier)
			}
		})
	}
}

func TestSharesFirstCommitUnlessBothClonesNameOneRemote(t *testing.T) {
	tests := []struct {
		kept, clone string
		want        bool
	}{
		{"git@example.invalid:acme/app.git", "https://example.invalid/acme/app", false},
		{"git@example.invalid:acme/app.git", "git@example.invalid:acme/fork.git", true},
		{"", "", true},
		{"", "git@example.invalid:acme/app.git", true},
		{"git@example.invalid:acme/app.git", "", true},
	}
	for _, tt := range tests {
		if got := SharesFirstCommit(tt.kept, tt.clone); got != tt.want {
			t.Errorf("SharesFirstCommit(%q, %q) = %v, want %v", tt.kept, tt.clone, got, tt.want)
		}
	}
}

func TestRemotesDifferOnlyWhenBothNameADifferentRepository(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"git@example.invalid:acme/app.git", "https://example.invalid/acme/app", false},
		{"git@example.invalid:acme/app.git", "git@example.invalid:acme/other.git", true},
		{"", "git@example.invalid:acme/app.git", false},
		{"git@example.invalid:acme/app.git", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		if got := RemotesDiffer(tt.a, tt.b); got != tt.want {
			t.Errorf("RemotesDiffer(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestMatchRepositoryReportsALookupFailure(t *testing.T) {
	failure := errors.New("the store is down")
	_, _, _, err := MatchRepository("c", []string{"a", "c"}, nil, nil, func(string) (Repository, bool, error) { return Repository{}, false, failure })
	if !errors.Is(err, failure) {
		t.Errorf("the failure reads %v", err)
	}
}

func TestOnlyARemoteOnAHostNamesARepository(t *testing.T) {
	for remote, want := range map[string]bool{
		"https://example.invalid/acme/design.git": true,
		"git@example.invalid:acme/design.git":     true,
		"ssh://git@example.invalid:22/acme/d.git": true,
		"../design.git":              false,
		"./design.git":               false,
		"/srv/git/design.git":        false,
		"file:///srv/git/design.git": false,
		"design.git":                 false,
		"":                           false,
		"  ":                         false,
	} {
		if got := NetworkRemote(remote); got != want {
			t.Errorf("NetworkRemote(%q) = %v, want %v", remote, got, want)
		}
	}
}
