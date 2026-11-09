package authority_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

const minuteMs = 60 * 1000

// TestClaimFollowsTheLevelAndTheWindow is the table of FR-ORCH-6: a level, and whether a window a
// member opened stands now.
func TestClaimFollowsTheLevelAndTheWindow(t *testing.T) {
	cases := []struct {
		name   string
		level  authority.Level
		window bool
		want   bool
	}{
		{"manual leaves claims to members", authority.LevelManual, false, false},
		{"a window changes nothing for manual", authority.LevelManual, true, false},
		{"attended with no window waits", authority.LevelAttended, false, false},
		{"attended claims inside a window", authority.LevelAttended, true, true},
		{"unattended claims with no window", authority.LevelUnattended, false, true},
		{"unattended claims inside one too", authority.LevelUnattended, true, true},
		{"a level this build does not know claims nothing", authority.Level("wide-open"), true, false},
		{"no level claims nothing", authority.Level(""), true, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			standing := authority.AutonomyStanding{Level: tt.level, WindowOpen: tt.window}
			if got := standing.MayClaim(); got != tt.want {
				t.Fatalf("MayClaim = %v, want %v", got, tt.want)
			}
		})
	}
}

// Only manual keeps an orchestrator from the rest of the work: it works the question ladder alone.
func TestManualLeavesAnOrchestratorTheLadder(t *testing.T) {
	cases := []struct {
		level authority.Level
		want  bool
	}{
		{authority.LevelManual, false}, {authority.LevelAttended, true}, {authority.LevelUnattended, true}, {authority.Level(""), false},
	}
	for _, tt := range cases {
		if got := (authority.AutonomyStanding{Level: tt.level}).MayWork(); got != tt.want {
			t.Errorf("%q: MayWork = %v, want %v", tt.level, got, tt.want)
		}
	}
}

