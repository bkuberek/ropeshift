package entry

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// probes call every method of the refusal wired in one field of serverReads, and return what each answered.
// A slice that replaces a field with a real read removes its row from unavailableReads and its probe here.
var probes = map[string]func(reads serverReads) []error{
	"creator": func(reads serverReads) []error {
		_, checkErr := reads.creator.Check(context.Background(), ledger.RepositoryCreation{})
		_, createErr := reads.creator.Create(context.Background(), ledger.RepositoryCreation{})
		return []error{checkErr, createErr}
	},
	"knowledge": func(reads serverReads) []error {
		var out []error
		for _, source := range reads.knowledge {
			_, err := source.Describe(context.Background())
			out = append(out, err)
			_, err = source.List(context.Background())
			out = append(out, err)
			_, err = source.Read(context.Background(), ledger.ExternalKey{}, "")
			out = append(out, err)
			_, err = source.Versions(context.Background(), ledger.ExternalKey{})
			out = append(out, err)
			_, err = source.Health(context.Background())
			out = append(out, err)
		}
		return out
	},
	"starts": func(reads serverReads) []error {
		ctx := context.Background()
		_, tipErr := reads.starts.Bases.BaseTip(ctx, "repository", "main")
		_, containsErr := reads.starts.Bases.Contains(ctx, "repository", "a", "b")
		_, defaultErr := reads.starts.Bases.DefaultBranch(ctx, "repository")
		_, branchErr := reads.starts.Bases.Branch(ctx, "repository", "main")
		_, baseErr := reads.starts.Bases.MergeBase(ctx, "repository", "a", "b")
		_, changesErr := reads.starts.Bases.Changes(ctx, "repository", "a", "b")
		_, checkoutErr := reads.starts.Checkout(ctx, "project", "repository")
		return []error{tipErr, containsErr, defaultErr, branchErr, baseErr, changesErr, checkoutErr}
	},
	"branches": func(reads serverReads) []error {
		_, err := reads.branches(context.Background(), "project", "repository", "lane-1")
		return []error{err}
	},
	"checkouts": func(reads serverReads) []error {
		ctx := context.Background()
		_, _, findErr := reads.checkouts.Find(ctx, []string{"/somewhere"}, 1)
		_, _, resolveErr := reads.checkouts.Resolve(ctx, "/somewhere")
		_, sameErr := reads.checkouts.SameFile(ctx, "/a", "/b")
		keepErr := reads.checkouts.Keep(ctx, "project", "repository", "/somewhere")
		forgetErr := reads.checkouts.Forget(ctx, "project", "repository")
		_, ofErr := reads.checkouts.Of(ctx, "project")
		_, everyErr := reads.checkouts.Every(ctx)
		return []error{findErr, resolveErr, sameErr, keepErr, forgetErr, ofErr, everyErr}
	},
	"parseManifest": func(reads serverReads) []error {
		_, err := reads.parseManifest([]byte("[[repository]]\nname = \"api\"\n"))
		return []error{err}
	},
	"panes": func(reads serverReads) []error {
		_, screenErr := reads.panes.Screen(context.Background(), "lane-1", 1)
		sendErr := reads.panes.Send(context.Background(), "lane-1", 1, server.PaneInput{Text: "hello"})
		return []error{screenErr, sendErr}
	},
	"workSource": func(reads serverReads) []error {
		ctx := context.Background()
		source := reads.workSource(ledger.Repository{ID: "repository", Name: "api"})
		_, describeErr := source.Describe(ctx)
		_, fieldsErr := source.Fields(ctx, ports.ScopeRef{})
		_, pollErr := source.Poll(ctx, "")
		_, readErr := source.Read(ctx, ledger.ExternalKey{})
		_, relateErr := source.Relate(ctx, "a", "b")
		_, verifyErr := source.VerifyDelivery(ctx, ports.InboundDelivery{})
		_, applyErr := source.Apply(ctx, ports.OutboxItem{})
		_, healthErr := source.Health(ctx)
		return []error{describeErr, fieldsErr, pollErr, readErr, relateErr, verifyErr, applyErr, healthErr}
	},
	"acceptedCommits": func(reads serverReads) []error {
		_, err := reads.acceptedCommits(context.Background(), "repository", "0123456789abcdef")
		return []error{err}
	},
	"layouts": func(reads serverReads) []error {
		ctx := context.Background()
		_, ignoredErr := reads.layouts.IgnoredDirectories(ctx, "/somewhere")
		_, planErr := reads.layouts.Plan(ctx, "/somewhere")
		_, documentsErr := reads.layouts.Documents(ctx, "/somewhere")
		_, tipErr := reads.layouts.Tip(ctx, "/somewhere")
		_, trackedErr := reads.layouts.Tracked(ctx, "/somewhere", "0123456789abcdef")
		_, treesErr := reads.layouts.CodeTrees(ctx, "/somewhere")
		_, instructionsErr := reads.layouts.Instructions(ctx, "/somewhere")
		return []error{ignoredErr, planErr, documentsErr, tipErr, trackedErr, treesErr, instructionsErr}
	},
}

// A server has no clone and no terminal beside it, so every read it would make from one is wired to a
// refusal, and this test lists each wiring field with the refusal it gets. G2c replaces the repository
// reads with reads of mirrors, and G2e the panes with a relay, each by removing its rows here.
func TestEveryReadAServerCannotMakeIsRefusedByName(t *testing.T) {
	t.Parallel()
	reads := unavailableServerReads()
	for _, read := range unavailableReads {
		t.Run(read.Field, func(t *testing.T) {
			probe, ok := probes[read.Field]
			if !ok {
				t.Fatalf("no probe calls the refusal of %s", read.Field)
			}
			answers := probe(reads)
			if len(answers) == 0 {
				t.Fatalf("the probe of %s called nothing", read.Field)
			}
			for i, err := range answers {
				if !errors.Is(err, read.Reason) {
					t.Errorf("call %d of %s answered %v, not %v", i+1, read.Field, err, read.Reason)
					continue
				}
				if !strings.Contains(err.Error(), read.Read) {
					t.Errorf("call %d of %s answered %q, which does not name %q", i+1, read.Field, err, read.Read)
				}
			}
		})
	}
}

// The table is the list of the wiring: a field of serverReads with no row is a read wired to nothing, and
// a row with no field is a refusal nobody wires.
func TestTheListOfRefusedReadsIsTheWiring(t *testing.T) {
	t.Parallel()
	var listed []string
	for _, read := range unavailableReads {
		listed = append(listed, read.Field)
		if read.Read == "" || read.Reason == nil {
			t.Errorf("%s names no read or no reason", read.Field)
		}
	}
	var fields []string
	typeOf := reflect.TypeOf(serverReads{})
	for i := range typeOf.NumField() {
		fields = append(fields, typeOf.Field(i).Name)
	}
	slices.Sort(listed)
	slices.Sort(fields)
	if !slices.Equal(listed, fields) {
		t.Errorf("serverReads holds %v and the list names %v", fields, listed)
	}
	if len(probes) != len(listed) {
		t.Errorf("%d probes for %d refusals", len(probes), len(listed))
	}
}

// Only a pane has its own reason: every other read is a repository's.
func TestAPaneIsRefusedAsARelayAndEveryOtherReadAsARepositoryRead(t *testing.T) {
	t.Parallel()
	for _, read := range unavailableReads {
		want := ports.ErrRepositoryReadsUnavailable
		if read.Field == "panes" {
			want = ports.ErrPaneRelayUnavailable
		}
		if read.Reason != want {
			t.Errorf("%s is refused with %v, want %v", read.Field, read.Reason, want)
		}
	}
}
