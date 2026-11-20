package authority

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
)

// PolicySpend holds the values that stop agent sessions from spending on waste (ADR-0037). Every value is a
// named key with a default, and only a member publishes a version, so no plan, repository file, or agent can
// move one. With no version published, the built-in one is in force.
const PolicySpend PolicyKind = "spend"

// ErrBadSpendPolicy marks a spend policy a project cannot run on.
var ErrBadSpendPolicy = errors.New("invalid spend policy")

// The wait rules a spend policy names. A rule is a class of command that waits by construction.
var waitRuleIDs = []string{"W1", "W2", "W3", "W4"}

// SessionClasses are the capability classes a session kind can run on, as the agent roster names them.
var SessionClasses = []string{"small", "standard", "large", "frontier"}

// The quantiles of an estimate band a soft limit can read.
var softQuantiles = []string{"p50", "p85"}

// The ranges of the keys. Within one, every value is the member's: a range only keeps a wait, a limit, a repeat,
// and an alert bounded, because an unbounded one is the waste the policy exists to stop.
const (
	minLiveWait         = time.Minute
	maxLiveWait         = time.Hour
	minToolSleep        = time.Second
	maxToolSleep        = time.Hour
	maxPatternsPerRule  = 64
	maxPatternBytes     = 200
	defaultMaxLiveWait  = "5m"
	defaultMaxToolSleep = "60s"
	defaultAlertWindow  = "30m"

	minMultiplier, maxMultiplier          = 1.0, 10.0
	minOutcomes, maxOutcomes              = 1, 100
	maxAmount                             = 1_000_000.0
	minRepeatLimit, maxRepeatLimit        = 2, 20
	minRepeatDenials, maxRepeatDenials    = 1, 10
	minAlertWindow, maxAlertWindow        = time.Minute, 24 * time.Hour
	minAlertCalls, maxAlertCalls          = 1, 1000
	defaultSoftQuantile, defaultClassName = "p85", "standard"
	// defaultConsultationLimit is a modest cap on one consultation, a share of what a small work order may spend.
	defaultConsultationLimit = 2.0
)

// SpendSizeLimits is an amount for each work order size.
type SpendSizeLimits struct {
	Small  float64 `toml:"small" json:"small"`
	Medium float64 `toml:"medium" json:"medium"`
	Large  float64 `toml:"large" json:"large"`
}

