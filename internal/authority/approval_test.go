package authority_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

var (
	aMember        = ledger.Actor{Kind: ledger.ActorMember, ID: "m1"}
	anOrchestrator = ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "w1"}
	theLane        = ledger.Actor{Kind: ledger.ActorLane, ID: "lane-1"}
)

func call(t *testing.T, tool, input string) authority.ToolCall {
	t.Helper()
	read, err := authority.ReadToolCall(tool, []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	return read
}

func request(t *testing.T, requester ledger.Actor) authority.Approval {
	t.Helper()
	read := call(t, "Bash", `{"command":"git tag v1"}`)
	return authority.Approval{
		ID: "a1", Lane: "lane-1", Kind: authority.KindToolCall, Subject: read.Tool, SubjectDigest: read.Digest, Input: read.Input,
		Summary: read.Summary, Command: read.Command, RequestedBy: authority.Principal(requester), State: authority.ApprovalPending,
	}
}

func decision(a authority.Approval, allow bool) authority.Decision {
	return authority.Decision{ID: a.ID, Digest: a.SubjectDigest, Allow: allow}
}

func TestADigestCoversTheToolAndEveryByteOfItsInput(t *testing.T) {
	same := call(t, "Bash", `{"timeout":1,"command":"go test"}`)
	if other := call(t, "Bash", `{"command":"go test","timeout":1}`); other.Digest != same.Digest {
		t.Error("two spellings of one object read as two calls")
	}
	for name, other := range map[string]authority.ToolCall{
		"another tool":       call(t, "Edit", `{"timeout":1,"command":"go test"}`),
		"another command":    call(t, "Bash", `{"timeout":1,"command":"go test "}`),
		"another field":      call(t, "Bash", `{"timeout":2,"command":"go test"}`),
		"a number's digits":  call(t, "Bash", `{"timeout":1.00000000000000000001,"command":"go test"}`),
		"a field's presence": call(t, "Bash", `{"command":"go test"}`),
	} {
		if other.Digest == same.Digest {
			t.Errorf("%s reads as the same call", name)
		}
	}
}

// One input can land in two places, so a decision covers where it lands. A hook knows no worktree,
// and it goes on naming the call by its input.
func TestADigestCoversWhereACallLands(t *testing.T) {
	read := call(t, "Edit", `{"file_path":"docs/a.md"}`)
	if read.Digest != read.Seen || read.Seen != authority.ToolDigest("Edit", read.Input) {
		t.Fatalf("before a worker reads it: %+v", read)
	}
	here, there := read.Lands("docs/a.md"), read.Lands("/outside/a.md")
	if here.Digest == read.Digest || here.Digest == there.Digest {
		t.Errorf("two landings read as one: %s and %s", here.Digest, there.Digest)
	}
	if here.Seen != read.Seen || there.Seen != read.Seen {
		t.Errorf("a landing moved what a hook names: %+v", there)
	}
	if here.Digest != authority.LandingDigest("Edit", read.Input, "docs/a.md") {
		t.Errorf("a call's digest is not the one a request is held to: %s", here.Digest)
	}
	if here.Summary != read.Summary {
		t.Errorf("a call that lands where it says reads %q", here.Summary)
	}
	if !strings.HasPrefix(there.Summary, read.Summary) || !strings.Contains(there.Summary, "/outside/a.md") {
		t.Errorf("a reviewer reads %q", there.Summary)
	}
	command := call(t, "Bash", `{"command":"go test"}`)
	if bound := command.Lands(""); bound.Digest != command.Digest || bound.Summary != command.Summary {
		t.Errorf("a call with no path: %+v", bound)
	}
	spelled := call(t, "Edit", `{"file_path":"a\u0000b"}`)
	if strings.ContainsRune(spelled.Input, 0) {
		t.Errorf("an input can spell the byte a landing follows: %q", spelled.Input)
	}
}

// A class reads the field a tool reads. A second key that only folds to its name must not stand in
// for it, or a call could show a covered path and act on another.
func TestACallIsReadByItsExactKeys(t *testing.T) {
	tests := []struct {
		name, input, command, path string
	}{
		{name: "a command", input: `{"command":"go test"}`, command: "go test"},
		{name: "a file", input: `{"file_path":"docs/a.md"}`, path: "docs/a.md"},
		{name: "a notebook", input: `{"notebook_path":"docs/a.ipynb"}`, path: "docs/a.ipynb"},
		{name: "a key in capitals", input: `{"COMMAND":"go test"}`},
		{name: "a key that folds to a notebook", input: "{\"notebook_path\":\"/etc/keys\",\"notebooK_path\":\"docs/a.ipynb\"}", path: "/etc/keys"},
		{name: "a value of the wrong type", input: `{"command":1,"file_path":"docs/a.md"}`, path: "docs/a.md"},
		{name: "two paths, which name none", input: `{"file_path":"docs/a.md","notebook_path":"/etc/keys"}`},
		{name: "the same path twice", input: `{"file_path":"docs/a.md","notebook_path":"docs/a.md"}`, path: "docs/a.md"},
		{name: "no object", input: `["go test"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := call(t, "Bash", tt.input)
			if got.Command != tt.command || got.Path != tt.path {
				t.Errorf("command %q, path %q", got.Command, got.Path)
			}
		})
	}
}

// What a person reads comes from what the digest covers, and a call too long to show says so.
func TestASummaryComesFromTheCallAndSaysWhatItLeftOut(t *testing.T) {
	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'a'
	}
	read := call(t, "Bash", `{"command":"`+string(long)+`"}`)
	if len(read.Summary) > 4200 || read.Summary[:6] != "Bash: " {
		t.Errorf("summary of %d bytes", len(read.Summary))
	}
	if want := "904 more bytes"; !contains(read.Summary, want) {
		t.Errorf("a long call does not say what it left out: %q", read.Summary[len(read.Summary)-60:])
	}
}

func contains(text, part string) bool {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

func TestNoPrincipalDecidesItsOwnRequest(t *testing.T) {
	for name, requester := range map[string]ledger.Actor{"a lane": theLane, "an orchestrator": anOrchestrator, "a member": aMember} {
		t.Run(name, func(t *testing.T) {
			a := request(t, requester)
			_, err := a.Decide(decision(a, true), requester, 0, 1)
			if requester.Kind == ledger.ActorLane {
				if !errors.Is(err, authority.ErrDeciderKind) {
					t.Fatalf("err %v", err)
				}
				return
			}
			if !errors.Is(err, authority.ErrSelfApproval) {
				t.Fatalf("err %v", err)
			}
		})
	}
}

// A lane decides nothing, and neither does a worker: a kind with no place here is refused even on
// a request that is not its own.
func TestOnlyAMemberOrAnOrchestratorDecides(t *testing.T) {
	a := request(t, theLane)
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorLane, ID: "lane-2"}, {Kind: ledger.ActorWorker, ID: "w1"}, {Kind: ledger.ActorSystem, ID: "tick"},
	} {
		if _, err := a.Decide(decision(a, true), actor, 0, 1); !errors.Is(err, authority.ErrDeciderKind) {
			t.Errorf("%s: %v", actor.Kind, err)
		}
	}
}

func TestADecisionNamesTheDigestItSaw(t *testing.T) {
	a := request(t, theLane)
	stale := decision(a, true)
	stale.Digest = call(t, "Bash", `{"command":"git tag v2"}`).Digest
	if _, err := a.Decide(stale, aMember, 0, 1); !errors.Is(err, authority.ErrStaleDigest) {
		t.Fatalf("err %v", err)
	}
	decided, err := a.Decide(decision(a, true), aMember, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if decided.State != authority.ApprovalApproved || decided.DecidedDigest != a.SubjectDigest || decided.DecidedBy != "member:m1" || decided.PolicyVersion != 7 {
		t.Errorf("decided %+v", decided)
	}
}

func TestAClosedRequestTakesNoSecondDecision(t *testing.T) {
	a := request(t, theLane)
	approved, err := a.Decide(decision(a, true), aMember, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := approved.Decide(decision(a, false), ledger.Actor{Kind: ledger.ActorMember, ID: "m2"}, 0, 2); !errors.Is(err, authority.ErrApprovalClosed) {
		t.Errorf("a decided request: %v", err)
	}
	for _, state := range []authority.ApprovalState{authority.ApprovalVoid, authority.ApprovalExpired, authority.ApprovalAnswered, authority.ApprovalUsed} {
		closed := a
		closed.State = state
		if _, err := closed.Decide(decision(a, true), aMember, 0, 2); !errors.Is(err, authority.ErrApprovalClosed) {
			t.Errorf("%s: %v", state, err)
		}
	}
}

// A retry after a lost answer is the same decision. Anything else about it is not a retry.
func TestARepeatIsTheSameDecisionByTheSamePrincipal(t *testing.T) {
	a := request(t, theLane)
	approved, err := a.Decide(decision(a, true), aMember, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !approved.Repeats(decision(a, true), aMember) {
		t.Error("the same decision is no repeat")
	}
	if approved.Repeats(decision(a, false), aMember) {
		t.Error("a denial repeats an approval")
	}
	if approved.Repeats(decision(a, true), ledger.Actor{Kind: ledger.ActorMember, ID: "m2"}) {
		t.Error("another member repeats a decision")
	}
	voided := approved
	voided.State = authority.ApprovalVoid
	if voided.Repeats(decision(a, true), aMember) {
		t.Error("a void approval repeats")
	}
}

func approved(t *testing.T) authority.Approval {
	t.Helper()
	a := request(t, theLane)
	decided, err := a.Decide(decision(a, true), aMember, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	return decided
}

// One approval runs one call, and another call on it is a replay, whatever it presents.
func TestAnApprovalRunsOneCall(t *testing.T) {
	a := approved(t)
	used, err := a.Use(a.SubjectDigest, "call-1")
	if err != nil || used.State != authority.ApprovalUsed {
		t.Fatalf("used %+v, %v", used, err)
	}
	if _, err := used.Use(used.SubjectDigest, "call-2"); !errors.Is(err, authority.ErrApprovalClosed) {
		t.Errorf("a replay: %v", err)
	}
	if _, err := used.Use(used.SubjectDigest, ""); !errors.Is(err, authority.ErrApprovalClosed) {
		t.Errorf("a replay naming no call: %v", err)
	}
	if again, err := used.Use(used.SubjectDigest, "call-1"); err != nil || again.State != authority.ApprovalUsed {
		t.Errorf("the call whose answer was lost: %+v, %v", again, err)
	}
}

// An artifact that changed voids a decision, and nothing brings it back.
func TestAChangedDigestVoidsADecision(t *testing.T) {
	a := approved(t)
	void, err := a.Use(call(t, "Bash", `{"command":"git tag v2"}`).Digest, "call-1")
	if !errors.Is(err, authority.ErrStaleDigest) || void.State != authority.ApprovalVoid {
		t.Fatalf("void %+v, %v", void, err)
	}
	if _, err := void.Use(a.SubjectDigest, "call-1"); !errors.Is(err, authority.ErrApprovalClosed) {
		t.Errorf("a void approval ran: %v", err)
	}
}

// A replay hears the answer it lost only as the call it was, with the digest it had.
func TestAReplayIsTheSameCallWithTheSameDigest(t *testing.T) {
	a := approved(t)
	used, err := a.Use(a.SubjectDigest, "call-1")
	if err != nil {
		t.Fatal(err)
	}
	changed := call(t, "Bash", `{"command":"git tag v2"}`)
	if _, err := used.Use(changed.Digest, "call-1"); !errors.Is(err, authority.ErrApprovalClosed) {
		t.Errorf("the same call with another digest: %v", err)
	}
	unnamed, err := a.Use(a.SubjectDigest, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unnamed.Use(a.SubjectDigest, ""); !errors.Is(err, authority.ErrApprovalClosed) {
		t.Errorf("two calls that name nothing read as one: %v", err)
	}
}

// A retry names what it saw, so a decision on another digest is a new one, and it is refused.
func TestARepeatNamesTheDigestItSaw(t *testing.T) {
	a := approved(t)
	other := decision(a, true)
	other.Digest = call(t, "Bash", `{"command":"git tag v2"}`).Digest
	if a.Repeats(other, aMember) {
		t.Error("a decision on another digest repeats this one")
	}
}

// Standing reads a lane's requests for one call, and which of them a write must settle.
func TestStandingReadsOneCallAmongALanesRequests(t *testing.T) {
	gated := call(t, "Bash", `{"command":"git tag v1"}`)
	changed := call(t, "Bash", `{"command":"git tag v2"}`)
	row := func(state authority.ApprovalState, generation int64) authority.Approval {
		return authority.Approval{ID: "a1", Subject: "Bash", SubjectDigest: gated.Digest, State: state, Generation: generation}
	}
	spent := func(by string) authority.Approval {
		used := row(authority.ApprovalUsed, 2)
		used.UsedBy = by
		return used
	}
	otherTool := row(authority.ApprovalApproved, 2)
	otherTool.Subject = "Edit"
	otherTool.SubjectDigest = changed.Digest
	tests := []struct {
		name    string
		row     authority.Approval
		digest  string
		peek    bool
		write   bool
		state   authority.ApprovalState
		allowed bool
	}{
		{name: "an approval of another tool", row: otherTool, digest: gated.Digest},
		{name: "an approval to spend", row: row(authority.ApprovalApproved, 2), digest: gated.Digest, write: true},
		{name: "a look at one spends nothing", row: row(authority.ApprovalApproved, 2), digest: gated.Digest, peek: true, state: authority.ApprovalApproved},
		{name: "a call that moved from an approval", row: row(authority.ApprovalApproved, 2), digest: changed.Digest, write: true},
		{name: "a second open call of this turn", row: row(authority.ApprovalPending, 2), digest: changed.Digest},
		{name: "a request of an earlier generation", row: row(authority.ApprovalPending, 1), digest: changed.Digest, write: true},
		{name: "a decision spent on this call", row: spent("call-1"), digest: gated.Digest, state: authority.ApprovalUsed, allowed: true},
		{name: "a decision spent on another", row: spent("call-2"), digest: gated.Digest, state: authority.ApprovalUsed},
		{name: "a decision spent on no call", row: spent(""), digest: gated.Digest, state: authority.ApprovalUsed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			use := authority.ApprovalUse{Subject: "Bash", Digest: tt.digest, Call: "call-1", Generation: 2, Peek: tt.peek}
			answer, write := authority.Standing([]authority.Approval{tt.row}, use)
			if write != tt.write || answer.State != tt.state || answer.Allowed != tt.allowed {
				t.Errorf("answer %+v, write %v", answer, write)
			}
		})
	}
	use := authority.ApprovalUse{Subject: "Bash", Digest: gated.Digest, Generation: 2}
	if answer, _ := authority.Standing([]authority.Approval{spent("")}, use); answer.Allowed {
		t.Error("a call naming nothing read a decision spent on nothing as its own")
	}
}

func TestADeniedOrOpenRequestRunsNothing(t *testing.T) {
	a := request(t, theLane)
	if _, err := a.Use(a.SubjectDigest, "call-1"); !errors.Is(err, authority.ErrApprovalClosed) {
		t.Errorf("an open request: %v", err)
	}
	denied, err := a.Decide(decision(a, false), aMember, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := denied.Use(a.SubjectDigest, "call-1"); !errors.Is(err, authority.ErrApprovalClosed) {
		t.Errorf("a denied request: %v", err)
	}
}

// Only a decision nobody has spent names a call to make, and a call too long to state says so by its
// summary, so a session that cannot repeat it asks again.
func TestOnlyAnApprovedRequestNamesACallToMake(t *testing.T) {
	a := request(t, theLane)
	for _, state := range []authority.ApprovalState{
		authority.ApprovalPending, authority.ApprovalDenied, authority.ApprovalUsed, authority.ApprovalVoid,
		authority.ApprovalExpired, authority.ApprovalAnswered,
	} {
		a.State = state
		if got := a.Repeat(); got != "" {
			t.Errorf("%s names %q", state, got)
		}
	}
	a.State = authority.ApprovalApproved
	if got := a.Repeat(); got != "Tool: Bash\nInput: "+a.Input {
		t.Errorf("approved names %q", got)
	}
	if got := a.Outcome(); got != "" {
		t.Errorf("an approved request also says %q", got)
	}
	a.Input = strings.Repeat("x", 1<<16+1)
	if got := a.Repeat(); got != a.Summary {
		t.Errorf("a long call names %d bytes", len(got))
	}
}

func TestARequestBindsToWhatItNames(t *testing.T) {
	good := request(t, theLane)
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	forged := good
	forged.Input = `{"command":"rm -rf ."}`
	if err := forged.Validate(); !errors.Is(err, authority.ErrBadApproval) {
		t.Errorf("a digest that does not cover its input: %v", err)
	}
	moved := good
	moved.Path = "docs/a.md"
	if err := moved.Validate(); !errors.Is(err, authority.ErrBadApproval) {
		t.Errorf("a digest that does not cover its path: %v", err)
	}
	moved.SubjectDigest = authority.LandingDigest(moved.Subject, moved.Input, moved.Path)
	if err := moved.Validate(); err != nil {
		t.Errorf("a request bound to where its call lands: %v", err)
	}
	for name, change := range map[string]func(*authority.Approval){
		"no digest":    func(a *authority.Approval) { a.SubjectDigest = "" },
		"no requester": func(a *authority.Approval) { a.RequestedBy = "" },
		"no lane":      func(a *authority.Approval) { a.Lane = "" },
		"an odd kind":  func(a *authority.Approval) { a.Kind = "wish" },
	} {
		bad := good
		change(&bad)
		if err := bad.Validate(); !errors.Is(err, authority.ErrBadApproval) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDelegationClassesReachOnlyWhatTheyDeclare(t *testing.T) {
	policy := authority.DelegationPolicy{Version: 1, Classes: []authority.DelegationClass{
		{Tool: "Bash", CommandPrefix: "go test"},
		{Tool: "Edit", Path: "docs/**"},
		{Tool: "Write", Path: "notes/*.md"},
	}}
	tests := []struct {
		name          string
		tool, command string
		path          string
		want          bool
	}{
		{"the prefix alone", "Bash", "go test", "", true},
		{"the prefix with arguments", "Bash", "go test ./...", "", true},
		{"a longer word", "Bash", "go testify", "", false},
		{"another tool with that command", "Task", "go test", "", false},
		{"a second command", "Bash", "go test; rm -rf .", "", false},
		{"a chain", "Bash", "go test && rm -rf .", "", false},
		{"a pipe", "Bash", "go test | sh", "", false},
		{"a substitution", "Bash", "go test $(rm -rf .)", "", false},
		{"backticks", "Bash", "go test `rm -rf .`", "", false},
		{"a redirect", "Bash", "go test > .git/config", "", false},
		{"a second line", "Bash", "go test\nrm -rf .", "", false},
		{"a background job", "Bash", "go test & rm -rf .", "", false},
		{"a variable in front", "Bash", "GOFLAGS=-x go test", "", false},
		{"no command at all", "Bash", "", "", false},
		{"a file under the directory", "Edit", "", "docs/a.md", true},
		{"a file deep under it", "Edit", "", "docs/a/b/c.md", true},
		{"the directory's name in a longer one", "Edit", "", "docs2/a.md", false},
		{"a path class on a call that also runs a command", "Edit", "rm -rf .", "docs/a.md", false},
		{"a prefix class on a call that also names a path", "Bash", "go test", "docs/a.md", false},
		{"a way out of it", "Edit", "", "docs/../secrets", false},
		{"a parent", "Edit", "", "../docs/a.md", false},
		{"an absolute path", "Edit", "", "/etc/docs/a.md", false},
		{"no path", "Edit", "", "", false},
		{"one level of a pattern", "Write", "", "notes/a.md", true},
		{"past that level", "Write", "", "notes/a/b.md", false},
		{"another tool on that path", "Write", "", "docs/a.md", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := authority.Approval{Kind: authority.KindToolCall, Subject: tt.tool, Command: tt.command, Path: tt.path}
			if got := policy.Covers(a); got != tt.want {
				t.Errorf("covers %v", got)
			}
		})
	}
	artifact := authority.Approval{Kind: authority.KindArtifact, Subject: "Bash", Command: "go test"}
	if policy.Covers(artifact) {
		t.Error("a class reached an artifact")
	}
}

// A class can name anything, and a path that leaves the worktree still goes to a member.
func TestAPathClassEndsAtTheWorktree(t *testing.T) {
	policy := authority.DelegationPolicy{Version: 1, Classes: []authority.DelegationClass{
		{Tool: "Edit", Path: "/etc/**"}, {Tool: "Edit", Path: "../**"}, {Tool: "Edit", Path: ".."},
	}}
	for _, target := range []string{"/etc/passwd", "../secrets", ".."} {
		a := authority.Approval{Kind: authority.KindToolCall, Subject: "Edit", Path: target}
		if policy.Covers(a) {
			t.Errorf("a class reached %q", target)
		}
	}
}

func TestADelegationPolicyDeclaresNoToolOutright(t *testing.T) {
	for name, class := range map[string]authority.DelegationClass{
		"a tool alone":    {Tool: "Bash"},
		"no tool":         {CommandPrefix: "go test"},
		"both":            {Tool: "Bash", CommandPrefix: "go test", Path: "docs/**"},
		"a prefix's meta": {Tool: "Bash", CommandPrefix: "go test;"},
	} {
		policy := authority.DelegationPolicy{Version: 1, Classes: []authority.DelegationClass{class}}
		if err := policy.Check(); err == nil {
			t.Errorf("%s passed", name)
		}
	}
	if err := (authority.DelegationPolicy{Version: 1}).Check(); err != nil {
		t.Errorf("a policy that delegates nothing: %v", err)
	}
}

func TestABodyThatChangedAfterReviewDecidesNothing(t *testing.T) {
	draft := authority.DelegationDraft{Body: authority.BuiltInDelegationBody, Policy: authority.DelegationPolicy{Version: 1}}
	version, err := authority.NewDelegationVersion("p1", "built-in", draft, 1)
	if err != nil {
		t.Fatal(err)
	}
	version.Body += "\n[[delegation_policy.classes]]\ntool = \"Bash\"\ncommand_prefix = \"sudo\"\n"
	if _, err := authority.ReadDelegation(version); !errors.Is(err, authority.ErrPolicyDigest) {
		t.Errorf("err %v", err)
	}
}

func planApproval(requester ledger.Actor) authority.Approval {
	return authority.Approval{
		ID: "pa1", Kind: authority.KindPlan, Subject: "plan-1@2", SubjectDigest: "sha256:plan", Plan: "plan-1", PlanVersion: 2,
		Summary: "Plan the retry budget, version 2", RequestedBy: authority.Principal(requester), State: authority.ApprovalPending,
	}
}

// A plan belongs to no lane, so a request of that kind binds to a plan version and not to a lane.
func TestAPlanApprovalNamesAPlanAndNoLane(t *testing.T) {
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}
	if err := planApproval(system).Validate(); err != nil {
		t.Fatalf("a request for a plan version: %v", err)
	}
	if err := planApproval(theLane).Validate(); err != nil {
		t.Fatalf("a request the planning lane made: %v", err)
	}
	for name, mutate := range map[string]func(*authority.Approval){
		"no digest":       func(a *authority.Approval) { a.SubjectDigest = "" },
		"no requester":    func(a *authority.Approval) { a.RequestedBy = "" },
		"no subject":      func(a *authority.Approval) { a.Subject = "" },
		"a lane":          func(a *authority.Approval) { a.Lane = "lane-1" },
		"a generation":    func(a *authority.Approval) { a.Generation = 1 },
		"a tool's detail": func(a *authority.Approval) { a.Input = `{"command":"ls"}` },
	} {
		t.Run(name, func(t *testing.T) {
			bad := planApproval(system)
			mutate(&bad)
			if err := bad.Validate(); !errors.Is(err, authority.ErrBadApproval) {
				t.Fatalf("got %v", err)
			}
		})
	}
	other := request(t, system)
	other.Lane = ""
	if err := other.Validate(); !errors.Is(err, authority.ErrBadApproval) {
		t.Fatalf("a tool call that names no lane: %v", err)
	}
}

func TestOnlyAMemberDecidesAPlanApproval(t *testing.T) {
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}
	pending := planApproval(system)
	decided, err := pending.Decide(decision(pending, true), aMember, 1, 100)
	if err != nil {
		t.Fatalf("a member: %v", err)
	}
	if decided.State != authority.ApprovalApproved || decided.DecidedBy != "member:m1" || decided.DecidedDigest != pending.SubjectDigest {
		t.Fatalf("decision %+v", decided)
	}
	for _, actor := range []ledger.Actor{anOrchestrator, theLane, {Kind: ledger.ActorWorker, ID: "w"}, system} {
		if _, err := pending.Decide(decision(pending, true), actor, 1, 100); !errors.Is(err, authority.ErrDeciderKind) {
			t.Errorf("%s decided a plan: %v", actor.Kind, err)
		}
	}
}

// The planning lane wrote the plan and requested its approval, and nothing it does can approve it.
func TestThePlanningLaneCannotApproveThePlanItWrote(t *testing.T) {
	pending := planApproval(theLane)
	_, err := pending.Decide(decision(pending, true), theLane, 1, 100)
	if !errors.Is(err, authority.ErrSelfApproval) && !errors.Is(err, authority.ErrDeciderKind) {
		t.Fatalf("the lane approved its own plan: %v", err)
	}
	if _, err := pending.Decide(decision(pending, true), ledger.Actor{Kind: ledger.ActorLane, ID: "another-lane"}, 1, 100); err == nil {
		t.Fatal("another lane approved the plan")
	}
}

func TestAPlanApprovalBoundToAnotherDigestIsNotDecided(t *testing.T) {
	pending := planApproval(ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
	stale := authority.Decision{ID: pending.ID, Digest: "sha256:an-older-plan", Allow: true}
	if _, err := pending.Decide(stale, aMember, 1, 100); !errors.Is(err, authority.ErrStaleDigest) {
		t.Fatalf("got %v", err)
	}
}

func TestANewerVersionSupersedesAnOpenPlanApprovalAndNoOther(t *testing.T) {
	pending := planApproval(ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
	superseded, err := pending.Supersede()
	if err != nil {
		t.Fatal(err)
	}
	if superseded.State != authority.ApprovalSuperseded || superseded.Open() || superseded.Live() || !superseded.Decided() {
		t.Fatalf("a superseded request is %+v", superseded)
	}
	if _, err := superseded.Decide(decision(pending, true), aMember, 1, 100); !errors.Is(err, authority.ErrApprovalClosed) {
		t.Fatalf("a member approved a version a newer one replaced: %v", err)
	}
	approved, err := pending.Decide(decision(pending, true), aMember, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := approved.Supersede(); !errors.Is(err, authority.ErrApprovalClosed) {
		t.Fatalf("an approved version was replaced: %v", err)
	}
}

// A call that moved ends the request that stood for it, if the request could still lead to a tool
// running. Every other state is an end already reached, and a record of how a request ended does not
// change under a later word about a call.
func TestACallThatMovedEndsOnlyARequestThatCouldStillRun(t *testing.T) {
	const why = "the call moved"
	pending := request(t, theLane)
	granted := approved(t)
	denied, err := pending.Decide(decision(pending, false), aMember, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	used, err := granted.Use(granted.SubjectDigest, "call-1")
	if err != nil {
		t.Fatal(err)
	}
	reasoned := granted
	reasoned.Reason = "looks fine"
	other := call(t, "Bash", `{"command":"git tag v2"}`).Digest
	tests := []struct {
		name   string
		row    authority.Approval
		digest string
		voids  bool
	}{
		{name: "a request nobody decided", row: pending, digest: pending.SubjectDigest, voids: true},
		{name: "an approval nobody spent", row: granted, digest: granted.SubjectDigest, voids: true},
		{name: "an approval with a reason its decider gave", row: reasoned, digest: reasoned.SubjectDigest, voids: true},
		{name: "a request for another call", row: pending, digest: other},
		{name: "a denial", row: denied, digest: denied.SubjectDigest},
		{name: "an approval that ran", row: used, digest: used.SubjectDigest},
		{name: "an answer at a terminal", row: withState(pending, authority.ApprovalAnswered), digest: pending.SubjectDigest},
		{name: "a request that expired", row: withState(pending, authority.ApprovalExpired), digest: pending.SubjectDigest},
		{name: "a request already void", row: withState(pending, authority.ApprovalVoid), digest: pending.SubjectDigest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, voided := tt.row.Moved(tt.digest, why)
			if voided != tt.voids {
				t.Fatalf("voided %v, want %v", voided, tt.voids)
			}
			if !tt.voids {
				if next.State != tt.row.State || next.Reason != tt.row.Reason {
					t.Errorf("a request that ended changed: %+v", next)
				}
				return
			}
			if next.State != authority.ApprovalVoid || next.Channel == authority.ChannelTerminal {
				t.Errorf("request %+v", next)
			}
			if next.Reason != why {
				t.Errorf("reason %q, want %q", next.Reason, why)
			}
		})
	}
}

func withState(a authority.Approval, state authority.ApprovalState) authority.Approval {
	a.State = state
	return a
}

// A recovery request belongs to a work order, and its lane only when the act works on one. It names an
// act this build knows.
func TestARecoveryRequestNamesItsWorkAndItsAct(t *testing.T) {
	base := authority.Approval{
		ID: "r1", WorkOrder: "wo-1", Kind: authority.KindRecovery, Subject: string(authority.RecoverRetry),
		SubjectDigest: authority.RecoveryDigest(authority.RecoverRetry, "wo-1", "failed"), RequestedBy: "orchestrator:w1",
	}
	if err := base.Validate(); err != nil {
		t.Errorf("a retry request with no lane: %v", err)
	}
	noWork := base
	noWork.WorkOrder = ""
	takeover := base
	takeover.Subject = "work_takeover"
	for name, bad := range map[string]authority.Approval{"no work order": noWork, "an act nobody named": takeover} {
		if err := bad.Validate(); !errors.Is(err, authority.ErrBadApproval) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A recovery act was outside the policy every orchestrator is held to, so no orchestrator decides one, its
// own or another's, and a member does.
func TestOnlyAMemberDecidesARecoveryRequest(t *testing.T) {
	request := authority.Approval{
		ID: "r1", WorkOrder: "wo-1", Kind: authority.KindRecovery, Subject: string(authority.RecoverReset),
		SubjectDigest: "sha256:subject", RequestedBy: "orchestrator:w1", State: authority.ApprovalPending,
	}
	decision := authority.Decision{ID: "r1", Digest: "sha256:subject", Allow: true}
	for _, actor := range []ledger.Actor{{Kind: ledger.ActorOrchestrator, ID: "w2"}, {Kind: ledger.ActorOrchestrator, ID: "w1"}} {
		if _, err := request.Decide(decision, actor, 1, 5); !errors.Is(err, authority.ErrDeciderKind) {
			t.Errorf("%s decided a recovery request: %v", actor.ID, err)
		}
	}
	decided, err := request.Decide(decision, ledger.Actor{Kind: ledger.ActorMember, ID: "m"}, 1, 5)
	if err != nil || decided.State != authority.ApprovalApproved {
		t.Errorf("a member's decision: %+v, %v", decided, err)
	}
}
