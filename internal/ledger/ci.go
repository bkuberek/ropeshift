package ledger

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CIRunStatus is where one required run stands. A run the forge calls queued, waiting, requested, or pending
// is queued, because none of them is at work.
type CIRunStatus string

// The statuses of a required run.
const (
	CIRunQueued     CIRunStatus = "queued"
	CIRunInProgress CIRunStatus = "in_progress"
	CIRunCompleted  CIRunStatus = "completed"
)

// CISettings are a repository's `[ci]` keys (ADR-0037): how the control plane reads CI. They sit on the
// repository's record beside its merge method and review target, and each is a named key whose stated value is
// its default. A record nobody edited holds none, and reads as the defaults; once a key is edited the record
// holds every key, so a value of 0 for ci_auto_rerun is a value and not a key left out.
type CISettings struct {
	// Custom says a member edited a key, and every field below is then the value in force.
	Custom                 bool    `json:"custom,omitempty"`
	HistoryWindow          int64   `json:"ci_history_window,omitempty"`
	HistoryMinRecords      int64   `json:"ci_history_min_records,omitempty"`
	NoHistoryP50           string  `json:"ci_no_history_p50,omitempty"`
	NoHistoryP85           string  `json:"ci_no_history_p85,omitempty"`
	MinInterval            string  `json:"ci_min_interval,omitempty"`
	SteadyInterval         string  `json:"ci_steady_interval,omitempty"`
	TimeoutMultiplier      float64 `json:"ci_timeout_multiplier,omitempty"`
	TimeoutNoHistory       string  `json:"ci_timeout_no_history,omitempty"`
	QueueTimeoutMultiplier float64 `json:"ci_queue_timeout_multiplier,omitempty"`
	QueueTimeoutNoHistory  string  `json:"ci_queue_timeout_no_history,omitempty"`
	AutoRerun              int64   `json:"ci_auto_rerun,omitempty"`
	LogExcerptLines        int64   `json:"ci_log_excerpt_lines,omitempty"`
	LogExcerptBytes        int64   `json:"ci_log_excerpt_bytes,omitempty"`
	// WebhookSecretEnv names the environment variable of the server's process that holds the key a GitHub webhook
	// delivery for this repository is signed with. The ledger keeps the name and never the key, as every secret of
	// ropeshift is kept (FR-SPEND-20), and an empty name takes no delivery for the repository.
	WebhookSecretEnv string `json:"ci_webhook_secret_env,omitempty"`
}

// DefaultCISettings are the values the design states.
func DefaultCISettings() CISettings {
	return CISettings{
		Custom: true, HistoryWindow: 50, HistoryMinRecords: 5, NoHistoryP50: "10m", NoHistoryP85: "20m", MinInterval: "30s", SteadyInterval: "5m",
		TimeoutMultiplier: 3, TimeoutNoHistory: "3h", QueueTimeoutMultiplier: 3, QueueTimeoutNoHistory: "30m", AutoRerun: 1,
		LogExcerptLines: 200, LogExcerptBytes: 8192,
	}
}

// Effective is the settings in force: the record's own once a member edited one, and the defaults before.
func (s CISettings) Effective() CISettings {
	if !s.Custom {
		return DefaultCISettings()
	}
	return s
}

// The ranges of the keys. Within one, every value is the member's: a range only keeps a rerun, a schedule, and a
// log excerpt bounded, because an unbounded one is the waste the design stops.
const (
	ciWindowLow, ciWindowHigh           = 5, 500
	ciMinRecordsLow, ciMinRecordsHigh   = 1, 50
	ciP50Low, ciP50High                 = 10 * time.Second, 6 * time.Hour
	ciP85High                           = 12 * time.Hour
	ciMinLow, ciIntervalHigh            = 5 * time.Second, time.Hour
	ciMultiplierLow, ciMultiplierHigh   = 1.5, 20.0
	ciTimeoutLow, ciTimeoutHigh         = 10 * time.Minute, 72 * time.Hour
	ciQueueTimeoutLow, ciQueueTimeoutHi = time.Minute, 24 * time.Hour
	ciRerunHigh                         = 3
	ciLinesLow, ciLinesHigh             = 10, 2000
	ciBytesLow, ciBytesHigh             = 1024, 65536
)

