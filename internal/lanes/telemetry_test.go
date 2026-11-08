package lanes

import (
	"strings"
	"testing"
)

func TestResourceAttributesNameEveryTag(t *testing.T) {
	tags := TelemetryTags{
		Workspace: "default", WorkOrder: "wo-1", Lane: "lane-1", Role: RoleWriter, Generation: 3, Profile: "default",
		Repo: "example-service", Branch: "lane/checkout-retry", Worker: "desk-01", Driver: "claude-code",
	}
	want := "ropeshift.workspace=default,ropeshift.work_order.id=wo-1,ropeshift.lane.id=lane-1,ropeshift.lane.role=writer," +
		"ropeshift.generation=3,ropeshift.profile=default,ropeshift.repo=example-service,ropeshift.branch=lane/checkout-retry," +
		"ropeshift.worker=desk-01,ropeshift.driver=claude-code"
	if got := tags.ResourceAttributes(); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// A value is what a person named, so it can hold anything, and the variable's own grammar
// splits on a comma and stops at a space.
func TestResourceAttributeValuesHoldNoSpaceOrComma(t *testing.T) {
	tags := TelemetryTags{
		Workspace: "my space", Lane: "lane,1", Role: "a=b", Generation: 1, Profile: `say "hi"; \ there`,
		Repo: "50% done", Branch: "tab\there", Worker: "naïve", Driver: "new\nline",
	}
	got := tags.ResourceAttributes()
	pairs := strings.Split(got, ",")
	if len(pairs) != 9 {
		t.Fatalf("%d pairs in %q", len(pairs), got)
	}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || !strings.HasPrefix(key, "ropeshift.") {
			t.Fatalf("pair %q", pair)
		}
		if strings.ContainsAny(value, " ,;\"\\=\t\n") {
			t.Errorf("value %q of %s holds a character the grammar refuses", value, key)
		}
		for _, r := range value {
			if r > 0x7e || r < 0x21 {
				t.Errorf("value %q of %s holds %q", value, key, r)
			}
		}
	}
	if !strings.Contains(got, "ropeshift.workspace=my%20space") || !strings.Contains(got, "ropeshift.repo=50%25%20done") {
		t.Errorf("values are not percent-encoded: %s", got)
	}
}

func TestEmptyTagsAreLeftOut(t *testing.T) {
	got := TelemetryTags{Lane: "lane-1", Generation: 1}.ResourceAttributes()
	if got != "ropeshift.lane.id=lane-1,ropeshift.generation=1" {
		t.Errorf("got %s", got)
	}
}

func TestOmittingTheRepositoryLeavesBranchAndRepoOut(t *testing.T) {
	tags := TelemetryTags{Lane: "lane-1", Generation: 1, Repo: "r", Branch: "b"}.Omitting(TelemetrySettings{OmitRepository: true})
	if got := tags.ResourceAttributes(); strings.Contains(got, "repo") || strings.Contains(got, "branch") {
		t.Errorf("got %s", got)
	}
}
