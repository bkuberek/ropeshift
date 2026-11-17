package entry

import (
	"reflect"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/templates"
	"ropeshift.com/ropeshift/test/apitest"
)

// FR-TPL-5: the list a daemon serves is every template of the binary, sorted, each as Render returns it with the
// example data, with the shapes and the path the table of the files `new` writes gives it.
func TestTheTemplatesADaemonServesAreEveryTemplateTheBinaryEmbeds(t *testing.T) {
	t.Parallel()
	got, err := renderedTemplates()
	if err != nil {
		t.Fatal(err)
	}
	want, err := apitest.ExampleTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(templates.Names()) || len(got) < 20 {
		t.Fatalf("%d templates for %d names", len(got), len(templates.Names()))
	}
	if !reflect.DeepEqual(got, want) {
		for i := range got {
			if i < len(want) && !reflect.DeepEqual(got[i], want[i]) {
				t.Errorf("%s: served %+v, the table of files gives %+v", got[i].Name, got[i].Shapes, want[i].Shapes)
			}
		}
		t.Fatalf("the list differs from the one the table of files gives")
	}
	names := make([]string, len(got))
	for i, entry := range got {
		names[i] = entry.Name
	}
	if !slices.IsSorted(names) {
		t.Errorf("the list is not sorted: %v", names)
	}
}