func ciRefusal(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidRepository}, args...)...)
}

func ciDuration(key, text string, low, high time.Duration) (time.Duration, error) {
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, ciRefusal("%s %q is not a duration, such as \"5m\"", key, text)
	}
	if d < low || d > high {
		return 0, ciRefusal("%s is %s, and it runs from %s to %s", key, text, low, high)
	}
	return d, nil
}

func ciInt(key string, value, low, high int64) error {
	if value < low || value > high {
		return ciRefusal("%s is %d, and it runs from %d to %d", key, value, low, high)
	}
	return nil
}

func ciFloat(key string, value, low, high float64) error {
	if value < low || value > high {
		return ciRefusal("%s is %g, and it runs from %g to %g", key, value, low, high)
	}
	return nil
}

// Check accepts settings a repository can be read by. A value outside its key's range is refused, naming the
// key and the range, and so is a pair of keys that run backwards.
func (s CISettings) Check() error {
	if err := ciInt("ci_history_window", s.HistoryWindow, ciWindowLow, ciWindowHigh); err != nil {
		return err
	}
	if err := ciInt("ci_history_min_records", s.HistoryMinRecords, ciMinRecordsLow, ciMinRecordsHigh); err != nil {
		return err
	}
	if s.HistoryMinRecords > s.HistoryWindow {
		return ciRefusal("ci_history_min_records is %d, past ci_history_window, %d", s.HistoryMinRecords, s.HistoryWindow)
	}
	p50, err := ciDuration("ci_no_history_p50", s.NoHistoryP50, ciP50Low, ciP50High)
	if err != nil {
		return err
	}
	p85, err := ciDuration("ci_no_history_p85", s.NoHistoryP85, ciP50Low, ciP85High)
	if err != nil {
		return err
	}
	if p85 < p50 {
		return ciRefusal("ci_no_history_p85 is %s, under ci_no_history_p50, %s", s.NoHistoryP85, s.NoHistoryP50)
	}
	minimum, err := ciDuration("ci_min_interval", s.MinInterval, ciMinLow, ciIntervalHigh)
	if err != nil {
		return err
	}
	steady, err := ciDuration("ci_steady_interval", s.SteadyInterval, ciMinLow, ciIntervalHigh)
	if err != nil {
		return err
	}
	if steady < minimum {
		return ciRefusal("ci_steady_interval is %s, under ci_min_interval, %s", s.SteadyInterval, s.MinInterval)
	}
	if err := ciFloat("ci_timeout_multiplier", s.TimeoutMultiplier, ciMultiplierLow, ciMultiplierHigh); err != nil {
		return err
	}
	if _, err := ciDuration("ci_timeout_no_history", s.TimeoutNoHistory, ciTimeoutLow, ciTimeoutHigh); err != nil {
		return err
	}
	if err := ciFloat("ci_queue_timeout_multiplier", s.QueueTimeoutMultiplier, ciMultiplierLow, ciMultiplierHigh); err != nil {
		return err
	}
	if _, err := ciDuration("ci_queue_timeout_no_history", s.QueueTimeoutNoHistory, ciQueueTimeoutLow, ciQueueTimeoutHi); err != nil {
		return err
	}
	if err := ciInt("ci_auto_rerun", s.AutoRerun, 0, ciRerunHigh); err != nil {
		return err
	}
	if err := ciInt("ci_log_excerpt_lines", s.LogExcerptLines, ciLinesLow, ciLinesHigh); err != nil {
		return err
	}
	if err := ciInt("ci_log_excerpt_bytes", s.LogExcerptBytes, ciBytesLow, ciBytesHigh); err != nil {
		return err
	}
	if s.WebhookSecretEnv != "" && !secretEnvName.MatchString(s.WebhookSecretEnv) {
		return ciRefusal("ci_webhook_secret_env names a variable of the server's environment, such as ROPESHIFT_HOOK_WIDGETS, and never holds the key")
	}
	return nil
}

