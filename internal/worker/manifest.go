package worker

import (
	"context"
	"runtime"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// BaseManifest is what a worker says of itself before it asks a driver.
func BaseManifest(owner ledger.Owner, maxLanes int64) ledger.Manifest {
	return ledger.Manifest{
		Owner: owner, Capacity: ledger.Capacity{MaxLanes: maxLanes},
		Labels: map[string]string{"os": runtime.GOOS, "arch": runtime.GOARCH},
	}
}

// BuildManifest adds what a driver's doctor reports to what a worker says of itself. A
// report holds a kind and a status, so that is all that leaves this machine.
func BuildManifest(ctx context.Context, driver ports.AgentDriver, base ledger.Manifest) (ledger.Manifest, error) {
	report, err := driver.Doctor(ctx)
	if err != nil {
		return base, err
	}
	manifest := base
	manifest.Drivers = []ledger.DriverManifest{{
		Driver: report.Driver, Present: report.Present, Version: report.Version, Qualified: report.Qualified,
		SignIn: ledger.SignIn{Kind: report.SignIn, Status: report.SignInStatus}, Detail: report.Detail,
	}}
	return manifest, nil
}
