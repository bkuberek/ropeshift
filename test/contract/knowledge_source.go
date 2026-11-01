package contract

import (
	"context"
	"errors"
	"testing"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// KnowledgeRig is one knowledge source and the way a check changes what it holds.
type KnowledgeRig struct {
	Source ports.KnowledgeSource
	// Commit stands for a person committing one document.
	Commit func(path, content string)
}

// RunKnowledgeSource runs one contract on one source. The adapter and its fake both pass
// it, so a fake cannot drift from the source it stands for.
func RunKnowledgeSource(t *testing.T, fresh func(*testing.T) KnowledgeRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, KnowledgeRig)
	}{
		{"a list names each document, with no body", checkKnowledgeList},
		{"a read returns the current version", checkKnowledgeRead},
		{"an earlier version stays readable", checkKnowledgeEarlier},
		{"versions come newest first, one per change", checkKnowledgeVersions},
		{"another document's commit is not a version of this one", checkKnowledgeOtherCommit},
		{"an absent document or version is not found", checkKnowledgeAbsent},
		{"health answers", checkKnowledgeHealth},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.run(t, t.Context(), fresh(t))
		})
	}
}

func knowledgeKey(t *testing.T, ctx context.Context, rig KnowledgeRig, path string) ledger.ExternalKey {
	t.Helper()
	description, err := rig.Source.Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.ExternalKey{Adapter: description.Adapter, Instance: description.Instance, NativeID: path}
}

func checkKnowledgeList(t *testing.T, ctx context.Context, rig KnowledgeRig) {
	rig.Commit("docs/design.md", "# The design\n\nBody.\n")
	rig.Commit("docs/notes.md", "No heading here.\n")
	list, err := rig.Source.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list %+v", list)
	}
	first := list[0]
	if first.Key != knowledgeKey(t, ctx, rig, "docs/design.md") || first.Title != "The design" || first.Body != "" {
		t.Errorf("first %+v", first)
	}
	if first.Version == "" || first.Digest == "" || first.Source == "" {
		t.Errorf("first has no version: %+v", first)
	}
	if list[1].Title != "docs/notes.md" {
		t.Errorf("a document with no heading reads %q", list[1].Title)
	}
}

func checkKnowledgeRead(t *testing.T, ctx context.Context, rig KnowledgeRig) {
	rig.Commit("design.md", "# One\n")
	rig.Commit("design.md", "# Two\n")
	document, err := rig.Source.Read(ctx, knowledgeKey(t, ctx, rig, "design.md"), "")
	if err != nil {
		t.Fatal(err)
	}
	if document.Body != "# Two\n" || document.Title != "Two" {
		t.Errorf("document %+v", document)
	}
}

func checkKnowledgeEarlier(t *testing.T, ctx context.Context, rig KnowledgeRig) {
	rig.Commit("design.md", "# One\n")
	key := knowledgeKey(t, ctx, rig, "design.md")
	first, err := rig.Source.Read(ctx, key, "")
	if err != nil {
		t.Fatal(err)
	}
	rig.Commit("design.md", "# Two\n")
	again, err := rig.Source.Read(ctx, key, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	if again.Body != "# One\n" || again.Digest != first.Digest || again.Version != first.Version {
		t.Errorf("version %s reads %+v", first.Version, again)
	}
	now, err := rig.Source.Read(ctx, key, "")
	if err != nil {
		t.Fatal(err)
	}
	if now.Digest == first.Digest {
		t.Errorf("a changed document kept digest %s", now.Digest)
	}
}

func checkKnowledgeVersions(t *testing.T, ctx context.Context, rig KnowledgeRig) {
	rig.Commit("design.md", "# One\n")
	rig.Commit("design.md", "# Two\n")
	key := knowledgeKey(t, ctx, rig, "design.md")
	current, err := rig.Source.Read(ctx, key, "")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := rig.Source.Versions(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != current.Version || versions[0].Digest != current.Digest || versions[0].Digest == versions[1].Digest {
		t.Errorf("versions %+v of %+v", versions, current)
	}
}

func checkKnowledgeOtherCommit(t *testing.T, ctx context.Context, rig KnowledgeRig) {
	rig.Commit("design.md", "# One\n")
	rig.Commit("other.md", "# Other\n")
	versions, err := rig.Source.Versions(ctx, knowledgeKey(t, ctx, rig, "design.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Errorf("versions %+v", versions)
	}
}

func checkKnowledgeAbsent(t *testing.T, ctx context.Context, rig KnowledgeRig) {
	rig.Commit("design.md", "# One\n")
	key := knowledgeKey(t, ctx, rig, "design.md")
	absent := key
	absent.NativeID = "absent.md"
	if _, err := rig.Source.Read(ctx, absent, ""); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("an absent document: %v", err)
	}
	if _, err := rig.Source.Versions(ctx, absent); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the versions of an absent document: %v", err)
	}
	if _, err := rig.Source.Read(ctx, key, "0000000"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("an absent version: %v", err)
	}
}

func checkKnowledgeHealth(t *testing.T, ctx context.Context, rig KnowledgeRig) {
	rig.Commit("design.md", "# One\n")
	health, err := rig.Source.Health(ctx)
	if err != nil || health.State == "" {
		t.Errorf("health %+v, %v", health, err)
	}
}