// SpendPolicy is the body of a spend policy version. Durations stay text, so a domain value carries no parsing
// rule, and a key a later build adds is refused here as an unknown setting. Amounts are US dollars at list
// price, and 0 means no limit or no warning.
type SpendPolicy struct {
	Version int64 `toml:"version" json:"version"`
	// MaxLiveWait is how long a lane may wait live. A longer `lane_wait` parks the lane.
	MaxLiveWait string `toml:"max_live_wait" json:"max_live_wait"`
	// MaxToolSleep is the sleep inside one command that wait rule W1 denies, and any longer one.
	MaxToolSleep string `toml:"max_tool_sleep" json:"max_tool_sleep"`
	// WaitRulesOff names the rules a member turned off, by ID.
	WaitRulesOff []string `toml:"wait_rules_off" json:"wait_rules_off"`
	// WaitRulePatterns are command prefixes a rule also matches, by rule ID.
	WaitRulePatterns map[string][]string `toml:"wait_rule_patterns" json:"wait_rule_patterns"`

	// SoftQuantile is the quantile of a cost band where a work order's soft limit sits, p50 or p85.
	SoftQuantile string `toml:"soft_quantile" json:"soft_quantile"`
	// HardMultiplier times the band's p85 is a work order's hard limit.
	HardMultiplier float64 `toml:"hard_multiplier" json:"hard_multiplier"`
	// LowConfidenceOutcomes is how many outcomes a band needs to be trusted; under it the size's limit applies.
	LowConfidenceOutcomes int64 `toml:"low_confidence_outcomes" json:"low_confidence_outcomes"`
	// LowConfidenceSoftShare is the soft limit as a share of the hard limit when the band is not trusted.
	LowConfidenceSoftShare float64 `toml:"low_confidence_soft_share" json:"low_confidence_soft_share"`
	// NoHistoryLimit is the hard limit of a work order with no trusted band, by size.
	NoHistoryLimit SpendSizeLimits `toml:"no_history_limit" json:"no_history_limit"`
	// ReviewShare is a review's hard limit as a share of the reviewed work order's hard limit.
	ReviewShare float64 `toml:"review_share" json:"review_share"`
	// ReviewSoftShare is a review's soft limit as a share of its hard limit.
	ReviewSoftShare float64 `toml:"review_soft_share" json:"review_soft_share"`
	// PlanHardMultiplier times the forecast's p85 is a plan's hard limit.
	PlanHardMultiplier float64 `toml:"plan_hard_multiplier" json:"plan_hard_multiplier"`
	// ProjectDailyLimit and ProjectWeeklyLimit are the project's ceilings.
	ProjectDailyLimit  float64 `toml:"project_daily_limit" json:"project_daily_limit"`
	ProjectWeeklyLimit float64 `toml:"project_weekly_limit" json:"project_weekly_limit"`
	// ProjectWarnShare is where a project's soft notice sits, as a share of each limit.
	ProjectWarnShare float64 `toml:"project_warn_share" json:"project_warn_share"`
	// AssistantDailyWarn is the assistant's soft daily limit per project.
	AssistantDailyWarn float64 `toml:"assistant_daily_warn" json:"assistant_daily_warn"`
	// ConsultationLimit is the most one consultation an orchestrator starts may spend (FR-ORCH-9), within what the
	// project's daily and weekly limits leave. 0 turns consultations off, because a consultation never runs uncapped.
	ConsultationLimit float64 `toml:"consultation_limit" json:"consultation_limit"`
	// BudgetFlagHeadroomShare is the headroom over a hard limit that a headless generation's budget flag adds.
	BudgetFlagHeadroomShare float64 `toml:"budget_flag_headroom_share" json:"budget_flag_headroom_share"`

	// RepeatLimit is how many identical calls a lane may make since its last progress before the next is denied.
	RepeatLimit int64 `toml:"repeat_limit" json:"repeat_limit"`
	// RepeatDenialsToPark is how many denials park the lane.
	RepeatDenialsToPark int64 `toml:"repeat_denials_to_park" json:"repeat_denials_to_park"`
	// NoProgressShare is the share of the baselined p50 spent since progress that raises one message.
	NoProgressShare float64 `toml:"no_progress_share" json:"no_progress_share"`
	// NoProgressParkMultiplier times that share is where the lane parks.
	NoProgressParkMultiplier float64 `toml:"no_progress_park_multiplier" json:"no_progress_park_multiplier"`
	// NoHistoryNoProgress is the spend since progress that raises the message when there is no baselined p50.
	NoHistoryNoProgress float64 `toml:"no_history_no_progress" json:"no_history_no_progress"`

	// WaitingAlertShare is the share of recent spend that waiting may reach before a notice.
	WaitingAlertShare float64 `toml:"waiting_alert_share" json:"waiting_alert_share"`
	// WaitingAlertWindow is the span of that share, and WaitingAlertMinCalls the calls it needs.
	WaitingAlertWindow   string `toml:"waiting_alert_window" json:"waiting_alert_window"`
	WaitingAlertMinCalls int64  `toml:"waiting_alert_min_calls" json:"waiting_alert_min_calls"`

	// AssistantClass is the capability class the project's assistant runs on.
	AssistantClass string `toml:"assistant_class" json:"assistant_class"`
}

// SpendPolicyFile is the table a member publishes.
type SpendPolicyFile struct {
	SpendPolicy SpendPolicy `toml:"spend_policy"`
}

// SpendPolicyDraft is a spend policy as written, and what it says.
type SpendPolicyDraft struct {
	Body   string
	Policy SpendPolicy
}

// SpendPolicyKeys lists every settable key in the order a surface shows them. A key of a table or a list is
// named by its part, such as no_history_limit.medium.
var SpendPolicyKeys = []string{
	"max_live_wait", "max_tool_sleep", "wait_rules_off",
	"wait_rule_patterns.W1", "wait_rule_patterns.W2", "wait_rule_patterns.W3", "wait_rule_patterns.W4",
	"soft_quantile", "hard_multiplier", "low_confidence_outcomes", "low_confidence_soft_share",
	"no_history_limit.small", "no_history_limit.medium", "no_history_limit.large",
	"review_share", "review_soft_share", "plan_hard_multiplier", "project_daily_limit", "project_weekly_limit",
	"project_warn_share", "assistant_daily_warn", "consultation_limit", "budget_flag_headroom_share",
	"repeat_limit", "repeat_denials_to_park", "no_progress_share", "no_progress_park_multiplier", "no_history_no_progress",
	"waiting_alert_share", "waiting_alert_window", "waiting_alert_min_calls", "assistant_class",
}

