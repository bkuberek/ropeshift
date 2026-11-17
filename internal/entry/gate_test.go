package entry

import (
	"strings"
	"testing"
)

// The preview is on for one value, spelled exactly, and for nothing else a person might mean by it.
func TestGroupPreviewIsOnlyTheValuePreview(t *testing.T) {
	tests := []struct {
		value string
		on    bool
	}{
		{"", false},
		{"preview", true},
		{"Preview", false},
		{"preview ", false},
		{"1", false},
		{"true", false},
		{"on", false},
		{"previews", false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			getenv := func(name string) string {
				if name == "ROPESHIFT_GROUP_MODE" {
					return tt.value
				}
				return ""
			}
			if got := groupPreview(getenv); got != tt.on {
				t.Errorf("groupPreview with %q is %t, want %t", tt.value, got, tt.on)
			}
		})
	}
}

// Only the variable that names the gate counts: another one that happens to hold the value does not.
func TestGroupPreviewReadsOnlyItsOwnVariable(t *testing.T) {
	asked := map[string]bool{}
	getenv := func(name string) string {
		asked[name] = true
		return "preview"
	}
	if !groupPreview(getenv) {
		t.Fatal("the preview is off")
	}
	if len(asked) != 1 || !asked["ROPESHIFT_GROUP_MODE"] {
		t.Errorf("the gate read %v", asked)
	}
}

func TestTheRefusalNamesTheGateAndHowToPassIt(t *testing.T) {
	want := "group mode is a preview in this release; set ROPESHIFT_GROUP_MODE=preview to run it"
	if groupRefusal != want {
		t.Errorf("the refusal is %q, want %q", groupRefusal, want)
	}
	if strings.Contains(groupRefusal, "\n") {
		t.Errorf("the refusal runs over lines: %q", groupRefusal)
	}
}
