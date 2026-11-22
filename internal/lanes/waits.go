package lanes

import (
	"encoding/json"
	"math"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
)

// WaitRulesVersion is the version of the wait rule set a generation's policy carries. The rules below are
// the set it names, and a change to what a rule matches is a new version.
const WaitRulesVersion = 1

// The wait rules (FR-SPEND-2). Each classifies what a command does, and none reads how long it runs: a build
// holds no model while it runs, and only a command that waits by construction is denied.
const (
	// WaitSleep is a `sleep` of the project's `max_tool_sleep` or longer.
	WaitSleep = "W1"
	// WaitLoop is a loop whose body runs `sleep`, a poll.
	WaitLoop = "W2"
	// WaitWatch is a command that holds the turn until something outside changes.
	WaitWatch = "W3"
	// WaitStatus is a read of a forge's CI status, which the control plane reads and a lane has no use for.
	WaitStatus = "W4"
)

var waitOrder = []string{WaitSleep, WaitLoop, WaitWatch, WaitStatus}

// KnownWaitRule says an ID names a wait rule of this build.
func KnownWaitRule(id string) bool { return slices.Contains(waitOrder, id) }

// The reasons reach the model, so each names the rule and says what to do. None holds the command.
var waitReasons = map[string]string{
	WaitSleep: "ropeshift policy: W1: do not sleep inside a command. A lane never waits on a timer in a tool call: run the check once, " +
		"or call " + ToolLaneWait + " with the minutes, and the lane parks when the wait is long.",
	WaitLoop: "ropeshift policy: W2: do not poll in a loop. Run the command once. When the work is pushed, call " + ToolLaneReport +
		": ropeshift waits on CI and on a review, and starts the next step.",
	WaitWatch:  "ropeshift policy: W3: do not watch CI. Call " + ToolLaneReport + "; ropeshift waits on the checks.",
	WaitStatus: "ropeshift policy: W4: do not read CI status. ropeshift reads the checks and starts the next step. Call " + ToolLaneReport + " when the work is pushed.",
}

// WaitRules is the wait rule set a generation's hook reads from its policy file. The hook needs no worker to
// apply it, and a member's project policy is the only place a rule is turned off or given more patterns.
type WaitRules struct {
	Version int `json:"version"`
	// MaxToolSleepMs is the sleep W1 denies, and any longer one.
	MaxToolSleepMs int64 `json:"max_tool_sleep_ms"`
	// Off names the rules a member turned off.
	Off []string `json:"off,omitempty"`
	// Patterns are command prefixes a rule also matches, by rule ID, each as words.
	Patterns map[string][]string `json:"patterns,omitempty"`
}

// defaultMaxToolSleep and defaultMaxLiveWait are the values the design states.
const (
	defaultMaxToolSleep = time.Minute
	defaultMaxLiveWait  = 5 * time.Minute
)

// DefaultWaitRules is the rule set of a project that publishes no spend policy.
func DefaultWaitRules() WaitRules {
	return WaitRules{Version: WaitRulesVersion, MaxToolSleepMs: defaultMaxToolSleep.Milliseconds()}
}

// WaitRulesFrom reads the rule set out of the spend policy a lease carries, and the defaults out of none.
func WaitRulesFrom(spend *ledger.DesiredSpend) WaitRules {
	rules := DefaultWaitRules()
	if spend == nil {
		return rules
	}
	if spend.MaxToolSleepMs > 0 {
		rules.MaxToolSleepMs = spend.MaxToolSleepMs
	}
	rules.Off = slices.Clone(spend.WaitRulesOff)
	if len(spend.WaitRulePatterns) != 0 {
		rules.Patterns = make(map[string][]string, len(spend.WaitRulePatterns))
		for rule, patterns := range spend.WaitRulePatterns {
			rules.Patterns[rule] = slices.Clone(patterns)
		}
	}
	return rules
}

// MaxLiveWait is the longest a lane waits live under the spend policy a lease carries.
func MaxLiveWait(spend *ledger.DesiredSpend) time.Duration {
	if spend == nil || spend.MaxLiveWaitMs <= 0 {
		return defaultMaxLiveWait
	}
	return time.Duration(spend.MaxLiveWaitMs) * time.Millisecond
}