// SpendPolicyFileKeys are the keys a body must state, as the file spells them. A version states every one, so it
// always says what it runs on.
var SpendPolicyFileKeys = []string{
	"version", "max_live_wait", "max_tool_sleep", "wait_rules_off", "wait_rule_patterns",
	"soft_quantile", "hard_multiplier", "low_confidence_outcomes", "low_confidence_soft_share", "no_history_limit",
	"review_share", "review_soft_share", "plan_hard_multiplier", "project_daily_limit", "project_weekly_limit",
	"project_warn_share", "assistant_daily_warn", "consultation_limit", "budget_flag_headroom_share",
	"repeat_limit", "repeat_denials_to_park", "no_progress_share", "no_progress_park_multiplier", "no_history_no_progress",
	"waiting_alert_share", "waiting_alert_window", "waiting_alert_min_calls", "assistant_class",
}

// BuiltInSpendPolicyBody is what a project runs on until a member publishes a version.
var BuiltInSpendPolicyBody = DefaultSpendPolicy().Body()

// DefaultSpendPolicy is the version the design states, with every key at its default.
func DefaultSpendPolicy() SpendPolicy {
	return SpendPolicy{
		Version: 1, MaxLiveWait: defaultMaxLiveWait, MaxToolSleep: defaultMaxToolSleep, WaitRulesOff: []string{},
		WaitRulePatterns: map[string][]string{"W1": {}, "W2": {}, "W3": {}, "W4": {}},
		SoftQuantile:     defaultSoftQuantile, HardMultiplier: 2, LowConfidenceOutcomes: 6, LowConfidenceSoftShare: 0.5,
		NoHistoryLimit: SpendSizeLimits{Small: 10, Medium: 25, Large: 60},
		ReviewShare:    0.5, ReviewSoftShare: 0.5, PlanHardMultiplier: 2, ProjectWarnShare: 0.8, BudgetFlagHeadroomShare: 0.1,
		RepeatLimit: 3, RepeatDenialsToPark: 2, NoProgressShare: 0.5, NoProgressParkMultiplier: 2, NoHistoryNoProgress: 5,
		WaitingAlertShare: 0.5, WaitingAlertWindow: defaultAlertWindow, WaitingAlertMinCalls: 5, AssistantClass: defaultClassName,
		ConsultationLimit: defaultConsultationLimit,
	}
}

// durationOr reads a duration that passed Check. One that does not parse reads as the key's default, never as
// zero, because a zero wait or window would turn a stored defect into a limit nobody chose. ReadSpendPolicy
// refuses such a version before a caller sees it, so this is the last guard and not the usual path.
func durationOr(text, fallback string) time.Duration {
	d, err := time.ParseDuration(text)
	if err != nil {
		d, _ = time.ParseDuration(fallback)
	}
	return d
}

// LiveWait is the longest a lane waits live.
func (p SpendPolicy) LiveWait() time.Duration { return durationOr(p.MaxLiveWait, defaultMaxLiveWait) }

// ToolSleep is the sleep a command may hold before wait rule W1 denies it.
func (p SpendPolicy) ToolSleep() time.Duration {
	return durationOr(p.MaxToolSleep, defaultMaxToolSleep)
}

// AlertWindow is the span the waiting alert reads.
func (p SpendPolicy) AlertWindow() time.Duration {
	return durationOr(p.WaitingAlertWindow, defaultAlertWindow)
}

func spendRefusal(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrBadSpendPolicy}, args...)...)
}

func checkSpendDuration(key, text string, low, high time.Duration) error {
	d, err := time.ParseDuration(text)
	if err != nil {
		return spendRefusal("%s %q is not a duration, such as \"5m\"", key, text)
	}
	if d < low || d > high {
		return spendRefusal("%s is %s, and it runs from %s to %s", key, text, low, high)
	}
	return nil
}

