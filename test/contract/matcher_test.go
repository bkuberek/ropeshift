package contract

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/lexical"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

func TestMatcherContract(t *testing.T) {
	t.Run("lexical", func(t *testing.T) {
		RunMatcher(t, func(t *testing.T) MatcherRig {
			rig := NewRig(t, SQLiteFile)
			store := rig.Store()
			other := ledger.WorkspaceID("workspace-b")
			if _, err := store.CreateWorkspace(rig.Ctx, ledger.Workspace{ID: other, Name: "B"}, rig.Actor); err != nil {
				t.Fatal(err)
			}
			index := func(scope ledger.WorkspaceID) func(kind, id, text string) {
				return func(kind, id, text string) {
					err := store.Write(rig.Ctx, scope, "note", func(tx ports.WriteTx) error {
						if err := tx.Index(kind, id, text); err != nil {
							return err
						}
						_, err := tx.Append(rig.noteDraft(id))
						return err
					})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			return MatcherRig{Matcher: lexical.Matcher{Store: store}, Scope: rig.Scope, Add: index(rig.Scope), Other: index(other)}
		})
	})
	t.Run("fake", func(t *testing.T) {
		RunMatcher(t, func(*testing.T) MatcherRig {
			matcher := &fake.Matcher{}
			scope := ledger.WorkspaceID("workspace-a")
			return MatcherRig{
				Matcher: matcher, Scope: scope,
				Add:   func(kind, id, text string) { matcher.Add(scope, kind, id, text) },
				Other: func(kind, id, text string) { matcher.Add("workspace-b", kind, id, text) },
			}
		})
	})
}