// secretEnvName is what the name of a variable looks like, so a key pasted in its place is refused and never
// reaches the ledger, as a notify policy's is.
var secretEnvName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// Set edits one key by its name and its text, over the settings in force, and returns settings that check. A key
// nobody offers is refused.
func (s CISettings) Set(key, value string) (CISettings, error) {
	out := s.Effective()
	value = strings.TrimSpace(value)
	integer := func(into *int64) error {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return ciRefusal("%s %q is not a whole number", key, value)
		}
		*into = n
		return nil
	}
	multiplier := func(into *float64) error {
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return ciRefusal("%s %q is not a number", key, value)
		}
		*into = n
		return nil
	}
	var err error
	switch key {
	case "ci_history_window":
		err = integer(&out.HistoryWindow)
	case "ci_history_min_records":
		err = integer(&out.HistoryMinRecords)
	case "ci_no_history_p50":
		out.NoHistoryP50 = value
	case "ci_no_history_p85":
		out.NoHistoryP85 = value
	case "ci_min_interval":
		out.MinInterval = value
	case "ci_steady_interval":
		out.SteadyInterval = value
	case "ci_timeout_multiplier":
		err = multiplier(&out.TimeoutMultiplier)
	case "ci_timeout_no_history":
		out.TimeoutNoHistory = value
	case "ci_queue_timeout_multiplier":
		err = multiplier(&out.QueueTimeoutMultiplier)
	case "ci_queue_timeout_no_history":
		out.QueueTimeoutNoHistory = value
	case "ci_auto_rerun":
		err = integer(&out.AutoRerun)
	case "ci_log_excerpt_lines":
		err = integer(&out.LogExcerptLines)
	case "ci_log_excerpt_bytes":
		err = integer(&out.LogExcerptBytes)
	case "ci_webhook_secret_env":
		out.WebhookSecretEnv = value
	default:
		return s, ciRefusal("%s is no CI key", key)
	}
	if err != nil {
		return s, err
	}
	if err := out.Check(); err != nil {
		return s, err
	}
	return out, nil
}

// CIKeys lists the keys in the order a surface shows them.
var CIKeys = []string{
	"ci_history_window", "ci_history_min_records", "ci_no_history_p50", "ci_no_history_p85", "ci_min_interval", "ci_steady_interval",
	"ci_timeout_multiplier", "ci_timeout_no_history", "ci_queue_timeout_multiplier", "ci_queue_timeout_no_history", "ci_auto_rerun",
	"ci_log_excerpt_lines", "ci_log_excerpt_bytes", "ci_webhook_secret_env",
}

// Text is the value in force of a key, as `Set` reads it.
func (s CISettings) Text(key string) string {
	e := s.Effective()
	switch key {
	case "ci_history_window":
		return strconv.FormatInt(e.HistoryWindow, 10)
	case "ci_history_min_records":
		return strconv.FormatInt(e.HistoryMinRecords, 10)
	case "ci_no_history_p50":
		return e.NoHistoryP50
	case "ci_no_history_p85":
		return e.NoHistoryP85
	case "ci_min_interval":
		return e.MinInterval
	case "ci_steady_interval":
		return e.SteadyInterval
	case "ci_timeout_multiplier":
		return strconv.FormatFloat(e.TimeoutMultiplier, 'g', -1, 64)
	case "ci_timeout_no_history":
		return e.TimeoutNoHistory
	case "ci_queue_timeout_multiplier":
		return strconv.FormatFloat(e.QueueTimeoutMultiplier, 'g', -1, 64)
	case "ci_queue_timeout_no_history":
		return e.QueueTimeoutNoHistory
	case "ci_auto_rerun":
		return strconv.FormatInt(e.AutoRerun, 10)
	case "ci_log_excerpt_lines":
		return strconv.FormatInt(e.LogExcerptLines, 10)
	case "ci_log_excerpt_bytes":
		return strconv.FormatInt(e.LogExcerptBytes, 10)
	case "ci_webhook_secret_env":
		return e.WebhookSecretEnv
	}
	return ""
}