// checkSpendFloat refuses a number outside its range, and one that is no number at all.
func checkSpendFloat(key string, value, low, high float64) error {
	if math.IsNaN(value) || value < low || value > high {
		return spendRefusal("%s is %g, and it runs from %g to %g", key, value, low, high)
	}
	return nil
}

func checkSpendInt(key string, value, low, high int64) error {
	if value < low || value > high {
		return spendRefusal("%s is %d, and it runs from %d to %d", key, value, low, high)
	}
	return nil
}

func checkSpendChoice(key, value string, choices []string) error {
	if !slices.Contains(choices, value) {
		return spendRefusal("%s is %q, and it is one of %s", key, value, strings.Join(choices, ", "))
	}
	return nil
}

// Check accepts a policy a project can run on. A value outside its key's range is refused, and the refusal
// names the key and the range.
func (p SpendPolicy) Check() error {
	if p.Version < 1 {
		return spendRefusal("version %d", p.Version)
	}
	if err := checkSpendDuration("max_live_wait", p.MaxLiveWait, minLiveWait, maxLiveWait); err != nil {
		return err
	}
	if err := checkSpendDuration("max_tool_sleep", p.MaxToolSleep, minToolSleep, maxToolSleep); err != nil {
		return err
	}
	for _, rule := range p.WaitRulesOff {
		if !slices.Contains(waitRuleIDs, rule) {
			return spendRefusal("wait_rules_off names %q, and the rules are %s", rule, strings.Join(waitRuleIDs, ", "))
		}
	}
	for rule, patterns := range p.WaitRulePatterns {
		if !slices.Contains(waitRuleIDs, rule) {
			return spendRefusal("wait_rule_patterns names %q, and the rules are %s", rule, strings.Join(waitRuleIDs, ", "))
		}
		if len(patterns) > maxPatternsPerRule {
			return spendRefusal("wait_rule_patterns holds %d patterns for %s, and a rule holds at most %d", len(patterns), rule, maxPatternsPerRule)
		}
		for _, pattern := range patterns {
			if strings.TrimSpace(pattern) == "" || len(pattern) > maxPatternBytes {
				return spendRefusal("wait_rule_patterns holds a pattern for %s that is empty or past %d bytes", rule, maxPatternBytes)
			}
		}
	}
	return p.checkLimits()
}

func (p SpendPolicy) checkLimits() error {
	if err := checkSpendChoice("soft_quantile", p.SoftQuantile, softQuantiles); err != nil {
		return err
	}
	multipliers := []struct {
		key   string
		value float64
	}{{"hard_multiplier", p.HardMultiplier}, {"plan_hard_multiplier", p.PlanHardMultiplier}, {"no_progress_park_multiplier", p.NoProgressParkMultiplier}}
	for _, m := range multipliers {
		if err := checkSpendFloat(m.key, m.value, minMultiplier, maxMultiplier); err != nil {
			return err
		}
	}
	if err := checkSpendInt("low_confidence_outcomes", p.LowConfidenceOutcomes, minOutcomes, maxOutcomes); err != nil {
		return err
	}
	shares := []struct {
		key   string
		value float64
	}{
		{"low_confidence_soft_share", p.LowConfidenceSoftShare}, {"review_share", p.ReviewShare}, {"review_soft_share", p.ReviewSoftShare},
		{"project_warn_share", p.ProjectWarnShare}, {"budget_flag_headroom_share", p.BudgetFlagHeadroomShare},
		{"no_progress_share", p.NoProgressShare}, {"waiting_alert_share", p.WaitingAlertShare},
	}
	for _, s := range shares {
		if err := checkSpendFloat(s.key, s.value, 0, 1); err != nil {
			return err
		}
	}
	amounts := []struct {
		key   string
		value float64
	}{
		{"no_history_limit.small", p.NoHistoryLimit.Small}, {"no_history_limit.medium", p.NoHistoryLimit.Medium},
		{"no_history_limit.large", p.NoHistoryLimit.Large}, {"project_daily_limit", p.ProjectDailyLimit},
		{"project_weekly_limit", p.ProjectWeeklyLimit}, {"assistant_daily_warn", p.AssistantDailyWarn},
		{"consultation_limit", p.ConsultationLimit}, {"no_history_no_progress", p.NoHistoryNoProgress},
	}
	for _, a := range amounts {
		if err := checkSpendFloat(a.key, a.value, 0, maxAmount); err != nil {
			return err
		}
	}
	if err := checkSpendInt("repeat_limit", p.RepeatLimit, minRepeatLimit, maxRepeatLimit); err != nil {
		return err
	}
	if err := checkSpendInt("repeat_denials_to_park", p.RepeatDenialsToPark, minRepeatDenials, maxRepeatDenials); err != nil {
		return err
	}
	if err := checkSpendDuration("waiting_alert_window", p.WaitingAlertWindow, minAlertWindow, maxAlertWindow); err != nil {
		return err
	}
	if err := checkSpendInt("waiting_alert_min_calls", p.WaitingAlertMinCalls, minAlertCalls, maxAlertCalls); err != nil {
		return err
	}
	return checkSpendChoice("assistant_class", p.AssistantClass, SessionClasses)
}

