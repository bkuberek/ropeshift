package claudecode

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The settings a probe's sessions start with refuse the bypass mode, as a lane's do: a key the file names with an
// empty value is rejected by the CLI, which then reads none of the file.
func TestAProbesSettingsRefuseTheBypassModeWithAValueTheCLIReads(t *testing.T) {
	t.Parallel()
	settings, _, err := writeProbeFiles(t.TempDir(), "/state/bin/ropeshift", inboundAccept)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Permissions map[string]any `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if got := file.Permissions["disableBypassPermissionsMode"]; got != "disable" {
		t.Errorf("disableBypassPermissionsMode reads %#v, want %q", got, "disable")
	}
	for key, value := range file.Permissions {
		if text, ok := value.(string); ok && text == "" {
			t.Errorf("the settings file holds %q with an empty value", key)
		}
	}
	if strings.Contains(string(raw), `"disableBypassPermissionsMode": ""`) {
		t.Errorf("the settings file holds an empty bypass value:\n%s", raw)
	}
}