// TestDefaultFollowsTheSignIn holds the row of each sign-in kind, and what a kind nobody could read
// starts at, which is never more than a subscription's.
func TestDefaultFollowsTheSignIn(t *testing.T) {
	policy := authority.DefaultAutonomyPolicy()
	cases := []struct {
		name string
		kind string
		want authority.Level
	}{
		{"a subscription stays attended", ledger.SignInSubscription, authority.LevelAttended},
		{"an API key runs unattended", ledger.SignInAPIKey, authority.LevelUnattended},
		{"a kind nobody could read stays attended", "", authority.LevelAttended},
		{"a kind this build does not know stays attended", "some-other-kind", authority.LevelAttended},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := policy.LevelFor(tt.kind, authority.WorkerAutonomy{}); got != tt.want {
				t.Fatalf("level %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUnattendedNeedsTheCurrentAcknowledgement(t *testing.T) {
	policy := authority.DefaultAutonomyPolicy()
	current := authority.WorkerAutonomy{
		Level: authority.LevelUnattended, AcknowledgedBy: "member-1", AcknowledgedDigest: authority.RiskDigest(),
	}
	cases := []struct {
		name    string
		kind    string
		setting authority.WorkerAutonomy
		want    authority.Level
	}{
		{"no acknowledgement holds a subscription at attended", ledger.SignInSubscription, authority.WorkerAutonomy{Level: authority.LevelUnattended}, authority.LevelAttended},
		{"an acknowledgement lets it go", ledger.SignInSubscription, current, authority.LevelUnattended},
		{"one of an older statement does not", ledger.SignInSubscription, authority.WorkerAutonomy{
			Level: authority.LevelUnattended, AcknowledgedBy: "member-1", AcknowledgedDigest: "sha256:older",
		}, authority.LevelAttended},
		{"one nobody signed does not", ledger.SignInSubscription, authority.WorkerAutonomy{
			Level: authority.LevelUnattended, AcknowledgedDigest: authority.RiskDigest(),
		}, authority.LevelAttended},
		{"a kind nobody could read needs one too", "", authority.WorkerAutonomy{Level: authority.LevelUnattended}, authority.LevelAttended},
		{"an API key needs none", ledger.SignInAPIKey, authority.WorkerAutonomy{Level: authority.LevelUnattended}, authority.LevelUnattended},
		{"an acknowledgement chooses no level by itself", ledger.SignInSubscription, authority.WorkerAutonomy{
			AcknowledgedBy: "member-1", AcknowledgedDigest: authority.RiskDigest(),
		}, authority.LevelAttended},
		{"manual stays manual with one", ledger.SignInSubscription, authority.WorkerAutonomy{
			Level: authority.LevelManual, AcknowledgedBy: "member-1", AcknowledgedDigest: authority.RiskDigest(),
		}, authority.LevelManual},
		{"an API key can be held to manual", ledger.SignInAPIKey, authority.WorkerAutonomy{Level: authority.LevelManual}, authority.LevelManual},
		{"attended holds an API key too", ledger.SignInAPIKey, authority.WorkerAutonomy{Level: authority.LevelAttended}, authority.LevelAttended},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := policy.LevelFor(tt.kind, tt.setting); got != tt.want {
				t.Fatalf("level %q, want %q", got, tt.want)
			}
		})
	}
}

// A policy can name unattended for a subscription, and that still needs a member's word per worker:
// a default is not an acknowledgement.
func TestAPolicyDefaultIsNoAcknowledgement(t *testing.T) {
	policy := authority.DefaultAutonomyPolicy()
	policy.SubscriptionDefault = authority.LevelUnattended
	if got := policy.LevelFor(ledger.SignInSubscription, authority.WorkerAutonomy{}); got != authority.LevelAttended {
		t.Fatalf("level %q", got)
	}
}

func TestAPolicyMustBeOneAWorkspaceCanRunOn(t *testing.T) {
	good := authority.DefaultAutonomyPolicy()
	cases := []struct {
		name   string
		break_ func(*authority.AutonomyPolicy)
	}{
		{"no version", func(p *authority.AutonomyPolicy) { p.Version = 0 }},
		{"an unknown default for a subscription", func(p *authority.AutonomyPolicy) { p.SubscriptionDefault = "open" }},
		{"an unknown default for a key", func(p *authority.AutonomyPolicy) { p.APIKeyDefault = "" }},
		{"no bound on lanes", func(p *authority.AutonomyPolicy) { p.AttendedMaxLanes = 0 }},
		{"no bound on respawns", func(p *authority.AutonomyPolicy) { p.AttendedMaxRespawnsPerDay = 0 }},
	}
	if err := good.Check(); err != nil {
		t.Fatalf("the built-in policy: %v", err)
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			policy := good
			tt.break_(&policy)
			if err := policy.Check(); !errors.Is(err, authority.ErrBadLanePolicy) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestABuiltInPolicySaysWhatItsBodySays(t *testing.T) {
	version, err := authority.NewAutonomyVersion("id", "built-in", authority.AutonomyDraft{
		Body: authority.BuiltInAutonomyBody, Policy: authority.DefaultAutonomyPolicy(),
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if version.Kind != authority.PolicyAutonomy || version.Version != 1 {
		t.Fatalf("version %+v", version)
	}
	read, err := authority.ReadAutonomy(version)
	if err != nil {
		t.Fatal(err)
	}
	if read != authority.DefaultAutonomyPolicy() {
		t.Fatalf("read %+v", read)
	}
	version.Body += "\n# changed after a member read it\n"
	if _, err := authority.ReadAutonomy(version); !errors.Is(err, authority.ErrPolicyDigest) {
		t.Fatalf("err = %v", err)
	}
}

func TestAWindowStandsUntilItClosesOrIsClosed(t *testing.T) {
	window := authority.RunWindow{OpenedAt: 100, ClosesAt: 200}
	cases := []struct {
		name   string
		window authority.RunWindow
		now    int64
		want   bool
	}{
		{"inside", window, 150, true},
		{"at its first moment", window, 100, true},
		{"at its close", window, 200, false},
		{"past its close", window, 201, false},
		{"before it opened", window, 99, false},
		{"closed by a member", authority.RunWindow{OpenedAt: 100, ClosesAt: 200, ClosedAt: 120}, 150, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.window.Stands(tt.now); got != tt.want {
				t.Fatalf("Stands = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAWindowHasALength(t *testing.T) {
	cases := []struct {
		name string
		ask  int64
		want int64
	}{
		{"nothing asked takes the default", 0, authority.DefaultWindowMs},
		{"a length inside the bound stands", 30 * minuteMs, 30 * minuteMs},
		{"a length past the bound stops there", authority.MaxWindowMs + 1, authority.MaxWindowMs},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := authority.BoundWindow(tt.ask)
			if err != nil || got != tt.want {
				t.Fatalf("got %d, %v", got, err)
			}
		})
	}
	if _, err := authority.BoundWindow(-1); !errors.Is(err, authority.ErrBadWindow) {
		t.Fatalf("a negative length: %v", err)
	}
}

// The statement a member acknowledges names the policy pages, says what can happen, and promises
// nothing: an acknowledgement moves the risk to a person, and it removes none of it.
func TestTheRiskStatementLinksThePagesAndPromisesNothing(t *testing.T) {
	statement := authority.RiskStatement
	for _, want := range []string{
		"https://www.anthropic.com/legal/consumer-terms",
		"https://code.claude.com/docs/en/legal-and-compliance",
		"https://support.claude.com/en/articles/15036540",
		"without prior notice",
		"ordinary, individual usage",
		"automated or non-human means",
		"not permission",
	} {
		if !strings.Contains(statement, want) {
			t.Errorf("the statement leaves out %q", want)
		}
	}
	promises := regexp.MustCompile(`(?i)\b(is|are|stays?|remains?|be|kept?|keeps?)\s+(safe|allowed|permitted|compliant|approved|fine|ok)\b|` +
		`\b(safely|guarantee[sd]?|no risk|risk[- ]free|approved by|compliant with|complies with|endorse[sd]?)\b`)
	if found := promises.FindString(statement); found != "" {
		t.Errorf("the statement promises something: %q", found)
	}
	if authority.RiskDigest() != authority.PolicyDigest(statement) {
		t.Error("the digest covers other text")
	}
}

func TestReplayRebuildsAutonomy(t *testing.T) {
	window := authority.RunWindow{ID: "w1", WorkerID: "worker-1", OpenedBy: "member-1", OpenedAt: 10, ClosesAt: 20}
	closed := window
	closed.ClosedBy, closed.ClosedAt = "member-1", 15
	setting := authority.WorkerAutonomy{WorkerID: "worker-1", Level: authority.LevelUnattended}
	acknowledged := setting
	acknowledged.AcknowledgedBy, acknowledged.AcknowledgedDigest, acknowledged.AcknowledgedAt = "member-1", authority.RiskDigest(), 12
	var events []ledger.Event
	for _, draft := range []struct {
		kind string
		data any
	}{
		{authority.EventAutonomySet, setting}, {authority.EventRiskAcknowledged, acknowledged},
		{authority.EventWindowOpened, window}, {authority.EventWindowClosed, closed},
	} {
		event, err := authority.AutonomyEvent(draft.kind, "worker-1", draft.data, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, ledger.Event{Type: event.Type, Actor: event.Actor, Subject: event.Subject, Data: event.Data})
	}
	settings, windows, err := authority.ReplayAutonomy(events)
	if err != nil {
		t.Fatal(err)
	}
	if settings["worker-1"] != acknowledged {
		t.Errorf("settings %+v", settings)
	}
	if windows["w1"] != closed {
		t.Errorf("windows %+v", windows)
	}
}

// A record of this kind stands only on a member's event, so a forged line in the log rebuilds nothing.
func TestReplayKeepsOnlyAMembersWord(t *testing.T) {
	window := authority.RunWindow{ID: "w1", WorkerID: "worker-1", OpenedBy: "lane-1", OpenedAt: 10, ClosesAt: 20}
	event, err := authority.AutonomyEvent(authority.EventWindowOpened, "worker-1", window, ledger.Actor{Kind: ledger.ActorLane, ID: "lane-1"})
	if err != nil {
		t.Fatal(err)
	}
	_, windows, err := authority.ReplayAutonomy([]ledger.Event{{Type: event.Type, Actor: event.Actor, Subject: event.Subject, Data: event.Data}})
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 0 {
		t.Fatalf("windows %+v", windows)
	}
}
