package api

import (
	"context"
	"net/http"
)

// PersonalSetupAcceptancesPath is where a member accepts a piece of their own agent CLI setup for one worker's lanes.
const PersonalSetupAcceptancesPath = "/v1/workers/{id}/personal-setup/acceptances"

// AcceptPersonalSetup accepts one piece of the member's own setup for a worker's lanes on one CLI version, so the leak
// is not raised again there. Accepting it again changes nothing.
func (p *ProjectClient) AcceptPersonalSetup(ctx context.Context, worker string, in AcceptPersonalSetup) error {
	return p.do(ctx, http.MethodPost, workerPath(worker, "personal-setup", "acceptances"), in, nil)
}