func (p SpendPolicy) clone() SpendPolicy {
	out := p
	out.WaitRulesOff = slices.Clone(p.WaitRulesOff)
	out.WaitRulePatterns = make(map[string][]string, len(p.WaitRulePatterns))
	for rule, patterns := range p.WaitRulePatterns {
		out.WaitRulePatterns[rule] = slices.Clone(patterns)
	}
	return out
}

// splitList reads a comma-separated list, dropping blanks.
func splitList(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseSpendFloat(key, value string) (float64, error) {
	n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, spendRefusal("%s %q is not a number", key, value)
	}
	return n, nil
}

func parseSpendInt(key, value string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, spendRefusal("%s %q is not a whole number", key, value)
	}
	return n, nil
}

// Set returns the policy with one key changed to a value, given as text, and every other key as it was. The
// result must check, so a value outside the key's range is refused naming the key and the range. A key nobody
// offers is refused. The version is the caller's to move.
func (p SpendPolicy) Set(key, value string) (SpendPolicy, error) {
	out := p.clone()
	value = strings.TrimSpace(value)
	floats := map[string]*float64{
		"hard_multiplier": &out.HardMultiplier, "low_confidence_soft_share": &out.LowConfidenceSoftShare,
		"no_history_limit.small": &out.NoHistoryLimit.Small, "no_history_limit.medium": &out.NoHistoryLimit.Medium,
		"no_history_limit.large": &out.NoHistoryLimit.Large, "review_share": &out.ReviewShare, "review_soft_share": &out.ReviewSoftShare,
		"plan_hard_multiplier": &out.PlanHardMultiplier, "project_daily_limit": &out.ProjectDailyLimit,
		"project_weekly_limit": &out.ProjectWeeklyLimit, "project_warn_share": &out.ProjectWarnShare,
		"assistant_daily_warn": &out.AssistantDailyWarn, "budget_flag_headroom_share": &out.BudgetFlagHeadroomShare,
		"no_progress_share": &out.NoProgressShare, "no_progress_park_multiplier": &out.NoProgressParkMultiplier,
		"no_history_no_progress": &out.NoHistoryNoProgress, "waiting_alert_share": &out.WaitingAlertShare,
		"consultation_limit": &out.ConsultationLimit,
	}
	ints := map[string]*int64{
		"low_confidence_outcomes": &out.LowConfidenceOutcomes, "repeat_limit": &out.RepeatLimit,
		"repeat_denials_to_park": &out.RepeatDenialsToPark, "waiting_alert_min_calls": &out.WaitingAlertMinCalls,
	}
	texts := map[string]*string{
		"max_live_wait": &out.MaxLiveWait, "max_tool_sleep": &out.MaxToolSleep, "soft_quantile": &out.SoftQuantile,
		"waiting_alert_window": &out.WaitingAlertWindow, "assistant_class": &out.AssistantClass,
	}
	switch {
	case floats[key] != nil:
		n, err := parseSpendFloat(key, value)
		if err != nil {
			return p, err
		}
		*floats[key] = n
	case ints[key] != nil:
		n, err := parseSpendInt(key, value)
		if err != nil {
			return p, err
		}
		*ints[key] = n
	case texts[key] != nil:
		*texts[key] = value
	case key == "wait_rules_off":
		out.WaitRulesOff = splitList(value)
	case strings.HasPrefix(key, "wait_rule_patterns."):
		rule := strings.TrimPrefix(key, "wait_rule_patterns.")
		if !slices.Contains(waitRuleIDs, rule) {
			return p, spendRefusal("wait_rule_patterns names %q, and the rules are %s", rule, strings.Join(waitRuleIDs, ", "))
		}
		out.WaitRulePatterns[rule] = splitList(value)
	case key == "wait_rule_patterns", key == "no_history_limit":
		return p, spendRefusal("%s is set by its parts, such as %s", key, map[string]string{
			"wait_rule_patterns": "wait_rule_patterns.W3", "no_history_limit": "no_history_limit.medium"}[key])
	default:
		return p, spendRefusal("%s is no spend key", key)
	}
	if err := out.Check(); err != nil {
		return p, err
	}
	return out, nil
}

