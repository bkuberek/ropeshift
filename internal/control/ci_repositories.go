package control

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var _ CIRepositories = (*Plane)(nil)

// CIRepository finds every repository, in every active project, whose forge binding is the host, the owner, and the
// name, without regard to case as GitHub names them, with the variable each one's record names for its webhook
// secret. A repository two projects hold is found in each. It answers from memory, which a heartbeat that sees a
// project's revision move makes stale, and which is read again when it has grown old.
func (p *Plane) CIRepository(ctx context.Context, host, owner, repo string) ([]CIRegistration, error) {
	return p.ciRegistry.CIRepository(ctx, host, owner, repo)
}

// readCIRegistrations reads every active project's repository records once, each in the same read as the project's
// revision, so a record written after the read shows as a revision that has moved.
func (p *Plane) readCIRegistrations(ctx context.Context) (CIRegistrations, error) {
	projects, err := p.serving(ctx)
	if err != nil {
		return CIRegistrations{}, err
	}
	read := CIRegistrations{Repositories: map[string][]CIRegistration{}, Revisions: map[string]int64{}}
	for _, project := range projects {
		w, err := p.workers(ctx, project)
		if err != nil {
			return CIRegistrations{}, err
		}
		err = w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
			workspace, err := tx.Workspace()
			if err != nil {
				return err
			}
			records, err := tx.Repositories().Repositories()
			if err != nil {
				return err
			}
			read.Revisions[string(project.ID)] = workspace.Rev
			for _, record := range records {
				binding, err := ledger.ParseForgeBinding(record.ForgeBinding)
				if err != nil {
					continue
				}
				key := CIKey(binding.Host, binding.Owner, binding.Repo)
				read.Repositories[key] = append(read.Repositories[key], CIRegistration{Project: project.ID, Binding: binding, SecretEnv: record.CI.WebhookSecretEnv})
			}
			return nil
		})
		if err != nil {
			return CIRegistrations{}, err
		}
	}
	return read, nil
}
