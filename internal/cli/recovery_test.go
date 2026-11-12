package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
)

func recoveryApproval(t *testing.T) authority.Approval {
	t.Helper()
	detail, err := json.Marshal(authority.RecoveryDetail{
		Act: authority.RecoverReset, WorkOrder: "wo-1", Lane: "lane-1", Reason: "a flaky test crashed it",
		Check:         authority.CheckBreakerResets,
		Limit:         "orchestrators reset this work order's breaker 2 of max_breaker_resets_per_day 2 times in the last day",
		PolicyVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return authority.Approval{
		ID: "recovery-1", Kind: authority.KindRecovery, WorkOrder: "wo-1", Lane: "lane-1", Subject: string(authority.RecoverReset),
		SubjectDigest: "sha256:subject", Summary: "lane_reset on work order wo-1", RequestedBy: "orchestrator:w1",
		State: authority.ApprovalPending, Detail: string(detail),
	}
}

// A member reads what an orchestrator asked to do, why, and the limit of the policy it went past, before
// approving it.
func TestApprovalShowSaysWhatARecoveryRequestAsks(t *testing.T) {
	p := newPlane(t)
	p.on("GET /v1/approvals/{id}", func(*http.Request, []byte) (any, *api.Error) { return recoveryApproval(t), nil })
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"approval", "show", "recovery-1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range []string{
		"recovery: lane_reset of work order wo-1, lane lane-1\n",
		"reason: a flaky test crashed it\n",
		"outside recovery policy version 1: max_breaker_resets_per_day: orchestrators reset this work order's breaker 2 of max_breaker_resets_per_day 2 times in the last day\n",
		"requested by: orchestrator:w1\n",
		"digest: sha256:subject\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("approval show lacks %q:\n%s", want, stdout.String())
		}
	}
}