// ChangesTo names the keys a later version changed, with each value before and after, in the order the keys are listed. A
// notice that says a setting changed names these and the member who changed them (FR-SPEND-19).
func (p SpendPolicy) ChangesTo(next SpendPolicy) []ledger.SpendKeyChange {
	return keyChanges(SpendPolicyKeys, p.Text, next.Text)
}

// keyChanges lists the keys whose text differs between two readings of a policy.
func keyChanges(keys []string, before, after func(string) string) []ledger.SpendKeyChange {
	var out []ledger.SpendKeyChange
	for _, key := range keys {
		if was, now := before(key), after(key); was != now {
			out = append(out, ledger.SpendKeyChange{Key: key, Before: was, After: now})
		}
	}
	return out
}

// Text is the value of a key as Set reads it, and empty for a key nobody offers.
func (p SpendPolicy) Text(key string) string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	i := func(v int64) string { return strconv.FormatInt(v, 10) }
	switch key {
	case "max_live_wait":
		return p.MaxLiveWait
	case "max_tool_sleep":
		return p.MaxToolSleep
	case "wait_rules_off":
		return strings.Join(p.WaitRulesOff, ",")
	case "soft_quantile":
		return p.SoftQuantile
	case "hard_multiplier":
		return f(p.HardMultiplier)
	case "low_confidence_outcomes":
		return i(p.LowConfidenceOutcomes)
	case "low_confidence_soft_share":
		return f(p.LowConfidenceSoftShare)
	case "no_history_limit.small":
		return f(p.NoHistoryLimit.Small)
	case "no_history_limit.medium":
		return f(p.NoHistoryLimit.Medium)
	case "no_history_limit.large":
		return f(p.NoHistoryLimit.Large)
	case "review_share":
		return f(p.ReviewShare)
	case "review_soft_share":
		return f(p.ReviewSoftShare)
	case "plan_hard_multiplier":
		return f(p.PlanHardMultiplier)
	case "project_daily_limit":
		return f(p.ProjectDailyLimit)
	case "project_weekly_limit":
		return f(p.ProjectWeeklyLimit)
	case "project_warn_share":
		return f(p.ProjectWarnShare)
	case "assistant_daily_warn":
		return f(p.AssistantDailyWarn)
	case "consultation_limit":
		return f(p.ConsultationLimit)
	case "budget_flag_headroom_share":
		return f(p.BudgetFlagHeadroomShare)
	case "repeat_limit":
		return i(p.RepeatLimit)
	case "repeat_denials_to_park":
		return i(p.RepeatDenialsToPark)
	case "no_progress_share":
		return f(p.NoProgressShare)
	case "no_progress_park_multiplier":
		return f(p.NoProgressParkMultiplier)
	case "no_history_no_progress":
		return f(p.NoHistoryNoProgress)
	case "waiting_alert_share":
		return f(p.WaitingAlertShare)
	case "waiting_alert_window":
		return p.WaitingAlertWindow
	case "waiting_alert_min_calls":
		return i(p.WaitingAlertMinCalls)
	case "assistant_class":
		return p.AssistantClass
	}
	if rule, ok := strings.CutPrefix(key, "wait_rule_patterns."); ok {
		return strings.Join(p.WaitRulePatterns[rule], ",")
	}
	return ""
}

