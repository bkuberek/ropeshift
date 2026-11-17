package control

import (
	"context"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var _ CIRepositories = (*Plane)(nil)

// CIRepository finds every repository, in every active project, whose forge binding is the host, the owner, and the
// name, without regard to case as GitHub names them, with the variable each one's record names for its webhook
// secret. A repository two projects hold is found in each.
func (p *Plane) CIRepository(ctx context.Context, host, owner, repo string) ([]CIRegistration, error) {
	projects, err := p.serving(ctx)
	if err != nil {
		return nil, err
	}
	var found []CIRegistration
	for _, project := range projects {
		w, err := p.workers(ctx, project)
		if err != nil {
			return nil, err
		}
		err = w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
			records, err := tx.Repositories().Repositories()
			if err != nil {
				return err
			}
			for _, record := range records {
				binding, err := ledger.ParseForgeBinding(record.ForgeBinding)
				if err != nil {
					continue
				}
				if strings.EqualFold(binding.Host, host) && strings.EqualFold(binding.Owner, owner) && strings.EqualFold(binding.Repo, repo) {
					found = append(found, CIRegistration{Binding: binding, SecretEnv: record.CI.WebhookSecretEnv})
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return found, nil
}
