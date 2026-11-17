package sqlstore

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/version"
)

// TestARefusalLinksTheTroubleshootingPageOfThisBuild holds that the link a refusal ends with names the tag
// of the release that refuses, so the page matches the build, and main for a development build.
func TestARefusalLinksTheTroubleshootingPageOfThisBuild(t *testing.T) {
	ref := "main"
	if v := version.Read("ropeshiftd").Version; v != version.Development {
		ref = v
	}
	want := "https://github.com/bkuberek/ropeshift/blob/" + ref + "/docs/troubleshooting.md#database-refuses-to-start"
	err := Refusal([]*MigrationError{{Set: "ledger", Version: 1, File: "0001_ledger.sql", Recorded: "recorded", Shipped: "shipped"}})
	if !strings.HasSuffix(err.Error(), want) {
		t.Errorf("the refusal %q does not end with the link %s", err, want)
	}
}