func (s CISettings) duration(text string) time.Duration {
	d, _ := time.ParseDuration(text)
	return d
}

// Min is the closest two reads of one head may be.
func (s CISettings) Min() time.Duration { return s.duration(s.Effective().MinInterval) }

// Steady is the gap between reads while every run is queued and once a run is past its p85.
func (s CISettings) Steady() time.Duration { return s.duration(s.Effective().SteadyInterval) }

// CIDurations are a repository's expected push-to-completed time.
type CIDurations struct{ P50, P85 time.Duration }

// NoHistory is the expected duration of a repository with no history.
func (s CISettings) NoHistory() CIDurations {
	e := s.Effective()
	return CIDurations{P50: s.duration(e.NoHistoryP50), P85: s.duration(e.NoHistoryP85)}
}

// CIHistory is what a repository's own CI records say: the p50 and p85 of push-to-completed time over its recent
// records with a test result. A repository with too few records has none.
type CIHistory = CIDurations

// CIExpected is the duration the schedule reads by, and whether it came from the repository's history.
type CIExpected struct {
	CIDurations
	History bool
}

// Expectation is the expected duration of a repository: its history when it has one, and the no-history keys
// when it has none.
func (s CISettings) Expectation(history *CIHistory) CIExpected {
	if history != nil {
		return CIExpected{CIDurations: *history, History: true}
	}
	return CIExpected{CIDurations: s.NoHistory()}
}

// CIRun is one required run of a head, as the schedule reads it.
type CIRun struct {
	Required  bool
	Status    CIRunStatus
	StartedAt time.Time
}

// CIRunsOf reads the required runs of a pull request's checks. A start the forge gave in a form this cannot read
// is no start, and a check with no status is a run that has not started.
func CIRunsOf(checks []PullRequestCheck) []CIRun {
	runs := make([]CIRun, 0, len(checks))
	for _, check := range checks {
		run := CIRun{Required: check.Required, Status: check.Status}
		if started, err := time.Parse(time.RFC3339, check.StartedAt); err == nil {
			run.StartedAt = started
		}
		runs = append(runs, run)
	}
	return runs
}

// NextRead is when to read a head's required checks again after a read at now (FR-SPEND-20). The clock runs only
// while a required run is in progress, from the earliest start among the required runs that began, so queued
// time never advances it. Every steady interval while every run is queued, or none is left running; once at the
// p50; then at half the remaining time to the p85 each time, never closer than the minimum gap; then every
// steady interval until every run completes. No part of it ends: a run that takes hours is read every steady
// interval for as long as it runs, and a read starts nothing.
func (s CISettings) NextRead(expected CIExpected, runs []CIRun, now time.Time) time.Time {
	var earliest time.Time
	working := false
	for _, run := range runs {
		if !run.Required || run.Status == CIRunQueued || run.Status == "" {
			continue
		}
		working = working || run.Status == CIRunInProgress
		if !run.StartedAt.IsZero() && (earliest.IsZero() || run.StartedAt.Before(earliest)) {
			earliest = run.StartedAt
		}
	}
	steady := now.Add(s.Steady())
	if !working || earliest.IsZero() {
		return steady
	}
	elapsed := max(now.Sub(earliest), 0)
	switch {
	case elapsed < expected.P50:
		return now.Add(max(expected.P50-elapsed, s.Min()))
	case elapsed < expected.P85:
		return now.Add(max((expected.P85-elapsed)/2, s.Min()))
	}
	return steady
}
