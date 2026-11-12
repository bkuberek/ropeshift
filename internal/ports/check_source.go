package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ForgeCheck is one finished check a forge ran on a commit. Criterion names the contract
// criterion it speaks for, and the source owns that mapping.
type ForgeCheck struct {
	Name       string
	Criterion  string
	Status     workorders.CheckStatus
	DetailsURI string
}

// ForgeRequest names one commit in one repository.
type ForgeRequest struct {
	Repository string
	Commit     string
}

// CheckSource reports what ran on a commit, as the control plane reads it for acceptance. A result from
// here is evidence a lane did not write, so it counts in acceptance by itself. No release ships a source
// yet, so the port and its fake are what exist (D16).
type CheckSource interface {
	// Checks lists the finished checks on a commit, by name. A check still running is not
	// listed, and a commit nothing ran on lists nothing.
	Checks(ctx context.Context, scope ledger.WorkspaceID, request ForgeRequest) ([]ForgeCheck, error)
}