// tomlFloat writes a number so TOML reads it as a float.
func tomlFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

func tomlList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, strconv.Quote(item))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// Body is the policy as a member would write it: a `[spend_policy]` table that states every key. A version made
// by changing a key states the rest as they were.
func (p SpendPolicy) Body() string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("[spend_policy]")
	line("version = %d", p.Version)
	line("# Waits")
	line("max_live_wait = %q", p.MaxLiveWait)
	line("max_tool_sleep = %q", p.MaxToolSleep)
	line("wait_rules_off = %s", tomlList(p.WaitRulesOff))
	parts := make([]string, 0, len(waitRuleIDs))
	for _, rule := range waitRuleIDs {
		parts = append(parts, rule+" = "+tomlList(p.WaitRulePatterns[rule]))
	}
	line("wait_rule_patterns = { %s }", strings.Join(parts, ", "))
	line("# Limits")
	line("soft_quantile = %q", p.SoftQuantile)
	line("hard_multiplier = %s", tomlFloat(p.HardMultiplier))
	line("low_confidence_outcomes = %d", p.LowConfidenceOutcomes)
	line("low_confidence_soft_share = %s", tomlFloat(p.LowConfidenceSoftShare))
	line("no_history_limit = { small = %s, medium = %s, large = %s }",
		tomlFloat(p.NoHistoryLimit.Small), tomlFloat(p.NoHistoryLimit.Medium), tomlFloat(p.NoHistoryLimit.Large))
	line("review_share = %s", tomlFloat(p.ReviewShare))
	line("review_soft_share = %s", tomlFloat(p.ReviewSoftShare))
	line("plan_hard_multiplier = %s", tomlFloat(p.PlanHardMultiplier))
	line("project_daily_limit = %s", tomlFloat(p.ProjectDailyLimit))
	line("project_weekly_limit = %s", tomlFloat(p.ProjectWeeklyLimit))
	line("project_warn_share = %s", tomlFloat(p.ProjectWarnShare))
	line("assistant_daily_warn = %s", tomlFloat(p.AssistantDailyWarn))
	line("consultation_limit = %s", tomlFloat(p.ConsultationLimit))
	line("budget_flag_headroom_share = %s", tomlFloat(p.BudgetFlagHeadroomShare))
	line("# No progress")
	line("repeat_limit = %d", p.RepeatLimit)
	line("repeat_denials_to_park = %d", p.RepeatDenialsToPark)
	line("no_progress_share = %s", tomlFloat(p.NoProgressShare))
	line("no_progress_park_multiplier = %s", tomlFloat(p.NoProgressParkMultiplier))
	line("no_history_no_progress = %s", tomlFloat(p.NoHistoryNoProgress))
	line("# Waiting alert")
	line("waiting_alert_share = %s", tomlFloat(p.WaitingAlertShare))
	line("waiting_alert_window = %q", p.WaitingAlertWindow)
	line("waiting_alert_min_calls = %d", p.WaitingAlertMinCalls)
	line("# Session classes")
	line("assistant_class = %q", p.AssistantClass)
	return b.String()
}

// NewSpendPolicyVersion stores a draft under a generated ID.
func NewSpendPolicyVersion(id, authoredBy string, draft SpendPolicyDraft, createdAt int64) (PolicyVersion, error) {
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return PolicyVersion{}, err
	}
	return PolicyVersion{
		ID: id, Kind: PolicySpend, Version: draft.Policy.Version, Digest: PolicyDigest(draft.Body), Body: draft.Body,
		Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadSpendPolicy reads a version's settings, once its body is what its digest covers, and refuses a version whose
// settings fail Check. The digest covers the body and the code reads the settings, so a stored value outside its
// range must not reach a limit or a wait. A key an earlier build did not state reads as its default.
func ReadSpendPolicy(version PolicyVersion) (SpendPolicy, error) {
	if err := version.CheckDigest(); err != nil {
		return SpendPolicy{}, err
	}
	policy := DefaultSpendPolicy()
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return SpendPolicy{}, err
	}
	if err := policy.Check(); err != nil {
		return SpendPolicy{}, fmt.Errorf("spend policy version %d is refused: %w", version.Version, err)
	}
	return policy, nil
}
