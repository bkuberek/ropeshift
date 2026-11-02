package doubles

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A record is named for a process, and process IDs come round: a session that started later can hold
// the smaller number, and a name sorts as text in any case. Sessions read in the order they started.
func TestSessionsReadInTheOrderTheyStarted(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, SeenDir), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Hour)
	for i, record := range []struct{ name, body string }{{"99990.json", `{"args":["first"]}`}, {"105.json", `{"args":["second"]}`}} {
		path := filepath.Join(dir, SeenDir, record.name)
		if err := os.WriteFile(path, []byte(record.body), 0o600); err != nil {
			t.Fatal(err)
		}
		at := start.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	seen := SeenBy(t, dir)
	if len(seen) != 2 || seen[0].Args[0] != "first" || seen[1].Args[0] != "second" {
		t.Errorf("seen %+v", seen)
	}
}
