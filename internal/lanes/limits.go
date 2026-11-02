package lanes

import (
	"encoding/json"
	"time"

	"ropeshift.dev/ropeshift/internal/parking"
)

// The statuses a driver reports of a sign-in's limit, in a vendor's own words.
const (
	UsageAllowed  = "allowed"
	UsageWarning  = "allowed_warning"
	UsageRejected = "rejected"
)

// The failures of a turn that say something about a sign-in and not about a lane.
const (
	FailureUsageLimit = "rate_limit"
	FailureSignIn     = "authentication_failed"
)

// NearLimitPercent is where a worker stops starting new work, so lanes that run can finish. The
// design names no figure, and a vendor's `allowed_warning` holds work back at any figure.
const NearLimitPercent = 90

// RetriesBeforeLimit is how many retries of a rate limit a session makes before a worker calls it
// a usage limit, because one 429 is not a limit, and a limit parks every lane on a sign-in.
const RetriesBeforeLimit = 3

// The bounds of the wait for a limit nobody gave a reset time for.
const (
	DefaultUsageBackoff = 5 * time.Minute
	MaxUsageBackoff     = time.Hour
)

// UsageReading is what one session said of its sign-in's limit. Every field can be missing, and no
// window is assumed.
type UsageReading struct {
	// Status is empty on a status line, which has none.
	Status string `json:"status,omitempty"`
	// ResetsAtMs is zero when nothing named one.
	ResetsAtMs int64 `json:"resets_at_ms,omitempty"`
	// Window is the vendor's name for it, which only a person reads.
	Window string `json:"window,omitempty"`
	// UsedPercent runs from 0 to 100. UsedKnown is false when no figure came.
	UsedPercent float64 `json:"used_percent,omitempty"`
	UsedKnown   bool    `json:"used_known,omitempty"`
}

// Near says this reading holds new work back. A reading past its own reset says nothing.
func (u UsageReading) Near(nowMs int64) bool {
	if u.ResetsAtMs != 0 && nowMs >= u.ResetsAtMs {
		return false
	}
	return u.Status == UsageWarning || (u.UsedKnown && u.UsedPercent >= NearLimitPercent)
}

// Reset reads the moment a limit ends, or zero for a reading that names none still ahead.
func (u UsageReading) Reset(nowMs int64) int64 {
	if u.ResetsAtMs > nowMs {
		return u.ResetsAtMs
	}
	return 0
}

// SignInState is what a worker knows of its one sign-in. It lives in the journal, so a restart
// keeps parking and waking on it.
type SignInState struct {
	// Last is the last reading any lane reported.
	Last UsageReading `json:"last"`
	// Limited says a limit stands. ResetAtMs is when it ends, and zero means nobody said.
	Limited   bool  `json:"limited,omitempty"`
	ResetAtMs int64 `json:"reset_at_ms,omitempty"`
	// Probe is the lane that tries again when nobody said, and ProbeAtMs is when. Tries counts the
	// times it found the limit still there.
	Probe     string `json:"probe,omitempty"`
	ProbeAtMs int64  `json:"probe_at_ms,omitempty"`
	Tries     int64  `json:"tries,omitempty"`
	// Hint is `/usage`, free text that may name local plugins, so it never leaves this machine.
	// HintRead says a read was made, which a run mode with no such read has nothing to show for.
	Hint     string `json:"hint,omitempty"`
	HintRead bool   `json:"hint_read,omitempty"`
	// SignedOut says this sign-in expired.
	SignedOut bool `json:"signed_out,omitempty"`
}

// UsageBackoff is how long a limit with no known reset waits, by how many tries found it.
func UsageBackoff(base time.Duration, tries int64) time.Duration {
	if base <= 0 {
		base = DefaultUsageBackoff
	}
	wait := base
	for range tries {
		wait *= 2
		if wait >= MaxUsageBackoff {
			return MaxUsageBackoff
		}
	}
	return min(wait, MaxUsageBackoff)
}

// wakeAtTerm is the wake language's word for a time.
const wakeAtTerm = "at"

// UsageWake is one lane's wait on a limit. Every lane names the sign-in, so one signal wakes them
// all, and a time when one is known (D10).
func UsageWake(key string, atMs int64) string {
	terms := []any{map[parking.Kind]string{parking.UsageReset: key}}
	if atMs != 0 {
		terms = append(terms, map[string]int64{wakeAtTerm: atMs})
	}
	raw, err := json.Marshal(map[string]any{"any_of": terms})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// SignInWake is what a lane waits on after its sign-in expired.
func SignInWake(key string) string {
	raw, err := json.Marshal(map[parking.Kind]string{parking.SigninRestored: key})
	if err != nil {
		panic(err)
	}
	return string(raw)
}
