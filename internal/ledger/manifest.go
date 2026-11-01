package ledger

import "fmt"

// The kinds of sign-in a worker can state. Only a kind leaves the machine, never an
// identity or a credential.
const (
	SignInSubscription = "subscription"
	SignInAPIKey       = "api_key"
)

// The statuses of a sign-in. Unknown says the doctor could not tell, which is no evidence
// that a driver cannot run.
const (
	SignedIn      = "signed_in"
	SignedOut     = "signed_out"
	SignInUnknown = "unknown"
)

// The keys of a work order's needs that a manifest answers itself. Any other key is a label.
const (
	NeedDriver = "driver"
	NeedSignIn = "sign_in"
)

// SignIn is what a worker says about how a driver is signed in.
type SignIn struct {
	Kind   string `json:"kind,omitempty"`
	Status string `json:"status"`
}

// DriverManifest is what a worker can run with one agent CLI.
type DriverManifest struct {
	Driver    string `json:"driver"`
	Present   bool   `json:"present"`
	Version   string `json:"version,omitempty"`
	Qualified bool   `json:"qualified"`
	SignIn    SignIn `json:"sign_in"`
	// Detail says why a driver is not usable.
	Detail string `json:"detail,omitempty"`
}

// Owner says who a worker belongs to.
type Owner struct {
	Kind     string `json:"kind,omitempty"`
	MemberID string `json:"member_id,omitempty"`
}

// Capacity says how many lanes a worker takes. Zero leaves it to the workspace.
type Capacity struct {
	MaxLanes int64 `json:"max_lanes"`
}

// Manifest is what a worker advertises (FR-DRV-7), and all that the control plane knows of
// what it can run (FR-WO-6).
type Manifest struct {
	Owner    Owner             `json:"owner"`
	Drivers  []DriverManifest  `json:"drivers,omitempty"`
	Capacity Capacity          `json:"capacity"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// usable reads a driver the way an offer does. A status it could not read stands, because a
// failed read must not take a worker out of the fleet.
func (d DriverManifest) usable() bool { return d.Present && d.SignIn.Status != SignedOut }

// Runs says whether this worker can run work with these needs, and if not, why not.
func (m Manifest) Runs(needs map[string]string) (bool, string) {
	for key, want := range needs {
		switch key {
		case NeedDriver:
			if !m.hasDriver(want) {
				return false, fmt.Sprintf("no usable driver %s", want)
			}
		case NeedSignIn:
			if !m.hasSignIn(want) {
				return false, fmt.Sprintf("no sign-in of kind %s", want)
			}
		default:
			if got, ok := m.Labels[key]; !ok || got != want {
				return false, fmt.Sprintf("no label %s=%s", key, want)
			}
		}
	}
	return true, ""
}

func (m Manifest) hasDriver(name string) bool {
	for _, driver := range m.Drivers {
		if driver.Driver == name && driver.usable() {
			return true
		}
	}
	return false
}

func (m Manifest) hasSignIn(kind string) bool {
	for _, driver := range m.Drivers {
		if driver.usable() && driver.SignIn.Kind == kind {
			return true
		}
	}
	return false
}
