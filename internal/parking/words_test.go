package parking_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/parking"
)

var update = flag.Bool("update", false, "write the wake words the web tests read")

// wakeWords is the one table of how a wake reads to a person. The web UI says a wake in its own code,
// and its test holds what it renders to the golden this test writes, so the web UI and `ropeshift top`
// say each wake the same way.
var wakeWords = []struct {
	Name       string `json:"name"`
	Expression string `json:"expression"`
	Words      string `json:"words"`
	Short      string `json:"short"`
}{
	{"a question", `{"question_answered":"q-signing-key"}`, "When question q-signing-key is answered", "question q-signing-key"},
	{"an approval", `{"approval_decided":"approval-1"}`, "When approval approval-1 is decided", "approval approval-1"},
	{"a decision", `{"decision_recorded":"payments"}`, "When a decision on payments is recorded", "a decision on payments"},
	{"a usage limit", `{"usage_reset":"worker-a:claude-code"}`, "When the usage limit on worker-a:claude-code resets", "a usage reset on worker-a:claude-code"},
	{"a sign-in", `{"signin_restored":"worker-a:claude-code"}`, "When the sign-in on worker-a:claude-code is restored", "a sign-in on worker-a:claude-code"},
	{"a work order", `{"work_order_done":"wo-1"}`, "When work order wo-1 is done", "work order wo-1"},
	{"a check", `{"external_check":"ci"}`, "When check ci reports back", "check ci"},
	{"a person", `{"human_resume":true}`, "When a person resumes it", "a person"},
	{"a time", `{"at":1790000000000}`, "When the time reaches 2026-09-21 14:13 UTC", "2026-09-21 14:13 UTC"},
	{
		"a blocking question", `{"any_of":[{"all_of":[{"question_answered":"q-signing-key"}]},{"human_resume":true}]}`,
		"When question q-signing-key is answered, or when a person resumes it", "question q-signing-key or a person",
	},
	{
		"two blocking questions", `{"any_of":[{"all_of":[{"question_answered":"q-1"},{"question_answered":"q-2"}]},{"human_resume":true}]}`,
		"When question q-1 is answered and question q-2 is answered, or when a person resumes it", "(question q-1 and question q-2) or a person",
	},
	{
		"three at once", `{"all_of":[{"question_answered":"q-1"},{"approval_decided":"approval-1"},{"work_order_done":"wo-1"}]}`,
		"When question q-1 is answered, approval approval-1 is decided, and work order wo-1 is done", "question q-1 and approval approval-1 and work order wo-1",
	},
	{
		"a usage limit or its time", `{"any_of":[{"usage_reset":"worker-a:claude-code"},{"at":1790000000000}]}`,
		"When the usage limit on worker-a:claude-code resets, or when the time reaches 2026-09-21 14:13 UTC", "a usage reset on worker-a:claude-code or 2026-09-21 14:13 UTC",
	},
	{
		"a choice inside a whole", `{"all_of":[{"any_of":[{"at":1790000000000},{"external_check":"ci"}]},{"question_answered":"q-1"}]}`,
		"When either the time reaches 2026-09-21 14:13 UTC or check ci reports back, and question q-1 is answered", "(2026-09-21 14:13 UTC or check ci) and question q-1",
	},
	{
		"a choice inside a choice", `{"any_of":[{"any_of":[{"question_answered":"q-1"},{"question_answered":"q-2"}]},{"human_resume":true}]}`,
		"When question q-1 is answered, or when question q-2 is answered, or when a person resumes it", "question q-1 or question q-2 or a person",
	},
}

func TestAWakeReadsInWords(t *testing.T) {
	for _, tt := range wakeWords {
		t.Run(tt.Name, func(t *testing.T) {
			expr := parse(t, tt.Expression)
			if got := expr.Words(); got != tt.Words {
				t.Errorf("words %q, want %q", got, tt.Words)
			}
			if got := expr.ShortWords(); got != tt.Short {
				t.Errorf("short words %q, want %q", got, tt.Short)
			}
		})
	}
}

// TestTheWebUISaysAWakeTheSameWay writes the table where the web tests read it. Run it with -update
// after a change to the table.
func TestTheWebUISaysAWakeTheSameWay(t *testing.T) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(wakeWords); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "web", "testdata", "wake-words.json")
	if *update {
		if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: run this test with -update", err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("the wake words changed: run this test with -update, and read the diff\ngot:\n%s", out.Bytes())
	}
}

func TestEveryKindOfFactHasWords(t *testing.T) {
	kinds := []parking.Kind{
		parking.QuestionAnswered, parking.ApprovalDecided, parking.DecisionRecorded, parking.UsageReset,
		parking.SigninRestored, parking.WorkOrderDone, parking.ExternalCheck, parking.HumanResume,
	}
	for _, kind := range kinds {
		covered := false
		for _, tt := range wakeWords {
			covered = covered || parse(t, tt.Expression).Names(kind)
		}
		if !covered {
			t.Errorf("no words say %s", kind)
		}
	}
}