// ParksOnWait says a `lane_wait` of this many minutes is longer than the lane may wait live (FR-SPEND-1).
func ParksOnWait(minutes int64, bound time.Duration) bool {
	return time.Duration(minutes)*time.Minute > bound
}

// ParkWait is a lane that declared a wait longer than it may hold a session for.
const ParkWait ParkTrigger = "lane_wait"

// WaitWake is what a lane waits on after a long `lane_wait`: the end of the wait, or a person who lets it go on.
func WaitWake(atMs int64) string {
	raw, err := json.Marshal(map[string]any{"any_of": []any{
		map[string]int64{wakeAtTerm: atMs},
		map[parking.Kind]bool{parking.HumanResume: true},
	}})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// WaitDenial is a command a wait rule refuses, and what the model is told.
type WaitDenial struct {
	Rule   string
	Reason string
}

func (w WaitRules) off(rule string) bool { return slices.Contains(w.Off, rule) }

// clone copies a rule set, so a compiled policy shares nothing with the settings it came from.
func (w WaitRules) clone() WaitRules {
	w.Off = slices.Clone(w.Off)
	if w.Patterns != nil {
		patterns := make(map[string][]string, len(w.Patterns))
		for rule, list := range w.Patterns {
			patterns[rule] = slices.Clone(list)
		}
		w.Patterns = patterns
	}
	return w
}

// Check reads a shell command, with every command inside it, and denies it under the first rule that is on and
// that it matches. A command that matches two rules is denied under the earlier, so turning one off never
// hides the other.
func (w WaitRules) Check(command string) (WaitDenial, bool) {
	found := map[string]bool{}
	w.scan(command, false, 0, found)
	for _, rule := range waitOrder {
		if found[rule] && !w.off(rule) {
			return WaitDenial{Rule: rule, Reason: waitReasons[rule]}, true
		}
	}
	return WaitDenial{}, false
}

// WaitDenial reads a call's wait rules, which only a shell command holds.
func (p DenyPolicy) WaitDenial(call ToolCall) (WaitDenial, bool) {
	if p.Waits == nil || call.Tool != ToolBash {
		return WaitDenial{}, false
	}
	return p.Waits.Check(call.Command)
}

// DecideRule is Decide, and the ID of the wait rule when that is what denied the call.
func (p DenyPolicy) DecideRule(call ToolCall) (reason, rule string, denied bool) {
	reason, denied = p.Decide(call)
	if !denied {
		return "", "", false
	}
	if wait, ok := p.WaitDenial(call); ok && wait.Reason == reason {
		rule = wait.Rule
	}
	return reason, rule, true
}

// scan walks the commands of one text. A loop opened here holds its body until its `done`, and a text inside a
// command, as a substitution or `sh -c` holds, is read inside the loop its command stands in.
func (w WaitRules) scan(text string, inLoop bool, depth int, found map[string]bool) {
	if depth > maxShellDepth {
		return
	}
	commands, nested := parseShell(text)
	for _, inner := range nested {
		w.scan(inner, inLoop, depth+1, found)
	}
	loops := 0
	for _, command := range commands {
		if len(command.words) != 0 {
			switch command.words[0].text {
			case "while", "until", "for":
				loops++
			case "done":
				loops = max(loops-1, 0)
				continue
			}
		}
		for _, inner := range command.processes {
			w.scan(inner, inLoop || loops > 0, depth+1, found)
		}
		words := stripLeaders(command.words)
		words = afterOperand(command.words, words)
		if len(words) == 0 {
			continue
		}
		w.read(words, inLoop || loops > 0, depth, found)
	}
}

// afterOperand skips the duration a wrapper takes before its command, as `timeout 600 sleep 300` has.
func afterOperand(all, words []shellWord) []shellWord {
	leaders := all[:len(all)-len(words)]
	if len(leaders) == 0 || len(words) < 2 {
		return words
	}
	switch path.Base(leaders[len(leaders)-1].text) {
	case "timeout", "nice":
		if operand.MatchString(words[0].text) {
			return stripLeaders(words[1:])
		}
	}
	return words
}

var operand = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[smhd]?$`)

// read classifies one simple command.
func (w WaitRules) read(words []shellWord, inLoop bool, depth int, found map[string]bool) {
	w.matchPatterns(words, found)
	name := path.Base(words[0].text)
	switch {
	case name == "eval":
		w.scan(joinWords(words[1:]), inLoop, depth+1, found)
	case slices.Contains(shells, name):
		if inner, ok := shellArgument(words[1:]); ok {
			w.scan(inner, inLoop, depth+1, found)
		}
	case name == "sleep":
		if inLoop {
			found[WaitLoop] = true
		}
		if sleeps(words[1:]) >= time.Duration(w.MaxToolSleepMs)*time.Millisecond {
			found[WaitSleep] = true
		}
	case name == "watch":
		found[WaitWatch] = true
	case name == "gh":
		readGH(words[1:], found)
	}
}

// matchPatterns adds the rules whose patterns a command starts with, word for word.
func (w WaitRules) matchPatterns(words []shellWord, found map[string]bool) {
	for rule, patterns := range w.Patterns {
		for _, pattern := range patterns {
			prefix := strings.Fields(pattern)
			if len(prefix) == 0 || len(prefix) > len(words) {
				continue
			}
			if slices.EqualFunc(prefix, words[:len(prefix)], func(want string, got shellWord) bool { return want == got.text }) {
				found[rule] = true
			}
		}
	}
}

var duration = regexp.MustCompile(`^([0-9]*\.?[0-9]+)([smhd]?)$`)

// sleeps adds up the operands of `sleep` as GNU sleep does. An operand the hook cannot read, because it holds
// an expansion or is no time, reads as a sleep without end, because a hook fails closed.
func sleeps(args []shellWord) time.Duration {
	var total float64
	for _, arg := range args {
		if strings.HasPrefix(arg.text, "-") && !arg.dynamic {
			continue
		}
		text := strings.ToLower(arg.text)
		if arg.dynamic || arg.glob || text == "infinity" || text == "inf" {
			return math.MaxInt64
		}
		match := duration.FindStringSubmatch(text)
		if match == nil {
			return math.MaxInt64
		}
		n, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			return math.MaxInt64
		}
		total += n * unitSeconds[match[2]]
	}
	if total >= float64(math.MaxInt64/int64(time.Second)) {
		return math.MaxInt64
	}
	return time.Duration(total * float64(time.Second))
}

var unitSeconds = map[string]float64{"": 1, "s": 1, "m": 60, "h": 3600, "d": 86400}

// readGH classifies a `gh` command: a watch is W3, and a read of a run, a pull request, or a check path is W4.
func readGH(args []shellWord, found map[string]bool) {
	var names []string
	for _, arg := range args {
		if !strings.HasPrefix(arg.text, "-") {
			names = append(names, arg.text)
		}
		if len(names) == 2 {
			break
		}
	}
	if len(names) < 2 {
		return
	}
	switch group, sub := names[0], names[1]; {
	case group == "run" && sub == "watch":
		found[WaitWatch] = true
	case group == "run" && (sub == "view" || sub == "list"):
		found[WaitStatus] = true
	case group == "pr" && sub == "view":
		found[WaitStatus] = true
	case group == "pr" && sub == "checks":
		found[WaitStatus] = true
		for _, arg := range args {
			if arg.text == "-w" || arg.text == "--watch" || strings.HasPrefix(arg.text, "--watch=") {
				found[WaitWatch] = true
			}
		}
	}
	if names[0] == "api" {
		for _, arg := range args {
			if !strings.HasPrefix(arg.text, "-") && statusPath(arg.text) {
				found[WaitStatus] = true
			}
		}
	}
}

// statusPath says an API path reads checks, a status, or a workflow run.
func statusPath(target string) bool {
	target, _, _ = strings.Cut(target, "?")
	segments := strings.Split(strings.Trim(target, "/"), "/")
	for i, segment := range segments {
		switch segment {
		case "check-runs", "check-suites", "status", "statuses":
			return true
		case "actions":
			if i+1 < len(segments) && segments[i+1] == "runs" {
				return true
			}
		}
	}
	return false
}
