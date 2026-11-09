package lanes

import (
	"encoding/json"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
)

// Surface names one undocumented surface of an agent CLI. A driver uses one only on a version
// whose check for it passed (FR-DRV-2).
type Surface string

// CheckResult is what one check observed of its surface.
type CheckResult string

// The values of CheckResult. Only a pass makes a surface usable: a surface that failed, or that a
// check could not reach, stays off.
const (
	CheckPassed     CheckResult = "passed"
	CheckFailed     CheckResult = "failed"
	CheckUnobserved CheckResult = "unobserved"
)

// The sources of a Qualification.
const (
	// QualifiedByMatrix is the row the release's qualification suite left in the binary.
	QualifiedByMatrix = "matrix"
	// QualifiedByProbe is what a probe observed on this machine.
	QualifiedByProbe = "probe"
)

// Qualification is what is known of one version's undocumented surfaces. It holds versions,
// surfaces, and results, and nothing of a session or an account.
type Qualification struct {
	Driver  string `json:"driver"`
	Version string `json:"version"`
	// Source is QualifiedByMatrix or QualifiedByProbe.
	Source string `json:"source"`
	// SignIn is the kind of sign-in a probe ran on, because one check reads a subscription's login.
	SignIn string                  `json:"sign_in,omitempty"`
	Checks map[Surface]CheckResult `json:"checks"`
	// Degraded says a surface the driver relies on did not pass, which the driver decides, because
	// only it knows which surfaces it relies on.
	Degraded bool `json:"degraded"`
}

// Passed reports a surface whose check passed.
func (q Qualification) Passed(s Surface) bool { return q.Checks[s] == CheckPassed }

// For reports a qualification of exactly this version. An empty version is none.
func (q Qualification) For(version string) bool { return version != "" && q.Version == version }

// Holds reports whether this qualification still answers for the installed version, so a version
// that changed under a worker reads as one nobody has probed.
func (q Qualification) Holds(installed string) bool {
	return q.Source == QualifiedByProbe && q.For(installed)
}

// Check reports a qualification a worker could have sent: a driver, a version, a probe as its
// source, and only the three results.
func (q Qualification) Check() error {
	if q.Driver == "" || q.Version == "" || q.Source != QualifiedByProbe {
		return fmt.Errorf("a qualification names its driver, its version, and a probe: %+v", q)
	}
	for surface, result := range q.Checks {
		switch result {
		case CheckPassed, CheckFailed, CheckUnobserved:
		default:
			return fmt.Errorf("surface %s reads %q", surface, result)
		}
	}
	return nil
}

// Unprobed is a probe that could not run: every surface it names reads unobserved, so the version is
// degraded and nothing waits on it.
func Unprobed(driver, version, signIn string, surfaces []Surface) Qualification {
	q := Qualification{Driver: driver, Version: version, Source: QualifiedByProbe, SignIn: signIn, Checks: map[Surface]CheckResult{}, Degraded: true}
	for _, surface := range surfaces {
		q.Checks[surface] = CheckUnobserved
	}
	return q
}

// QualificationKey names a worker's driver, the way a sign-in is named (D10).
func QualificationKey(worker, driver string) string { return worker + ":" + driver }

// ReplayQualifications rebuilds what each worker's driver was last qualified as, keyed by
// QualificationKey. A later probe of the same driver replaces an earlier one, whatever version.
func ReplayQualifications(events []ledger.Event) (map[string]Qualification, error) {
	out := map[string]Qualification{}
	for _, event := range events {
		if event.Type != ledger.EventDriverQualified {
			continue
		}
		var q Qualification
		if err := json.Unmarshal(event.Data, &q); err != nil {
			return nil, fmt.Errorf("event %s: %w", event.ID, err)
		}
		out[QualificationKey(event.Subject.ID, q.Driver)] = q
	}
	return out, nil
}
